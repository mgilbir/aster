package layout

import (
	"math"

	"github.com/mgilbir/aster/internal/scene"
)

// titleLayout positions the title group of a view group relative to the
// bounds of the rest of the content (frame "bounds") or to the group's own
// size (frame "group"), then returns the title group's bounds.
func titleLayout(mark *scene.Mark, width, height float64, viewBounds *scene.Bounds) *scene.Bounds {
	group := firstItem(mark)
	if group == nil {
		return &mark.Bounds
	}
	frame := extraStr(group, "frame")
	orient := orientOf(group)
	anchor := extraStr(group, "anchor")
	offset := extraNum(group, "offset")
	padding := extraNum(group, "padding")
	title := childItem(group, 0, 0)
	if title == nil {
		return &group.Bounds
	}
	subtitle := childItem(group, 1, 0)

	endPos := width
	if orient == left || orient == right {
		endPos = height
	}
	startPos := 0.0
	if frame != groupFrame {
		switch orient {
		case left:
			startPos, endPos = viewBounds.Y2, viewBounds.Y1
		case right:
			startPos, endPos = viewBounds.Y1, viewBounds.Y2
		default:
			startPos, endPos = viewBounds.X1, viewBounds.X2
		}
	} else if orient == left {
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
		var sx, sy float64
		switch orient {
		case top, bottom:
			sy = title.Bounds.Height() + padding
		case left:
			sx = title.Bounds.Width() + padding
		case right:
			sx = -title.Bounds.Width() - padding
		}
		temp.Union(&subtitle.Bounds)
		temp.Translate(sx-orZero(xOf(subtitle)), sy-orZero(yOf(subtitle)))
		cx := setNum(&subtitle.X, sx)
		cy := setNum(&subtitle.Y, sy)
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
	temp.Union(&title.Bounds)

	// position the title group
	var x, y float64
	// with an unknown orient the group keeps the coordinates it has; assigning
	// them back is a change only when they are NaN (an unset one is undefined,
	// which equals itself)
	keep := false
	switch orient {
	case top:
		x = pos
		y = viewBounds.Y1 - temp.Height() - offset
	case left:
		x = viewBounds.X1 - temp.Width() - offset
		y = pos
	case right:
		x = viewBounds.X2 + temp.Width() + offset
		y = pos
	case bottom:
		x = pos
		y = viewBounds.Y2 + offset
	default:
		x = xOf(group)
		y = yOf(group)
		keep = true
	}

	var cx, cy bool
	if keep {
		cx = group.X.Set() && math.IsNaN(x)
		cy = group.Y.Set() && math.IsNaN(y)
	} else {
		cx = setNum(&group.X, x)
		cy = setNum(&group.Y, y)
	}
	if cx || cy {
		temp.Translate(x, y)
		group.Bounds.Clear()
		group.Bounds.Union(&temp)
		mark.Bounds.Clear()
		mark.Bounds.Union(&temp)
	}
	return &group.Bounds
}
