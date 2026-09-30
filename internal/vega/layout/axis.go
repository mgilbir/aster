package layout

import (
	"github.com/mgilbir/aster/internal/scene"
)

// isYAxis reports whether an axis mark is a vertical axis (left or right).
func isYAxis(m *scene.Mark) bool {
	it := firstItem(m)
	if it == nil {
		return false
	}
	o := orientOf(it)
	return o == left || o == right
}

// axisIndices locates the tick, label and title marks among an axis group's
// children, which are generated in the order grid, ticks, labels, domain,
// title and only when enabled; -1 marks an absent one.
func axisIndices(it *scene.Item) (ticks, labels, title int) {
	datum := it.Datum
	index := 0
	if datum.Get("grid").IsTruthy() {
		index = 1
	}
	ticks, labels = -1, -1
	if datum.Get("ticks").IsTruthy() {
		ticks = index
		index++
	}
	if datum.Get("labels").IsTruthy() {
		labels = index
		index++
	}
	title = index
	if datum.Get("domain").IsTruthy() {
		title++
	}
	return
}

// axisLayout positions an axis group inside its parent group of the given
// width and height and returns the bounds it now occupies. The group's bounds
// are recomputed from its ticks and labels alone (the domain and the grid do
// not push the title away), padded out to minExtent/maxExtent, and the title
// is placed beyond them.
func axisLayout(axis *scene.Mark, width, height float64) *scene.Bounds {
	item := firstItem(axis)
	if item == nil {
		return &axis.Bounds
	}
	delta := 0.5
	if t := extra(item, "translate"); !t.IsNullish() {
		delta = extraNum(item, "translate")
	}
	orient := orientOf(item)
	ti, li, si := axisIndices(item)
	rng := extraNum(item, "range")
	offset := extraNum(item, "offset")
	position := orZero(extraNum(item, "position"))
	minExtent := extraNum(item, "minExtent")
	maxExtent := extraNum(item, "maxExtent")
	titlePadding := extraNum(item, "titlePadding")
	bounds := &item.Bounds

	var title *scene.Item
	if item.Datum.Get("title").IsTruthy() {
		title = childItem(item, si, 0)
	}
	dl := 0.0
	if title != nil {
		dl = scene.MultiLineOffset(title)
	}

	bounds.Clear()
	if ti > -1 {
		if m := childMark(item, ti); m != nil {
			bounds.Union(&m.Bounds)
		}
	}
	if li > -1 {
		if m := childMark(item, li); m != nil {
			bounds.Union(&m.Bounds)
		}
	}

	var x, y float64
	extent := func(v float64) float64 { return jsMax(minExtent, jsMin(maxExtent, v)) }
	switch orient {
	case top:
		x = position
		y = -offset
		s := extent(-bounds.Y1)
		bounds.Add(0, -s).Add(rng, 0)
		if title != nil {
			axisTitleLayout(title, s, titlePadding, dl, false, -1, bounds)
		}
	case left:
		x = -offset
		y = position
		s := extent(-bounds.X1)
		bounds.Add(-s, 0).Add(0, rng)
		if title != nil {
			axisTitleLayout(title, s, titlePadding, dl, true, -1, bounds)
		}
	case right:
		x = width + offset
		y = position
		s := extent(bounds.X2)
		bounds.Add(0, 0).Add(s, rng)
		if title != nil {
			axisTitleLayout(title, s, titlePadding, dl, true, 1, bounds)
		}
	case bottom:
		x = position
		y = height + offset
		s := extent(bounds.Y2)
		bounds.Add(0, 0).Add(rng, s)
		if title != nil {
			// no multi-line offset below the axis: the text grows downward
			axisTitleLayout(title, s, titlePadding, 0, false, 1, bounds)
		}
	default:
		x = xOf(item)
		y = yOf(item)
	}

	scene.BoundStroke(bounds.Translate(x, y), item, false)
	setNum(&item.X, x+delta)
	setNum(&item.Y, y+delta)

	axis.Bounds.Clear()
	axis.Bounds.Union(bounds)
	return &axis.Bounds
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
func axisTitleLayout(title *scene.Item, offset, pad, dl float64, isYAxis bool, sign float64, bounds *scene.Bounds) {
	b := &title.Bounds
	if extra(title, "auto").IsTruthy() {
		v := sign * (offset + dl + pad)
		var dx, dy float64
		if isYAxis {
			dx = orZero(xOf(title)) - v
			title.X = scene.N(v)
		} else {
			dy = orZero(yOf(title)) - v
			title.Y = scene.N(v)
		}
		b.Translate(-dx, -dy)
		if title.Mark != nil {
			title.Mark.Bounds.Clear()
			title.Mark.Bounds.Union(b)
		}
	}
	bounds.Union(b)
}
