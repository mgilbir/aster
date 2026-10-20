package layout

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// legendConfig looks up legend layout settings (config.legend.layout): a key
// is searched in the block of the legend's orient first, then at the top level
// (`opt[key] != null ? opt[key] : config[key] != null ? config[key] : d`).
type legendConfig struct {
	cfg, orient jsval.Value
}

// newLegendConfig is lookup(config, orient): `config[orient] || {}`, the
// TypeError of a config that is null or undefined (no legend layout config).
func newLegendConfig(cfg jsval.Value, orient string) (legendConfig, error) {
	if cfg.IsNullish() {
		return legendConfig{}, fmt.Errorf("Cannot read properties of %s (reading '%s')", cfg.Kind(), orient)
	}
	o := jsval.Member(cfg, orient)
	if !o.IsTruthy() {
		o = jsval.Obj(jsval.NewObject(0))
	}
	return legendConfig{cfg: cfg, orient: o}, nil
}

func (c legendConfig) get(key string) jsval.Value {
	if v := jsval.Member(c.orient, key); !v.IsNullish() {
		return v
	}
	return jsval.Member(c.cfg, key)
}

func (c legendConfig) getOr(key string, d jsval.Value) jsval.Value {
	if v := c.get(key); !v.IsNullish() {
		return v
	}
	return d
}

// legendOffset is offsets(): the largest offset specified directly on the
// legends (Math.max, which NaN poisons), else the configured one.
func legendOffset(legends []*scene.Item, dflt jsval.Value) jsval.Value {
	max := math.Inf(-1)
	for _, it := range legends {
		if v := prop(it, "offset"); !v.IsNullish() {
			max = jsMax(max, num(v))
		}
	}
	if max > math.Inf(-1) {
		return jsval.Num(max)
	}
	return dflt
}

// legendParams computes the grid layout options that stack the legends of one
// orient: relative to the bounds of the axes (xb, yb) and to the group size
// (w, h), anchored at the legend anchor. The anchor's coordinates are the
// values upstream computes, which gridLayout reads as numbers.
func legendParams(g []*scene.Item, orient string, config jsval.Value, xb, yb *scene.Bounds, w, h float64) (gridOptions, error) {
	c, err := newLegendConfig(config, orient)
	if err != nil {
		return gridOptions{}, err
	}
	var wk jsval.Walker
	if len(g) > 0 {
		wk = g[0].Walker()
	}
	offset := legendOffset(g, c.getOr("offset", jsval.Int(0)))
	anchorV := c.getOr("anchor", jsval.Str(start))
	anchor, _ := anchorV.Strict() // compared with ===
	frame, _ := c.getOr("frame", jsval.Str(groupFrame)).Strict()
	mult := 0.0
	switch anchor {
	case end:
		mult = 1
	case middle:
		mult = 0.5
	}
	var ax, ay float64
	if frame == boundsFrame {
		ax = xb.PlusNum(0, float64(mult*xb.Width()))
		ay = yb.PlusNum(1, float64(mult*yb.Height()))
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

	bounds, _ := c.getOr("bounds", jsval.Str(flush)).Strict()
	p := gridOptions{
		alignCol: each,
		alignRow: each,
		bounds:   bounds,
		columns:  len(g),
	}
	if d, _ := c.get("direction").Strict(); d == "vertical" {
		p.columns = 1
	}
	margin := c.getOr("margin", jsval.Int(8))
	p.padCol = getOr(margin, "column")
	p.padRow = getOr(margin, "row")
	center := c.get("center")
	p.centerCol = pairValue(center, "column").IsTruthy()
	p.centerRow = pairValue(center, "row").IsTruthy()

	// offset - n is a number; n + offset concatenates a text offset
	minus := func(n float64) float64 { return n - num(offset) }
	plus := func(n float64) float64 { return num(jsAdd(wk, jsval.Num(n), offset)) }
	off := num(offset)
	switch orient {
	case left:
		p.anchor = gridAnchor{x: minus(math.Floor(xb.X1)), column: end, y: ay, row: anchor}
	case right:
		p.anchor = gridAnchor{x: plus(math.Ceil(xb.X2)), y: ay, row: anchor}
	case top:
		p.anchor = gridAnchor{y: minus(math.Floor(yb.Y1)), row: end, x: ax, column: anchor}
	case bottom:
		p.anchor = gridAnchor{y: plus(math.Ceil(yb.Y2)), x: ax, column: anchor}
	case topLeft:
		p.anchor = gridAnchor{x: off, y: off}
	case topRight:
		p.anchor = gridAnchor{x: w - off, y: off, column: end}
	case bottomLeft:
		p.anchor = gridAnchor{x: off, y: h - off, row: end}
	case bottomRight:
		p.anchor = gridAnchor{x: w - off, y: h - off, column: end, row: end}
	}
	return p, nil
}

// legendLayout sizes a legend group to its content and returns its item. The
// group is placed at the origin unless its orient is "none" (===), in which
// case the specification positions it.
//
// The layout reads the structure the legend parser builds (the entries group
// first, a title second) without checking it, so a mark given the role
// "legend" that lacks it throws, as upstream does: the error abandons the
// layout, with the item's bounds cleared already.
func legendLayout(legend *scene.Mark) (*scene.Item, error) {
	item := firstItem(legend)
	if item == nil {
		return nil, legendUndefined("datum")
	}
	wk := item.Walker()
	datum := item.Datum
	orient := item.OrientValue()
	bounds := &item.Bounds
	x, y := prop(item, "x"), prop(item, "y")
	padding := prop(item, "padding")
	pad := num(padding)

	bounds.Clear()

	// adjust the legend to accommodate padding and title
	entry := childItem(item, 0, 0)
	if entry == nil {
		return nil, legendUndefined("0")
	}
	if err := legendGroupLayout(item, entry); err != nil {
		return nil, err
	}

	// aggregate bounds to determine size, and include origin
	for _, m := range item.Items {
		bounds.Union(&m.Bounds)
	}
	if bounds.Empty() {
		// set the upper-right corner for empty legends (no entries or title);
		// otherwise the -MAX_VALUE sentinels would inflate the size (vega#2881)
		bounds.X2 = pad
		bounds.Y2 = pad
	}
	// anchor to the legend origin
	bounds.X1 = pad
	bounds.Y1 = pad

	w := 2 * pad
	h := 2 * pad
	if !bounds.Empty() {
		w = math.Ceil(bounds.Width() + w)
		h = math.Ceil(bounds.Height() + h)
	}

	if datum.Get("type").IsStr() && datum.Get("type").StrValue() == symbols {
		// item.items[0].items[0].items[0].items
		if entry.Items == nil {
			return nil, legendUndefined("0")
		}
		scope := childMark(entry, 0)
		if scope == nil {
			return nil, legendUndefined("items")
		}
		legendEntryLayout(scope.Items)
	}

	if !orientIs(orient, none) {
		x, y = jsval.Num(0), jsval.Num(0)
		setPos(item, "x", x)
		setPos(item, "y", y)
	}
	_, _ = item.Set("width", jsval.Num(w))
	_, _ = item.Set("height", jsval.Num(h))
	// bounds.set(x, y, x + w, y + h)
	bounds.Set(num(x), num(y), num(jsAdd(wk, x, jsval.Num(w))), num(jsAdd(wk, y, jsval.Num(h))))
	scene.BoundStroke(bounds, item, false)
	legend.Bounds.Clear()
	legend.Bounds.Union(bounds)
	return item, nil
}

// legendUndefined is the TypeError of reading a property of undefined in the
// legend layout.
func legendUndefined(prop string) error {
	return &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of undefined (reading '" + prop + "')"}
}

// legendGroupLayout shifts the entries and the title to make room for the
// padding and for each other, according to the title's orient and anchor.
// The shifts are JavaScript values: a text titlePadding concatenates into
// them, and the items' coordinates become that text.
func legendGroupLayout(item, entry *scene.Item) error {
	wk := item.Walker()
	if item.Datum.IsNullish() {
		return legendUndefined("title")
	}
	pad := num(prop(item, "padding"))
	ex := jsval.Num(pad - num(prop(entry, "x")))
	ey := jsval.Num(pad - num(prop(entry, "y")))

	if !item.Datum.Get("title").IsTruthy() {
		if ex.IsTruthy() || ey.IsTruthy() {
			translateItem(entry, ex, ey)
		}
		return nil
	}
	title := childItem(item, 1, 0)
	if title == nil {
		// item.items[1].items[0] is undefined: reading its anchor throws
		return legendUndefined("anchor")
	}
	anchor := extraStr(title, "anchor")
	tpad := orZeroV(prop(item, "titlePadding"))
	tx := jsval.Num(pad - num(prop(title, "x")))
	ty := jsval.Num(pad - num(prop(title, "y")))
	orient := title.OrientValue()

	switch {
	case orientIs(orient, left):
		ex = jsAdd(wk, ex, jsAdd(wk, jsval.Num(math.Ceil(title.Bounds.Width())), tpad))
	case orientIs(orient, right), orientIs(orient, bottom):
	default:
		ey = jsAdd(wk, ey, jsAdd(wk, jsval.Num(title.Bounds.Height()), tpad))
	}
	if ex.IsTruthy() || ey.IsTruthy() {
		translateItem(entry, ex, ey)
	}

	var offErr error
	off := func(anchor string, y int, lr, noBar bool) jsval.Value {
		v, err := legendTitleOffset(item, entry, title, anchor, y, lr, noBar)
		if err != nil && offErr == nil {
			offErr = err
		}
		return jsval.Num(v)
	}
	switch {
	case orientIs(orient, left):
		ty = jsAdd(wk, ty, off(anchor, 1, true, false))
	case orientIs(orient, right):
		tx = jsAdd(wk, tx, jsAdd(wk, off(end, 0, false, false), tpad))
		ty = jsAdd(wk, ty, off(anchor, 1, true, false))
	case orientIs(orient, bottom):
		tx = jsAdd(wk, tx, off(anchor, 0, false, false))
		ty = jsAdd(wk, ty, jsAdd(wk, off(end, -1, false, true), tpad))
	default:
		tx = jsAdd(wk, tx, off(anchor, 0, false, false))
	}
	if offErr != nil {
		return offErr
	}
	if tx.IsTruthy() || ty.IsTruthy() {
		translateItem(title, tx, ty)
	}

	// translate the legend if the title pushes into negative coordinates
	if d := jsmath.Round(title.Bounds.X1 - pad); d < 0 {
		translateItem(entry, jsval.Num(-d), jsval.Num(0))
		translateItem(title, jsval.Num(-d), jsval.Num(0))
	}
	return nil
}

// legendTitleOffset is the offset of a legend title along one axis. y selects
// the vertical extent (1), the horizontal one (0), or (-1) the vertical extent
// without the multi-line offset; lr says the title sits left or right of a
// vertical gradient; noBar excludes the gradient bar itself.
func legendTitleOffset(item, entry, title *scene.Item, anchor string, y int, lr, noBar bool) (float64, error) {
	grad := !(item.Datum.Get("type").IsStr() && item.Datum.Get("type").StrValue() == symbols)
	if title.Datum.IsNullish() {
		return 0, legendUndefined("vgrad")
	}
	vgrad := title.Datum.Get("vgrad").IsTruthy()

	// for gradients the extent of the bar (the entry's first child mark) counts
	b := &entry.Bounds
	if grad && (lr || !vgrad) && !noBar {
		// entry.items[0].bounds: an entry without a mark throws
		if len(entry.Items) == 0 || entry.Items[0] == nil {
			return 0, legendUndefined("bounds")
		}
		b = &entry.Items[0].Bounds
	}
	e := b.X2
	if y != 0 {
		e = b.Y2
	}
	s := e - num(prop(item, "padding"))
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
		return jsmath.Round(u), nil
	case end:
		return jsmath.Round(v - o), nil
	}
	return jsmath.Round(0.5 * (s - o)), nil
}

// translateItem moves an item, its bounds and its mark's bounds:
// `item.x += dx; item.y += dy`, which concatenates a text.
func translateItem(it *scene.Item, dx, dy jsval.Value) {
	wk := it.Walker()
	setPos(it, "x", jsAdd(wk, prop(it, "x"), dx))
	setPos(it, "y", jsAdd(wk, prop(it, "y"), dy))
	translateV(wk, &it.Bounds, dx, dy)
	if it.Mark != nil {
		translateV(wk, &it.Mark.Bounds, dx, dy)
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
		// g.width = ...; g.height = ...: a number replaces a text given
		_, _ = g.Set("width", jsval.Num(widths[col(g)]))
		_, _ = g.Set("height", jsval.Num(g.Bounds.Y2-yOf(g)))
	}
}
