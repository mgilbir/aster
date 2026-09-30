package layout

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// legendConfig looks up legend layout settings (config.legend.layout): a key
// is searched in the block of the legend's orient first, then at the top level.
type legendConfig struct {
	cfg, orient jsval.Value
}

func newLegendConfig(cfg jsval.Value, orient string) legendConfig {
	return legendConfig{cfg: cfg, orient: cfg.Get(orient)}
}

func (c legendConfig) get(key string) jsval.Value {
	if v := c.orient.Get(key); !v.IsNullish() {
		return v
	}
	return c.cfg.Get(key)
}

func (c legendConfig) getOr(key string, d jsval.Value) jsval.Value {
	if v := c.get(key); !v.IsNullish() {
		return v
	}
	return d
}

// legendOffset is the largest offset specified directly on the legends, else
// the configured one.
func legendOffset(legends []*scene.Item, dflt float64) float64 {
	max := math.Inf(-1)
	for _, it := range legends {
		if v := extra(it, "offset"); !v.IsNullish() {
			max = jsMax(max, jsval.ToNumber(v))
		}
	}
	if max > math.Inf(-1) || math.IsNaN(max) {
		return max
	}
	return dflt
}

// legendParams computes the grid layout options that stack the legends of one
// orient: relative to the bounds of the axes (xb, yb) and to the group size
// (w, h), anchored at the legend anchor.
func legendParams(g []*scene.Item, orient string, config jsval.Value, xb, yb *scene.Bounds, w, h float64) gridOptions {
	c := newLegendConfig(config, orient)
	offset := legendOffset(g, jsval.ToNumber(c.getOr("offset", jsval.Int(0))))
	anchor := c.getOr("anchor", jsval.Str(start)).AsString()
	frame := c.getOr("frame", jsval.Str(groupFrame)).AsString()
	mult := 0.0
	switch anchor {
	case end:
		mult = 1
	case middle:
		mult = 0.5
	}
	var ax, ay float64
	if frame == boundsFrame {
		ax = xb.X1 + float64(mult*xb.Width())
		ay = yb.Y1 + float64(mult*yb.Height())
	} else {
		span := w
		if span == 0 || math.IsNaN(span) {
			span = yb.Width() + float64(2*yb.X1)
		}
		ax = float64(mult * span)
		span = h
		if span == 0 || math.IsNaN(span) {
			span = xb.Height() + float64(2*xb.Y1)
		}
		ay = float64(mult * span)
	}

	p := gridOptions{
		alignCol: each,
		alignRow: each,
		bounds:   c.getOr("bounds", jsval.Str(flush)).AsString(),
		columns:  len(g),
	}
	if c.get("direction").IsStr() && c.get("direction").StrValue() == "vertical" {
		p.columns = 1
	}
	margin := c.getOr("margin", jsval.Int(8))
	p.padCol = pairNum(margin, "column")
	p.padRow = pairNum(margin, "row")
	center := c.get("center")
	p.centerCol = pairValue(center, "column").IsTruthy()
	p.centerRow = pairValue(center, "row").IsTruthy()

	switch orient {
	case left:
		p.anchor = gridAnchor{x: math.Floor(xb.X1) - offset, column: end, y: ay, row: anchor}
	case right:
		p.anchor = gridAnchor{x: math.Ceil(xb.X2) + offset, y: ay, row: anchor}
	case top:
		p.anchor = gridAnchor{y: math.Floor(yb.Y1) - offset, row: end, x: ax, column: anchor}
	case bottom:
		p.anchor = gridAnchor{y: math.Ceil(yb.Y2) + offset, x: ax, column: anchor}
	case topLeft:
		p.anchor = gridAnchor{x: offset, y: offset}
	case topRight:
		p.anchor = gridAnchor{x: w - offset, y: offset, column: end}
	case bottomLeft:
		p.anchor = gridAnchor{x: offset, y: h - offset, row: end}
	case bottomRight:
		p.anchor = gridAnchor{x: w - offset, y: h - offset, column: end, row: end}
	}
	return p
}

// legendLayout sizes a legend group to its content and returns its item. The
// group is placed at the origin unless its orient is "none", in which case
// the specification positions it.
func legendLayout(legend *scene.Mark) *scene.Item {
	item := firstItem(legend)
	if item == nil {
		return nil
	}
	datum := item.Datum
	orient := orientOf(item)
	bounds := &item.Bounds
	x, y := xOf(item), yOf(item)
	padding := extraNum(item, "padding")

	bounds.Clear()

	// adjust the legend to accommodate padding and title
	if entry := childItem(item, 0, 0); entry != nil {
		legendGroupLayout(item, entry)
	}

	// aggregate bounds to determine size, and include origin
	for _, m := range item.Items {
		bounds.Union(&m.Bounds)
	}
	if bounds.Empty() {
		// set the upper-right corner for empty legends (no entries or title);
		// otherwise the -MAX_VALUE sentinels would inflate the size (vega#2881)
		bounds.X2 = padding
		bounds.Y2 = padding
	}
	// anchor to the legend origin
	bounds.X1 = padding
	bounds.Y1 = padding

	w := 2 * padding
	h := 2 * padding
	if !bounds.Empty() {
		w = math.Ceil(bounds.Width() + w)
		h = math.Ceil(bounds.Height() + h)
	}

	if datum.Get("type").IsStr() && datum.Get("type").StrValue() == symbols {
		if grp := childItem(item, 0, 0); grp != nil {
			if scope := childMark(grp, 0); scope != nil {
				legendEntryLayout(scope.Items)
			}
		}
	}

	if orient != none {
		item.X = scene.N(0)
		item.Y = scene.N(0)
		x, y = 0, 0
	}
	item.Width = scene.N(w)
	item.Height = scene.N(h)
	bounds.Set(x, y, x+w, y+h)
	scene.BoundStroke(bounds, item, false)
	legend.Bounds.Clear()
	legend.Bounds.Union(bounds)
	return item
}

// legendGroupLayout shifts the entries and the title to make room for the
// padding and for each other, according to the title's orient and anchor.
func legendGroupLayout(item, entry *scene.Item) {
	pad := extraNum(item, "padding")
	ex := pad - xOf(entry)
	ey := pad - yOf(entry)

	if !item.Datum.Get("title").IsTruthy() {
		if ex != 0 || ey != 0 {
			translate(entry, ex, ey)
		}
		return
	}
	title := childItem(item, 1, 0)
	if title == nil {
		if ex != 0 || ey != 0 {
			translate(entry, ex, ey)
		}
		return
	}
	anchor := extraStr(title, "anchor")
	tpad := orZero(extraNum(item, "titlePadding"))
	tx := pad - xOf(title)
	ty := pad - yOf(title)
	orient := orientOf(title)

	switch orient {
	case left:
		ex += math.Ceil(title.Bounds.Width()) + tpad
	case right, bottom:
	default:
		ey += title.Bounds.Height() + tpad
	}
	if ex != 0 || ey != 0 {
		translate(entry, ex, ey)
	}

	switch orient {
	case left:
		ty += legendTitleOffset(item, entry, title, anchor, 1, true, false)
	case right:
		tx += legendTitleOffset(item, entry, title, end, 0, false, false) + tpad
		ty += legendTitleOffset(item, entry, title, anchor, 1, true, false)
	case bottom:
		tx += legendTitleOffset(item, entry, title, anchor, 0, false, false)
		ty += legendTitleOffset(item, entry, title, end, -1, false, true) + tpad
	default:
		tx += legendTitleOffset(item, entry, title, anchor, 0, false, false)
	}
	if tx != 0 || ty != 0 {
		translate(title, tx, ty)
	}

	// translate the legend if the title pushes into negative coordinates
	if d := jsRound(title.Bounds.X1 - pad); d < 0 {
		translate(entry, -d, 0)
		translate(title, -d, 0)
	}
}

// legendTitleOffset is the offset of a legend title along one axis. y selects
// the vertical extent (1), the horizontal one (0), or (-1) the vertical extent
// without the multi-line offset; lr says the title sits left or right of a
// vertical gradient; noBar excludes the gradient bar itself.
func legendTitleOffset(item, entry, title *scene.Item, anchor string, y int, lr, noBar bool) float64 {
	grad := !(item.Datum.Get("type").IsStr() && item.Datum.Get("type").StrValue() == symbols)
	vgrad := title.Datum.Get("vgrad").IsTruthy()

	// for gradients the extent of the bar (the entry's first child mark) counts
	b := &entry.Bounds
	if grad && (lr || !vgrad) && !noBar {
		if len(entry.Items) > 0 {
			b = &entry.Items[0].Bounds
		}
	}
	e := b.X2
	if y != 0 {
		e = b.Y2
	}
	s := e - extraNum(item, "padding")
	var u, v float64
	if vgrad && lr {
		u = s
	} else {
		v = s
	}
	o := 0.0
	if y > 0 {
		o = scene.MultiLineOffset(title)
	}
	switch anchor {
	case start:
		return jsRound(u)
	case end:
		return jsRound(v - o)
	}
	return jsRound(0.5 * (s - o))
}

// translate moves an item, its bounds and its mark's bounds.
func translate(it *scene.Item, dx, dy float64) {
	it.X = scene.N(xOf(it) + dx)
	it.Y = scene.N(yOf(it) + dy)
	it.Bounds.Translate(dx, dy)
	if it.Mark != nil {
		it.Mark.Bounds.Translate(dx, dy)
	}
}

// legendEntryLayout gives the entry groups of a symbol legend a common width
// per column and their own height, which the grid layout of the entries uses.
func legendEntryLayout(entries []*scene.Item) {
	widths := map[string]float64{}
	col := func(g *scene.Item) string { return jsval.JSNumberString(extraNum(g, "column")) }
	for _, g := range entries {
		k := col(g)
		widths[k] = jsMax(g.Bounds.X2-xOf(g), orZero(widths[k]))
	}
	for _, g := range entries {
		g.Width = scene.N(widths[col(g)])
		g.Height = scene.N(g.Bounds.Y2 - yOf(g))
	}
}
