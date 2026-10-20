package layout

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// isYAxis reports whether an axis mark is a vertical axis: its orient is
// "left" or "right" (===: ["left"] is not).
func isYAxis(m *scene.Mark) (bool, error) {
	it := firstItem(m)
	if it == nil {
		return false, legendUndefined("orient") // mark.items[0].orient
	}
	o := it.OrientValue()
	return orientIs(o, left) || orientIs(o, right), nil
}

// axisIndices locates the tick, label and title marks among an axis group's
// children, which are generated in the order grid, ticks, labels, domain,
// title and only when enabled; a negative index marks an absent one. The
// indices are numbers as upstream computes them: `index = +datum.grid` is NaN
// for a datum that has no grid flag, which no child sits at (a NaN is not
// above -1, so the tick and label marks are skipped; the title's lookup
// finds nothing).
func axisIndices(it *scene.Item) (ticks, labels, title float64) {
	datum := it.Datum
	index := jsval.ToNumber(datum.Get("grid"))
	ticks, labels = -1, -1
	if datum.Get("ticks").IsTruthy() {
		ticks = index
		index++
	}
	if datum.Get("labels").IsTruthy() {
		labels = index
		index++
	}
	title = index + jsval.ToNumber(datum.Get("domain"))
	return
}

// childAt is `item.items[f]` for an index that is a number: nil for an
// undefined element, which is every index that is no whole number in range.
func childAt(it *scene.Item, f float64) *scene.Mark {
	if f < 0 || f != math.Trunc(f) || f >= float64(len(it.Items)) {
		return nil
	}
	return it.Items[int(f)]
}

// axisLayout positions an axis group inside its parent group of the given
// width and height and returns the bounds it now occupies. The group's bounds
// are recomputed from its ticks and labels alone (the domain and the grid do
// not push the title away), padded out to minExtent/maxExtent, and the title
// is placed beyond them.
//
// The layout reads the children the axis parser builds without checking them,
// so a mark given the role "axis" that lacks the ones its datum announces
// throws, as upstream does (after the axis item's bounds were cleared, when
// it is the bounds of a child that is missing).
func axisLayout(axis *scene.Mark, width, height float64) (*scene.Bounds, error) {
	item := firstItem(axis)
	if item == nil {
		return nil, legendUndefined("datum")
	}
	if item.Datum.IsNullish() {
		return nil, legendUndefined("grid")
	}
	w := item.Walker()
	delta := jsval.Value(jsval.Num(0.5))
	if t := prop(item, "translate"); !t.IsNullish() {
		delta = t
	}
	orient := item.OrientValue()
	ti, li, si := axisIndices(item)
	rng := num(prop(item, "range"))
	offset := prop(item, "offset")
	position := prop(item, "position")
	minExtent := num(prop(item, "minExtent"))
	maxExtent := num(prop(item, "maxExtent"))
	titlePadding := prop(item, "titlePadding")
	bounds := &item.Bounds

	var title *scene.Item
	if item.Datum.Get("title").IsTruthy() {
		// item.items[indices[2]].items[0]: an undefined mark throws
		tm := childAt(item, si)
		if tm == nil {
			return nil, legendUndefined("items")
		}
		if len(tm.Items) > 0 {
			title = tm.Items[0]
		}
	}
	dl := 0.0
	if title != nil {
		dl = scene.MultiLineOffset(title)
	}

	bounds.Clear()
	if ti > -1 {
		m := childAt(item, ti)
		if m == nil {
			return nil, legendUndefined("bounds")
		}
		bounds.Union(&m.Bounds)
	}
	if li > -1 {
		m := childAt(item, li)
		if m == nil {
			return nil, legendUndefined("bounds")
		}
		bounds.Union(&m.Bounds)
	}

	var x, y jsval.Value
	extent := func(v float64) float64 { return jsMax(minExtent, jsMin(maxExtent, v)) }
	switch {
	case orientIs(orient, top):
		x = orZeroV(position)
		y = jsval.Num(-num(offset))
		s := extent(-bounds.Y1)
		bounds.Add(0, -s).Add(rng, 0)
		if title != nil {
			axisTitleLayout(title, s, titlePadding, dl, false, -1, bounds)
		}
	case orientIs(orient, left):
		x = jsval.Num(-num(offset))
		y = orZeroV(position)
		s := extent(-bounds.X1)
		bounds.Add(-s, 0).Add(0, rng)
		if title != nil {
			axisTitleLayout(title, s, titlePadding, dl, true, -1, bounds)
		}
	case orientIs(orient, right):
		x = jsAdd(w, jsval.Num(width), offset)
		y = orZeroV(position)
		s := extent(bounds.X2)
		bounds.Add(0, 0).Add(s, rng)
		if title != nil {
			axisTitleLayout(title, s, titlePadding, dl, true, 1, bounds)
		}
	case orientIs(orient, bottom):
		x = orZeroV(position)
		y = jsAdd(w, jsval.Num(height), offset)
		s := extent(bounds.Y2)
		bounds.Add(0, 0).Add(rng, s)
		if title != nil {
			// no multi-line offset below the axis: the text grows downward
			axisTitleLayout(title, s, titlePadding, 0, false, 1, bounds)
		}
	default:
		x = prop(item, "x")
		y = prop(item, "y")
	}

	scene.BoundStroke(translateV(w, bounds, x, y), item, false)
	setPos(item, "x", jsAdd(w, x, delta))
	setPos(item, "y", jsAdd(w, y, delta))

	axis.Bounds.Clear()
	axis.Bounds.Union(bounds)
	return &axis.Bounds, nil
}

func childMark(it *scene.Item, j int) *scene.Mark {
	if j < 0 || j >= len(it.Items) {
		return nil
	}
	return it.Items[j]
}

// axisTitleLayout offsets an automatically positioned axis title from the
// axis line by the tick/label extent, the multi-line offset and the title
// padding, and grows the axis bounds to include it. sign is -1 for the top and
// left sides.
func axisTitleLayout(title *scene.Item, offset float64, pad jsval.Value, dl float64, isYAxis bool, sign float64, bounds *scene.Bounds) {
	b := &title.Bounds
	if extra(title, "auto").IsTruthy() {
		// sign * (offset + dl + pad): a text padding concatenates
		v := float64(sign * num(jsAdd(title.Walker(), jsval.Num(offset+dl), pad)))
		var dx, dy float64
		if isYAxis {
			dx = title.OrZero("x") - v
			setPos(title, "x", jsval.Num(v))
		} else {
			dy = title.OrZero("y") - v
			setPos(title, "y", jsval.Num(v))
		}
		b.Translate(-dx, -dy)
		if title.Mark != nil {
			title.Mark.Bounds.Clear()
			title.Mark.Bounds.Union(b)
		}
	}
	bounds.Union(b)
}
