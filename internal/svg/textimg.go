package svg

import (
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
	if it.AngleTruthy() {
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
