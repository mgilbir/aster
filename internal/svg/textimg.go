package svg

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func textAnchor(align string) lit {
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
	dx := it.Dx().Zero()
	dy := it.Dy().Zero() + scene.BaselineOffset(it)
	x, y := scene.AnchorPoint(it)

	w.attrLit("text-anchor", textAnchor(it.Align))
	w.attrOpen("transform")
	if xv, yv := it.PosValue("x"), it.PosValue("y"); (xv.IsStr() || yv.IsStr()) && it.Radius().Zero() == 0 {
		w.wordTransform(it, xv, yv, dx, dy)
	} else if it.AngleTruthy() {
		w.translate(x, y)
		w.valLit(" ")
		w.rotate(it)
		if dx != 0 || dy != 0 {
			w.valLit(" ")
			w.translate(dx, dy)
		}
	} else {
		w.translate(x+dx, y+dy)
	}
	w.attrClose()
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
			w.attrLit("x", "0")
			w.attrNum("dy", lh)
		}
		w.text(r.metrics.TextValue(it, l))
		w.end()
	}
}

// wordTransform is the text transform of attr() in vega-scenegraph's
// marks/text.js when x or y is a string, where `x + dx` joins the strings
// instead of adding. The words are spec or data text, written escaped.
func (w *writer) wordTransform(it *scene.Item, xv, yv jsval.Value, dx, dy float64) {
	// join is `a + b`: a string followed by the number, or the sum.
	join := func(a jsval.Value, b float64) {
		if a.IsStr() {
			w.valStr(a.StrValue())
			w.valNum(b)
			return
		}
		w.valNum(jsval.ToNumber(a) + b)
	}
	// text is the anchor coordinate as translate() prints it.
	text := func(v jsval.Value) {
		if v.IsStr() {
			w.valStr(v.StrValue())
			return
		}
		w.valNum(jsval.ToNumber(v))
	}
	w.valLit("translate(")
	if it.AngleTruthy() {
		text(xv)
		w.valLit(",")
		text(yv)
		w.valLit(") ")
		w.rotate(it)
		if dx != 0 || dy != 0 {
			w.valLit(" ")
			w.translate(dx, dy)
		}
		return
	}
	join(xv, dx)
	w.valLit(",")
	join(yv, dy)
	w.valLit(")")
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
		info = r.image(it.URL(), !it.Width.Set() || !it.Height.Set())
	}
	x, y, iw, ih := scene.ImageGeometry(it, info.Width, info.Height)
	w.attr("xlink:href", info.Src)
	w.attrOpen("transform")
	w.translate(x, y)
	w.attrClose()
	w.attrNum("width", iw)
	w.attrNum("height", ih)
	if it.Aspect().IsFalse() {
		w.attrLit("preserveAspectRatio", "none")
	} else {
		w.attrLit("preserveAspectRatio", "xMidYMid")
	}
	r.style(m, it, "image", it.Fill, it.Stroke)
}

func (r *renderer) image(url string, size bool) ImageInfo {
	if r.opt.Image != nil {
		return r.opt.Image(url, size)
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
