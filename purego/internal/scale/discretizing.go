package scale

import (
	"math"
	"sort"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// strictEq is JavaScript's === as used by Array.prototype.indexOf on range
// values: primitives by value (NaN never equal), objects by reference.
func strictEq(a, b jsval.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case jsval.KindNum:
		return a.NumValue() == b.NumValue()
	case jsval.KindTimestamp:
		return a.NumValue() == b.NumValue()
	}
	return jsval.SameRef(a, b)
}

func indexOfValue(s []jsval.Value, y jsval.Value) int {
	for i, v := range s {
		if strictEq(v, y) {
			return i
		}
	}
	return -1
}

func rangeAt(rng []jsval.Value, i int) jsval.Value {
	if i < 0 || i >= len(rng) {
		return jsval.Undefined
	}
	return rng[i]
}

func numOrUndef(s []float64, i int) jsval.Value {
	if i < 0 || i >= len(s) {
		return jsval.Undefined
	}
	return jsval.Num(s[i])
}

// selfComparable implements the `x != null && x <= x` guard of quantize and
// threshold scales. A number (or date) is usable unless it is NaN. A string,
// array or object always passes the guard - it compares with itself - but
// has no numeric order against the thresholds, so d3's bisect returns index 0;
// that is reported as NaN's stand-in -Inf, which bisects to 0 for any sorted
// numeric thresholds. ok is false when the scale must answer `unknown`.
func selfComparable(x jsval.Value) (f float64, ok bool) {
	if x.IsNullish() {
		return 0, false
	}
	f = jsval.ToNumber(x)
	if f == f {
		return f, true
	}
	switch x.Kind() {
	case jsval.KindStr, jsval.KindArr, jsval.KindObj, jsval.KindPattern:
		return math.Inf(-1), true
	}
	return 0, false
}

// thresholdOrUndef reads a quantile threshold; NaN stands for upstream's
// undefined (thresholds of an empty domain).
func thresholdOrUndef(s []float64, i int) jsval.Value {
	if i < 0 || i >= len(s) || s[i] != s[i] {
		return jsval.Undefined
	}
	return jsval.Num(s[i])
}

// Quantile is d3.scaleQuantile: maps a sampled domain to range values by
// quantile.
type Quantile struct {
	meta
	domain     []float64 // sorted, NaN-free
	rng        []jsval.Value
	thresholds []float64
	unknown    jsval.Value
}

// NewQuantile is d3.scaleQuantile.
func NewQuantile() *Quantile { q := &Quantile{}; q.rescale(); return q }

func (q *Quantile) rescale() {
	n := max(1, len(q.rng))
	q.thresholds = make([]float64, n-1)
	for i := 1; i < n; i++ {
		v, ok := quantileSorted(q.domain, float64(i)/float64(n))
		if !ok {
			v = math.NaN() // upstream stores undefined; a filtered domain never yields NaN otherwise
		}
		q.thresholds[i-1] = v
	}
}

// Apply is scale(x).
func (q *Quantile) Apply(x jsval.Value) jsval.Value {
	if x.IsNullish() {
		return q.unknown
	}
	f := jsval.ToNumber(x)
	if f != f {
		return q.unknown
	}
	return rangeAt(q.rng, bisectRight(q.thresholds, f, 0, len(q.thresholds)))
}

// InvertExtent is scale.invertExtent(y).
func (q *Quantile) InvertExtent(y jsval.Value) [2]jsval.Value {
	i := indexOfValue(q.rng, y)
	if i < 0 {
		return [2]jsval.Value{jsval.Num(math.NaN()), jsval.Num(math.NaN())}
	}
	var lo, hi jsval.Value
	if i > 0 {
		lo = thresholdOrUndef(q.thresholds, i-1)
	} else {
		lo = numOrUndef(q.domain, 0)
	}
	if i < len(q.thresholds) {
		hi = thresholdOrUndef(q.thresholds, i)
	} else {
		hi = numOrUndef(q.domain, len(q.domain)-1)
	}
	return [2]jsval.Value{lo, hi}
}

// Domain returns the sorted sample.
func (q *Quantile) Domain() []jsval.Value { return numsToValues(q.domain) }

// SetDomain is scale.domain(d): nullish and NaN entries are dropped and the
// rest sorted ascending.
func (q *Quantile) SetDomain(d []jsval.Value) {
	q.domain = q.domain[:0:0]
	for _, v := range d {
		if v.IsNullish() {
			continue
		}
		if f := jsval.ToNumber(v); f == f {
			q.domain = append(q.domain, f)
		}
	}
	sort.Float64s(q.domain)
	q.rescale()
}

// Range returns a copy of the range.
func (q *Quantile) Range() []jsval.Value { return cloneValues(q.rng) }

// SetRange is scale.range(r).
func (q *Quantile) SetRange(r []jsval.Value) { q.rng = cloneValues(r); q.rescale() }

// Unknown returns the value produced for null/NaN inputs.
func (q *Quantile) Unknown() jsval.Value { return q.unknown }

// SetUnknown is scale.unknown(v).
func (q *Quantile) SetUnknown(v jsval.Value) { q.unknown = v }

// Quantiles returns the quantile thresholds (NaN entries mean undefined, which
// happens only for an empty domain).
func (q *Quantile) Quantiles() []float64 { return append([]float64(nil), q.thresholds...) }

// Copy is d3's quantile copy.
func (q *Quantile) Copy() Scale {
	n := &Quantile{meta: meta{typ: q.typ}, domain: append([]float64(nil), q.domain...),
		rng: cloneValues(q.rng), unknown: q.unknown}
	n.rescale()
	return n
}

// Quantize is d3.scaleQuantize: divides a continuous [x0, x1] domain into
// uniform segments, one per range value.
type Quantize struct {
	meta
	x0, x1  float64
	n       int
	domain  []float64 // thresholds
	rng     []jsval.Value
	unknown jsval.Value
}

// NewQuantize is d3.scaleQuantize (domain [0, 1], range [0, 1]).
func NewQuantize() *Quantize {
	q := &Quantize{x1: 1, n: 1, rng: []jsval.Value{jsval.Num(0), jsval.Num(1)}}
	q.rescale()
	return q
}

func (q *Quantize) rescale() {
	n := q.n
	if n < 0 {
		n = 0 // d3 throws (new Array(-1)) for an empty range
	}
	q.domain = make([]float64, n)
	for i := 0; i < n; i++ {
		q.domain[i] = (float64(float64(i+1)*q.x1) - float64(float64(i-n)*q.x0)) / float64(n+1)
	}
}

// Apply is scale(x).
func (q *Quantize) Apply(x jsval.Value) jsval.Value {
	f, ok := selfComparable(x)
	if !ok {
		return q.unknown
	}
	return rangeAt(q.rng, bisectRight(q.domain, f, 0, max(q.n, 0)))
}

// InvertExtent is scale.invertExtent(y).
func (q *Quantize) InvertExtent(y jsval.Value) [2]jsval.Value {
	i := indexOfValue(q.rng, y)
	switch {
	case i < 0:
		return [2]jsval.Value{jsval.Num(math.NaN()), jsval.Num(math.NaN())}
	case i < 1:
		return [2]jsval.Value{jsval.Num(q.x0), numOrUndef(q.domain, 0)}
	case i >= q.n:
		return [2]jsval.Value{numOrUndef(q.domain, q.n-1), jsval.Num(q.x1)}
	}
	return [2]jsval.Value{numOrUndef(q.domain, i-1), numOrUndef(q.domain, i)}
}

// Domain returns [x0, x1].
func (q *Quantize) Domain() []jsval.Value { return []jsval.Value{jsval.Num(q.x0), jsval.Num(q.x1)} }

// SetDomain is scale.domain([x0, x1]).
func (q *Quantize) SetDomain(d []jsval.Value) {
	q.x0, q.x1 = numFromValues(d, 0), numFromValues(d, 1)
	q.rescale()
}

// Range returns a copy of the range.
func (q *Quantize) Range() []jsval.Value { return cloneValues(q.rng) }

// SetRange is scale.range(r): n = r.length - 1 thresholds.
func (q *Quantize) SetRange(r []jsval.Value) {
	q.rng = cloneValues(r)
	q.n = len(q.rng) - 1
	q.rescale()
}

// Unknown returns the value produced for null/NaN inputs.
func (q *Quantize) Unknown() jsval.Value { return q.unknown }

// SetUnknown is scale.unknown(v).
func (q *Quantize) SetUnknown(v jsval.Value) { q.unknown = v }

// Thresholds returns the internal thresholds.
func (q *Quantize) Thresholds() []float64 { return append([]float64(nil), q.domain...) }

// Ticks is scale.ticks(count) (quantize is linearish).
func (q *Quantize) Ticks(count TickCount) []float64 {
	return Ticks(q.x0, q.x1, count.countOrDefault())
}

// Nice is scale.nice(count).
func (q *Quantize) Nice(count TickCount) {
	if d := linearNice([]float64{q.x0, q.x1}, count.countOrDefault()); d != nil {
		q.x0, q.x1 = d[0], d[1]
		q.rescale()
	}
}

// HasTickFormat reports true (quantize is linearish).
func (q *Quantize) HasTickFormat() bool { return true }

// Copy is d3's quantize copy.
func (q *Quantize) Copy() Scale {
	n := &Quantize{meta: meta{typ: q.typ}, x0: q.x0, x1: q.x1, n: q.n,
		rng: cloneValues(q.rng), unknown: q.unknown}
	n.rescale()
	return n
}

// Threshold is d3.scaleThreshold: maps values to range entries by comparing
// with an ascending list of thresholds.
type Threshold struct {
	meta
	domain  []jsval.Value
	dnum    []float64
	rng     []jsval.Value
	n       int
	unknown jsval.Value
}

// NewThreshold is d3.scaleThreshold (domain [0.5], range [0, 1]).
func NewThreshold() *Threshold {
	t := &Threshold{
		domain: []jsval.Value{jsval.Num(0.5)},
		dnum:   []float64{0.5},
		rng:    []jsval.Value{jsval.Num(0), jsval.Num(1)},
	}
	t.n = 1
	return t
}

func (t *Threshold) resize() { t.n = min(len(t.domain), len(t.rng)-1) }

// Apply is scale(x).
func (t *Threshold) Apply(x jsval.Value) jsval.Value {
	f, ok := selfComparable(x)
	if !ok {
		return t.unknown
	}
	return rangeAt(t.rng, bisectRight(t.dnum, f, 0, max(t.n, 0)))
}

// InvertExtent is scale.invertExtent(y).
func (t *Threshold) InvertExtent(y jsval.Value) [2]jsval.Value {
	i := indexOfValue(t.rng, y)
	return [2]jsval.Value{rangeAt(t.domain, i-1), rangeAt(t.domain, i)}
}

// Domain returns a copy of the thresholds as given.
func (t *Threshold) Domain() []jsval.Value { return cloneValues(t.domain) }

// SetDomain is scale.domain(d).
func (t *Threshold) SetDomain(d []jsval.Value) {
	t.domain = cloneValues(d)
	t.dnum = valuesToNums(d)
	t.resize()
}

// Range returns a copy of the range.
func (t *Threshold) Range() []jsval.Value { return cloneValues(t.rng) }

// SetRange is scale.range(r).
func (t *Threshold) SetRange(r []jsval.Value) { t.rng = cloneValues(r); t.resize() }

// Unknown returns the value produced for null/NaN inputs.
func (t *Threshold) Unknown() jsval.Value { return t.unknown }

// SetUnknown is scale.unknown(v).
func (t *Threshold) SetUnknown(v jsval.Value) { t.unknown = v }

// Copy is d3's threshold copy.
func (t *Threshold) Copy() Scale {
	n := NewThreshold()
	n.typ = t.typ
	n.SetDomain(t.domain)
	n.rng = cloneValues(t.rng)
	n.resize()
	n.unknown = t.unknown
	return n
}

// BinOrdinal is vega-scale's bin-ordinal scale: maps a number to the range
// entry of the bin containing it (cycling through the range).
type BinOrdinal struct {
	meta
	domain []float64
	rng    []jsval.Value
}

// NewBinOrdinal is vega-scale's bin-ordinal scale.
func NewBinOrdinal() *BinOrdinal { return &BinOrdinal{} }

// Apply is scale(x); the result is undefined for null and NaN inputs. Like
// upstream, a value below the first bin edge yields range[-1], i.e. undefined.
func (b *BinOrdinal) Apply(x jsval.Value) jsval.Value {
	if x.IsNullish() || len(b.rng) == 0 {
		return jsval.Undefined
	}
	f := jsval.ToNumber(x)
	var pos int
	switch {
	case f == f:
		pos = bisectRight(b.domain, f, 0, len(b.domain))
	case x.IsNum():
		return jsval.Undefined // x !== x
	case x.IsTimestamp():
		pos = len(b.domain) // an invalid Date is not self-comparable: bisect answers hi
	default:
		pos = 0 // non-numeric strings and objects have no order against the edges
	}
	return rangeAt(b.rng, (pos-1)%len(b.rng))
}

// Domain returns the bin edges.
func (b *BinOrdinal) Domain() []jsval.Value { return numsToValues(b.domain) }

// SetDomain is scale.domain(d): every entry goes through toNumber.
func (b *BinOrdinal) SetDomain(d []jsval.Value) { b.domain = valuesToNums(d) }

// Range returns a copy of the range.
func (b *BinOrdinal) Range() []jsval.Value { return cloneValues(b.rng) }

// SetRange is scale.range(r).
func (b *BinOrdinal) SetRange(r []jsval.Value) { b.rng = cloneValues(r) }

// HasTickFormat reports true: bin-ordinal has a tickFormat method.
func (b *BinOrdinal) HasTickFormat() bool { return true }

// Copy is bin-ordinal's copy.
func (b *BinOrdinal) Copy() Scale {
	n := &BinOrdinal{meta: meta{typ: b.typ}, domain: append([]float64(nil), b.domain...), rng: cloneValues(b.rng)}
	return n
}

// Identity is d3.scaleIdentity: the domain doubles as the range.
type Identity struct {
	meta
	domain  []float64
	unknown jsval.Value
}

// NewIdentity is d3.scaleIdentity (domain [0, 1]).
func NewIdentity() *Identity { return &Identity{domain: []float64{0, 1}} }

// Apply is scale(x): x itself as a number.
func (s *Identity) Apply(x jsval.Value) jsval.Value {
	if x.IsNullish() {
		return s.unknown
	}
	f := jsval.ToNumber(x)
	if f != f {
		return s.unknown
	}
	return jsval.Num(f)
}

// Invert is the same function as Apply.
func (s *Identity) Invert(y jsval.Value) jsval.Value { return s.Apply(y) }

// InvertRange is vega-scale's invertRange for a scale with invert.
func (s *Identity) InvertRange(lo, hi jsval.Value) (jsval.Value, bool) {
	if hi.IsNum() && lo.IsNum() && hi.NumValue() < lo.NumValue() {
		lo, hi = hi, lo
	}
	return jsval.ArrOf(s.Invert(lo), s.Invert(hi)), true
}

// Domain returns the domain.
func (s *Identity) Domain() []jsval.Value { return numsToValues(s.domain) }

// SetDomain sets the domain (and range).
func (s *Identity) SetDomain(d []jsval.Value) { s.domain = valuesToNums(d) }

// Range is the same list as the domain.
func (s *Identity) Range() []jsval.Value { return numsToValues(s.domain) }

// SetRange sets the range, which is also the domain.
func (s *Identity) SetRange(r []jsval.Value) { s.domain = valuesToNums(r) }

// Unknown returns the value produced for null/NaN inputs.
func (s *Identity) Unknown() jsval.Value { return s.unknown }

// SetUnknown is scale.unknown(v).
func (s *Identity) SetUnknown(v jsval.Value) { s.unknown = v }

// Ticks is scale.ticks(count).
func (s *Identity) Ticks(count TickCount) []float64 {
	u, v := endpoints(s.domain)
	return Ticks(u, v, count.countOrDefault())
}

// Nice is scale.nice(count).
func (s *Identity) Nice(count TickCount) {
	if d := linearNice(s.domain, count.countOrDefault()); d != nil {
		s.domain = d
	}
}

// HasTickFormat reports true (identity is linearish).
func (s *Identity) HasTickFormat() bool { return true }

// Copy is identity's copy.
func (s *Identity) Copy() Scale {
	return &Identity{meta: meta{typ: s.typ}, domain: append([]float64(nil), s.domain...), unknown: s.unknown}
}

var (
	_ Scale          = (*Quantile)(nil)
	_ ExtentInverter = (*Quantile)(nil)
	_ Scale          = (*Quantize)(nil)
	_ ExtentInverter = (*Quantize)(nil)
	_ Scale          = (*Threshold)(nil)
	_ ExtentInverter = (*Threshold)(nil)
	_ Scale          = (*BinOrdinal)(nil)
	_ Scale          = (*Identity)(nil)
	_ Inverter       = (*Identity)(nil)
	_ Ticker         = (*Identity)(nil)
	_ Ticker         = (*Quantize)(nil)
)

// InvertRangeExtent is vega-scale's invertRange for scales that only have
// invertExtent (quantile, quantize, threshold): the domain extent spanned by
// the range values that lie within [lo, hi]. ok is false where upstream
// answers undefined.
func InvertRangeExtent(s interface {
	Scale
	ExtentInverter
}, loV, hiV jsval.Value) (jsval.Value, bool) {
	rng := s.Range()
	if jsGreater(loV, hiV) {
		loV, hiV = hiV, loV
	}
	mn, mx := -1, 0
	for i, r := range rng {
		if jsGreaterEq(r, loV) && jsGreaterEq(hiV, r) {
			if mn < 0 {
				mn = i
			}
			mx = i
		}
	}
	if mn < 0 {
		return jsval.Undefined, false
	}
	lo := s.InvertExtent(rng[mn])
	hi := s.InvertExtent(rng[mx])
	a, b := lo[0], hi[1]
	if lo[0].IsUndefined() {
		a = lo[1]
	}
	if hi[1].IsUndefined() {
		b = hi[0]
	}
	return jsval.ArrOf(a, b), true
}

// jsCompare implements the abstract relational comparison for the value kinds
// scales see: two strings compare lexically, anything else numerically. ok is
// false when either side is NaN (every relation is then false).
func jsCompare(a, b jsval.Value) (c int, ok bool) {
	if a.IsStr() && b.IsStr() {
		x, y := a.StrValue(), b.StrValue()
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	}
	x, y := jsval.ToNumber(a), jsval.ToNumber(b)
	if x != x || y != y {
		return 0, false
	}
	switch {
	case x < y:
		return -1, true
	case x > y:
		return 1, true
	}
	return 0, true
}

func jsGreater(a, b jsval.Value) bool   { c, ok := jsCompare(a, b); return ok && c > 0 }
func jsGreaterEq(a, b jsval.Value) bool { c, ok := jsCompare(a, b); return ok && c >= 0 }
