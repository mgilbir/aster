package layout

import (
	"github.com/mgilbir/aster/internal/jssort"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// OverlapParams are the parameters of vega-view-transforms' Overlap
// transform.
type OverlapParams struct {
	// Method is "parity" (hide every other item until nothing overlaps) or
	// "greedy" (hide items that overlap the last visible one). Any other
	// truthy value selects parity; a falsy one disables overlap removal.
	Method jsval.Value
	// Separation is the minimum pixel gap between visible items.
	Separation float64
	// Order, if non-empty, is the field path (e.g. "datum.index") items are
	// ordered by (ascending) before removal.
	Order string
	// Sort, if set, orders the items instead (the `sort` comparator
	// parameter the parser attaches for an overlap `order`).
	Sort func(a, b *scene.Item) int
	// Bound, when set, hides items whose bounds leave the range of the scale
	// along the orientation of an axis (top/bottom bound x, others y), give
	// or take Tolerance pixels (0 means 1, as upstream's `tolerance || 1`).
	Bound *OverlapBound
}

// OverlapBound describes the scale range items must stay inside.
type OverlapBound struct {
	Range     [2]float64
	Orient    string
	Tolerance float64
}

// intersect tests the boxes for overlap including `sep` pixels of separation.
func intersect(a, b *scene.Bounds, sep float64) bool {
	return sep > math.Max(math.Max(b.X1-a.X2, a.X1-b.X2), math.Max(b.Y1-a.Y2, a.Y1-b.Y2))
}

func hasOverlap(items []*scene.Item, pad float64) bool {
	for i := 1; i < len(items); i++ {
		if intersect(&items[i-1].Bounds, &items[i].Bounds, pad) {
			return true
		}
	}
	return false
}

// hasBounds skips items without a meaningful box (empty labels).
func hasBounds(it *scene.Item) bool {
	return it.Bounds.Width() > 1 && it.Bounds.Height() > 1
}

func setOpacity(it *scene.Item, v float64) { it.Opacity = scene.N(v) }

func visible(it *scene.Item) bool { return it.Opacity.Truthy() }

// reduceParity hides every odd-indexed item and returns the even ones.
func reduceParity(items []*scene.Item) []*scene.Item {
	out := make([]*scene.Item, 0, (len(items)+1)/2)
	for i, it := range items {
		if i%2 != 0 {
			setOpacity(it, 0)
		} else {
			out = append(out, it)
		}
	}
	return out
}

// reduceGreedy scans in order, hiding items that overlap the last kept one.
func reduceGreedy(items []*scene.Item, sep float64) []*scene.Item {
	out := make([]*scene.Item, 0, len(items))
	var a *scene.Item
	for i, b := range items {
		if i == 0 || !intersect(&a.Bounds, &b.Bounds, sep) {
			a = b
			out = append(out, b)
		} else {
			setOpacity(b, 0)
		}
	}
	return out
}

// compareField orders items by a field path ("datum.index" reads the tuple),
// ascending, the way vega's compare() does for one field.
func compareField(field string) func(a, b *scene.Item) int {
	read := func(it *scene.Item) jsval.Value {
		if rest, ok := strings.CutPrefix(field, "datum."); ok {
			return it.Datum.Field(rest)
		}
		if it.Extra == nil {
			return jsval.Null
		}
		return jsval.Obj(it.Extra).Field(field)
	}
	return func(a, b *scene.Item) int { return compareValues(read(a), read(b)) }
}

func compareValues(u, v jsval.Value) int {
	// u < v || u == null && v != null ? -1 : u > v || v == null && u != null ? 1 : ...
	un, vn := u.IsNullish(), v.IsNullish()
	switch {
	case un && vn:
		return 0
	case un:
		return -1
	case vn:
		return 1
	}
	if u.IsStr() && v.IsStr() {
		switch {
		case u.StrValue() < v.StrValue():
			return -1
		case u.StrValue() > v.StrValue():
			return 1
		}
		return 0
	}
	a, b := jsval.ToNumber(u), jsval.ToNumber(v)
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	case a != a && b == b:
		return -1
	case b != b && a == a:
		return 1
	}
	return 0
}

// Overlap hides overlapping labels by setting the opacity of the items of a
// mark to 0. It preserves at least two items (the first and the last) when
// overlap persists, and afterwards recomputes the mark bounds from the visible
// items. The work is bounded: every round removes at least one item.
func Overlap(mark *scene.Mark, p OverlapParams) {
	if len(mark.Items) == 0 || !p.Method.IsTruthy() {
		// with a falsy method upstream only resets opacity when the method
		// has just been changed; a stateless call has nothing to undo
		return
	}
	greedy := p.Method.IsStr() && p.Method.StrValue() == "greedy"
	sep := p.Separation
	if math.IsNaN(sep) {
		sep = 0
	}

	// skip labels with no content
	source := make([]*scene.Item, 0, len(mark.Items))
	for _, it := range mark.Items {
		if hasBounds(it) {
			source = append(source, it)
		}
	}
	if len(source) == 0 {
		return
	}
	if p.Order != "" || p.Sort != nil {
		cmp := p.Sort
		if cmp == nil {
			cmp = compareField(p.Order)
		}
		sorted := make([]*scene.Item, len(source))
		copy(sorted, source)
		jssort.Sort(sorted, cmp)
		source = sorted
	}

	// reset all items to be fully opaque
	for _, it := range source {
		setOpacity(it, 1)
	}
	items := source

	if len(items) >= 3 && hasOverlap(items, sep) {
		for {
			if greedy {
				items = reduceGreedy(items, sep)
			} else {
				items = reduceParity(items)
			}
			if !(len(items) >= 3 && hasOverlap(items, sep)) {
				break
			}
		}
		last := source[len(source)-1]
		if len(items) < 3 && !visible(last) {
			if len(items) > 1 {
				setOpacity(items[len(items)-1], 0)
			}
			setOpacity(last, 1)
		}
	}

	if b := p.Bound; b != nil && b.Tolerance >= 0 {
		tol := b.Tolerance
		if tol == 0 || math.IsNaN(tol) {
			tol = 1
		}
		var box scene.Bounds
		if b.Orient == top || b.Orient == bottom {
			box.Set(b.Range[0], math.Inf(-1), b.Range[1], math.Inf(1))
		} else {
			box.Set(math.Inf(-1), b.Range[0], math.Inf(1), b.Range[1])
		}
		box.Expand(tol)
		for _, it := range source {
			if !box.Encloses(&it.Bounds) {
				setOpacity(it, 0)
			}
		}
	}

	// re-calculate mark bounds
	mark.Bounds.Clear()
	for _, it := range source {
		if visible(it) {
			mark.Bounds.Union(&it.Bounds)
		}
	}
}

// ParseOverlap converts the `overlap` property of a guide or text mark
// definition ({method, separation, order, bound: {scale, orient, tolerance}},
// signals already resolved) into transform parameters, as vega-parser's
// parseOverlap does. scaleRange returns the range of the named scale; a bound
// on an unknown scale is dropped, like a bound without a scale upstream.
func ParseOverlap(spec jsval.Value, scaleRange func(name string) ([2]float64, bool)) OverlapParams {
	p := OverlapParams{Method: spec.Get("method"), Separation: jsval.ToNumber(spec.Get("separation"))}
	if o := spec.Get("order"); o.IsStr() {
		p.Order = o.StrValue()
	}
	if b := spec.Get("bound"); b.IsTruthy() && scaleRange != nil {
		if r, ok := scaleRange(b.Get("scale").AsString()); ok {
			p.Bound = &OverlapBound{Range: r, Orient: strOf(b.Get("orient")), Tolerance: jsval.ToNumber(b.Get("tolerance"))}
		}
	}
	return p
}
