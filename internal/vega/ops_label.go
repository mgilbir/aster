package vega

import (
	"math"

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

		opts := label.Options{
			Size:       [2]float64{size[0], size[1]},
			LineAnchor: p.str("lineAnchor"),
			MarkIndex:  int(p.num("markIndex", 0)), // `_.markIndex || 0`
			Method:     p.str("method"),
		}
		if p.has("anchor") {
			opts.Anchor = p.strs("anchor")
		}
		if p.has("offset") {
			opts.Offset = p.nums("offset")
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
		for _, m := range p.list("avoidMarks") {
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
			l.FontSize = t.FontSize.Val()
			l.X, l.Y = t.X.Val(), t.Y.Val()
			if !t.Text.IsNullish() {
				l.Text = t.Text.AsString()
			}
			l.Base = labelBase(v, t, opts.MarkIndex)
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
		MarkType: b.Mark.Type.String(),
		X:        b.X.Val(), Y: b.Y.Val(),
		Bounds: label.Box{X1: b.Bounds.X1, Y1: b.Bounds.Y1, X2: b.Bounds.X2, Y2: b.Bounds.Y2},
		Ref:    b,
	}
	if b.Mark.Type == scene.MarkGroup {
		base.Marks = make([]label.SubMark, len(b.Items))
		for i, m := range b.Items {
			base.Marks[i].MarkType = m.Type.String()
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
	if typ != scene.MarkGroup {
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
	img, err := raster.Render([]byte(doc), raster.Options{Shaper: v.shaper, Context: v.ctx, Scale: float64(ss)})
	if err != nil {
		failErr(err)
	}
	b := img.Bounds()
	for y := 0; y < b.Dy() && y/ss < m.Height; y++ {
		if y&255 == 0 {
			if err := v.ctx.Err(); err != nil {
				failErr(err)
			}
		}
		row := img.Pix[y*img.Stride:]
		for x := 0; x < b.Dx() && x/ss < m.Width; x++ {
			if row[x*4+3] != 0 {
				m.Set(x/ss, y/ss)
			}
		}
	}
}

// outlineItem is markBitmaps' prepare(): an item with a stroke or a fill that
// is not fully transparent is redrawn as an opaque black stroke (keeping its
// own stroke width and dash) with no fill; other items are drawn as they are.
func outlineItem(it *scene.Item) {
	strokes := it.Stroke.Truthy() && it.StrokeOpacity.Val() != 0
	fills := it.Fill.Truthy() && it.FillOpacity.Val() != 0
	if strokes || fills {
		it.StrokeOpacity = scene.N(1)
		it.Stroke = scene.Color("#000")
		it.FillOpacity = scene.N(0)
	}
}
