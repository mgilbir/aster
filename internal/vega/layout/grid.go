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
	padCol, padRow     float64
	columns            int // 0 selects one column per group
	centerCol          bool
	centerRow          bool
	anchor             gridAnchor
	bounds             string // "flush": group boxes; otherwise full item bounds
}

// GridSpec is a parsed group `layout` property: the grid parameters plus the
// trellis header, footer and title settings.
type GridSpec struct {
	gridOptions
	// invalid records a columns value a JavaScript array could not be sized
	// with (negative, fractional or too long); upstream's gridLayout fails with
	// a RangeError there (ErrInvalidArrayLength).
	invalid bool

	rowHeader, columnHeader, rowFooter, columnFooter, rowTitle, columnTitle float64

	headerBandRow, headerBandColumn *float64
	footerBandRow, footerBandColumn *float64
	titleBandRow, titleBandColumn   float64
	titleAnchorRow, titleAnchorCol  string
}

// optGet is layout's get(opt, key): a scalar option applies to every key, an
// object option is indexed. Absent options read as undefined.
func optGet(opt jsval.Value, key string) jsval.Value {
	if isObjectValue(opt) {
		return opt.Get(key)
	}
	return opt
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

// pairValue and pairNum read the row or column entry of a per-axis option.
func pairValue(opt jsval.Value, key string) jsval.Value { return optGet(opt, key) }
func pairNum(opt jsval.Value, key string) float64       { return numOr(optGet(opt, key)) }

// ParseGridSpec reads a group's `layout` property. Signals must already be
// resolved. Scalars apply to both rows and columns, as in Vega.
func ParseGridSpec(opt jsval.Value) *GridSpec {
	g := &GridSpec{}
	g.alignCol = strOf(optGet(opt.Get("align"), "column"))
	g.alignRow = strOf(optGet(opt.Get("align"), "row"))
	g.padCol = pairNum(opt.Get("padding"), "column")
	g.padRow = pairNum(opt.Get("padding"), "row")
	if c := opt.Get("columns"); c.IsTruthy() {
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
	g.centerCol = pairValue(opt.Get("center"), "column").IsTruthy()
	g.centerRow = pairValue(opt.Get("center"), "row").IsTruthy()
	anchor := opt.Get("anchor")
	g.anchor = gridAnchor{
		x:      numOr(optGet(anchor, "x")),
		y:      numOr(optGet(anchor, "y")),
		column: strOf(optGet(anchor, "column")),
		row:    strOf(optGet(anchor, "row")),
	}
	if b := opt.Get("bounds"); b.IsStr() {
		g.bounds = b.StrValue()
	}

	off := opt.Get("offset")
	g.rowHeader = numOr(optGet(off, "rowHeader"))
	g.columnHeader = numOr(optGet(off, "columnHeader"))
	g.rowFooter = numOr(optGet(off, "rowFooter"))
	g.columnFooter = numOr(optGet(off, "columnFooter"))
	g.rowTitle = numOr(optGet(off, "rowTitle"))
	g.columnTitle = numOr(optGet(off, "columnTitle"))

	g.headerBandRow = bandOf(optGet(opt.Get("headerBand"), "row"))
	g.headerBandColumn = bandOf(optGet(opt.Get("headerBand"), "column"))
	g.footerBandRow = bandOf(optGet(opt.Get("footerBand"), "row"))
	g.footerBandColumn = bandOf(optGet(opt.Get("footerBand"), "column"))
	tb := opt.Get("titleBand")
	g.titleBandRow, g.titleBandColumn = 0.5, 0.5
	if v := optGet(tb, "row"); !v.IsNullish() {
		g.titleBandRow = jsval.ToNumber(v)
	}
	if v := optGet(tb, "column"); !v.IsNullish() {
		g.titleBandColumn = jsval.ToNumber(v)
	}
	g.titleAnchorRow = strOf(optGet(opt.Get("titleAnchor"), "row"))
	g.titleAnchorCol = strOf(optGet(opt.Get("titleAnchor"), "column"))
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
		return newBoundsSet(0, 0, orZero(it.Width.Val()), orZero(it.Height.Val()))
	}
	b := it.Bounds
	if b.Empty() {
		b.Set(0, 0, 0, 0)
		return b
	}
	b.Translate(-orZero(xOf(it)), -orZero(yOf(it)))
	return b
}

// gridLayout arranges groups in a table and returns the bounds of the result.
// Columns are aligned per column ("each") or globally ("all"), rows likewise;
// full bounds let negative extents (axes to the left/above) push cells apart.
func gridLayout(groups []*scene.Item, opt *gridOptions) scene.Bounds {
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
		g.X = scene.N(orZero(xOf(g)))
		g.Y = scene.N(orZero(yOf(g)))
		c, r := i%ncols, i/ncols
		px, py := math.Ceil(b.X2), math.Ceil(b.Y2)
		xMax = jsMax(xMax, px)
		yMax = jsMax(yMax, py)
		xExtent[c] = jsMax(xExtent[c], px)
		yExtent[r] = jsMax(yExtent[r], py)
		xOffset[i] = opt.padCol + offsetValue(b.X1)
		yOffset[i] = opt.padRow + offsetValue(b.Y1)
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
		g.X = scene.N(xOf(g) + dx[i])
		g.Y = scene.N(yOf(g) + dy[i])
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
