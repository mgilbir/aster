package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Assembling the optimized graph into Vega data definitions —
// vega-lite/src/compile/data/assemble.ts.

// vgData is a data definition under construction. Its transform list is kept
// as a Go slice so nodes can append to it, and written into obj when finished.
type vgData struct {
	obj       *Object
	transform []Value
}

func newVgData(name Value, source Value) *vgData {
	d := &vgData{obj: mk("name", name, "source", source, "transform", jsval.Undefined)}
	// upstream builds {name, source, transform} and later assigns; keys with
	// undefined values keep their position.
	return d
}

func (d *vgData) name() Value   { return d.obj.Lookup("name") }
func (d *vgData) source() Value { return d.obj.Lookup("source") }

func (d *vgData) finish() Value {
	if len(d.transform) == 0 {
		d.obj.Delete("transform")
	} else {
		d.obj.Set("transform", jsval.Arr(d.transform))
	}
	return jsval.Obj(d.obj)
}

func makeWalkTree(data *[]*vgData) func(node dfNode, ds *vgData) {
	datasetIndex := 0
	var walk func(node dfNode, ds *vgData)
	walk = func(node dfNode, ds *vgData) {
		cc := node.base().cc
		cc.work(1)
		if sn, ok := node.(*sourceNode); ok {
			if cc.v5 && !sn.generator && sn.data == nil {
				// 5.8's isUrlData(node.data) of a source that is none of
				// inline, url, sphere or named data.
				jsIn(undef, "url")
			}
			if !sn.generator && !isUrlData(jsval.Obj(sn.data)) {
				*data = append(*data, ds)
				ds = newVgData(jsval.Null, ds.name())
			}
		}
		if pn, ok := node.(*parseNode); ok {
			if _, parentIsSource := pn.par.(*sourceNode); parentIsSource && ds.source().IsUndefined() {
				f := merged(ds.obj.Lookup("format"))
				f.Set("parse", jsval.Obj(pn.assembleFormatParse()))
				ds.obj.Set("format", jsval.Obj(f))
				ds.transform = append(ds.transform, pn.assembleTransforms(true)...)
			} else {
				ds.transform = append(ds.transform, pn.assembleTransforms(false)...)
			}
		}
		if fn, ok := node.(*facetNode); ok {
			if !ds.name().IsTruthy() {
				ds.obj.Set("name", jsval.Str("data_"+jsval.JSNumberString(float64(datasetIndex))))
				datasetIndex++
			}
			if !ds.source().IsTruthy() || len(ds.transform) > 0 {
				*data = append(*data, ds)
				fn.data = ds.name()
			} else {
				fn.data = ds.source()
			}
			for _, v := range fn.assemble() {
				*data = append(*data, vgDataFromValue(v))
			}
			return
		}
		switch t := node.(type) {
		case *graticuleNode:
			ds.transform = append(ds.transform, t.assemble())
		case *sequenceNode:
			ds.transform = append(ds.transform, t.assemble())
		case *filterInvalidNode:
			ds.transform = append(ds.transform, t.assemble())
		case *filterNode:
			ds.transform = append(ds.transform, t.assemble())
		case *calculateNode:
			ds.transform = append(ds.transform, t.assemble())
		case *geoPointNode:
			ds.transform = append(ds.transform, t.assemble())
		case *aggregateNode:
			ds.transform = append(ds.transform, t.assemble())
		case *identifierNode:
			ds.transform = append(ds.transform, mkv("type", "identifier", "as", selectionID))
		case *xformNode:
			if t.kind == "impute" {
				ds.transform = append(ds.transform, t.assemble()...)
			} else {
				ds.transform = append(ds.transform, t.assemble()...)
			}
		case *binNode:
			ds.transform = append(ds.transform, t.assemble()...)
		case *timeUnitNode:
			ds.transform = append(ds.transform, t.assemble()...)
		case *stackNode:
			ds.transform = append(ds.transform, t.assemble()...)
		case *geoJSONNode:
			ds.transform = append(ds.transform, t.assemble()...)
		}
		if on, ok := node.(*outputNode); ok {
			switch {
			case ds.source().IsTruthy() && len(ds.transform) == 0:
				on.setSource(ds.source())
			case isOutput(on.par):
				on.setSource(ds.name())
			default:
				if !ds.name().IsTruthy() {
					ds.obj.Set("name", jsval.Str("data_"+jsval.JSNumberString(float64(datasetIndex))))
					datasetIndex++
				}
				on.setSource(ds.name())
				if on.numChildren() == 1 {
					*data = append(*data, ds)
					ds = newVgData(jsval.Null, ds.name())
				}
			}
		}
		switch node.base().numChildren() {
		case 0:
			if _, ok := node.(*outputNode); ok && (!ds.source().IsTruthy() || len(ds.transform) > 0) {
				*data = append(*data, ds)
			}
		case 1:
			walk(node.base().child(0), ds)
		default:
			if !ds.name().IsTruthy() {
				ds.obj.Set("name", jsval.Str("data_"+jsval.JSNumberString(float64(datasetIndex))))
				datasetIndex++
			}
			source := ds.name()
			if !ds.source().IsTruthy() || len(ds.transform) > 0 {
				*data = append(*data, ds)
			} else {
				source = ds.source()
			}
			for _, child := range node.base().children() {
				walk(child, newVgData(jsval.Null, source))
			}
		}
	}
	return walk
}

func isOutput(n dfNode) bool { _, ok := n.(*outputNode); return ok }

// vgDataFromValue adapts an already-assembled definition (facet header data).
func vgDataFromValue(v Value) *vgData {
	o := cloneObj(v.ObjValue())
	d := &vgData{obj: o}
	if t := o.Lookup("transform"); t.IsArr() {
		d.transform = append([]Value(nil), t.Items()...)
	}
	return d
}

func assembleFacetData(root *facetNode) []Value {
	cc := root.cc
	var data []*vgData
	walk := makeWalkTree(&data)
	for _, child := range append([]dfNode(nil), root.children()...) {
		// upstream passes {source: root.name, name: null, transform: []}: source first.
		walk(child, &vgData{obj: mk("source", root.name, "name", jsval.Null, "transform", jsval.Undefined)})
	}
	out := make([]Value, len(data))
	for i, d := range data {
		cc.work(1)
		out[i] = d.finish()
	}
	return out
}

func assembleRootData(cc *compileCtx, dc *dataComponent, datasets Value) []Value {
	var data []*vgData
	walk := makeWalkTree(&data)
	sourceIndex := 0
	for _, root := range dc.sources.items {
		cc.work(1)
		sn := root.(*sourceNode)
		if !sn.hasName() {
			sn.name = jsval.Str("source_" + jsval.JSNumberString(float64(sourceIndex)))
			sourceIndex++
		}
		d := &vgData{obj: sn.assemble()}
		d.transform = nil
		walk(root, d)
	}
	// Data sets with no transform and no source come first. Upstream moves
	// each one with data.splice(whereTo++, 0, data.splice(i, 1)[0]) as it
	// scans, a quadratic stable partition; the scan never revisits an
	// element, so a single stable pass gives the same order (ADR 0011).
	first := make([]*vgData, 0, len(data))
	var rest []*vgData
	for _, d := range data {
		cc.work(1)
		if len(d.transform) == 0 && !d.source().IsTruthy() {
			first = append(first, d)
		} else {
			rest = append(rest, d)
		}
	}
	data = append(first, rest...)
	for _, d := range data {
		cc.work(1 + int64(len(d.transform)))
		for i, t := range d.transform {
			if t.Get("type").IsStr() && t.Get("type").StrValue() == "lookup" {
				o := cloneObj(t.ObjValue())
				if node, ok := dc.outputNodes[t.Get("from").AsString()]; ok && node != nil {
					o.Set("from", node.getSource())
				}
				d.transform[i] = jsval.Obj(o)
			}
		}
	}
	for _, d := range data {
		cc.work(1)
		// if (d.name in datasets) d.values = datasets[d.name], datasets
		// defaulting to {}: a name every object inherits is in it too.
		ds := datasets
		if ds.IsUndefined() {
			ds = mkv()
		}
		if key := d.name().AsString(); jsIn(ds, key) {
			d.obj.Set("values", jsProp(ds, key))
		}
	}
	out := make([]Value, len(data))
	for i, d := range data {
		cc.work(1)
		out[i] = d.finish()
	}
	return out
}
