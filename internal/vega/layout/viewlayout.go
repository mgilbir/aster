package layout

import (
	"fmt"
	"math"
	"sort"
	"strconv"

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
// signal, read as ViewLayout reads it: `auto = _.autosize || {}`, then
// `auto.type`, compared with ===).
type Autosize struct {
	// Type is fit, fit-x, fit-y, pad or none, or another string; a truthy
	// value that is no string is AutosizeOther, which no type equals; ""
	// (a falsy type) disables the adjustment.
	Type string
	// Contains is "content" (the default) or "padding": whether the view's
	// width and height include the padding.
	Contains string
	// Resize asks the view to re-run layout when the window changes size.
	Resize bool
}

// errNoItems is the TypeError a layout of an item without child marks
// raises (group.items.length).
var errNoItems = &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of undefined (reading 'length')"}

// AutosizeOther is the Type of a truthy autosize type that is no string.
const AutosizeOther = "\x00"

// ParseAutosize reads the autosize signal's value as ViewLayout does: its
// type, contains and resize members (whatever the value: the parser makes
// it an object, but a signal can hold anything); a falsy value is `{}`, no
// adjustment.
func ParseAutosize(v jsval.Value) Autosize {
	var a Autosize
	if !v.IsTruthy() {
		return a
	}
	t := jsval.Member(v, "type")
	if s, ok := t.Strict(); ok {
		a.Type = s
	} else if t.IsTruthy() {
		a.Type = AutosizeOther
	}
	if s, ok := jsval.Member(v, "contains").Strict(); ok {
		a.Contains = s
	}
	a.Resize = jsval.Member(v, "resize").IsTruthy()
	return a
}

// Padding is the view padding.
type Padding struct{ Left, Right, Top, Bottom float64 }

// View carries the view-level state ViewLayout reads.
type View struct {
	// Width and Height are the values of the width and height signals (the
	// data rectangle of the root group), as they are: upstream's
	// view._width, which an autosize "none" (or a type it does not know)
	// hands back as the view size uncoerced.
	Width, Height jsval.Value
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
	// group inside the view. The sizes are values: a width signal's value
	// passes through an autosize "none" as it is.
	ViewWidth, ViewHeight jsval.Value
	Width, Height         jsval.Value
	Origin                [2]float64
	Resize                bool
}

// ViewLayout is vega-view-transforms' ViewLayout: for every group item of
// mark it applies the grid layout (if requested), positions the axes, legends
// and title, and computes the size adjustment. The sizes are returned in item
// order, only for groups that request one. An error (ErrInvalidArrayLength)
// is upstream's exception: it abandons the layout of the remaining groups,
// while the groups laid out before it keep their size adjustments (upstream
// resizes the view as it lays out each group); those are returned with it.
func ViewLayout(mark *scene.Mark, view *View, p Params) ([]Size, error) {
	var sizes []Size
	if mark == nil || view == nil {
		return nil, nil
	}
	for _, group := range mark.Items {
		if group == nil {
			continue
		}
		if group.Items == nil {
			// an item of a mark that is no group (its type given as a
			// list, say) has no items array, whose length the layout reads
			return sizes, errNoItems
		}
		if p.Grid != nil {
			if err := trellisLayout(group, p.Grid, view.Warn); err != nil {
				return sizes, err
			}
		}
		s, ok, err := layoutGroup(view, group, p)
		if err != nil {
			return sizes, err
		}
		if ok {
			sizes = append(sizes, s)
		}
	}
	return sizes, nil
}

func layoutGroup(view *View, group *scene.Item, p Params) (Size, bool, error) {
	width := jsMax(0, group.OrZero("width"))
	height := jsMax(0, group.OrZero("height"))
	viewBounds := newBoundsSet(0, 0, width, height)
	xBounds, yBounds := viewBounds.Clone(), viewBounds.Clone()
	var legends []*scene.Item
	var title *scene.Mark

	// layout axes, gather legends, collect bounds
	for _, mark := range group.Items {
		if mark == nil {
			continue
		}
		switch mark.Role {
		case roleAxis:
			b := &yBounds
			y, err := isYAxis(mark)
			if err != nil {
				return Size{}, false, err
			}
			if y {
				b = &xBounds
			}
			ab, err := axisLayout(mark, width, height)
			if err != nil {
				return Size{}, false, err
			}
			b.Union(ab)
		case roleTitle:
			title = mark
		case roleLegend:
			it, err := legendLayout(mark)
			if err != nil {
				return Size{}, false, err
			}
			legends = append(legends, it)
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
		// group legends by orient: `l[item.orient || 'right']`, a plain
		// object keyed by the orient's text, whose keys for-in lists
		// integers first
		var orients []string
		byOrient := map[string][]*scene.Item{}
		for _, it := range legends {
			ov := it.OrientValue()
			if !ov.IsTruthy() {
				ov = jsval.Str(right)
			}
			if orientIs(ov, none) {
				continue
			}
			o, _ := ov.PropertyKey(jsval.MaxStringLength)
			if _, ok := byOrient[o]; !ok {
				if f, inherited := jsval.Inherited(o); o == "__proto__" || inherited && f.IsTruthy() {
					// l[orient] is Object.prototype's member: no push
					return Size{}, false, fmt.Errorf("(l[orient] || (l[orient] = [])).push is not a function")
				}
				orients = append(orients, o)
			}
			byOrient[o] = append(byOrient[o], it)
		}
		sortObjectKeys(orients)
		// perform grid layout for each orient group
		for _, o := range orients {
			g := byOrient[o]
			opt, err := legendParams(g, o, p.Legends, &xBounds, &yBounds, width, height)
			if err != nil {
				return Size{}, false, err
			}
			if _, err := gridLayout(g, &opt); err != nil {
				return Size{}, false, err
			}
		}

		// update view bounds
		fit := p.Autosize != nil && (p.Autosize.Type == AutosizeFit || p.Autosize.Type == AutosizeFitX || p.Autosize.Type == AutosizeFitY)
		for _, it := range legends {
			b := &it.Bounds
			if fit {
				// For autosize fit, incorporate the orthogonal dimension only.
				// Legends that overrun the chart area will then be clipped;
				// otherwise the chart area gets reduced to nothing!
				o := it.OrientValue()
				switch {
				case orientIs(o, left), orientIs(o, right):
					viewBounds.Add(b.X1, 0).Add(b.X2, 0)
				case orientIs(o, top), orientIs(o, bottom):
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
		tb, err := titleLayout(title, width, height, &viewBounds)
		if err != nil {
			return Size{}, false, err
		}
		viewBounds.Union(tb)
	}

	// override aggregated view bounds if content is clipped
	if group.Clip.IsTrue() || group.ClipPath != nil {
		viewBounds.Set(0, 0, group.OrZero("width"), group.OrZero("height"))
	}

	// perform size adjustment
	if p.Autosize == nil {
		return Size{}, false, nil
	}
	s, ok := viewSizeLayout(view, group, &viewBounds, *p.Autosize)
	return s, ok, nil
}

// sortObjectKeys orders keys as a plain object lists them: the array
// indexes (canonical integers up to 2^32 - 2) ascending first, then the
// others in the order they were added.
func sortObjectKeys(keys []string) {
	index := func(k string) (uint64, bool) {
		n, err := strconv.ParseUint(k, 10, 32)
		return n, err == nil && n < 1<<32-1 && strconv.FormatUint(n, 10) == k
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, ai := index(keys[i])
		b, bi := index(keys[j])
		switch {
		case ai && bi:
			return a < b
		case ai != bi:
			return ai
		}
		return false
	})
}

// viewSizeLayout derives the view and group size from the aggregate content
// bounds: the content overhang on each side becomes the origin offset (left,
// top) and the room to reserve (right, bottom). The view size starts as the
// width and height signals' values, which only subtraction and the fit and
// pad types make numbers.
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
	num := jsval.ToNumber

	if auto.Contains == "padding" {
		viewWidth = jsval.Num(num(viewWidth) - (view.Padding.Left + view.Padding.Right))
		viewHeight = jsval.Num(num(viewHeight) - (view.Padding.Top + view.Padding.Bottom))
	}

	w, h := jsval.Num(width), jsval.Num(height)
	switch auto.Type {
	case AutosizeNone:
		left, top = 0, 0
		w, h = viewWidth, viewHeight
	case AutosizeFit:
		w = jsval.Num(jsMax(0, num(viewWidth)-left-right))
		h = jsval.Num(jsMax(0, num(viewHeight)-top-bottom))
	case AutosizeFitX:
		w = jsval.Num(jsMax(0, num(viewWidth)-left-right))
		viewHeight = jsval.Num(height + top + bottom)
	case AutosizeFitY:
		viewWidth = jsval.Num(width + left + right)
		h = jsval.Num(jsMax(0, num(viewHeight)-top-bottom))
	case AutosizePad:
		viewWidth = jsval.Num(width + left + right)
		viewHeight = jsval.Num(height + top + bottom)
	}

	return Size{
		ViewWidth: viewWidth, ViewHeight: viewHeight,
		Width: w, Height: h,
		Origin: [2]float64{left, top},
		Resize: auto.Resize,
	}, true
}
