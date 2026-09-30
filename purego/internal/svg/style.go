package svg

import (
	"github.com/mgilbir/aster/purego/internal/scene"
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
		w.attrRaw("pointer-events", "none")
	}
	if tag == "bgfore" {
		if m.NonInteractive {
			w.attrRaw("pointer-events", "none")
		}
		w.attrRaw("display", "none")
		if !fill.IsNull() {
			return
		}
	}

	var smoothOff bool
	if tag == "image" && it.Smooth.IsFalse() {
		smoothOff = true
	}

	if tag == "text" {
		w.attr("font-family", scene.FontFamily(it, false))
		w.attrName("font-size")
		w.buf = scene.AppendNumber(w.buf, scene.FontSize(it))
		w.buf = append(w.buf, "px\""...)
		if it.FontStyle != "" {
			w.attr("font-style", it.FontStyle)
		}
		if it.FontVariant != "" {
			w.attr("font-variant", it.FontVariant)
		}
		if it.FontWeight != "" {
			w.attr("font-weight", it.FontWeight)
		}
	}

	r.paintAttr("fill", fill)
	r.itemNumAttr(it, "fillOpacity", "fill-opacity", it.FillOpacity)
	r.paintAttr("stroke", stroke)
	r.itemNumAttr(it, "strokeOpacity", "stroke-opacity", it.StrokeOpacity)
	r.itemNumAttr(it, "strokeWidth", "stroke-width", it.StrokeWidth)
	if it.StrokeCap != "" {
		w.attr("stroke-linecap", it.StrokeCap)
	}
	if it.StrokeJoin != "" {
		w.attr("stroke-linejoin", it.StrokeJoin)
	}
	switch {
	case it.StrokeDashStr != "":
		w.attr("stroke-dasharray", it.StrokeDashStr)
	case it.StrokeDash != nil:
		// An array coerces to its comma-joined elements; empty gives "".
		w.attrName("stroke-dasharray")
		for i, d := range it.StrokeDash {
			if i > 0 {
				w.buf = append(w.buf, ',')
			}
			w.buf = scene.AppendNumber(w.buf, d)
		}
		w.buf = append(w.buf, '"')
	}
	r.itemNumAttr(it, "strokeDashOffset", "stroke-dashoffset", it.StrokeDashOffset)
	r.itemNumAttr(it, "strokeMiterLimit", "stroke-miterlimit", it.StrokeMiterLimit)
	r.itemNumAttr(it, "opacity", "opacity", it.Opacity)

	if smoothOff || it.Blend != "" {
		w.attrName("style")
		sep := false
		if smoothOff {
			w.buf = append(w.buf, "image-rendering: optimizeSpeed; image-rendering: pixelated;"...)
			sep = true
		}
		if it.Blend != "" {
			if sep {
				w.buf = append(w.buf, ' ')
			}
			w.buf = append(w.buf, "mix-blend-mode: "...)
			w.buf = appendEscaped(w.buf, it.Blend, true)
			w.buf = append(w.buf, ';')
		}
		w.buf = append(w.buf, '"')
	}
}

// itemNumAttr is numAttr for an item property, writing a non-numeric value
// the property was given as String(value), as upstream's renderer does.
func (r *renderer) itemNumAttr(it *scene.Item, prop, name string, n scene.Num) {
	if v, ok := it.RawValue(prop); ok {
		r.w.attr(name, v.AsString())
		return
	}
	r.numAttr(name, n)
}

// appendAngle writes an item's angle as upstream interpolates it into
// rotate(...): the raw value when it was not a number.
func appendAngle(dst []byte, it *scene.Item) []byte {
	if v, ok := it.RawValue("angle"); ok {
		return append(dst, v.AsString()...)
	}
	return scene.AppendNumber(dst, it.Angle.Val())
}

func (r *renderer) numAttr(name string, n scene.Num) {
	if n.Set() {
		r.w.attrNum(name, n.Val())
	}
}

// paintAttr writes a fill or stroke: colours pass through ("transparent" is not
// legal SVG, so it is skipped in favour of the default none) and gradients
// become url references, registering their definition.
func (r *renderer) paintAttr(name string, p scene.Paint) {
	if !p.Present() {
		return
	}
	if g := p.Gradient(); g != nil {
		r.w.attrRaw(name, r.gradientRef(g))
		return
	}
	if p.Str() == "transparent" {
		return
	}
	r.w.attr(name, p.Str())
}
