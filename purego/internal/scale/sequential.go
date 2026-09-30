package scale

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// UnitInterpolator maps t in [0, 1] to a value: a colour ramp or any other
// interpolator of a sequential or diverging scale (d3's `interpolator`).
type UnitInterpolator func(t float64) jsval.Value

func identityInterpolator(t float64) jsval.Value { return jsval.Num(t) }

// Sequential is d3.scaleSequential and its log/pow/sqrt/symlog variants: a
// scale from a two-value domain to the unit interval, fed to an interpolator.
type Sequential struct {
	meta
	tspec
	x0, x1  float64
	t0, t1  float64
	k10     float64
	tr      transform
	interp  UnitInterpolator // nil: identity
	clampOn bool
	unknown jsval.Value
}

func newSequential(kind tkind, x0, x1 float64) *Sequential {
	s := &Sequential{tspec: newTspec(kind), x0: x0, x1: x1}
	s.retransform()
	return s
}

// NewSequential is d3.scaleSequential (domain [0, 1]).
func NewSequential() *Sequential { return newSequential(kindLinear, 0, 1) }

// NewSequentialLog is d3.scaleSequentialLog (domain [1, 10]).
func NewSequentialLog() *Sequential { return newSequential(kindLog, 1, 10) }

// NewSequentialPow is d3.scaleSequentialPow.
func NewSequentialPow() *Sequential { return newSequential(kindPow, 0, 1) }

// NewSequentialSqrt is d3.scaleSequentialSqrt.
func NewSequentialSqrt() *Sequential {
	s := NewSequentialPow()
	s.SetExponent(0.5)
	return s
}

// NewSequentialSymlog is d3.scaleSequentialSymlog.
func NewSequentialSymlog() *Sequential { return newSequential(kindSymlog, 0, 1) }

func (s *Sequential) retransform() {
	s.tr = s.tspec.transform(s.x0)
	s.t0, s.t1 = s.tr.apply(s.x0), s.tr.apply(s.x1)
	if s.t0 == s.t1 {
		s.k10 = 0
	} else {
		s.k10 = 1 / (s.t1 - s.t0)
	}
}

// Apply is scale(x).
func (s *Sequential) Apply(x jsval.Value) jsval.Value {
	if x.IsNullish() {
		return s.unknown
	}
	f := jsval.ToNumber(x)
	if f != f {
		return s.unknown
	}
	return s.ApplyNumber(f)
}

// ApplyNumber is scale(x) for a non-NaN number.
func (s *Sequential) ApplyNumber(x float64) jsval.Value {
	if x != x {
		return s.unknown
	}
	return s.interpolate(s.fraction(x))
}

// Fraction is the interpolator argument for x: the position of x in the
// (transformed, optionally clamped) domain, 0.5 for a degenerate domain.
func (s *Sequential) Fraction(x float64) float64 { return s.fraction(x) }

func (s *Sequential) fraction(x float64) float64 {
	if s.k10 == 0 {
		return 0.5
	}
	x = float64(s.tr.apply(x)-s.t0) * s.k10
	if s.clampOn {
		return math.Max(0, math.Min(1, x))
	}
	return x
}

func (s *Sequential) interpolate(t float64) jsval.Value {
	if s.interp == nil {
		return jsval.Num(t)
	}
	return s.interp(t)
}

// Domain returns [x0, x1].
func (s *Sequential) Domain() []jsval.Value {
	return []jsval.Value{jsval.Num(s.x0), jsval.Num(s.x1)}
}

// DomainNumbers returns [x0, x1] without boxing.
func (s *Sequential) DomainNumbers() []float64 { return []float64{s.x0, s.x1} }

// SetDomain is scale.domain([x0, x1]); missing entries read as NaN.
func (s *Sequential) SetDomain(d []jsval.Value) {
	s.x0, s.x1 = numFromValues(d, 0), numFromValues(d, 1)
	s.retransform()
}

func numFromValues(d []jsval.Value, i int) float64 {
	if i >= len(d) {
		return math.NaN()
	}
	return jsval.ToNumber(d[i])
}

// Range is [interpolator(0), interpolator(1)].
func (s *Sequential) Range() []jsval.Value {
	return []jsval.Value{s.interpolate(0), s.interpolate(1)}
}

// SetRange is scale.range([r0, r1]): interpolator = interpolate(r0, r1).
func (s *Sequential) SetRange(r []jsval.Value) { s.setRange(r, InterpolateValue) }

// SetRangeRound is scale.rangeRound([r0, r1]).
func (s *Sequential) SetRangeRound(r []jsval.Value) { s.setRange(r, InterpolateRound) }

func (s *Sequential) setRange(r []jsval.Value, f Interpolator) {
	f01 := f(valAt(r, 0), valAt(r, 1))
	s.interp = f01
}

// Interpolator returns the interpolator (nil means the identity).
func (s *Sequential) Interpolator() UnitInterpolator {
	if s.interp == nil {
		return identityInterpolator
	}
	return s.interp
}

// SetInterpolator is scale.interpolator(f).
func (s *Sequential) SetInterpolator(f UnitInterpolator) { s.interp = f }

// Clamp reports whether clamping is on.
func (s *Sequential) Clamp() bool { return s.clampOn }

// SetClamp is scale.clamp(b).
func (s *Sequential) SetClamp(b bool) { s.clampOn = b }

// Unknown returns the value produced for undefined/NaN inputs.
func (s *Sequential) Unknown() jsval.Value { return s.unknown }

// SetUnknown is scale.unknown(v).
func (s *Sequential) SetUnknown(v jsval.Value) { s.unknown = v }

// SetBase is loggish base(b); false for other kinds.
func (s *Sequential) SetBase(b float64) bool {
	if s.kind != kindLog {
		return false
	}
	s.base = b
	s.retransform()
	return true
}

// Base is the log base and whether this is a log scale.
func (s *Sequential) Base() (float64, bool) { return s.base, s.kind == kindLog }

// SetExponent is pow.exponent(e); false for other kinds.
func (s *Sequential) SetExponent(e float64) bool {
	if s.kind != kindPow {
		return false
	}
	s.exponent = e
	s.retransform()
	return true
}

// Exponent is the pow exponent and whether this is a pow scale.
func (s *Sequential) Exponent() (float64, bool) { return s.exponent, s.kind == kindPow }

// SetConstant is symlog.constant(k); false for other kinds.
func (s *Sequential) SetConstant(k float64) bool {
	if s.kind != kindSymlog {
		return false
	}
	s.constant = k
	s.retransform()
	return true
}

// Constant is the symlog constant and whether this is a symlog scale.
func (s *Sequential) Constant() (float64, bool) { return s.constant, s.kind == kindSymlog }

// Ticks is scale.ticks(count).
func (s *Sequential) Ticks(count TickCount) []float64 {
	return s.tspec.ticks([]float64{s.x0, s.x1}, count)
}

// Nice is scale.nice(count).
func (s *Sequential) Nice(count TickCount) {
	if d := s.tspec.nice([]float64{s.x0, s.x1}, count); d != nil {
		s.x0, s.x1 = d[0], d[1]
		s.retransform()
	}
}

// HasTickFormat reports true (linearish scales have tickFormat).
func (s *Sequential) HasTickFormat() bool { return true }

// Copy is d3's sequential copy: domain, interpolator, clamp, unknown and the
// family parameter.
func (s *Sequential) Copy() Scale {
	n := *s
	n.meta = meta{typ: s.typ}
	return &n
}

// Diverging is d3.scaleDiverging and its variants: a three-value domain mapped
// through two linear halves to the unit interval.
type Diverging struct {
	meta
	tspec
	x0, x1, x2 float64
	t0, t1, t2 float64
	k10, k21   float64
	sgn        float64
	tr         transform
	interp     UnitInterpolator
	clampOn    bool
	unknown    jsval.Value
}

func newDiverging(kind tkind, x0, x1, x2 float64) *Diverging {
	d := &Diverging{tspec: newTspec(kind), x0: x0, x1: x1, x2: x2}
	d.retransform()
	return d
}

// NewDiverging is d3.scaleDiverging (domain [0, 0.5, 1]).
func NewDiverging() *Diverging { return newDiverging(kindLinear, 0, 0.5, 1) }

// NewDivergingLog is d3.scaleDivergingLog (domain [0.1, 1, 10]).
func NewDivergingLog() *Diverging { return newDiverging(kindLog, 0.1, 1, 10) }

// NewDivergingPow is d3.scaleDivergingPow.
func NewDivergingPow() *Diverging { return newDiverging(kindPow, 0, 0.5, 1) }

// NewDivergingSqrt is d3.scaleDivergingSqrt.
func NewDivergingSqrt() *Diverging {
	d := NewDivergingPow()
	d.SetExponent(0.5)
	return d
}

// NewDivergingSymlog is d3.scaleDivergingSymlog.
func NewDivergingSymlog() *Diverging { return newDiverging(kindSymlog, 0, 0.5, 1) }

func (d *Diverging) retransform() {
	d.tr = d.tspec.transform(d.x0)
	d.t0, d.t1, d.t2 = d.tr.apply(d.x0), d.tr.apply(d.x1), d.tr.apply(d.x2)
	if d.t0 == d.t1 {
		d.k10 = 0
	} else {
		d.k10 = 0.5 / (d.t1 - d.t0)
	}
	if d.t1 == d.t2 {
		d.k21 = 0
	} else {
		d.k21 = 0.5 / (d.t2 - d.t1)
	}
	if d.t1 < d.t0 {
		d.sgn = -1
	} else {
		d.sgn = 1
	}
}

// Apply is scale(x). Unlike the other scales, upstream's diverging scale does
// not test for null before coercing, so null is 0 here.
func (d *Diverging) Apply(x jsval.Value) jsval.Value {
	f := jsval.ToNumber(x)
	if f != f {
		return d.unknown
	}
	return d.ApplyNumber(f)
}

// ApplyNumber is scale(x) for a non-NaN number.
func (d *Diverging) ApplyNumber(x float64) jsval.Value {
	if x != x {
		return d.unknown
	}
	return d.interpolate(d.fraction(x))
}

// Fraction is the interpolator argument for x.
func (d *Diverging) Fraction(x float64) float64 { return d.fraction(x) }

func (d *Diverging) fraction(x float64) float64 {
	tx := d.tr.apply(x)
	k := d.k21
	if d.sgn*tx < d.sgn*d.t1 {
		k = d.k10
	}
	x = 0.5 + float64((tx-d.t1)*k)
	if d.clampOn {
		return math.Max(0, math.Min(1, x))
	}
	return x
}

func (d *Diverging) interpolate(t float64) jsval.Value {
	if d.interp == nil {
		return jsval.Num(t)
	}
	return d.interp(t)
}

// Domain returns [x0, x1, x2].
func (d *Diverging) Domain() []jsval.Value {
	return []jsval.Value{jsval.Num(d.x0), jsval.Num(d.x1), jsval.Num(d.x2)}
}

// DomainNumbers returns the domain without boxing.
func (d *Diverging) DomainNumbers() []float64 { return []float64{d.x0, d.x1, d.x2} }

// SetDomain is scale.domain([x0, x1, x2]); missing entries read as NaN.
func (d *Diverging) SetDomain(v []jsval.Value) {
	d.x0, d.x1, d.x2 = numFromValues(v, 0), numFromValues(v, 1), numFromValues(v, 2)
	d.retransform()
}

// Range is [interpolator(0), interpolator(0.5), interpolator(1)].
func (d *Diverging) Range() []jsval.Value {
	return []jsval.Value{d.interpolate(0), d.interpolate(0.5), d.interpolate(1)}
}

// SetRange is scale.range([r0, r1, r2]): a piecewise interpolator through the
// three values.
func (d *Diverging) SetRange(r []jsval.Value) { d.setRange(r, InterpolateValue) }

// SetRangeRound is scale.rangeRound([r0, r1, r2]).
func (d *Diverging) SetRangeRound(r []jsval.Value) { d.setRange(r, InterpolateRound) }

func (d *Diverging) setRange(r []jsval.Value, f Interpolator) {
	vals := []jsval.Value{valAt(r, 0), valAt(r, 1), valAt(r, 2)}
	d.interp = Piecewise(f, vals)
}

// Interpolator returns the interpolator (nil means the identity).
func (d *Diverging) Interpolator() UnitInterpolator {
	if d.interp == nil {
		return identityInterpolator
	}
	return d.interp
}

// SetInterpolator is scale.interpolator(f).
func (d *Diverging) SetInterpolator(f UnitInterpolator) { d.interp = f }

// Clamp reports whether clamping is on.
func (d *Diverging) Clamp() bool { return d.clampOn }

// SetClamp is scale.clamp(b).
func (d *Diverging) SetClamp(b bool) { d.clampOn = b }

// Unknown returns the value produced for NaN inputs.
func (d *Diverging) Unknown() jsval.Value { return d.unknown }

// SetUnknown is scale.unknown(v).
func (d *Diverging) SetUnknown(v jsval.Value) { d.unknown = v }

// SetBase is loggish base(b); false for other kinds.
func (d *Diverging) SetBase(b float64) bool {
	if d.kind != kindLog {
		return false
	}
	d.base = b
	d.retransform()
	return true
}

// Base is the log base and whether this is a log scale.
func (d *Diverging) Base() (float64, bool) { return d.base, d.kind == kindLog }

// SetExponent is pow.exponent(e); false for other kinds.
func (d *Diverging) SetExponent(e float64) bool {
	if d.kind != kindPow {
		return false
	}
	d.exponent = e
	d.retransform()
	return true
}

// Exponent is the pow exponent and whether this is a pow scale.
func (d *Diverging) Exponent() (float64, bool) { return d.exponent, d.kind == kindPow }

// SetConstant is symlog.constant(k); false for other kinds.
func (d *Diverging) SetConstant(k float64) bool {
	if d.kind != kindSymlog {
		return false
	}
	d.constant = k
	d.retransform()
	return true
}

// Constant is the symlog constant and whether this is a symlog scale.
func (d *Diverging) Constant() (float64, bool) { return d.constant, d.kind == kindSymlog }

// Ticks is scale.ticks(count): from the first to the last domain value.
func (d *Diverging) Ticks(count TickCount) []float64 {
	return d.tspec.ticks([]float64{d.x0, d.x1, d.x2}, count)
}

// Nice is scale.nice(count): only the end points move.
func (d *Diverging) Nice(count TickCount) {
	if v := d.tspec.nice([]float64{d.x0, d.x1, d.x2}, count); v != nil {
		d.x0, d.x1, d.x2 = v[0], v[1], v[2]
		d.retransform()
	}
}

// HasTickFormat reports true (linearish scales have tickFormat).
func (d *Diverging) HasTickFormat() bool { return true }

// Copy is d3's sequential copy applied to a diverging scale.
func (d *Diverging) Copy() Scale {
	n := *d
	n.meta = meta{typ: d.typ}
	return &n
}

var (
	_ Scale         = (*Sequential)(nil)
	_ Clamper       = (*Sequential)(nil)
	_ Ticker        = (*Sequential)(nil)
	_ Niceable      = (*Sequential)(nil)
	_ Scale         = (*Diverging)(nil)
	_ Clamper       = (*Diverging)(nil)
	_ Ticker        = (*Diverging)(nil)
	_ Niceable      = (*Diverging)(nil)
	_ Rounder       = (*Sequential)(nil)
	_ Rounder       = (*Diverging)(nil)
	_ TickFormatter = (*Sequential)(nil)
)
