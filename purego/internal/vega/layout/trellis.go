package layout

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/purego/internal/scene"
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
		if mark == nil || mark.Type != scene.MarkGroup {
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
func trellisLayout(group *scene.Item, opt *GridSpec, warn func(string)) {
	if opt.invalid {
		return
	}
	views := gridLayoutGroups(group)
	groups := views.marks
	bbox := boundFull
	if opt.bounds == flush {
		bbox = boundFlush
	}
	ncols := opt.columns
	if ncols <= 0 {
		ncols = len(groups)
	}
	nrows := 1
	if ncols > 0 {
		nrows = (len(groups) + ncols - 1) / ncols
	}
	cells := nrows * ncols

	// initial grid layout
	bounds := gridLayout(groups, &opt.gridOptions)
	if bounds.Empty() {
		bounds.Set(0, 0, 0, 0) // empty grid
	}

	var x, y, x2, y2 float64

	// row headers
	x = layoutHeaders(views.rowHeaders, groups, ncols, nrows, -opt.rowHeader, aggMin, false, bbox, 'l', 0, ncols, 1, opt.headerBandRow, warn)
	// column headers
	y = layoutHeaders(views.colHeaders, groups, ncols, ncols, -opt.columnHeader, aggMin, true, bbox, 't', 0, 1, ncols, opt.headerBandColumn, warn)
	// row footers
	x2 = layoutHeaders(views.rowFooters, groups, ncols, nrows, opt.rowFooter, aggMax, false, bbox, 'r', ncols-1, ncols, 1, opt.footerBandRow, warn)
	// column footers
	y2 = layoutHeaders(views.colFooters, groups, ncols, ncols, opt.columnFooter, aggMax, true, bbox, 'b', cells-ncols, 1, ncols, opt.footerBandColumn, warn)

	// row title
	if views.rowTitle != nil {
		offset := opt.rowTitle
		if opt.titleAnchorRow == end {
			offset += x2
		} else {
			offset = x - offset
		}
		layoutTitle(views.rowTitle, offset, false, &bounds, opt.titleBandRow)
	}
	// column title
	if views.colTitle != nil {
		offset := opt.columnTitle
		if opt.titleAnchorCol == end {
			offset += y2
		} else {
			offset = y - offset
		}
		layoutTitle(views.colTitle, offset, true, &bounds, opt.titleBandColumn)
	}
}

// aggregation functions for grid margin determination
func aggMin(a, b float64) float64 { return math.Floor(jsMin(a, b)) }
func aggMax(a, b float64) float64 { return math.Ceil(jsMax(a, b)) }

// layoutHeaders positions a row of headers (or footers) relative to the cells
// they annotate and returns the edge of the result. start and stride select
// the cells that line up with each header, back steps toward an earlier cell
// when the table has holes, and band, when set, centres a header within its
// cell along the other axis.
func layoutHeaders(headers, groups []*scene.Item, ncols, limit int, offset float64,
	agg func(a, b float64) float64, isX bool, bound boundFn, bf byte,
	start, stride, back int, band *float64, warn func(string)) float64 {

	n := len(groups)
	init := 0.0
	edge := 0.0

	// if there are no groups, exit early
	if n == 0 {
		return init
	}
	// compute the margin
	for i := start; i >= 0 && i < n; i += stride {
		if groups[i] != nil {
			init = agg(init, bound(groups[i], bf))
		}
	}
	// if there are no headers, return the margin
	if len(headers) == 0 {
		return init
	}
	// check if the number of headers exceeds the number of rows or columns
	if len(headers) > limit {
		if warn != nil {
			warn(fmt.Sprintf("Grid headers exceed limit: %d", limit))
		}
		headers = headers[:limit]
	}
	init += offset

	// clear the mark bounds of all headers
	for _, h := range headers {
		if h.Mark != nil {
			h.Mark.Bounds.Clear()
		}
	}

	// layout each header
	i := start
	for _, h := range headers {
		// search for the nearest group to align to, which is necessary if the
		// table has empty cells
		k := i
		if k >= n && back > 0 {
			// step back over the holes of the table in one go
			k -= ((k-n)/back + 1) * back
		}
		var g *scene.Item
		if k >= 0 && k < n {
			g = groups[k]
		}
		if g != nil {
			var x, y float64
			if isX {
				x, y = 0, 0
				if band == nil {
					x = xOf(g)
				} else {
					x = jsRound(g.Bounds.X1 + float64(*band*g.Bounds.Width()))
				}
				y = init
			} else {
				x = init
				if band == nil {
					y = yOf(g)
				} else {
					y = jsRound(g.Bounds.Y1 + float64(*band*g.Bounds.Height()))
				}
			}
			hb := &h.Bounds
			hb.Translate(x-orZero(xOf(h)), y-orZero(yOf(h)))
			if h.Mark != nil {
				h.Mark.Bounds.Union(hb)
			}
			h.X = scene.N(x)
			h.Y = scene.N(y)

			// update the current edge of the layout bounds
			var mb *scene.Bounds = hb
			if h.Mark != nil {
				mb = &h.Mark.Bounds
			}
			edge = agg(edge, boundFull2(mb, bf))
		}
		i += stride
	}
	return edge
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
func layoutTitle(g *scene.Item, offset float64, isX bool, bounds *scene.Bounds, band float64) {
	x, y := offset, offset
	if isX {
		x = jsRound(bounds.X1 + float64(band*bounds.Width()))
	} else {
		y = jsRound(bounds.Y1 + float64(band*bounds.Height()))
	}
	g.Bounds.Translate(x-orZero(xOf(g)), y-orZero(yOf(g)))
	if g.Mark != nil {
		g.Mark.Bounds.Clear()
		g.Mark.Bounds.Union(&g.Bounds)
	}
	g.X = scene.N(x)
	g.Y = scene.N(y)
}
