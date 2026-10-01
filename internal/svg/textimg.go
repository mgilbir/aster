package svg

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func textAnchor(align string) string {
	switch align {
	case "center":
		return "middle"
	case "right":
		return "end"
	}
	return "start"
}

// textItem writes the attributes and content of a <text> element: the anchor
// and transform (rotation about the anchor point, then the dx/dy offset, with
// baseline adjustment done here rather than by the SVG alignment-baseline
// property), the font styling, and the text as one run or one <tspan> per line.
func (r *renderer) textItem(m *scene.Mark, it *scene.Item) {
	w := &r.w
	dx := it.Dx.Zero()
	dy := it.Dy.Zero() + scene.BaselineOffset(it)
	x, y := scene.AnchorPoint(it)

	w.attrRaw("text-anchor", textAnchor(it.Align))
	w.attrName("transform")
	if xv, yv := it.PosValue("x"), it.PosValue("y"); (xv.IsStr() || yv.IsStr()) && it.Radius.Zero() == 0 {
		// A position given a word: the template strings concatenate.
		w.buf = appendWordTransform(w.buf, it, xv, yv, dx, dy)
	} else if it.AngleTruthy() {
		w.buf = appendTranslate(w.buf, x, y)
		w.buf = append(w.buf, " rotate("...)
		w.buf = appendAngle(w.buf, it)
		w.buf = append(w.buf, ')')
		if dx != 0 || dy != 0 {
			w.buf = append(w.buf, ' ')
			w.buf = appendTranslate(w.buf, dx, dy)
		}
	} else {
		w.buf = appendTranslate(w.buf, x+dx, y+dy)
	}
	w.buf = append(w.buf, '"')
	r.style(m, it, "text", it.Fill, it.Stroke)

	line, lines := scene.TextLine(it)
	if lines == nil {
		w.text(r.metrics.TextValue(it, line))
		return
	}
	lh := scene.LineHeight(it)
	for i, l := range lines {
		w.start("tspan")
		if i > 0 {
			w.attrRaw("x", "0")
			w.attrNum("dy", lh)
		}
		w.text(r.metrics.TextValue(it, l))
		w.end()
	}
}

// appendWordTransform is the text transform of attr() in vega-scenegraph's
// marks/text.js when x or y is a string, where `x + dx` joins the strings
// instead of adding.
func appendWordTransform(dst []byte, it *scene.Item, xv, yv jsval.Value, dx, dy float64) []byte {
	join := func(a jsval.Value, b float64) string {
		if a.IsStr() {
			return a.StrValue() + string(scene.AppendNumber(nil, b))
		}
		return string(scene.AppendNumber(nil, jsval.ToNumber(a)+b))
	}
	text := func(v jsval.Value) string {
		if v.IsStr() {
			return v.StrValue()
		}
		return string(scene.AppendNumber(nil, jsval.ToNumber(v)))
	}
	if it.AngleTruthy() {
		dst = append(dst, "translate("...)
		dst = append(dst, text(xv)...)
		dst = append(dst, ',')
		dst = append(dst, text(yv)...)
		dst = append(dst, ") rotate("...)
		dst = appendAngle(dst, it)
		dst = append(dst, ')')
		if dx != 0 || dy != 0 {
			dst = append(dst, ' ')
			dst = appendTranslate(dst, dx, dy)
		}
		return dst
	}
	dst = append(dst, "translate("...)
	dst = append(dst, join(xv, dx)...)
	dst = append(dst, ',')
	dst = append(dst, join(yv, dy)...)
	return append(dst, ')')
}

// imageItem writes an <image>: source, placement (alignment offsets from the
// image size), size and aspect ratio handling.
func (r *renderer) imageItem(m *scene.Mark, it *scene.Item) {
	w := &r.w
	var info ImageInfo
	if b, ok := it.Bitmap(); ok {
		// A canvas is written as it is, not through the URL sanitizer: the
		// data URL is made here, not taken from the specification.
		bw, bh := b.Size()
		info = ImageInfo{Src: b.DataURL(), Width: float64(bw), Height: float64(bh)}
	} else {
		info = r.image(it.URL)
	}
	x, y, iw, ih := scene.ImageGeometry(it, info.Width, info.Height)
	w.attr("xlink:href", info.Src)
	w.attrName("transform")
	w.buf = appendTranslate(w.buf, x, y)
	w.buf = append(w.buf, '"')
	w.attrNum("width", iw)
	w.attrNum("height", ih)
	if it.Aspect.IsFalse() {
		w.attrRaw("preserveAspectRatio", "none")
	} else {
		w.attrRaw("preserveAspectRatio", "xMidYMid")
	}
	r.style(m, it, "image", it.Fill, it.Stroke)
}

func (r *renderer) image(url string) ImageInfo {
	if r.opt.Image != nil {
		return r.opt.Image(url)
	}
	// Without a canvas vega keeps the sanitized URL as the image source and
	// knows nothing of the size; a rejected URL gives an empty source.
	if url == "" {
		return ImageInfo{}
	}
	if src, ok := SanitizeURL(url, URLOptions{}); ok {
		return ImageInfo{Src: src}
	}
	return ImageInfo{}
}
