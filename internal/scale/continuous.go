package scale

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// Continuous is d3's continuous scale (linear, log, pow, sqrt, symlog) and the
// base of the time scales. Which family it belongs to is decided at
// construction; base/exponent/constant only apply to their own family.
type Continuous struct {
	meta
	tspec
	domain  []float64
	rng     []jsval.Value
	interp  Interpolator // nil: d3.interpolate
	round   bool         // rangeRound: interpolateRound
	tr      transform
	clampOn bool
	// clampLo/Hi are refreshed by rescale from the first and n-th domain values.
	clampLo, clampHi float64
	unknown          jsval.Value

	out, in *pmap
}

var unitRange = []jsval.Value{jsval.Num(0), jsval.Num(1)}

func newContinuous(kind tkind, domain []float64) *Continuous {
	c := &Continuous{
		tspec:  newTspec(kind),
		domain: domain,
		rng:    unitRange,
	}
	c.tr = c.tspec.transform(firstOf(domain))
	c.rescale()
	return c
}

func firstOf(d []float64) float64 {
	if len(d) == 0 {
		return math.NaN()
	}
	return d[0]
}

// NewLinear is d3.scaleLinear.
func NewLinear() *Continuous { return newContinuous(kindLinear, []float64{0, 1}) }

// NewLog is d3.scaleLog (base 10, domain [1, 10]).
func NewLog() *Continuous { return newContinuous(kindLog, []float64{1, 10}) }

// NewPow is d3.scalePow (exponent 1).
func NewPow() *Continuous { return newContinuous(kindPow, []float64{0, 1}) }

// NewSqrt is d3.scaleSqrt (a pow scale with exponent 0.5).
func NewSqrt() *Continuous {
	c := NewPow()
	c.SetExponent(0.5)
	return c
}

// NewSymlog is d3.scaleSymlog (constant 1).
func NewSymlog() *Continuous { return newContinuous(kindSymlog, []float64{0, 1}) }

func (c *Continuous) rescale() {
	n := min(len(c.domain), len(c.rng))
	if c.clampOn {
		// d3 builds the clamper from domain[0] and domain[n-1] where n is the
		// shorter of domain and range; with nothing there it clamps to NaN.
		lo, hi := math.NaN(), math.NaN()
		if n > 0 {
			lo, hi = c.domain[0], c.domain[n-1]
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		c.clampLo, c.clampHi = lo, hi
	}
	c.out, c.in = nil, nil
}

func (c *Continuous) clampX(x float64) float64 {
	if !c.clampOn {
		return x
	}
	return math.Max(c.clampLo, math.Min(c.clampHi, x))
}

func (c *Continuous) output() *pmap {
	if c.out == nil {
		d := c.domain
		if c.tr.fwd != nil {
			d = make([]float64, len(c.domain))
			for i, x := range c.domain {
				d[i] = c.tr.fwd(x)
			}
		}
		c.out = newValueMap(d, c.rng, c.interp, c.round)
	}
	return c.out
}

func (c *Continuous) input() *pmap {
	if c.in == nil {
		d := make([]float64, len(c.domain))
		for i, x := range c.domain {
			d[i] = c.tr.apply(x)
		}
		c.in = newNumMap(valuesToNums(c.rng), d)
	}
	return c.in
}

// Apply is scale(x).
func (c *Continuous) Apply(x jsval.Value) jsval.Value {
	if x.IsNullish() {
		return c.unknown
	}
	f := jsval.ToNumber(x)
	if f != f {
		return c.unknown
	}
	return c.ApplyNumber(f)
}

// ApplyNumber is scale(x) for a non-NaN number.
func (c *Continuous) ApplyNumber(x float64) jsval.Value {
	if x != x {
		return c.unknown
	}
	return c.output().value(c.tr.apply(c.clampX(x)))
}

// ApplyFloat is scale(x) for scales with a numeric range, without boxing. It
// returns NaN when the input is NaN or the result is not a number (including
// an unknown value that is not a number).
func (c *Continuous) ApplyFloat(x float64) float64 {
	if x != x {
		return c.unknown.AsDouble()
	}
	return c.output().number(c.tr.apply(c.clampX(x)))
}

// Invert is scale.invert(y).
func (c *Continuous) Invert(y jsval.Value) jsval.Value {
	return jsval.Num(c.invert(y))
}

func (c *Continuous) invert(y jsval.Value) float64 {
	m := c.input()
	var v float64
	if y.IsNullish() && m.poly {
		v = m.numberAtHi(jsval.ToNumber(y))
	} else {
		v = m.number(jsval.ToNumber(y))
	}
	return c.clampX(c.tr.unapply(v))
}

// InvertNumber is scale.invert(y) for a numeric y.
func (c *Continuous) InvertNumber(y float64) float64 {
	return c.clampX(c.tr.unapply(c.input().number(y)))
}

// InvertRange is vega-scale's invertRange: the domain extent covered by the
// range interval (order of the endpoints does not matter).
func (c *Continuous) InvertRange(lo, hi jsval.Value) (jsval.Value, bool) {
	if hi.IsNum() && lo.IsNum() && hi.NumValue() < lo.NumValue() {
		lo, hi = hi, lo
	}
	return jsval.ArrOf(c.Invert(lo), c.Invert(hi)), true
}

// Domain returns the domain as numbers.
func (c *Continuous) Domain() []jsval.Value { return numsToValues(c.domain) }

// DomainNumbers returns the domain without boxing.
func (c *Continuous) DomainNumbers() []float64 { return append([]float64(nil), c.domain...) }

// SetDomain is scale.domain(d): every element goes through Number().
func (c *Continuous) SetDomain(d []jsval.Value) { c.SetDomainNumbers(valuesToNums(d)) }

// SetDomainNumbers sets the domain from numbers; the slice is copied.
func (c *Continuous) SetDomainNumbers(d []float64) {
	c.domain = append([]float64(nil), d...)
	c.tr = c.tspec.transform(firstOf(c.domain))
	c.rescale()
}

// Range returns a copy of the range.
func (c *Continuous) Range() []jsval.Value { return cloneValues(c.rng) }

// SetRange is scale.range(r).
func (c *Continuous) SetRange(r []jsval.Value) {
	c.rng = cloneValues(r)
	c.rescale()
}

// SetRangeRound is scale.rangeRound(r): sets the range and rounds results.
func (c *Continuous) SetRangeRound(r []jsval.Value) {
	c.rng = cloneValues(r)
	c.interp, c.round = nil, true
	c.rescale()
}

// Clamp reports whether clamping is on.
func (c *Continuous) Clamp() bool { return c.clampOn }

// SetClamp is scale.clamp(b).
func (c *Continuous) SetClamp(b bool) {
	c.clampOn = b
	c.rescale()
}

// Interpolate returns the range interpolator (d3.interpolate by default).
func (c *Continuous) Interpolate() Interpolator {
	if c.round {
		return InterpolateRound
	}
	if c.interp == nil {
		return InterpolateValue
	}
	return c.interp
}

// SetInterpolate is scale.interpolate(f); nil restores the default.
func (c *Continuous) SetInterpolate(f Interpolator) {
	c.interp, c.round = f, false
	c.rescale()
}

// Unknown returns the value produced for undefined/NaN inputs.
func (c *Continuous) Unknown() jsval.Value { return c.unknown }

// SetUnknown is scale.unknown(v).
func (c *Continuous) SetUnknown(v jsval.Value) { c.unknown = v }

// Base is the log base and reports whether this is a log scale.
func (c *Continuous) Base() (float64, bool) { return c.base, c.kind == kindLog }

// SetBase is log.base(b); ok is false for other kinds of scale.
func (c *Continuous) SetBase(b float64) bool {
	if c.kind != kindLog {
		return false
	}
	c.base = b
	c.tr = c.tspec.transform(firstOf(c.domain))
	c.rescale()
	return true
}

// Exponent is the pow exponent and reports whether this is a pow scale.
func (c *Continuous) Exponent() (float64, bool) { return c.exponent, c.kind == kindPow }

// SetExponent is pow.exponent(e); ok is false for other kinds of scale.
func (c *Continuous) SetExponent(e float64) bool {
	if c.kind != kindPow {
		return false
	}
	c.exponent = e
	c.tr = c.tspec.transform(firstOf(c.domain))
	c.rescale()
	return true
}

// Constant is the symlog constant and reports whether this is a symlog scale.
func (c *Continuous) Constant() (float64, bool) { return c.constant, c.kind == kindSymlog }

// SetConstant is symlog.constant(k); ok is false for other kinds of scale.
func (c *Continuous) SetConstant(k float64) bool {
	if c.kind != kindSymlog {
		return false
	}
	c.constant = k
	c.tr = c.tspec.transform(firstOf(c.domain))
	c.rescale()
	return true
}

// Ticks is scale.ticks(count).
func (c *Continuous) Ticks(count TickCount) []float64 { return c.tspec.ticks(c.domain, count) }

// Nice is scale.nice(count). For log scales the count is ignored.
func (c *Continuous) Nice(count TickCount) {
	if d := c.tspec.nice(c.domain, count); d != nil {
		c.domain = d
		c.rescale()
	}
}

// HasTickFormat reports true: every continuous scale has a tickFormat method.
func (c *Continuous) HasTickFormat() bool { return true }

// Copy returns an independent copy, as d3's scale.copy() does (domain, range,
// interpolate, clamp, unknown and the family parameter).
func (c *Continuous) Copy() Scale { return c.copyContinuous() }

func (c *Continuous) copyContinuous() *Continuous {
	n := &Continuous{
		meta:    meta{typ: c.typ},
		tspec:   c.tspec,
		domain:  append([]float64(nil), c.domain...),
		rng:     cloneValues(c.rng),
		interp:  c.interp,
		round:   c.round,
		tr:      c.tr,
		clampOn: c.clampOn,
		unknown: c.unknown,
	}
	n.rescale()
	return n
}

var (
	_ Scale         = (*Continuous)(nil)
	_ Clamper       = (*Continuous)(nil)
	_ Rounder       = (*Continuous)(nil)
	_ Inverter      = (*Continuous)(nil)
	_ RangeInverter = (*Continuous)(nil)
	_ Unknowner     = (*Continuous)(nil)
	_ Interpolating = (*Continuous)(nil)
	_ Ticker        = (*Continuous)(nil)
	_ Niceable      = (*Continuous)(nil)
	_ TickFormatter = (*Continuous)(nil)
	_ Typed         = (*Continuous)(nil)
)
