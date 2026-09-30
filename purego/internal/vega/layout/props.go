package layout

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
)

// Mark roles the layout recognises (vega-view-transforms constants).
const (
	roleAxis      = "axis"
	roleTitle     = "title"
	roleFrame     = "frame"
	roleScope     = "scope"
	roleLegend    = "legend"
	roleRowHeader = "row-header"
	roleRowFooter = "row-footer"
	roleRowTitle  = "row-title"
	roleColHeader = "column-header"
	roleColFooter = "column-footer"
	roleColTitle  = "column-title"
)

// Orientations, anchors and options.
const (
	top         = "top"
	left        = "left"
	right       = "right"
	bottom      = "bottom"
	topLeft     = "top-left"
	topRight    = "top-right"
	bottomLeft  = "bottom-left"
	bottomRight = "bottom-right"

	start  = "start"
	middle = "middle"
	end    = "end"

	groupFrame  = "group"
	boundsFrame = "bounds"

	all   = "all"
	each  = "each"
	flush = "flush"

	symbols = "symbol"
	none    = "none"
)

// extra reads an item property that the scene package leaves untyped.
func extra(it *scene.Item, name string) jsval.Value {
	if it == nil || it.Extra == nil {
		return jsval.Undefined
	}
	return it.Extra.Lookup(name)
}

// extraNum reads an untyped item property as a number the way arithmetic
// does: undefined is NaN, null is 0.
func extraNum(it *scene.Item, name string) float64 {
	return jsval.ToNumber(extra(it, name))
}

// extraStr reads an untyped string property ("" when absent).
func extraStr(it *scene.Item, name string) string {
	v := extra(it, name)
	if v.IsStr() {
		return v.StrValue()
	}
	return ""
}

// orientOf is item.orient: the typed field, or an untyped property the
// runtime may have stored instead.
func orientOf(it *scene.Item) string {
	if it.Orient != "" {
		return it.Orient
	}
	return extraStr(it, "orient")
}

// setNum is upstream's set(item, property, value) for x and y: assign, and
// report whether the value changed (an unset property always counts as
// changed, NaN never equals itself).
func setNum(n *scene.Num, v float64) bool {
	if n.Set() && n.Val() == v {
		return false
	}
	*n = scene.N(v)
	return true
}

// xy reads item.x / item.y for arithmetic (undefined is NaN).
func xOf(it *scene.Item) float64 { return it.X.Val() }
func yOf(it *scene.Item) float64 { return it.Y.Val() }

// jsRound is Math.round: halves round toward +Infinity.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}

// jsMax and jsMin are Math.max/Math.min; NaN wins.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

func jsMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Min(a, b)
}

// orZero is `v || 0`.
func orZero(v float64) float64 {
	if v == 0 || math.IsNaN(v) {
		return 0
	}
	return v
}

// markItem returns the i-th child item of a guide group item's j-th child mark
// (item.items[j].items[i]), or nil when the structure is not there.
func childItem(it *scene.Item, j, i int) *scene.Item {
	if it == nil || j < 0 || j >= len(it.Items) || it.Items[j] == nil {
		return nil
	}
	m := it.Items[j]
	if i < 0 || i >= len(m.Items) {
		return nil
	}
	return m.Items[i]
}

// firstItem is mark.items[0], or nil.
func firstItem(m *scene.Mark) *scene.Item {
	if m == nil || len(m.Items) == 0 {
		return nil
	}
	return m.Items[0]
}

// translateBounds is Bounds.translate returning nothing.
func newBoundsSet(x1, y1, x2, y2 float64) scene.Bounds {
	b := scene.NewBounds()
	b.Set(x1, y1, x2, y2)
	return b
}

func cloneBounds(b *scene.Bounds) scene.Bounds { return *b }
