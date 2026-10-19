package svg

import (
	"github.com/mgilbir/aster/internal/scene"
)

// style writes the presentation attributes of an item element, in vega's fixed
// order: pointer-events and display for the group background/foreground paths,
// font attributes for text, then fill, fill-opacity, stroke, stroke-opacity,
// stroke-width, stroke-linecap, stroke-linejoin, stroke-dasharray,
// stroke-dashoffset, stroke-miterlimit and opacity, and finally the `style`
// attribute (image smoothing and blend mode).
//
// tag is the element kind ("path", "line", "text", "image") or one of the
// group pseudo-tags "bgrect" and "bgfore". fill and stroke are the paints in
// effect, which for group boxes differ from the item's own while the foreground
// stroke is being separated.
func (r *renderer) style(m *scene.Mark, it *scene.Item, tag string, fill, stroke scene.Paint) {
	w := &r.w
	if tag == "bgrect" && m.NonInteractive {
		w.attrLit("pointer-events", "none")
	}
	if tag == "bgfore" {
		if m.NonInteractive {
			w.attrLit("pointer-events", "none")
		}
		w.attrLit("display", "none")
		if !fill.IsNull() {
			return
		}
	}

	var smoothOff bool
	if tag == "image" && it.Smooth().IsFalse() {
		smoothOff = true
	}

	if tag == "text" {
		if v, ok := it.RawValue("font"); ok && v.IsTruthy() {
			w.attrJS("font-family", v) // `font || 'sans-serif'` keeps a truthy array
		} else {
			w.attr("font-family", scene.FontFamily(it, false))
		}
		w.attrOpen("font-size")
		w.valNum(scene.FontSize(it))
		w.valLit("px")
		w.attrClose()
		if it.FontStyle() != "" {
			w.attr("font-style", it.FontStyle())
		}
		if it.FontVariant() != "" {
			w.attr("font-variant", it.FontVariant())
		}
		if it.FontWeight() != "" {
			w.attr("font-weight", it.FontWeight())
		}
	}

	r.paintAttr("fill", fill)
	r.itemNumAttr(it, "fillOpacity", "fill-opacity", it.FillOpacity)
	r.paintAttr("stroke", stroke)
	r.itemNumAttr(it, "strokeOpacity", "stroke-opacity", it.StrokeOpacity)
	r.itemNumAttr(it, "strokeWidth", "stroke-width", it.StrokeWidth)
	if it.StrokeCap() != "" {
		w.attr("stroke-linecap", it.StrokeCap())
	}
	if it.StrokeJoin() != "" {
		w.attr("stroke-linejoin", it.StrokeJoin())
	}
	if !it.StrokeDash().IsNullish() {
		// String(value): an array joins with commas, an empty one gives "".
		w.attrJS("stroke-dasharray", it.StrokeDash())
	}
	r.itemNumAttr(it, "strokeDashOffset", "stroke-dashoffset", it.StrokeDashOffset())
	r.itemNumAttr(it, "strokeMiterLimit", "stroke-miterlimit", it.StrokeMiterLimit())
	r.itemNumAttr(it, "opacity", "opacity", it.Opacity)

	blend := blendMode(it.Blend())
	if smoothOff || blend != "" {
		w.attrOpen("style")
		sep := false
		if smoothOff {
			w.valLit("image-rendering: optimizeSpeed; image-rendering: pixelated;")
			sep = true
		}
		if blend != "" {
			if sep {
				w.valLit(" ")
			}
			w.valLit("mix-blend-mode: ")
			w.valLit(blend)
			w.valLit(";")
		}
		w.attrClose()
	}
}

// itemNumAttr is numAttr for an item property, writing a non-numeric value
// the property was given as String(value), as upstream's renderer does.
func (r *renderer) itemNumAttr(it *scene.Item, prop string, name lit, n scene.Num) {
	if v, ok := it.RawValue(prop); ok {
		r.w.attrJS(name, v)
		return
	}
	r.numAttr(name, n)
}

func (r *renderer) numAttr(name lit, n scene.Num) {
	if n.Set() {
		r.w.attrNum(name, n.Val())
	}
}

// paintAttr writes a fill or stroke: colours pass through ("transparent" is not
// legal SVG, so it is skipped in favour of the default none) and gradients
// become url references, registering their definition.
func (r *renderer) paintAttr(name lit, p scene.Paint) {
	if !p.Present() {
		return
	}
	if g := p.Gradient(); g != nil {
		r.w.attr(name, r.gradientRef(g))
		return
	}
	if p.Str() == "transparent" {
		return
	}
	r.w.attr(name, p.Str())
}
