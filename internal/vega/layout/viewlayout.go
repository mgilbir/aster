package layout

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// Autosize types.
const (
	AutosizeFit  = "fit"
	AutosizeFitX = "fit-x"
	AutosizeFitY = "fit-y"
	AutosizePad  = "pad"
	AutosizeNone = "none"
)

// Autosize is a resolved autosize configuration (the value of the `autosize`
// signal).
type Autosize struct {
	Type string // fit, fit-x, fit-y, pad or none; "" disables the adjustment
	// Contains is "content" (the default) or "padding": whether the view's
	// width and height include the padding.
	Contains string
	// Resize asks the view to re-run layout when the window changes size.
	Resize bool
}

// ParseAutosize reads an autosize specification: a type name, an object, or
// nothing (which means "pad").
func ParseAutosize(v jsval.Value) Autosize {
	switch {
	case v.IsObj():
		a := Autosize{Type: strOf(v.Get("type")), Contains: strOf(v.Get("contains")), Resize: v.Get("resize").IsTruthy()}
		return a
	case v.IsStr() && v.StrValue() != "":
		return Autosize{Type: v.StrValue()}
	}
	return Autosize{Type: AutosizePad}
}

// Padding is the view padding.
type Padding struct{ Left, Right, Top, Bottom float64 }

// View carries the view-level state ViewLayout reads.
type View struct {
	// Width and Height are the values of the width and height signals (the
	// data rectangle of the root group).
	Width, Height float64
	Padding       Padding
	// AutosizeActive is upstream's `view._autosize >= 1`: the autosize
	// adjustment applies until the view has been resized once since the
	// last change of a size or padding signal.
	AutosizeActive bool
	// Warn receives view warnings (may be nil).
	Warn func(string)
}

// Params are the parameters of one ViewLayout run.
type Params struct {
	// Grid is the group's `layout` property (see ParseGridSpec), or nil.
	Grid *GridSpec
	// Legends is config.legend.layout: offsets, anchors, margins and
	// directions of the legend stacks, with per-orient overrides.
	Legends jsval.Value
	// Autosize is set for the root group only.
	Autosize *Autosize
}

// Size is the view size adjustment a group's layout requests
// (upstream's view._resizeView call).
type Size struct {
	// ViewWidth and ViewHeight are the content size of the view (padding
	// excluded unless Autosize.Contains is "padding"); Width and Height are
	// the new size of the group's data rectangle; Origin is the offset of the
	// group inside the view.
	ViewWidth, ViewHeight float64
	Width, Height         float64
	Origin                [2]float64
	Resize                bool
}

// ViewLayout is vega-view-transforms' ViewLayout: for every group item of
// mark it applies the grid layout (if requested), positions the axes, legends
// and title, and computes the size adjustment. The sizes are returned in item
// order, only for groups that request one. An error (ErrInvalidArrayLength)
// is upstream's exception: it abandons the layout of every remaining group and
// the size adjustments gathered so far.
func ViewLayout(mark *scene.Mark, view *View, p Params) ([]Size, error) {
	var sizes []Size
	if mark == nil || view == nil {
		return nil, nil
	}
	for _, group := range mark.Items {
		if group == nil {
			continue
		}
		if p.Grid != nil {
			if err := trellisLayout(group, p.Grid, view.Warn); err != nil {
				return nil, err
			}
		}
		if s, ok := layoutGroup(view, group, p); ok {
			sizes = append(sizes, s)
		}
	}
	return sizes, nil
}

func layoutGroup(view *View, group *scene.Item, p Params) (Size, bool) {
	width := jsMax(0, group.OrZero("width"))
	height := jsMax(0, group.OrZero("height"))
	viewBounds := newBoundsSet(0, 0, width, height)
	xBounds, yBounds := viewBounds, viewBounds
	var legends []*scene.Item
	var title *scene.Mark

	// layout axes, gather legends, collect bounds
	for _, mark := range group.Items {
		if mark == nil {
			continue
		}
		switch mark.Role {
		case roleAxis:
			if firstItem(mark) == nil {
				continue
			}
			b := &yBounds
			if isYAxis(mark) {
				b = &xBounds
			}
			b.Union(axisLayout(mark, width, height))
		case roleTitle:
			title = mark
		case roleLegend:
			if it := legendLayout(mark); it != nil {
				legends = append(legends, it)
			}
		case roleFrame, roleScope, roleRowHeader, roleRowFooter, roleRowTitle,
			roleColHeader, roleColFooter, roleColTitle:
			xBounds.Union(&mark.Bounds)
			yBounds.Union(&mark.Bounds)
		default:
			viewBounds.Union(&mark.Bounds)
		}
	}

	// layout legends, adjust viewBounds
	if len(legends) > 0 {
		// group legends by orient, keeping the order orients first appear in
		var orients []string
		byOrient := map[string][]*scene.Item{}
		for _, it := range legends {
			o := orientOf(it)
			if o == "" {
				o = right
			}
			if o == none {
				continue
			}
			if _, ok := byOrient[o]; !ok {
				orients = append(orients, o)
			}
			byOrient[o] = append(byOrient[o], it)
		}
		// perform grid layout for each orient group
		for _, o := range orients {
			g := byOrient[o]
			opt := legendParams(g, o, p.Legends, &xBounds, &yBounds, width, height)
			gridLayout(g, &opt)
		}

		// update view bounds
		fit := p.Autosize != nil && (p.Autosize.Type == AutosizeFit || p.Autosize.Type == AutosizeFitX || p.Autosize.Type == AutosizeFitY)
		for _, it := range legends {
			b := &it.Bounds
			if fit {
				// For autosize fit, incorporate the orthogonal dimension only.
				// Legends that overrun the chart area will then be clipped;
				// otherwise the chart area gets reduced to nothing!
				switch orientOf(it) {
				case left, right:
					viewBounds.Add(b.X1, 0).Add(b.X2, 0)
				case top, bottom:
					viewBounds.Add(0, b.Y1).Add(0, b.Y2)
				}
			} else {
				viewBounds.Union(b)
			}
		}
	}

	// combine bounding boxes
	viewBounds.Union(&xBounds).Union(&yBounds)

	// layout title, adjust bounds
	if title != nil {
		viewBounds.Union(titleLayout(title, width, height, &viewBounds))
	}

	// override aggregated view bounds if content is clipped
	if group.Clip.IsTrue() || group.ClipPath != nil {
		viewBounds.Set(0, 0, group.OrZero("width"), group.OrZero("height"))
	}

	// perform size adjustment
	if p.Autosize == nil {
		return Size{}, false
	}
	return viewSizeLayout(view, group, &viewBounds, *p.Autosize)
}

// viewSizeLayout derives the view and group size from the aggregate content
// bounds: the content overhang on each side becomes the origin offset (left,
// top) and the room to reserve (right, bottom).
func viewSizeLayout(view *View, group *scene.Item, vb *scene.Bounds, auto Autosize) (Size, bool) {
	if !view.AutosizeActive || auto.Type == "" {
		return Size{}, false
	}
	viewWidth, viewHeight := view.Width, view.Height
	width := jsMax(0, group.OrZero("width"))
	left := jsMax(0, math.Ceil(-vb.X1))
	height := jsMax(0, group.OrZero("height"))
	top := jsMax(0, math.Ceil(-vb.Y1))
	right := jsMax(0, math.Ceil(vb.X2-width))
	bottom := jsMax(0, math.Ceil(vb.Y2-height))

	if auto.Contains == "padding" {
		viewWidth -= view.Padding.Left + view.Padding.Right
		viewHeight -= view.Padding.Top + view.Padding.Bottom
	}

	switch auto.Type {
	case AutosizeNone:
		left, top = 0, 0
		width, height = viewWidth, viewHeight
	case AutosizeFit:
		width = jsMax(0, viewWidth-left-right)
		height = jsMax(0, viewHeight-top-bottom)
	case AutosizeFitX:
		width = jsMax(0, viewWidth-left-right)
		viewHeight = height + top + bottom
	case AutosizeFitY:
		viewWidth = width + left + right
		height = jsMax(0, viewHeight-top-bottom)
	case AutosizePad:
		viewWidth = width + left + right
		viewHeight = height + top + bottom
	}

	return Size{
		ViewWidth: viewWidth, ViewHeight: viewHeight,
		Width: width, Height: height,
		Origin: [2]float64{left, top},
		Resize: auto.Resize,
	}, true
}
