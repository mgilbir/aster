package layout

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// titleLayout positions the title group of a view group relative to the
// bounds of the rest of the content (frame "bounds") or to the group's own
// size (frame "group"), then returns the title group's bounds. The orient,
// anchor and frame are compared with ===; the offset and the subtitle
// padding are added as JavaScript adds them (a text concatenates, and the
// coordinate is that text).
//
// Like the legend's, the layout reads a structure it does not check (the
// title text item inside the group's first mark), so a mark given the role
// "title" without it throws, as upstream does.
func titleLayout(mark *scene.Mark, width, height float64, viewBounds *scene.Bounds) (*scene.Bounds, error) {
	group := firstItem(mark)
	if group == nil {
		return nil, legendUndefined("frame")
	}
	wk := group.Walker()
	frame := extraStr(group, "frame")
	orient := group.OrientValue()
	anchor := extraStr(group, "anchor")
	offset := prop(group, "offset")
	padding := prop(group, "padding")
	if len(group.Items) == 0 || group.Items[0] == nil {
		return nil, legendUndefined("items") // group.items[0].items
	}
	title := childItem(group, 0, 0) // nil: undefined, which only reads below throw on
	subtitle := childItem(group, 1, 0)

	isLeft, isRight := orientIs(orient, left), orientIs(orient, right)
	endPos := width
	if isLeft || isRight {
		endPos = height
	}
	startPos := 0.0
	if frame != groupFrame {
		switch {
		case isLeft:
			startPos, endPos = viewBounds.Y2, viewBounds.Y1
		case isRight:
			startPos, endPos = viewBounds.Y1, viewBounds.Y2
		default:
			startPos, endPos = viewBounds.X1, viewBounds.X2
		}
	} else if isLeft {
		startPos, endPos = height, 0
	}

	var pos float64
	switch anchor {
	case start:
		pos = startPos
	case end:
		pos = endPos
	default:
		pos = (startPos + endPos) / 2
	}

	temp := scene.NewBounds()
	if subtitle != nil && subtitle.Text().IsTruthy() {
		// position the subtitle below/beside the title
		sx, sy := jsval.Value(jsval.Num(0)), jsval.Value(jsval.Num(0))
		if title == nil && (orientIs(orient, top) || orientIs(orient, bottom) || isLeft || isRight) {
			return nil, legendUndefined("bounds")
		}
		switch {
		case orientIs(orient, top), orientIs(orient, bottom):
			sy = jsAdd(wk, jsval.Num(title.Bounds.Height()), padding)
		case isLeft:
			sx = jsAdd(wk, jsval.Num(title.Bounds.Width()), padding)
		case isRight:
			sx = jsval.Num(-title.Bounds.Width() - num(padding))
		}
		temp.Union(&subtitle.Bounds)
		temp.Translate(num(sx)-subtitle.OrZero("x"), num(sy)-subtitle.OrZero("y"))
		cx := setChanged(subtitle, "x", sx)
		cy := setChanged(subtitle, "y", sy)
		if cx || cy {
			subtitle.Bounds.Clear()
			subtitle.Bounds.Union(&temp)
			if subtitle.Mark != nil {
				subtitle.Mark.Bounds.Clear()
				subtitle.Mark.Bounds.Union(&temp)
			}
		}
		temp.Clear()
		temp.Union(&subtitle.Bounds)
	}
	if title == nil {
		return nil, legendUndefined("bounds")
	}
	temp.Union(&title.Bounds)

	// position the title group
	var x, y jsval.Value
	switch {
	case orientIs(orient, top):
		x = jsval.Num(pos)
		y = jsval.Num(viewBounds.Y1 - temp.Height() - num(offset))
	case isLeft:
		x = jsval.Num(viewBounds.X1 - temp.Width() - num(offset))
		y = jsval.Num(pos)
	case isRight:
		x = jsAdd(wk, viewBounds.PlusValue(2, temp.Width()), offset)
		y = jsval.Num(pos)
	case orientIs(orient, bottom):
		x = jsval.Num(pos)
		y = jsAdd(wk, viewBounds.Coord(3), offset)
	default:
		x = prop(group, "x")
		y = prop(group, "y")
	}

	cx := setChanged(group, "x", x)
	cy := setChanged(group, "y", y)
	if cx || cy {
		translateV(wk, &temp, x, y)
		group.Bounds.Clear()
		group.Bounds.Union(&temp)
		mark.Bounds.Clear()
		mark.Bounds.Union(&temp)
	}
	return &group.Bounds, nil
}

// setChanged is upstream's set(item, property, value) for x or y: assign
// unless the item holds that value already (===: NaN never does), and
// report whether it changed.
func setChanged(it *scene.Item, name string, v jsval.Value) bool {
	old := prop(it, name)
	if strictEquals(old, v) {
		return false
	}
	setPos(it, name, v)
	return true
}

// strictEquals is === of two primitives (an array or an object is equal
// only to itself).
func strictEquals(a, b jsval.Value) bool {
	switch {
	case a.IsUndefined() || b.IsUndefined():
		return a.IsUndefined() && b.IsUndefined()
	case a.IsNum() && b.IsNum():
		return a.NumValue() == b.NumValue()
	case a.IsStr() && b.IsStr():
		return a.StrValue() == b.StrValue()
	}
	return jsval.SameRef(a, b)
}
