package layout

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// gridViews are the pieces of a trellis group, sorted by role.
type gridViews struct {
	marks                  []*scene.Item
	rowHeaders, rowFooters []*scene.Item
	colHeaders, colFooters []*scene.Item
	rowTitle, colTitle     *scene.Item
}

// gridLayoutGroups sorts the group marks of a group into the cells of the
// table and its headers, footers and titles. Guide groups are not cells.
func gridLayoutGroups(group *scene.Item) gridViews {
	var v gridViews
	for _, mark := range group.Items {
		if mark == nil || !mark.IsGroup() {
			continue
		}
		switch mark.Role {
		case roleAxis, roleLegend, roleTitle:
		case roleRowHeader:
			v.rowHeaders = append(v.rowHeaders, mark.Items...)
		case roleRowFooter:
			v.rowFooters = append(v.rowFooters, mark.Items...)
		case roleColHeader:
			v.colHeaders = append(v.colHeaders, mark.Items...)
		case roleColFooter:
			v.colFooters = append(v.colFooters, mark.Items...)
		case roleRowTitle:
			if len(mark.Items) > 0 {
				v.rowTitle = mark.Items[0]
			} else {
				v.rowTitle = nil
			}
		case roleColTitle:
			if len(mark.Items) > 0 {
				v.colTitle = mark.Items[0]
			} else {
				v.colTitle = nil
			}
		default:
			v.marks = append(v.marks, mark.Items...)
		}
	}
	return v
}

// boundFn reads one side of a group's extent for header placement.
type boundFn func(it *scene.Item, field byte) float64

func boundFlush(it *scene.Item, field byte) float64 {
	switch field {
	case 'l': // x1
		return orZero(xOf(it))
	case 't': // y1
		return orZero(yOf(it))
	case 'r': // x2
		return orZero(xOf(it)) + orZero(it.Width.Val())
	}
	return orZero(yOf(it)) + orZero(it.Height.Val())
}

func boundFull(it *scene.Item, field byte) float64 {
	switch field {
	case 'l':
		return it.Bounds.X1
	case 't':
		return it.Bounds.Y1
	case 'r':
		return it.Bounds.X2
	}
	return it.Bounds.Y2
}

// trellisLayout lays out the cells of a group as a table, then positions the
// row/column headers, footers and titles around it. warn (may be nil)
// receives upstream's view warnings.
func trellisLayout(group *scene.Item, opt *GridSpec, warn func(string)) error {
	if opt.invalid {
		return ErrInvalidArrayLength
	}
	views := gridLayoutGroups(group)
	groups := views.marks
	bbox := boundFull
	if opt.bounds == flush {
		bbox = boundFlush
	}
	// ncols = opt.columns || groups.length (a text or a list as given);
	// nrows = ncols <= 0 ? 1 : Math.ceil(groups.length / ncols)
	ncols := jsval.Value(jsval.Num(float64(opt.columns)))
	if !opt.columnsRaw.IsUndefined() {
		ncols = opt.columnsRaw
	} else if opt.columns <= 0 {
		ncols = jsval.Num(float64(len(groups)))
	}
	nc := num(ncols)
	nrows := 1.0
	if !(nc <= 0) {
		nrows = math.Ceil(float64(len(groups)) / nc)
	}
	cells := float64(nrows * nc)

	// initial grid layout
	bounds, err := gridLayout(groups, &opt.gridOptions)
	if err != nil {
		return err
	}
	if bounds.Empty() {
		bounds.Set(0, 0, 0, 0) // empty grid
	}

	var x, y, x2, y2 float64
	neg := func(v jsval.Value) jsval.Value { return jsval.Num(-num(v)) }
	one, zero := jsval.Num(1), jsval.Num(0)
	h := func(headers []*scene.Item, limit, offset jsval.Value, agg func(a, b float64) float64, isX bool, bf byte, start, stride, back jsval.Value, band *float64) (float64, error) {
		return layoutHeaders(headers, groups, limit, offset, agg, isX, bbox, bf, start, stride, back, band, warn)
	}
	// row headers
	if x, err = h(views.rowHeaders, jsval.Num(nrows), neg(opt.rowHeader), aggMin, false, 'l', zero, ncols, one, opt.headerBandRow); err != nil {
		return err
	}
	// column headers
	if y, err = h(views.colHeaders, ncols, neg(opt.columnHeader), aggMin, true, 't', zero, one, ncols, opt.headerBandColumn); err != nil {
		return err
	}
	// row footers
	if x2, err = h(views.rowFooters, jsval.Num(nrows), opt.rowFooter, aggMax, false, 'r', jsval.Num(nc-1), ncols, one, opt.footerBandRow); err != nil {
		return err
	}
	// column footers
	if y2, err = h(views.colFooters, ncols, opt.columnFooter, aggMax, true, 'b', jsval.Num(cells-nc), one, ncols, opt.footerBandColumn); err != nil {
		return err
	}

	// offset = anchor === End ? x2 + offset : x - offset (a text concatenates)
	titleOffset := func(it *scene.Item, anchor string, off jsval.Value, edge2, edge float64) jsval.Value {
		if anchor == end {
			return jsAdd(it.Walker(), jsval.Num(edge2), off)
		}
		return jsval.Num(edge - num(off))
	}
	// row title
	if views.rowTitle != nil {
		layoutTitle(views.rowTitle, titleOffset(views.rowTitle, opt.titleAnchorRow, opt.rowTitle, x2, x), false, &bounds, opt.titleBandRow)
	}
	// column title
	if views.colTitle != nil {
		layoutTitle(views.colTitle, titleOffset(views.colTitle, opt.titleAnchorCol, opt.columnTitle, y2, y), true, &bounds, opt.titleBandColumn)
	}
	return nil
}

// aggregation functions for grid margin determination
func aggMin(a, b float64) float64 { return math.Floor(jsMin(a, b)) }
func aggMax(a, b float64) float64 { return math.Ceil(jsMax(a, b)) }

// layoutHeaders positions a row of headers (or footers) relative to the cells
// they annotate and returns the edge of the result. start and stride select
// the cells that line up with each header, back steps toward an earlier cell
// when the table has holes, and band, when set, centres a header within its
// cell along the other axis. The indexes are JavaScript values: a columns
// given as text steps by concatenation (`i += stride`), and an index that
// is no array key finds no cell. A header with no cell to align to reads a
// property of undefined: upstream's TypeError.
func layoutHeaders(headers, groups []*scene.Item, limit, offset jsval.Value,
	agg func(a, b float64) float64, isX bool, bound boundFn, bf byte,
	start, stride, back jsval.Value, band *float64, warn func(string)) (float64, error) {

	n := len(groups)
	nv := jsval.Num(float64(n))
	init := 0.0
	edge := 0.0
	var wk jsval.Walker
	if n > 0 {
		wk = groups[0].Walker()
	}
	guard := loopGuard{max: trellisLoopPerItem*(n+len(headers)) + trellisLoopBase}

	// if there are no groups, exit early
	if n == 0 {
		return init, nil
	}
	// compute the margin
	for i := start; jsLess(i, nv); i = jsAdd(wk, i, stride) {
		if err := guard.step(); err != nil {
			return 0, err
		}
		if g := groupAt(groups, i); g != nil {
			init = agg(init, bound(g, bf))
		}
	}
	// if there are no headers, return the margin
	if len(headers) == 0 {
		return init, nil
	}
	// check if the number of headers exceeds the number of rows or columns
	if float64(len(headers)) > num(limit) {
		if warn != nil {
			warn("Grid headers exceed limit: " + wk.String(limit))
		}
		headers = headers[:sliceEnd(len(headers), num(limit))]
	}
	initV := jsAdd(wk, jsval.Num(init), offset)

	// clear the mark bounds of all headers
	for _, h := range headers {
		if h.Mark != nil {
			h.Mark.Bounds.Clear()
		}
	}

	// layout each header
	var g *scene.Item
	i := start
	for _, h := range headers {
		// for (k=i; k >= 0 && (g = groups[k]) == null; k -= back);
		// search for the nearest group to align to, which is necessary if
		// the table has empty cells; g keeps its value when the loop does
		// not run
		for k := i; num(k) >= 0; k = jsval.Num(num(k) - num(back)) {
			if err := guard.step(); err != nil {
				return 0, err
			}
			if b := num(back); num(k) >= float64(n) && b > 0 {
				// groups[k] is undefined for every k from here down to
				// below n: step over them in one go
				g = nil
				k = jsval.Num(num(k) - float64(math.Ceil((num(k)-float64(n)+1)/b)*b))
				if !(num(k) >= 0) {
					break
				}
			}
			if g = groupAt(groups, k); g != nil {
				break
			}
		}
		if g == nil {
			prop := "x"
			if band != nil {
				prop = "bounds"
			}
			if !isX {
				prop = "y"
				if band != nil {
					prop = "bounds"
				}
			}
			return 0, fmt.Errorf("Cannot read properties of undefined (reading '%s')", prop)
		}
		var x, y jsval.Value
		if isX {
			if band == nil {
				x = prop(g, "x")
			} else {
				x = jsval.Num(jsRound(g.Bounds.PlusNum(0, float64(*band*g.Bounds.Width()))))
			}
			y = initV
		} else {
			x = initV
			if band == nil {
				y = prop(g, "y")
			} else {
				y = jsval.Num(jsRound(g.Bounds.PlusNum(1, float64(*band*g.Bounds.Height()))))
			}
		}
		hb := &h.Bounds
		hb.Translate(num(x)-h.OrZero("x"), num(y)-h.OrZero("y"))
		if h.Mark != nil {
			h.Mark.Bounds.Union(hb)
		}
		setPos(h, "x", x)
		setPos(h, "y", y)

		// update the current edge of the layout bounds
		mb := hb
		if h.Mark != nil {
			mb = &h.Mark.Bounds
		}
		edge = agg(edge, boundFull2(mb, bf))
		i = jsAdd(wk, i, stride)
	}
	return edge, nil
}

// boundFull2 reads a side of a Bounds by header field.
func boundFull2(b *scene.Bounds, field byte) float64 {
	switch field {
	case 'l':
		return b.X1
	case 't':
		return b.Y1
	case 'r':
		return b.X2
	}
	return b.Y2
}

// layoutTitle places a row or column title along the grid bounds.
func layoutTitle(g *scene.Item, offset jsval.Value, isX bool, bounds *scene.Bounds, band float64) {
	x, y := offset, offset
	if isX {
		x = jsval.Num(jsRound(bounds.PlusNum(0, float64(band*bounds.Width()))))
	} else {
		y = jsval.Num(jsRound(bounds.PlusNum(1, float64(band*bounds.Height()))))
	}
	g.Bounds.Translate(num(x)-g.OrZero("x"), num(y)-g.OrZero("y"))
	if g.Mark != nil {
		g.Mark.Bounds.Clear()
		g.Mark.Bounds.Union(&g.Bounds)
	}
	setPos(g, "x", x)
	setPos(g, "y", y)
}
