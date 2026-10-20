package layout

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// maxArrayLength is the largest length `Array(n)` accepts (2^32 - 1); any
// other n makes the constructor throw a RangeError.
const maxArrayLength = 1<<32 - 1

// ErrInvalidArrayLength is the RangeError upstream's gridLayout raises at
// `xExtent = Array(ncols)` when the `columns` of a layout is not a valid array
// length (negative, fractional or beyond 2^32 - 1). The error aborts the
// whole dataflow evaluation, so the layout, and every operator after it,
// never runs.
var ErrInvalidArrayLength = errors.New("RangeError: Invalid array length")

// maxGridColumns bounds the columns of a grid; a specification can otherwise
// ask for an arbitrarily wide table.
const maxGridColumns = 1 << 20

// gridAnchor positions a laid-out grid: (x, y) is the anchor point, and the
// column and row anchors say which edge of the grid sits on it.
type gridAnchor struct {
	x, y   float64
	column string // "end" or "middle" anchor by that edge; anything else by the start
	row    string
}

// gridOptions are the parameters of gridLayout.
type gridOptions struct {
	alignCol, alignRow string // "all", "each"; anything else means no alignment
	// padCol and padRow are get(opt.padding, column|row) as given: a text
	// concatenates into the offsets of the cells (see gridLayout)
	padCol, padRow jsval.Value
	columns        int // 0 selects one column per group
	// columnsRaw is the columns when given as text, a list or an object:
	// the layout is then gridLayoutExact's
	columnsRaw jsval.Value
	centerCol  bool
	centerRow  bool
	anchor     gridAnchor
	bounds     string // "flush": group boxes; otherwise full item bounds
}

// GridSpec is a parsed group `layout` property: the grid parameters plus the
// trellis header, footer and title settings.
type GridSpec struct {
	gridOptions
	// invalid records a columns value a JavaScript array could not be sized
	// with (negative, fractional or too long); upstream's gridLayout fails with
	// a RangeError there (ErrInvalidArrayLength).
	invalid bool

	// the offsets of headers, footers and titles as given (get(off, key)):
	// a footer's or an end-anchored title's is added, and concatenates
	rowHeader, columnHeader, rowFooter, columnFooter, rowTitle, columnTitle jsval.Value

	headerBandRow, headerBandColumn *float64
	footerBandRow, footerBandColumn *float64
	titleBandRow, titleBandColumn   float64
	titleAnchorRow, titleAnchorCol  string
}

// optGet is layout's get(opt, key) before its default: an object option
// (an array too) is indexed, any other option applies to every key.
func optGet(opt jsval.Value, key string) jsval.Value {
	if isObjectValue(opt) {
		return jsval.Member(opt, key)
	}
	return opt
}

// getOr is get(opt, key) with its default of 0: `v != null ? v : 0`.
func getOr(opt jsval.Value, key string) jsval.Value {
	if v := optGet(opt, key); !v.IsNullish() {
		return v
	}
	return jsval.Num(0)
}

// member is opt[key] of a layout option of any type (undefined for null
// and undefined, which upstream's layout never reads a member of).
func member(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		return jsval.Undefined
	}
	return jsval.Member(v, key)
}

func isObjectValue(v jsval.Value) bool {
	switch v.Kind() {
	case jsval.KindObj, jsval.KindArr, jsval.KindTimestamp, jsval.KindPattern:
		return true
	}
	return false
}

// numOr is `get(opt, key)` read as a number with a default of 0.
func numOr(v jsval.Value) float64 {
	if v.IsNullish() {
		return 0
	}
	return jsval.ToNumber(v)
}

func bandOf(v jsval.Value) *float64 {
	if v.IsNullish() {
		return nil
	}
	f := jsval.ToNumber(v)
	return &f
}

func strOf(v jsval.Value) string {
	if v.IsStr() {
		return v.StrValue()
	}
	return ""
}

// pairValue reads the row or column entry of a per-axis option.
func pairValue(opt jsval.Value, key string) jsval.Value { return optGet(opt, key) }

// ParseGridSpec reads a group's `layout` property, of any type upstream's
// trellisLayout accepts (a text or a number reads as an empty layout, its
// members as a string's or a number's). Signals must already be resolved.
// Scalars apply to both rows and columns, as in Vega.
func ParseGridSpec(opt jsval.Value) *GridSpec {
	g := &GridSpec{}
	g.alignCol = strOf(optGet(member(opt, "align"), "column"))
	g.alignRow = strOf(optGet(member(opt, "align"), "row"))
	g.padCol = getOr(member(opt, "padding"), "column")
	g.padRow = getOr(member(opt, "padding"), "row")
	if c := member(opt, "columns"); c.IsTruthy() {
		if isConcat(c) {
			// Array(ncols) of a text, a list or an object is an array of
			// that one value, and the layout's arithmetic on it is
			// JavaScript's: see gridLayoutExact
			g.columnsRaw = c
		} else {
			// Array(ncols) of a number that is no array length is a
			// RangeError (a boolean is the number it reads as)
			f := jsval.ToNumber(c)
			switch {
			case f != math.Floor(f) || f < 0 || f > maxArrayLength || math.IsNaN(f):
				g.invalid = true
			case f > maxGridColumns:
				g.columns = maxGridColumns
			default:
				g.columns = int(f)
			}
		}
	}
	g.centerCol = optGet(member(opt, "center"), "column").IsTruthy()
	g.centerRow = optGet(member(opt, "center"), "row").IsTruthy()
	anchor := member(opt, "anchor")
	g.anchor = gridAnchor{
		x:      numOr(optGet(anchor, "x")),
		y:      numOr(optGet(anchor, "y")),
		column: strOf(optGet(anchor, "column")),
		row:    strOf(optGet(anchor, "row")),
	}
	if b := member(opt, "bounds"); b.IsStr() {
		g.bounds = b.StrValue()
	}

	off := member(opt, "offset")
	g.rowHeader = getOr(off, "rowHeader")
	g.columnHeader = getOr(off, "columnHeader")
	g.rowFooter = getOr(off, "rowFooter")
	g.columnFooter = getOr(off, "columnFooter")
	g.rowTitle = getOr(off, "rowTitle")
	g.columnTitle = getOr(off, "columnTitle")

	g.headerBandRow = bandOf(optGet(member(opt, "headerBand"), "row"))
	g.headerBandColumn = bandOf(optGet(member(opt, "headerBand"), "column"))
	g.footerBandRow = bandOf(optGet(member(opt, "footerBand"), "row"))
	g.footerBandColumn = bandOf(optGet(member(opt, "footerBand"), "column"))
	tb := member(opt, "titleBand")
	g.titleBandRow, g.titleBandColumn = 0.5, 0.5
	if v := optGet(tb, "row"); !v.IsNullish() {
		g.titleBandRow = jsval.ToNumber(v)
	}
	if v := optGet(tb, "column"); !v.IsNullish() {
		g.titleBandColumn = jsval.ToNumber(v)
	}
	g.titleAnchorRow = strOf(optGet(member(opt, "titleAnchor"), "row"))
	g.titleAnchorCol = strOf(optGet(member(opt, "titleAnchor"), "column"))
	return g
}

func offsetValue(v float64) float64 {
	if v < 0 {
		return math.Ceil(-v)
	}
	return 0
}

// gridBox is the box of a group used to size its cell: its own size (flush)
// or its content bounds relative to its origin (full).
func gridBox(it *scene.Item, flushBounds bool) scene.Bounds {
	if flushBounds {
		return newBoundsSet(0, 0, it.OrZero("width"), it.OrZero("height"))
	}
	b := it.Bounds.Clone()
	if b.Empty() {
		b.Set(0, 0, 0, 0)
		return b
	}
	b.Translate(-it.OrZero("x"), -it.OrZero("y"))
	return b
}

// gridLayout arranges groups in a table and returns the bounds of the result.
// Columns are aligned per column ("each") or globally ("all"), rows likewise;
// full bounds let negative extents (axes to the left/above) push cells apart.
// The offsets are JavaScript values: a padding given as text concatenates
// into them, and the groups' coordinates become text (gridLayoutJS); with
// numbers throughout, gridLayoutNum computes the same with floats.
func gridLayout(groups []*scene.Item, opt *gridOptions) (scene.Bounds, error) {
	if !opt.columnsRaw.IsUndefined() {
		return gridLayoutExact(groups, opt)
	}
	if opt.padCol.IsNum() && opt.padRow.IsNum() {
		numeric := true
		for _, g := range groups {
			if _, ok := g.Raw["x"]; ok {
				numeric = false
				break
			}
			if _, ok := g.Raw["y"]; ok {
				numeric = false
				break
			}
		}
		if numeric {
			return gridLayoutNum(groups, opt, opt.padCol.NumValue(), opt.padRow.NumValue()), nil
		}
	}
	return gridLayoutJS(groups, opt)
}

// gridLayoutNum is gridLayout for numbers only: paddings that are numbers
// and groups positioned by numbers, the case of every real chart, without
// the JavaScript values of gridLayoutJS.
// Columns are aligned per column ("each") or globally ("all"), rows likewise;
// full bounds let negative extents (axes to the left/above) push cells apart.
func gridLayoutNum(groups []*scene.Item, opt *gridOptions, padCol, padRow float64) scene.Bounds {
	n := len(groups)
	bounds := newBoundsSet(0, 0, 0, 0)
	if n == 0 {
		return bounds
	}
	flushBounds := opt.bounds == flush
	alignCol, alignRow := opt.alignCol, opt.alignRow
	ncols := opt.columns
	if ncols <= 0 {
		ncols = n
	}
	nrows := (n + ncols - 1) / ncols
	nc := ncols // columns that can hold a group
	if nc > n {
		nc = n
	}

	xOffset, yOffset := make([]float64, n), make([]float64, n)
	xExtent, yExtent := make([]float64, nc), make([]float64, nrows)
	dx, dy := make([]float64, n), make([]float64, n)
	boxes := make([]scene.Bounds, n)
	var xMax, yMax float64

	// determine offsets for each group
	for i, g := range groups {
		b := gridBox(g, flushBounds)
		boxes[i] = b
		assign(g, "x", &g.X, orZero(xOf(g)))
		assign(g, "y", &g.Y, orZero(yOf(g)))
		c, r := i%ncols, i/ncols
		px, py := math.Ceil(b.X2), math.Ceil(b.Y2)
		xMax = jsMax(xMax, px)
		yMax = jsMax(yMax, py)
		xExtent[c] = jsMax(xExtent[c], px)
		yExtent[r] = jsMax(yExtent[r], py)
		xOffset[i] = padCol + offsetValue(b.X1)
		yOffset[i] = padRow + offsetValue(b.Y1)
	}

	// set initial alignment offsets
	for i := 0; i < n; i++ {
		if i%ncols == 0 {
			xOffset[i] = 0
		}
		if i < ncols {
			yOffset[i] = 0
		}
	}

	// enforce column alignment constraints
	switch alignCol {
	case each:
		for c := 1; c < nc; c++ {
			offset := 0.0
			for i := c; i < n; i += ncols {
				if offset < xOffset[i] {
					offset = xOffset[i]
				}
			}
			for i := c; i < n; i += ncols {
				xOffset[i] = offset + xExtent[c-1]
			}
		}
	case all:
		offset := 0.0
		for i := 0; i < n; i++ {
			if i%ncols != 0 && offset < xOffset[i] {
				offset = xOffset[i]
			}
		}
		for i := 0; i < n; i++ {
			if i%ncols != 0 {
				xOffset[i] = offset + xMax
			}
		}
	default:
		alignCol = ""
		for c := 1; c < nc; c++ {
			for i := c; i < n; i += ncols {
				xOffset[i] += xExtent[c-1]
			}
		}
	}

	// enforce row alignment constraints
	switch alignRow {
	case each:
		for r := 1; r < nrows; r++ {
			offset := 0.0
			lo := r * ncols
			hi := lo + ncols
			if hi > n {
				hi = n
			}
			for i := lo; i < hi; i++ {
				if offset < yOffset[i] {
					offset = yOffset[i]
				}
			}
			for i := lo; i < hi; i++ {
				yOffset[i] = offset + yExtent[r-1]
			}
		}
	case all:
		offset := 0.0
		for i := ncols; i < n; i++ {
			if offset < yOffset[i] {
				offset = yOffset[i]
			}
		}
		for i := ncols; i < n; i++ {
			yOffset[i] = offset + yMax
		}
	default:
		alignRow = ""
		for r := 1; r < nrows; r++ {
			lo := r * ncols
			hi := lo + ncols
			if hi > n {
				hi = n
			}
			for i := lo; i < hi; i++ {
				yOffset[i] += yExtent[r-1]
			}
		}
	}

	// perform horizontal grid layout
	x := 0.0
	for i := 0; i < n; i++ {
		prev := 0.0
		if i%ncols != 0 {
			prev = x
		}
		x = xOffset[i] + prev
		dx[i] += x - xOf(groups[i])
	}

	// perform vertical grid layout
	for c := 0; c < nc; c++ {
		y := 0.0
		for i := c; i < n; i += ncols {
			y += yOffset[i]
			dy[i] += y - yOf(groups[i])
		}
	}

	// perform horizontal centering
	if alignCol != "" && opt.centerCol && nrows > 1 {
		for i := 0; i < n; i++ {
			b := xMax
			if alignCol != all {
				b = xExtent[i%ncols]
			}
			x := b - boxes[i].X2 - xOf(groups[i]) - dx[i]
			if x > 0 {
				dx[i] += x / 2
			}
		}
	}

	// perform vertical centering
	if alignRow != "" && opt.centerRow && ncols != 1 {
		for i := 0; i < n; i++ {
			b := yMax
			if alignRow != all {
				b = yExtent[i/ncols]
			}
			y := b - boxes[i].Y2 - yOf(groups[i]) - dy[i]
			if y > 0 {
				dy[i] += y / 2
			}
		}
	}

	// position the grid relative to the anchor
	for i := 0; i < n; i++ {
		bounds.Union(boxes[i].Translate(dx[i], dy[i]))
	}
	ax, ay := opt.anchor.x, opt.anchor.y
	switch opt.anchor.column {
	case end:
		ax -= bounds.Width()
	case middle:
		ax -= bounds.Width() / 2
	}
	switch opt.anchor.row {
	case end:
		ay -= bounds.Height()
	case middle:
		ay -= bounds.Height() / 2
	}
	ax = jsRound(ax)
	ay = jsRound(ay)

	// update mark positions and bounds
	bounds.Clear()
	for _, g := range groups {
		if g.Mark != nil {
			g.Mark.Bounds.Clear()
		}
	}
	for i, g := range groups {
		dx[i] += ax
		dy[i] += ay
		assign(g, "x", &g.X, xOf(g)+dx[i])
		assign(g, "y", &g.Y, yOf(g)+dy[i])
		g.Bounds.Translate(dx[i], dy[i])
		if g.Mark != nil {
			g.Mark.Bounds.Union(&g.Bounds)
			bounds.Union(&g.Mark.Bounds)
		} else {
			bounds.Union(&g.Bounds)
		}
	}
	return bounds
}

// gridLayoutJS is gridLayout with the offsets as JavaScript values.
// Columns are aligned per column ("each") or globally ("all"), rows likewise;
// full bounds let negative extents (axes to the left/above) push cells apart.
func gridLayoutJS(groups []*scene.Item, opt *gridOptions) (scene.Bounds, error) {
	n := len(groups)
	bounds := newBoundsSet(0, 0, 0, 0)
	if n == 0 {
		return bounds, nil
	}
	flushBounds := opt.bounds == flush
	alignCol, alignRow := opt.alignCol, opt.alignRow
	ncols := opt.columns
	if ncols <= 0 {
		ncols = n
	}
	nrows := (n + ncols - 1) / ncols
	nc := ncols // columns that can hold a group
	if nc > n {
		nc = n
	}

	xOffset, yOffset := make([]jsval.Value, n), make([]jsval.Value, n)
	xExtent, yExtent := make([]float64, nc), make([]float64, nrows)
	dx, dy := make([]float64, n), make([]float64, n)
	boxes := make([]scene.Bounds, n)
	var xMax, yMax float64
	var wk jsval.Walker
	if n > 0 {
		wk = groups[0].Walker()
	}
	addf := func(a jsval.Value, b float64) jsval.Value { return jsAdd(wk, a, jsval.Num(b)) }
	add := func(a, b jsval.Value) jsval.Value { return jsAdd(wk, a, b) }
	zero := jsval.Num(0)

	// determine offsets for each group
	for i, g := range groups {
		b := gridBox(g, flushBounds)
		boxes[i] = b
		setPos(g, "x", orZeroV(prop(g, "x")))
		setPos(g, "y", orZeroV(prop(g, "y")))
		c, r := i%ncols, i/ncols
		px, py := math.Ceil(b.X2), math.Ceil(b.Y2)
		xMax = jsMax(xMax, px)
		yMax = jsMax(yMax, py)
		xExtent[c] = jsMax(xExtent[c], px)
		yExtent[r] = jsMax(yExtent[r], py)
		// padCol + offsetValue(b.x1): a text padding concatenates
		xOffset[i] = addf(opt.padCol, offsetValue(b.X1))
		yOffset[i] = addf(opt.padRow, offsetValue(b.Y1))
	}

	// set initial alignment offsets
	for i := 0; i < n; i++ {
		if i%ncols == 0 {
			xOffset[i] = zero
		}
		if i < ncols {
			yOffset[i] = zero
		}
	}

	// enforce column alignment constraints
	switch alignCol {
	case each:
		for c := 1; c < nc; c++ {
			offset := zero
			for i := c; i < n; i += ncols {
				if jsLess(offset, xOffset[i]) {
					offset = xOffset[i]
				}
			}
			for i := c; i < n; i += ncols {
				xOffset[i] = addf(offset, xExtent[c-1])
			}
		}
	case all:
		offset := zero
		for i := 0; i < n; i++ {
			if i%ncols != 0 && jsLess(offset, xOffset[i]) {
				offset = xOffset[i]
			}
		}
		for i := 0; i < n; i++ {
			if i%ncols != 0 {
				xOffset[i] = addf(offset, xMax)
			}
		}
	default:
		alignCol = ""
		for c := 1; c < nc; c++ {
			for i := c; i < n; i += ncols {
				xOffset[i] = addf(xOffset[i], xExtent[c-1])
			}
		}
	}

	// enforce row alignment constraints
	switch alignRow {
	case each:
		for r := 1; r < nrows; r++ {
			offset := zero
			lo := r * ncols
			hi := lo + ncols
			if hi > n {
				hi = n
			}
			for i := lo; i < hi; i++ {
				if jsLess(offset, yOffset[i]) {
					offset = yOffset[i]
				}
			}
			for i := lo; i < hi; i++ {
				yOffset[i] = addf(offset, yExtent[r-1])
			}
		}
	case all:
		offset := zero
		for i := ncols; i < n; i++ {
			if jsLess(offset, yOffset[i]) {
				offset = yOffset[i]
			}
		}
		for i := ncols; i < n; i++ {
			yOffset[i] = addf(offset, yMax)
		}
	default:
		alignRow = ""
		for r := 1; r < nrows; r++ {
			lo := r * ncols
			hi := lo + ncols
			if hi > n {
				hi = n
			}
			for i := lo; i < hi; i++ {
				yOffset[i] = addf(yOffset[i], yExtent[r-1])
			}
		}
	}

	// perform horizontal grid layout: x = xOffset[i] + (i % ncols ? x : 0)
	x := zero
	for i := 0; i < n; i++ {
		prev := zero
		if i%ncols != 0 {
			prev = x
		}
		x = jsAdd(wk, xOffset[i], prev)
		dx[i] += num(x) - num(prop(groups[i], "x"))
	}

	// perform vertical grid layout
	for c := 0; c < nc; c++ {
		y := zero
		for i := c; i < n; i += ncols {
			y = jsAdd(wk, y, yOffset[i])
			dy[i] += num(y) - num(prop(groups[i], "y"))
		}
	}

	// perform horizontal centering
	if alignCol != "" && opt.centerCol && nrows > 1 {
		for i := 0; i < n; i++ {
			b := xMax
			if alignCol != all {
				b = xExtent[i%ncols]
			}
			x := b - boxes[i].X2 - num(prop(groups[i], "x")) - dx[i]
			if x > 0 {
				dx[i] += x / 2
			}
		}
	}

	// perform vertical centering
	if alignRow != "" && opt.centerRow && ncols != 1 {
		for i := 0; i < n; i++ {
			b := yMax
			if alignRow != all {
				b = yExtent[i/ncols]
			}
			y := b - boxes[i].Y2 - num(prop(groups[i], "y")) - dy[i]
			if y > 0 {
				dy[i] += y / 2
			}
		}
	}

	return placeGrid(groups, opt, boxes, dx, dy, add), nil
}

// placeGrid positions the laid-out cells relative to the anchor and
// returns the bounds of the grid; add is JavaScript's +, which turns a
// group's text coordinate into the text of the sum.
func placeGrid(groups []*scene.Item, opt *gridOptions, boxes []scene.Bounds, dx, dy []float64, add func(a, b jsval.Value) jsval.Value) scene.Bounds {
	n := len(groups)
	bounds := newBoundsSet(0, 0, 0, 0)
	// position the grid relative to the anchor
	for i := 0; i < n; i++ {
		bounds.Union(boxes[i].Translate(dx[i], dy[i]))
	}
	ax, ay := opt.anchor.x, opt.anchor.y
	switch opt.anchor.column {
	case end:
		ax -= bounds.Width()
	case middle:
		ax -= bounds.Width() / 2
	}
	switch opt.anchor.row {
	case end:
		ay -= bounds.Height()
	case middle:
		ay -= bounds.Height() / 2
	}
	ax = jsRound(ax)
	ay = jsRound(ay)

	// update mark positions and bounds
	bounds.Clear()
	for _, g := range groups {
		if g.Mark != nil {
			g.Mark.Bounds.Clear()
		}
	}
	for i, g := range groups {
		dx[i] += ax
		dy[i] += ay
		setPos(g, "x", add(prop(g, "x"), jsval.Num(dx[i])))
		setPos(g, "y", add(prop(g, "y"), jsval.Num(dy[i])))
		g.Bounds.Translate(dx[i], dy[i])
		if g.Mark != nil {
			g.Mark.Bounds.Union(&g.Bounds)
			bounds.Union(&g.Mark.Bounds)
		} else {
			bounds.Union(&g.Bounds)
		}
	}
	return bounds
}
