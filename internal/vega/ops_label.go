package vega

import (
	"maps"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/svg"
	"github.com/mgilbir/aster/internal/transforms/label"
)

func init() {
	transformFactories["label"] = facLabel
}

// facLabel is vega-label's Label transform: it places the items of a text
// mark next to the marks they annotate and writes x, y, opacity, align and
// baseline (or the `as` fields) back into the text items.
func facLabel(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		cmp := p.comparator("sort")
		if !(p.Modified() || len(pulse.add) > 0 || len(pulse.rem) > 0 || (cmp != nil && len(pulse.mod) > 0)) {
			return nil
		}
		size := p.nums("size")
		if len(size) != 2 {
			fail("Size parameter should be specified as a [width, height] array.")
		}
		v := n.g.view
		as := label.DefaultOutput
		if names := p.strs("as"); len(names) > 0 {
			for i := range as {
				if i < len(names) {
					as[i] = names[i]
				}
			}
		}

		var sizeText [2]*string
		for i, e := range p.list("size") {
			if sv, _ := e.(jsval.Value); !sv.IsNullish() && !sv.IsNum() {
				t := v.jsString(sv)
				sizeText[i] = &t
			}
		}
		opts := label.Options{
			Size:       [2]float64{size[0], size[1]},
			SizeText:   sizeText,
			LineAnchor: p.str("lineAnchor"),
			MarkIndex:  markIndex(p.num("markIndex", 0)),
			Method:     p.str("method"),
		}
		if paramTruthy(p.Get("anchor")) { // array(_.anchor || Anchors)
			opts.Anchor = p.strs("anchor")
		}
		if p.has("offset") {
			// Math.max(...offset) converts each entry; the slots are
			// `_[i] || 0`, a Float64Array's number of a truthy entry
			if l := p.list("offset"); l != nil {
				opts.Offset = make([]float64, len(l))
				opts.OffsetSlots = make([]float64, len(l))
				for i, e := range l {
					v, _ := paramValueOf(e)
					opts.Offset[i] = jsval.ToNumber(v)
					if v.IsTruthy() {
						opts.OffsetSlots[i] = opts.Offset[i]
					}
				}
			}
		}
		switch pad := p.Value("padding"); {
		case pad.IsUndefined():
		case pad.IsNullish():
			opts.UnboundedPadding = true
		default:
			opts.Padding = jsval.ToNumber(pad)
		}
		// `_.avoidBaseMark !== false`
		av := p.Value("avoidBaseMark")
		opts.NoAvoidBaseMark = av.IsBool() && !av.IsTruthy()
		avoidUndefined := false
		for _, m := range p.list("avoidMarks") {
			if m == nil {
				avoidUndefined = true // the mark's items: undefined, a mark that never ran
			}
			if items, _ := m.([]*scene.Item); len(items) > 0 {
				ai := make([]any, len(items))
				for i, it := range items {
					ai[i] = it
				}
				opts.AvoidMarks = append(opts.AvoidMarks, ai)
			}
		}

		labels := make([]label.Label, len(pulse.items))
		for i, t := range pulse.items {
			l := &labels[i]
			l.Ref = t
			// d.fontSize, d.x, d.y as given, read by arithmetic: null is 0.
			l.FontSize = jsval.ToNumber(t.Get("fontSize"))
			l.X, l.Y = jsval.ToNumber(t.Get("x")), jsval.ToNumber(t.Get("y"))
			if !t.Text().IsNullish() {
				l.Text = t.Walker().String(t.Text())
			}
			l.Base = labelBase(v, t, opts.MarkIndex)
		}
		if avoidUndefined && len(labels) > 0 && !naiveGroupArea(labels[0].Base, opts.MarkIndex, opts.Method) {
			// markBitmaps: avoidMarks.forEach(items => draw(context, items)),
			// and draw reads items.length first.
			fail("TypeError: Cannot read properties of undefined (reading 'length')")
		}
		if cmp != nil {
			views := make(map[*scene.Item]jsval.Value, len(pulse.items))
			for _, t := range pulse.items {
				views[t] = v.itemTuple(t)
			}
			opts.Sort = func(a, b *label.Label) float64 {
				return float64(cmp(views[a.Ref.(*scene.Item)], views[b.Ref.(*scene.Item)]))
			}
		}
		opts.TextWidth = func(l *label.Label) float64 {
			return v.bounder.Metrics.Width(l.Ref.(*scene.Item), l.Text)
		}
		opts.Rasterizer = &labelPainter{v: v}

		placed, err := label.Layout(n.g.ctx, labels, opts)
		if err != nil {
			failErr(err)
		}
		for _, pl := range placed {
			t := pl.Label.Ref.(*scene.Item)
			x, y := jsval.Undefined, jsval.Undefined
			if pl.HasPos {
				x, y = jsval.Num(pl.X), jsval.Num(pl.Y)
			}
			align, baseline := jsval.Undefined, jsval.Undefined
			if pl.Align != "" {
				align = jsval.Str(pl.Align)
			}
			if pl.Baseline != "" {
				baseline = jsval.Str(pl.Baseline)
			}
			setItemProp(t, as[0], x)
			setItemProp(t, as[1], y)
			setItemProp(t, as[2], jsval.Num(pl.Opacity))
			setItemProp(t, as[3], align)
			setItemProp(t, as[4], baseline)
		}
		return reflowPulse(pulse)
	}), nil
}

// labelBase describes the item a text item annotates: the datum of a text
// mark derived from another mark is the view of that mark's item; with plain
// data there is no base (nil).
func labelBase(v *runView, t *scene.Item, markIndex int) *label.Base {
	obj := t.Datum.ObjValue()
	if obj == nil {
		return nil
	}
	b := v.viewItem[obj]
	if b == nil || b.Mark == nil {
		return nil
	}
	base := &label.Base{
		MarkType: strictTypeName(b.Mark),
		X:        b.X.Val(), Y: b.Y.Val(),
		Bounds: label.Box{X1: b.Bounds.X1, Y1: b.Bounds.Y1, X2: b.Bounds.X2, Y2: b.Bounds.Y2},
		Ref:    b,
	}
	if b.Mark.IsGroup() {
		base.Marks = make([]label.SubMark, len(b.Items))
		for i, m := range b.Items {
			base.Marks[i].MarkType = strictTypeName(m)
			if i != markIndex {
				continue // only the labelled mark's points are read
			}
			pts := make([]label.Point, len(m.Items))
			for j, it := range m.Items {
				pts[j] = label.Point{
					X: it.X.Val(), Y: it.Y.Val(), X2: it.X2.Val(), Y2: it.Y2.Val(),
					HasX2: it.X2.Set(), HasY2: it.Y2.Set(),
				}
			}
			base.Marks[i].Points = pts
		}
	}
	return base
}

// labelPainter is the Rasterizer behind the label transform. Upstream draws
// the marks to avoid with vega-scenegraph's canvas renderer onto a canvas of
// the layout size (no padding, no group translation: items are painted at
// their own coordinates) and treats every pixel with non-zero alpha as
// occupied. The painter renders the same items to SVG with the engine's own
// renderer and rasterizes that; a pixel counts when its alpha is non-zero.
//
// Approximation: cairo and this rasterizer disagree on pixels that a shape
// touches with only a sliver of coverage (cairo flattens curves coarsely and
// samples 15 sub-rows per pixel; resvg-style coverage, which the rasterizer
// reproduces, takes 4), and a single such pixel can move a label. The mask is
// therefore taken from a 4x supersampled rendering, where a pixel counts when
// any of its 16 samples is covered; that contains the slivers cairo reports.
// The rasterizer reports those pixels (raster.RenderCoverage) without
// holding the supersampled canvas.
// It is not cairo's exact coverage: on an unusual shape a sliver could still
// differ. Text is shaped with the engine's fonts, not the canvas's.
type labelPainter struct{ v *runView }

func (lp *labelPainter) Draw(m *label.Mask, items []any, outline bool) {
	list := make([]*scene.Item, 0, len(items))
	for _, x := range items {
		if it, _ := x.(*scene.Item); it != nil {
			list = append(list, it)
		}
	}
	lp.draw(m, list, outline, 0)
}

// draw mirrors markBitmaps' draw(): groups are flattened (child marks are
// painted in place, ignoring the group's origin), anything else is painted by
// its mark type.
func (lp *labelPainter) draw(m *label.Mask, items []*scene.Item, outline bool, depth int) {
	if len(items) == 0 || items[0].Mark == nil || depth > maxLabelGroupDepth {
		return
	}
	typ := items[0].Mark.Type
	if !items[0].Mark.IsGroup() {
		lp.paint(m, typ, items, outline)
		return
	}
	for _, g := range items {
		for _, child := range g.Items {
			lp.draw(m, child.Items, outline, depth+1)
		}
	}
}

// labelSupersample is the linear oversampling of the painted marks and
// maxLabelSamples the most samples one painted mark may take.
const (
	labelSupersample = 4
	maxLabelSamples  = 1 << 24
)

// maxLabelGroupDepth bounds the recursion through nested groups.
const maxLabelGroupDepth = 64

func (lp *labelPainter) paint(m *label.Mask, typ scene.MarkType, items []*scene.Item, outline bool) {
	v := lp.v
	if err := v.ctx.Err(); err != nil {
		failErr(err)
	}
	sg := scene.New()
	mark := sg.AddMark(scene.MarkDef{Type: typ}, nil, -1)
	mark.Items = make([]*scene.Item, len(items))
	for i, src := range items {
		c := *src
		c.Mark = mark
		canvasOpacities(&c)
		if outline {
			outlineItem(&c)
		}
		mark.Items[i] = &c
	}
	// Supersample so that every pixel a shape touches counts, however small
	// the coverage (see labelPainter); huge layouts fall back to fewer
	// samples to bound the pixels rasterized.
	ss := labelSupersample
	for ss > 1 && m.Width*m.Height > maxLabelSamples/(ss*ss) {
		ss--
	}
	doc, err := svg.Render(v.ctx, sg, svg.Options{
		Width: float64(m.Width), Height: float64(m.Height),
		Measurer: v.bounder.Measurer,
		MaxBytes: int(min(v.limits.MaxStringBytes, math.MaxInt32)),
	})
	if err != nil {
		failErr(err)
	}
	// The bitmap's work budget is the PNG budget at scale 1 for its pixels,
	// as for any image drawn at its own pixels: the supersampling is the
	// transform's, not a scale the caller asked for, and does not raise it.
	// Its pixel memory is bounded as a PNG's is (WithMemoryLimit's).
	lim := raster.Limits{MaxPixelOps: raster.DefaultMaxPixelOps, PixelOpsPerPixel: raster.DefaultPixelOpsPerPixel, MaxCanvasBytes: v.limits.RasterCanvasBytes}
	if v.limits.RasterPixelOps > 0 {
		lim.MaxPixelOps = v.limits.RasterPixelOps // WithPixelWorkLimit's
	}
	// The rasterizer reads bytes: the copy of the document, as long as the
	// marks make it, is charged to the call's ledger while it lives.
	led := budget.LedgerFrom(v.ctx)
	if err := led.Alloc(int64(len(doc))); err != nil {
		failErr(err)
	}
	// Only which ss x ss blocks of samples are left with alpha is read: the
	// rasterizer reports that without a canvas of the samples.
	cov, err := raster.RenderCoverage([]byte(doc), ss, raster.Options{Shaper: v.shaper, Context: v.ctx, Scale: float64(ss), Limits: lim})
	led.Free(int64(len(doc)))
	if err != nil {
		failErr(err)
	}
	for y := range min(cov.H, m.Height) {
		if y&255 == 0 {
			if err := v.ctx.Err(); err != nil {
				failErr(err)
			}
		}
		cov.Row(y, func(x int) { m.Set(x, y) })
	}
}

// canvasOpacities gives the copy of an item the opacities upstream's canvas
// draws it with: `opacity *= item.fillOpacity` multiplies a value given as
// a string or an array as the number it converts to ("" is 0, which draws
// nothing), where the SVG document the bitmap is drawn from would leave an
// attribute it cannot parse at its default.
func canvasOpacities(it *scene.Item) {
	// A NaN opacity draws nothing on a canvas (NaN > 0 is false), which an
	// SVG attribute "NaN" does not say: it is written as 0.
	for _, n := range [...]*scene.Num{&it.Opacity, &it.FillOpacity, &it.StrokeOpacity} {
		if n.Set() && n.Val() != n.Val() {
			*n = scene.N(0)
		}
	}
	if it.Raw == nil {
		return
	}
	cloned := false
	for _, p := range [...]struct {
		name string
		n    *scene.Num
	}{{"opacity", &it.Opacity}, {"fillOpacity", &it.FillOpacity}, {"strokeOpacity", &it.StrokeOpacity}} {
		v, ok := it.Raw[p.name]
		if !ok || v.IsNullish() {
			continue
		}
		if !cloned {
			// it is a copy of the item, whose Raw it shares.
			it.Raw, cloned = maps.Clone(it.Raw), true
		}
		f := jsval.ToNumber(v)
		if f != f {
			f = 0
		}
		*p.n = scene.N(f)
		delete(it.Raw, p.name)
	}
}

// outlineItem is markBitmaps' prepare(): an item with a stroke or a fill that
// is not fully transparent is redrawn as an opaque black stroke (keeping its
// own stroke width and dash) with no fill; other items are drawn as they are.
func outlineItem(it *scene.Item) {
	// item.strokeOpacity !== 0: strict, so "0" is not 0.
	strokes := it.Stroke.Truthy() && !it.StrictNum("strokeOpacity", it.StrokeOpacity, 0)
	fills := it.Fill.Truthy() && !it.StrictNum("fillOpacity", it.FillOpacity, 0)
	if strokes || fills {
		// it is a copy of the item, whose Raw it shares until it changes it.
		it.Raw = maps.Clone(it.Raw)
		it.StrokeOpacity = scene.N(1)
		it.ForgetRaw("strokeOpacity")
		it.Stroke = scene.Color("#000")
		it.ForgetRaw("stroke")
		it.FillOpacity = scene.N(0)
		it.ForgetRaw("fillOpacity")
	}
}

// markIndex is `_.markIndex || 0` as an index into a group's marks: one that
// is not a whole number in range reads no mark (-1), decided before it is an
// int.
func markIndex(f float64) int {
	switch {
	case f != f:
		return 0
	case f < 0 || f > 1<<30 || f != math.Trunc(f):
		return -1
	}
	return int(f)
}

// strictTypeName is a mark's type as code that compares it with ===
// reads it: its name, and for a type given as another value than a string
// (scene.Mark.LooseType) a text that equals no type name, though it is
// truthy as that value is.
func strictTypeName(m *scene.Mark) string {
	if m.LooseType {
		return "\x00" + m.Type.String()
	}
	return m.Type.String()
}

// naiveGroupArea is labelLayout's isNaiveGroupArea: labels of a group of
// areas placed by the naive method, which does not draw the marks to avoid.
func naiveGroupArea(b *label.Base, markIndex int, method string) bool {
	if b == nil || b.MarkType != "group" || markIndex < 0 || markIndex >= len(b.Marks) {
		return false
	}
	return b.Marks[markIndex].MarkType == "area" && (method == "" || method == "naive")
}
