package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Projections — vega-lite/src/compile/projection/*.ts.

var projectionProperties = []string{
	"type", "clipAngle", "clipExtent", "center", "rotate", "precision", "reflectX", "reflectY", "coefficient",
	"distance", "fraction", "lobes", "parallel", "radius", "ratio", "spacing", "tilt",
}

type projectionComponent struct {
	*split
	specifiedProjection Value
	size                []Value
	data                []Value
	hasData             bool
	merged              bool
}

func newProjectionComponent(name string, specified Value, size, data []Value, hasData bool) *projectionComponent {
	explicit := jsval.NewObject(4)
	spreadV(explicit, specified)
	return &projectionComponent{
		split: newSplitOf(explicit, mk("name", name)), specifiedProjection: specified, size: size, data: data, hasData: hasData,
	}
}

func (p *projectionComponent) isFit() bool { return p.hasData }

func parseProjection(m Model) {
	if u := asUnit(m); u != nil {
		u.comp.projection = parseUnitProjection(u)
	} else {
		m.b().comp.projection = parseNonUnitProjections(m)
	}
}

func parseUnitProjection(u *unitModel) *projectionComponent {
	if !u.hasProjection() {
		return nil
	}
	proj := replaceExprRef(u.ctx, u.specifiedProjection, 0)
	fit := !(proj.IsObj() && (!proj.Get("scale").IsNullish() || !proj.Get("translate").IsNullish()))
	var size, data []Value
	if fit {
		size = []Value{u.getSizeSignalRef("width"), u.getSizeSignalRef("height")}
		data = gatherFitData(u)
	}
	comp := newProjectionComponent(u.projectionName(true), jsval.Obj(merged(replaceExprRef(u.ctx, u.config.Get("projection"), 0), proj)), size, data, fit)
	if !comp.get("type").IsTruthy() {
		comp.set("type", jsval.Str("equalEarth"), false)
	}
	return comp
}

func gatherFitData(u *unitModel) []Value {
	cc := u.b().ctx

	var data []Value
	for _, pair := range [][2]string{{chLongitude, chLatitude}, {chLongitude2, chLatitude2}} {
		if getFieldOrDatumDef(cc, u.encoding.Get(pair[0])).IsTruthy() || getFieldOrDatumDef(cc, u.encoding.Get(pair[1])).IsTruthy() {
			data = append(data, mkv("signal", u.getName("geojson_"+jsval.JSNumberString(float64(len(data))))))
		}
	}
	if u.channelHasField(chShape) && channelDefType(u.typedFieldDef(chShape)) == "geojson" {
		data = append(data, mkv("signal", u.getName("geojson_"+jsval.JSNumberString(float64(len(data))))))
	}
	if len(data) == 0 {
		data = append(data, jsval.Str(u.requestDataName(dsMain)))
	}
	return data
}

func mergeProjectionIfNoConflict(cc *compileCtx, first, second *projectionComponent) *projectionComponent {
	allShared := true
	for _, prop := range projectionProperties {
		fe, se := first.explicit.Has(prop), second.explicit.Has(prop)
		if !fe && !se {
			continue
		}
		if fe && se && deepEqual(cc, first.get(prop), second.get(prop)) {
			continue
		}
		allShared = false
		break
	}
	sizeEq := len(first.size) == len(second.size)
	if sizeEq {
		for i := range first.size {
			if !deepEqual(cc, first.size[i], second.size[i]) {
				sizeEq = false
			}
		}
	}
	if sizeEq {
		if allShared {
			return first
		} else if explicitIsEmpty(cc, first) {
			return second
		} else if explicitIsEmpty(cc, second) {
			return first
		}
	}
	return nil
}

// explicitIsEmpty is deepEqual(p.explicit, {}): it reads the explicit
// object's valueOf, toString and constructor, which a specification's
// projection may hold as data (a TypeError, as upstream's), so it is not a
// count of the keys.
func explicitIsEmpty(cc *compileCtx, p *projectionComponent) bool {
	cc.work(int64(p.explicit.Len()))
	return deepEqual(cc, jsval.Obj(p.explicit.object()), jsval.Obj(jsval.NewObject(0)))
}

func parseNonUnitProjections(m Model) *projectionComponent {
	cc := m.b().ctx
	kids := m.children()
	if len(kids) == 0 {
		return nil
	}
	var nonUnit *projectionComponent
	for _, c := range kids {
		cc.work(1)
		parseProjection(c)
	}
	mergable := true
	for _, c := range kids {
		cc.work(1)
		p := c.b().comp.projection
		if p == nil {
			continue
		}
		if nonUnit == nil {
			nonUnit = p
			continue
		}
		mg := mergeProjectionIfNoConflict(cc, nonUnit, p)
		if mg != nil {
			nonUnit = mg
		} else {
			mergable = false
			break
		}
	}
	if nonUnit != nil && mergable {
		name := m.b().projectionName(true)
		data := append([]Value(nil), nonUnit.data...)
		mp := newProjectionComponent(name, nonUnit.specifiedProjection, nonUnit.size, data, nonUnit.hasData)
		for _, c := range kids {
			cc.work(1)
			p := c.b().comp.projection
			if p != nil {
				if p.isFit() {
					mp.data = append(mp.data, p.data...)
				}
				c.b().renameProjection(p.get("name").AsString(), name)
				p.merged = true
			}
		}
		return mp
	}
	return nil
}

func assembleProjections(m Model) []Value {
	cc := m.b().ctx
	if isLayerModel(m) || isConcatModel(m) {
		out := assembleProjectionForModel(m)
		for _, c := range m.children() {
			cc.work(modelStep)
			out = append(out, c.b().assembleProjections()...)
		}
		return out
	}
	return assembleProjectionForModel(m)
}

func assembleProjectionForModel(m Model) []Value {
	cc := m.b().ctx
	comp := m.b().comp.projection
	if comp == nil || comp.merged {
		return nil
	}
	projection := comp.combine()
	name := projection.Lookup("name")
	if !comp.hasData {
		o := mk("name", name, "translate", mkv("signal", "[width / 2, height / 2]"))
		spread(o, jsval.Obj(projection))
		return []Value{jsval.Obj(o)}
	}
	var sizes []string
	for _, r := range comp.size {
		sizes = append(sizes, signalOf(r))
	}
	size := mkv("signal", "["+joinCC(cc, sizes, ", ")+"]")
	// Upstream dedupes the sources with fit.includes, a scan of those so
	// far (quadratic over a projection fitted to many layers, C150: audit
	// 2026-10-18); a set of them keeps the same first-seen order.
	var fits []string
	seen := make(map[string]bool, len(comp.data))
	for _, d := range comp.data {
		cc.work(1)
		var source string
		if isSignalRef(cc, d) {
			source = signalOf(d)
		} else {
			source = "data('" + m.b().lookupDataSource(d.AsString()).AsString() + "')"
		}
		if !seen[source] {
			seen[source] = true
			fits = append(fits, source)
		}
	}
	if len(fits) == 0 {
		throw("Projection's fit didn't find any data sources")
	}
	fit := fits[0]
	if len(fits) > 1 {
		fit = "[" + joinCC(cc, fits, ", ") + "]"
	}
	o := mk("name", name, "size", size, "fit", mkv("signal", fit))
	spread(o, jsval.Obj(projection))
	return []Value{jsval.Obj(o)}
}
