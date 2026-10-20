package scene

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Upstream's Bounds is a plain object whose coordinates are whatever the
// bound functions computed, and they compute with the item's values as
// given: a rect at x "10" has the bounds x1 "10" (`item.x || 0`) and x2
// "1020" (`x + item.width`), a group at x "5" translates its box by
// concatenation (`this.x1 += dx`). Such a string travels on: union and
// intersect compare it (as text against another string, as a number against
// a number), translate and expand's `+=` append to it, everything else
// (`-=`, `*=`, Math.floor, rotation, width()) reads it as a number.
//
// A Bounds keeps such coordinates in s, which is nil for every box whose
// coordinates are all numbers (the fast path of every method), and holds
// the number each string converts to in the coordinate's field, which is
// what a numeric reader sees. A boundStrs is never modified once a Bounds
// refers to it (copies of a Bounds share it).
type boundStrs struct {
	v    [4]string // x1, y1, x2, y2
	has  [4]bool
	date [4]bool // the coordinate is a Date: v is its String, has is false
}

// coord is one coordinate as upstream holds it: a number, or a string (with
// the number it converts to).
type coord struct {
	f   float64
	s   string
	str bool
	// date marks a Date: f is its time value (what the comparisons and
	// every arithmetic but + read), s its String (what + concatenates).
	date bool
}

func numCoord(f float64) coord { return coord{f: f} }

func strCoord(s string) coord { return coord{f: jsval.StringToNumber(s), s: s, str: true} }

// coordOf is the coordinate a value makes: a string stays one, an array or
// object is the string it converts to (both `+` and the comparisons read
// one through its String), anything else the number it converts to.
func coordOf(w jsval.Walker, v jsval.Value) coord {
	switch v.Kind() {
	case jsval.KindNum:
		return coord{f: v.NumValue()}
	case jsval.KindTimestamp:
		return coord{f: jsval.ToNumber(v), s: w.String(v), date: true}
	case jsval.KindStr:
		return strCoord(v.StrValue())
	case jsval.KindArr, jsval.KindObj:
		return strCoord(w.String(v))
	}
	return coord{f: jsval.ToNumber(v)}
}

// text is String(c).
func (c coord) text() string {
	if c.str || c.date {
		return c.s
	}
	return jsval.JSNumberString(c.f)
}

// Value is the coordinate as a JavaScript value.
func (c coord) Value() jsval.Value {
	if c.str {
		return jsval.Str(c.s)
	}
	if c.date {
		return jsval.Timestamp(c.f)
	}
	return jsval.Num(c.f)
}

// coordLess is a < b: two strings compare as text, anything else as numbers.
func coordLess(a, b coord) bool {
	if a.str && b.str {
		return jsval.CompareUTF16(a.s, b.s) < 0
	}
	return a.f < b.f
}

// coordPlus is a + b: a string on either side makes it a concatenation.
func coordPlus(a, b coord) coord {
	if a.str || b.str || a.date || b.date {
		return strCoord(a.text() + b.text())
	}
	return numCoord(a.f + b.f)
}

// coordLooseEq is a == b.
func coordLooseEq(a, b coord) bool {
	if a.str && b.str {
		return a.s == b.s
	}
	return a.f == b.f
}

func (b *Bounds) field(i int) *float64 {
	switch i {
	case 0:
		return &b.X1
	case 1:
		return &b.Y1
	case 2:
		return &b.X2
	}
	return &b.Y2
}

// at is coordinate i (x1, y1, x2, y2).
func (b *Bounds) at(i int) coord {
	f := *b.field(i)
	if b.s != nil {
		if b.s.has[i] {
			return coord{f: f, s: b.s.v[i], str: true}
		}
		if b.s.date[i] {
			return coord{f: f, s: b.s.v[i], date: true}
		}
	}
	return coord{f: f}
}

// put sets coordinate i, copying the strings it shares with other boxes.
func (b *Bounds) put(i int, c coord) {
	*b.field(i) = c.f
	special := c.str || c.date
	if !special && (b.s == nil || !(b.s.has[i] || b.s.date[i])) {
		return
	}
	n := new(boundStrs)
	if b.s != nil {
		*n = *b.s
	}
	n.v[i], n.has[i], n.date[i] = c.s, c.str, c.date
	if !special {
		n.v[i] = ""
		any := false
		for j := 0; j < 4; j++ {
			any = any || n.has[j] || n.date[j]
		}
		if !any {
			n = nil
		}
	}
	b.s = n
}

// PlusNum is `coordinate i + f` read as a number (Math.round(b.x1 + k * w)):
// a string coordinate makes the sum a concatenation. It allocates nothing for
// a box whose coordinates are all numbers.
func (b *Bounds) PlusNum(i int, f float64) float64 {
	if b.s == nil {
		return *b.field(i) + f
	}
	return coordPlus(b.at(i), numCoord(f)).f
}

// PlusValue is PlusNum as the value the sum is (a number or a string).
func (b *Bounds) PlusValue(i int, f float64) jsval.Value {
	if b.s == nil {
		return jsval.Num(*b.field(i) + f)
	}
	return coordPlus(b.at(i), numCoord(f)).Value()
}

// HasStrings reports a coordinate that is a string (see boundStrs).
func (b *Bounds) HasStrings() bool { return b.s != nil }

// Coord is coordinate i (0 x1, 1 y1, 2 x2, 3 y2) as a JavaScript value.
func (b *Bounds) Coord(i int) jsval.Value { return b.at(i).Value() }

// SetJS is Set for values as an item gives them (bounds.set(x1, y1, x2, y2)
// of a bound function): strings and the strings of arrays and objects stay
// strings and order as text against each other.
func (b *Bounds) SetJS(w jsval.Walker, x1, y1, x2, y2 jsval.Value) *Bounds {
	b.setCoords(coordOf(w, x1), coordOf(w, y1), coordOf(w, x2), coordOf(w, y2))
	return b
}

func (b *Bounds) setCoords(x1, y1, x2, y2 coord) {
	b.s = nil
	if coordLess(x2, x1) {
		b.put(2, x1)
		b.put(0, x2)
	} else {
		b.put(0, x1)
		b.put(2, x2)
	}
	if coordLess(y2, y1) {
		b.put(3, y1)
		b.put(1, y2)
	} else {
		b.put(1, y1)
		b.put(3, y2)
	}
}

// AddJS is Add for values.
func (b *Bounds) AddJS(w jsval.Walker, x, y jsval.Value) *Bounds {
	b.addCoords(coordOf(w, x), coordOf(w, y))
	return b
}

func (b *Bounds) addCoords(x, y coord) {
	if coordLess(x, b.at(0)) {
		b.put(0, x)
	}
	if coordLess(y, b.at(1)) {
		b.put(1, y)
	}
	if coordLess(b.at(2), x) {
		b.put(2, x)
	}
	if coordLess(b.at(3), y) {
		b.put(3, y)
	}
}

// TranslateJS is Translate for offsets as an item gives them
// (`item.x || 0`): a string offset, or a string coordinate, concatenates.
func (b *Bounds) TranslateJS(w jsval.Walker, dx, dy jsval.Value) *Bounds {
	if b.s == nil && dx.IsNum() && dy.IsNum() {
		return b.Translate(dx.NumValue(), dy.NumValue())
	}
	cx, cy := coordOf(w, dx), coordOf(w, dy)
	b.put(0, coordPlus(b.at(0), cx))
	b.put(2, coordPlus(b.at(2), cx))
	b.put(1, coordPlus(b.at(1), cy))
	b.put(3, coordPlus(b.at(3), cy))
	return b
}

// The slow paths of the Bounds methods, for a box with a string coordinate.

func (b *Bounds) addSlow(x, y float64) {
	b.addCoords(numCoord(x), numCoord(y))
}

func (b *Bounds) expandSlow(d float64) {
	// -= reads the coordinate as a number, += appends to a string.
	b.put(0, numCoord(b.X1-d))
	b.put(1, numCoord(b.Y1-d))
	b.put(2, coordPlus(b.at(2), numCoord(d)))
	b.put(3, coordPlus(b.at(3), numCoord(d)))
}

func (b *Bounds) translateSlow(dx, dy float64) {
	x, y := numCoord(dx), numCoord(dy)
	b.put(0, coordPlus(b.at(0), x))
	b.put(2, coordPlus(b.at(2), x))
	b.put(1, coordPlus(b.at(1), y))
	b.put(3, coordPlus(b.at(3), y))
}

func (b *Bounds) unionSlow(o *Bounds) {
	if c := o.at(0); coordLess(c, b.at(0)) {
		b.put(0, c)
	}
	if c := o.at(1); coordLess(c, b.at(1)) {
		b.put(1, c)
	}
	if c := o.at(2); coordLess(b.at(2), c) {
		b.put(2, c)
	}
	if c := o.at(3); coordLess(b.at(3), c) {
		b.put(3, c)
	}
}

func (b *Bounds) intersectSlow(o *Bounds) {
	if c := o.at(0); coordLess(b.at(0), c) {
		b.put(0, c)
	}
	if c := o.at(1); coordLess(b.at(1), c) {
		b.put(1, c)
	}
	if c := o.at(2); coordLess(c, b.at(2)) {
		b.put(2, c)
	}
	if c := o.at(3); coordLess(c, b.at(3)) {
		b.put(3, c)
	}
}

func coordLessEq(a, b coord) bool {
	if a.str && b.str {
		return jsval.CompareUTF16(a.s, b.s) <= 0
	}
	return a.f <= b.f
}

func (b *Bounds) enclosesSlow(o *Bounds) bool {
	return coordLessEq(b.at(0), o.at(0)) && coordLessEq(o.at(2), b.at(2)) &&
		coordLessEq(b.at(1), o.at(1)) && coordLessEq(o.at(3), b.at(3))
}

func (b *Bounds) alignsWithSlow(o *Bounds) bool {
	for i := 0; i < 4; i++ {
		if coordLooseEq(b.at(i), o.at(i)) {
			return true
		}
	}
	return false
}

func (b *Bounds) intersectsSlow(o *Bounds) bool {
	return !(coordLess(b.at(2), o.at(0)) || coordLess(o.at(2), b.at(0)) ||
		coordLess(b.at(3), o.at(1)) || coordLess(o.at(3), b.at(1)))
}
