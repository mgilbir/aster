package expr

import (
	"unsafe"

	"github.com/mgilbir/aster/internal/jsval"
)

// The builders every builtin makes its strings and arrays with. A spec chooses
// how large they are, so each one checks the length limit and charges the
// string budget before it allocates, and charges its steps as work: a builtin
// that builds through them cannot forget either (ADR 0010).

// MaxStringLength bounds the strings expressions build, in UTF-16 code units
// as JavaScript measures them. It is an engine limit, far below V8's (see
// v8MaxStringLength): a string past it ends the render with an error wrapping
// budget.ErrLimit, which no expression can catch.
const MaxStringLength = 1 << 24

// v8MaxStringLength is V8's String::kMaxLength on 64-bit platforms. A string
// longer than this is the RangeError "Invalid string length" upstream throws
// too, so an expression that asks for one (pad(x, 1e12)) gets that exception.
const v8MaxStringLength = 1<<29 - 24

// checkUnits checks the length, in code units, of a string about to be built.
func (s *Scope) checkUnits(units float64) {
	if units > v8MaxStringLength {
		throw("RangeError", "Invalid string length")
	}
	if units > MaxStringLength {
		exceeded("a string of %.0f UTF-16 code units exceeds the limit of %d", units, MaxStringLength)
	}
}

// checkBytes checks a string of n bytes made of parts whose code units are
// not known. A byte holds at most one code unit, so only a string longer than
// MaxStringLength bytes is measured, by units() (which costs a pass over it).
func (s *Scope) checkBytes(n int, units func() int) {
	if n > MaxStringLength {
		s.checkUnits(float64(units()))
	}
}

// checkString is checkBytes for one string.
func (s *Scope) checkString(str string) {
	if len(str) > MaxStringLength {
		s.checkUnits(float64(utf16Len(str)))
	}
}

// concat is the + of two strings: checked, charged, and with a high surrogate
// at the end of x joined to a low one at the start of y, as JavaScript's code
// units join.
func (s *Scope) concat(x, y string) string {
	if x == "" {
		return y
	}
	if y == "" {
		return x
	}
	n := len(x) + len(y)
	s.checkBytes(n, func() int { return utf16Len(x) + utf16Len(y) })
	s.chargeBytes(n)
	s.workBytes(n)
	if !joins(x, y) {
		return x + y
	}
	b := make([]byte, 0, n)
	b = append(b, x...)
	return string(appendJoin(b, y))
}

// strBuilder builds a string from parts, re-pairing surrogate halves where
// two parts meet. It checks the length limit as it grows (measuring code units
// only once the bytes pass MaxStringLength, and then incrementally), charges
// the string budget for the buffer's growth before it grows (so the total
// charged is about the final length, once, however many parts there are), and
// charges a pass over each part as work.
type strBuilder struct {
	s       *Scope
	buf     []byte
	units   int // code units in buf once its bytes may pass MaxStringLength; -1 before
	charged int // bytes of capacity charged to the budget
}

func newStrBuilder(s *Scope) strBuilder {
	return strBuilder{s: s, units: -1}
}

// reserve makes room for n more bytes.
func (b *strBuilder) reserve(n int) {
	need := len(b.buf) + n
	if need <= cap(b.buf) {
		return
	}
	c := max(2*cap(b.buf), need)
	if c > smallString {
		b.s.chargeRaw(int64(c - b.charged))
		b.charged = c
	}
	nb := make([]byte, len(b.buf), c)
	copy(nb, b.buf)
	b.buf = nb
}

// measure checks the length limit before p is added.
func (b *strBuilder) measure(p string) {
	if len(b.buf)+len(p) <= MaxStringLength {
		return
	}
	if b.units < 0 {
		b.units = utf16Len(unsafe.String(unsafe.SliceData(b.buf), len(b.buf)))
	}
	b.units += utf16Len(p)
	b.s.checkUnits(float64(b.units))
}

func (b *strBuilder) add(p string) {
	if p == "" {
		return
	}
	b.measure(p)
	b.reserve(len(p))
	b.buf = appendJoin(b.buf, p)
	b.s.workBytes(len(p))
}

// addRepeat adds n copies of c, as n adds would.
func (b *strBuilder) addRepeat(c string, n int) {
	if n <= 0 || c == "" {
		return
	}
	b.add(c)
	if n--; n == 0 {
		return
	}
	if joins(c, c) {
		for range n {
			b.add(c)
		}
		return
	}
	rest := len(c) * n
	if len(b.buf)+rest > MaxStringLength {
		if b.units < 0 {
			b.units = utf16Len(unsafe.String(unsafe.SliceData(b.buf), len(b.buf)))
		}
		b.units += utf16Len(c) * n
		b.s.checkUnits(float64(b.units))
	}
	b.reserve(rest)
	seg, end := len(b.buf), len(b.buf)+rest
	b.buf = append(b.buf, c...)
	for len(b.buf) < end {
		b.buf = append(b.buf, b.buf[seg:seg+min(len(b.buf)-seg, end-len(b.buf))]...)
	}
	b.s.workBytes(rest)
}

func (b *strBuilder) addByte(c byte) {
	if len(b.buf)+1 > MaxStringLength {
		b.measure(string(rune(c)))
	}
	b.reserve(1)
	b.buf = append(b.buf, c)
	b.s.work(1)
}

// String returns the string built. The buffer is not used again, so the
// string is made from it without a copy.
func (b *strBuilder) String() string {
	if len(b.buf) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b.buf), len(b.buf))
}

// chainState is a strBuilder for a buffer the caller owns (a stack array,
// which a strBuilder would make escape): concatChain's.
type chainState struct {
	units   int // code units once the bytes may pass MaxStringLength; 0 before
	measure bool
	charged int
}

// add appends p to buf as strBuilder.add does.
func (c *chainState) add(s *Scope, buf []byte, p string) []byte {
	if p == "" {
		return buf
	}
	if c.measure || len(buf)+len(p) > MaxStringLength {
		if !c.measure {
			c.units, c.measure = utf16Len(unsafe.String(unsafe.SliceData(buf), len(buf))), true
		}
		c.units += utf16Len(p)
		s.checkUnits(float64(c.units))
	}
	if need := len(buf) + len(p); need > cap(buf) {
		n := max(2*cap(buf), need)
		if n > smallString {
			s.chargeRaw(int64(n - c.charged))
			c.charged = n
		}
		nb := make([]byte, len(buf), n)
		copy(nb, buf)
		buf = nb
	}
	s.workBytes(len(p))
	return appendJoin(buf, p)
}

// appended accounts for bytes a formatter appended to buf from start on.
func (c *chainState) appended(s *Scope, buf []byte, start int) {
	if c.measure || len(buf) > MaxStringLength {
		if !c.measure {
			c.units, c.measure = utf16Len(unsafe.String(unsafe.SliceData(buf), start)), true
		}
		c.units += utf16Len(unsafe.String(unsafe.SliceData(buf[start:]), len(buf)-start))
		s.checkUnits(float64(c.units))
	}
	if n := cap(buf); n > smallString && n > c.charged {
		s.chargeRaw(int64(n - c.charged))
		c.charged = n
	}
	s.workBytes(len(buf) - start)
}

// perItem is what one element of an array an expression builds holds: the
// jsval.Value (16 bytes) and the slack of a small string or box it refers to.
const perItem = 24

// chargeItems charges the budget for n array elements an expression is about
// to make. An array of up to 256 elements is free, as a small string is.
func (s *Scope) chargeItems(n int) {
	if n > 256 {
		s.chargeRaw(int64(n) * perItem)
	}
}

// makeItems makes an array of n elements for an expression, charged and
// metered before it is allocated.
func (s *Scope) makeItems(n int) []jsval.Value {
	s.chargeItems(n)
	s.work(n)
	return make([]jsval.Value, n)
}

// cloneItems is a charged copy of items.
func (s *Scope) cloneItems(items []jsval.Value) []jsval.Value {
	out := s.makeItems(len(items))
	copy(out, items)
	return out
}

// itemBuilder grows an array whose final length is not known up front,
// charging each growth before it is made (the total charged is at most twice
// the final length) and metering each element.
type itemBuilder struct {
	s       *Scope
	items   []jsval.Value
	charged int
}

func (b *itemBuilder) push(v jsval.Value) {
	if len(b.items) == cap(b.items) {
		c := max(2*cap(b.items), 8)
		if c > 256 {
			b.s.chargeRaw(int64(c-b.charged) * perItem)
			b.charged = c
		}
		n := make([]jsval.Value, len(b.items), c)
		copy(n, b.items)
		b.items = n
	}
	b.items = append(b.items, v)
	b.s.work(1)
}

// chargeMembers charges the budget for an object of n members an expression
// is about to make: a key and a value each, and the object's index.
func (s *Scope) chargeMembers(n int) {
	if n > 128 {
		s.chargeRaw(int64(n) * 2 * perItem)
	}
	s.work(n)
}
