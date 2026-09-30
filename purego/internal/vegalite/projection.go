package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
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
		split: &split{explicit, mk("name", name)}, specifiedProjection: specified, size: size, data: data, hasData: hasData,
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
	proj := replaceExprRef(u.specifiedProjection, 0)
	fit := !(proj.IsObj() && (!proj.Get("scale").IsNullish() || !proj.Get("translate").IsNullish()))
	var size, data []Value
	if fit {
		size = []Value{u.getSizeSignalRef("width"), u.getSizeSignalRef("height")}
		data = gatherFitData(u)
	}
	comp := newProjectionComponent(u.projectionName(true), jsval.Obj(merged(replaceExprRef(u.config.Get("projection"), 0), proj)), size, data, fit)
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

func mergeProjectionIfNoConflict(first, second *projectionComponent) *projectionComponent {
	allShared := true
	for _, prop := range projectionProperties {
		fe, se := first.explicit.Has(prop), second.explicit.Has(prop)
		if !fe && !se {
			continue
		}
		if fe && se && deepEqual(first.get(prop), second.get(prop)) {
			continue
		}
		allShared = false
		break
	}
	sizeEq := len(first.size) == len(second.size)
	if sizeEq {
		for i := range first.size {
			if !deepEqual(first.size[i], second.size[i]) {
				sizeEq = false
			}
		}
	}
	if sizeEq {
		if allShared {
			return first
		} else if first.explicit.Len() == 0 {
			return second
		} else if second.explicit.Len() == 0 {
			return first
		}
	}
	return nil
}

func parseNonUnitProjections(m Model) *projectionComponent {
	kids := m.children()
	if len(kids) == 0 {
		return nil
	}
	var nonUnit *projectionComponent
	for _, c := range kids {
		parseProjection(c)
	}
	mergable := true
	for _, c := range kids {
		p := c.b().comp.projection
		if p == nil {
			continue
		}
		if nonUnit == nil {
			nonUnit = p
			continue
		}
		mg := mergeProjectionIfNoConflict(nonUnit, p)
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
	if isLayerModel(m) || isConcatModel(m) {
		out := assembleProjectionForModel(m)
		for _, c := range m.children() {
			out = append(out, c.b().assembleProjections()...)
		}
		return out
	}
	return assembleProjectionForModel(m)
}

func assembleProjectionForModel(m Model) []Value {
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
	size := mkv("signal", "["+strings.Join(sizes, ", ")+"]")
	var fits []string
	for _, d := range comp.data {
		var source string
		if isSignalRef(d) {
			source = signalOf(d)
		} else {
			source = "data('" + m.b().lookupDataSource(d.AsString()) + "')"
		}
		if !contains(fits, source) {
			fits = append(fits, source)
		}
	}
	if len(fits) == 0 {
		throw("Projection's fit didn't find any data sources")
	}
	fit := fits[0]
	if len(fits) > 1 {
		fit = "[" + strings.Join(fits, ", ") + "]"
	}
	o := mk("name", name, "size", size, "fit", mkv("signal", fit))
	spread(o, jsval.Obj(projection))
	return []Value{jsval.Obj(o)}
}
