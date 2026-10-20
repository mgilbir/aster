package layout

import (
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// Upstream's layout reads the guide group's properties (offset, position,
// padding, titlePadding, translate) as they were encoded and does its
// arithmetic on them in JavaScript: `height + offset` concatenates when the
// offset is a string, an array or an object, and the text is what the item's
// coordinate becomes, printed as it is in the group's transform. The helpers
// below reproduce that. The bounds of the layout stay numbers: a coordinate
// upstream's Bounds holds as such a text is held as the number it converts
// to, which is what the comparisons and the arithmetic that follow read
// (Math.max, Math.ceil, a subtraction).

// prop is item[name] as the item holds it (undefined when unset).
func prop(it *scene.Item, name string) jsval.Value {
	// x and y, which the layouts read per item, without Get's lookup
	if len(it.Raw) == 0 {
		var n scene.Num
		switch name {
		case "x":
			n = it.X
		case "y":
			n = it.Y
		default:
			return it.Get(name)
		}
		if !n.Set() {
			return jsval.Undefined
		}
		return jsval.Num(n.Val())
	}
	return it.Get(name)
}

// isConcat reports a value that `+` turns into text: a string, or an array
// or an object (whose primitive is its String).
func isConcat(v jsval.Value) bool { return v.IsStr() || v.IsArr() || v.IsObj() || v.IsTimestamp() }

// jsAdd is JavaScript's a + b.
func jsAdd(w jsval.Walker, a, b jsval.Value) jsval.Value {
	if a.IsNum() && b.IsNum() {
		return jsval.Num(a.NumValue() + b.NumValue())
	}
	if isConcat(a) || isConcat(b) {
		return jsval.Str(w.String(a) + w.String(b))
	}
	return jsval.Num(jsval.ToNumber(a) + jsval.ToNumber(b))
}

// num is the number arithmetic other than + reads from v (ToNumber).
func num(v jsval.Value) float64 { return jsval.ToNumber(v) }

// orZeroV is `v || 0`.
func orZeroV(v jsval.Value) jsval.Value {
	if v.IsTruthy() {
		return v
	}
	return jsval.Num(0)
}

// translateV is Bounds.translate(dx, dy) with JavaScript's +: a coordinate
// moved by a text is the number the concatenation converts to.
func translateV(w jsval.Walker, b *scene.Bounds, dx, dy jsval.Value) *scene.Bounds {
	if !isConcat(dx) && !isConcat(dy) {
		return b.Translate(num(dx), num(dy))
	}
	add := func(c float64, d jsval.Value) float64 { return num(jsAdd(w, jsval.Num(c), d)) }
	b.X1, b.X2 = add(b.X1, dx), add(b.X2, dx)
	b.Y1, b.Y2 = add(b.Y1, dy), add(b.Y2, dy)
	return b
}

// setPos is `item[prop] = v` for "x" or "y": a text is kept as given, which
// the group's transform prints.
func setPos(it *scene.Item, name string, v jsval.Value) {
	if v.IsNum() {
		n := scene.N(v.NumValue())
		if it.Raw != nil {
			delete(it.Raw, name)
		}
		if name == "x" {
			it.X = n
		} else {
			it.Y = n
		}
		return
	}
	_, _ = it.Set(name, v)
}

// orientIs is `item.orient === name`.
func orientIs(o jsval.Value, name string) bool { return o.IsStr() && o.StrValue() == name }

// jsLess is JavaScript's a < b of two values: two strings compare as text
// (by UTF-16 code unit), anything else as numbers (false with NaN).
func jsLess(a, b jsval.Value) bool {
	if a.IsStr() && b.IsStr() {
		return utf16Less(a.StrValue(), b.StrValue())
	}
	return num(a) < num(b)
}

// utf16Less orders two strings by UTF-16 code unit.
func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// groupAt is groups[k] for an index of any type: the group at a canonical
// array index below the length, nil (undefined) for any other key.
func groupAt(groups []*scene.Item, k jsval.Value) *scene.Item {
	var f float64
	switch {
	case k.IsNum():
		f = k.NumValue()
	case k.IsStr():
		s := k.StrValue()
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != s {
			return nil
		}
		f = float64(n)
	default:
		return nil
	}
	if f < 0 || f != math.Floor(f) || f >= float64(len(groups)) {
		return nil
	}
	return groups[int(f)]
}

// sliceEnd is where Array.prototype.slice(0, end) stops: end is truncated
// toward zero, a negative end counts back from the length, NaN is 0.
func sliceEnd(length int, end float64) int {
	if math.IsNaN(end) {
		return 0
	}
	e := math.Trunc(end)
	if e < 0 {
		return int(math.Max(float64(length)+e, 0))
	}
	return int(math.Min(e, float64(length)))
}

// loopGuard bounds a loop whose count upstream's arithmetic on a
// specification's text decides (an index stepped by concatenation may
// shrink and never end): past max steps the layout fails with ErrLimit,
// where upstream would not finish.
type loopGuard struct{ n, max int }

// The bounds of those loops (docs/limits.md): steps per cell, plus a base,
// for the grid layout of a group's cells, and per cell and header for the
// trellis headers and footers.
const (
	gridLoopPerCell    = 64
	gridLoopBase       = 1 << 16
	trellisLoopPerItem = 4
	trellisLoopBase    = 64
)

func (g *loopGuard) step() error {
	g.n++
	if g.n > g.max {
		return fmt.Errorf("%w: a layout loop stepped by text did not end within %d steps", budget.ErrLimit, g.max)
	}
	return nil
}
