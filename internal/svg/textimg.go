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

// inheritedAnchor is textAlign[item.align] on a plain object for a name every
// object inherits: that member, whose text is the attribute.
func inheritedAnchor(align string) (string, bool) {
	switch align {
	case "left", "center", "right":
		return "", false
	}
	return jsval.InheritedText(align)
}

// textItem writes the attributes and content of a <text> element: the anchor
// and transform (rotation about the anchor point, then the dx/dy offset, with
// baseline adjustment done here rather than by the SVG alignment-baseline
// property), the font styling, and the text as one run or one <tspan> per line.
func (r *renderer) textItem(m *scene.Mark, it *scene.Item) error {
	w := &r.w
	if s, ok := inheritedAnchor(it.Align); ok {
		w.attr("text-anchor", s)
	} else {
		w.attrLit("text-anchor", textAnchor(it.Align))
	}
	w.attrOpen("transform")
	if it.Raw != nil {
		w.textTransformJS(it)
	} else {
		w.textTransform(it)
	}
	w.attrClose()
	r.style(m, it, "text", it.Fill, it.Stroke)

	// The lines are read in place, one tspan at a time, so the writer's
	// bound stops a text of many lines as it grows.
	lines, err := scene.TextLinesErr(it)
	if err != nil {
		return err
	}
	if !lines.Multi {
		w.text(r.metrics.TextValue(it, lines.Line()))
		return nil
	}
	lh, lhGiven := it.RawValue("lineHeight")
	lhGiven = lhGiven && !lh.IsNullish()
	lhNum := scene.LineHeight(it)
	for i := 0; w.err == nil; i++ {
		l, ok := lines.Next()
		if !ok {
			break
		}
		w.start("tspan")
		if i > 0 {
			w.attrLit("x", "0")
			// lineHeight(item): the value as given, else fontSize + 2.
			if lhGiven {
				w.attrJS("dy", lh)
			} else {
				w.attrNum("dy", lhNum)
			}
		}
		w.text(r.metrics.TextValue(it, l))
		w.end()
	}
	return nil
}

// textTransform is the transform of attr() in vega-scenegraph's
// marks/text.js for an item whose properties are all of their types.
func (w *writer) textTransform(it *scene.Item) {
	dx := it.Dx().Zero()
	dy := it.Dy().Zero() + scene.BaselineOffset(it)
	x, y := scene.AnchorPoint(it)
	if it.AngleTruthy() {
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
}

// textTransformJS is the text transform with the values as given: `x + dx`
// joins a string instead of adding, and the words are spec or data text,
// written escaped.
func (w *writer) textTransformJS(it *scene.Item) {
	dx, dy := scene.TextOffsetsJS(it)
	x, y := scene.AnchorPointJS(it)
	if it.AngleTruthy() {
		w.translateJS(x, y)
		w.valLit(" ")
		w.rotate(it)
		if dx.IsTruthy() || dy.IsTruthy() {
			w.valLit(" ")
			w.translateJS(dx, dy)
		}
		return
	}
	w.valLit("translate(")
	w.plusJS(x, dx)
	w.valLit(",")
	w.plusJS(y, dy)
	w.valLit(")")
}

// plusJS writes String(a + b): the text of a and of b one after the other when
// either is a string, array, object or date (a concatenation, each written
// as it is, so a large array is bounded as it grows), else their sum.
func (w *writer) plusJS(a, b jsval.Value) {
	concat := func(v jsval.Value) bool { return v.IsStr() || v.IsArr() || v.IsObj() || v.IsTimestamp() }
	if concat(a) || concat(b) {
		w.valJS(a)
		w.valJS(b)
		return
	}
	w.valNum(jsval.ToNumber(a) + jsval.ToNumber(b))
}

// translateJS is 'translate(' + x + ',' + y + ')' for values.
func (w *writer) translateJS(x, y jsval.Value) {
	w.valLit("translate(")
	w.valJS(x)
	w.valLit(",")
	w.valJS(y)
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
		// vega-loader's sanitize rejects a URL that is not a string.
		url := it.URL()
		if v, ok := it.RawValue("url"); ok && !v.IsStr() {
			url = ""
		}
		info = r.image(url, !it.Width.Set() || !it.Height.Set())
	}
	x, y, iw, ih := scene.ImageGeometry(it, info.Width, info.Height)
	w.attr("xlink:href", info.Src)
	w.attrOpen("transform")
	w.translate(x, y)
	w.attrClose()
	if it.Raw != nil {
		// emit('width', w): the size as given.
		wv, hv := scene.ImageSizeJS(it, info.Width, info.Height)
		w.attrOpen("width")
		w.valJS(wv)
		w.attrClose()
		w.attrOpen("height")
		w.valJS(hv)
		w.attrClose()
	} else {
		w.attrNum("width", iw)
		w.attrNum("height", ih)
	}
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
