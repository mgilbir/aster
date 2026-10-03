package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// Scene items are typed structs; encoders and expressions address their
// properties by name. Item.Set and Item.Get do the coercion, so the runtime
// follows the item model as the scene package evolves. This is the list of
// the property names Item.Get answers, for enumerating an item's properties.
var itemPropNames = []string{
	"x", "y", "x2", "y2", "width", "height", "align", "baseline", "fill", "stroke",
	"opacity", "fillOpacity", "strokeOpacity", "strokeWidth", "strokeCap", "strokeJoin",
	"strokeMiterLimit", "strokeDash", "strokeDashOffset", "strokeForeground", "strokeOffset",
	"blend", "cornerRadius", "cornerRadiusTopLeft", "cornerRadiusTopRight",
	"cornerRadiusBottomLeft", "cornerRadiusBottomRight", "startAngle", "endAngle", "padAngle",
	"innerRadius", "outerRadius", "shape", "size", "path", "scaleX", "scaleY", "interpolate",
	"tension", "orient", "defined", "text", "font", "fontSize", "fontWeight", "fontStyle",
	"fontVariant", "dx", "dy", "angle", "radius", "theta", "limit", "lineBreak", "lineHeight",
	"ellipsis", "dir", "url", "aspect", "smooth", "cursor", "href", "tooltip", "description",
	"aria", "ariaRole", "ariaRoleDescription", "zindex", "clip", "noBound",
}

// setItemProp assigns an encoded property and reports whether the stored value
// changed (upstream's `o[name] !== value`). Names the item type does not model
// are kept in Extra by Item.Set, so later reads (`item.name`) still see them.
func setItemProp(it *scene.Item, name string, v jsval.Value) bool {
	old := it.Get(name)
	assignItemProp(it, name, v)
	return !jsval.Equal(old, it.Get(name))
}

// setItemPropWith is setItemProp for a property whose Setter is at hand.
func setItemPropWith(it *scene.Item, name string, set scene.Setter, v jsval.Value) bool {
	old := it.Get(name)
	if err := set(it, v); err != nil {
		failErr(err)
	}
	return !jsval.Equal(old, it.Get(name))
}

// assignItemProp is setItemProp for a caller that does not ask whether the
// value changed.
func assignItemProp(it *scene.Item, name string, v jsval.Value) {
	if _, err := it.Set(name, v); err != nil {
		failErr(err)
	}
}

// getItemProp reads a property of an item as expressions see it.
func getItemProp(it *scene.Item, name string) jsval.Value {
	if it == nil {
		return jsval.Undefined
	}
	if name == "datum" {
		return it.Datum
	}
	return it.Get(name)
}

// eachItemProp calls f for every property of the item that is set.
func eachItemProp(it *scene.Item, f func(name string, v jsval.Value)) {
	for _, name := range itemPropNames {
		if v := it.Get(name); !v.IsUndefined() {
			f(name, v)
		}
	}
	if it.Extra != nil {
		for i := 0; i < it.Extra.Len(); i++ {
			f(it.Extra.KeyAt(i), it.Extra.ValueAt(i))
		}
	}
}
