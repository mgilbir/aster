package scale

import (
	"math"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// Radial is d3.scaleRadial: a linear scale over the squares of its range, whose
// output is the signed square root. Vega has no "radial" scale type, so it is
// not in the registry; callers construct it with NewRadial.
type Radial struct {
	meta
	squared *Continuous
	rng     []float64
	round   bool
	unknown jsval.Value
}

// NewRadial is d3.scaleRadial (domain [0, 1], range [0, 1]).
func NewRadial() *Radial {
	r := &Radial{squared: NewLinear(), rng: []float64{0, 1}}
	r.setSquaredRange()
	return r
}

func square(x float64) float64 { return jsSign(x) * x * x }

func unsquare(x float64) float64 { return jsSign(x) * math.Sqrt(math.Abs(x)) }

func (r *Radial) setSquaredRange() {
	sq := make([]jsval.Value, len(r.rng))
	for i, y := range r.rng {
		sq[i] = jsval.Num(square(y))
	}
	r.squared.SetRange(sq)
}

// Apply is scale(x).
func (r *Radial) Apply(x jsval.Value) jsval.Value {
	y := unsquare(jsval.ToNumber(r.squared.Apply(x)))
	if y != y {
		return r.unknown
	}
	if r.round {
		y = jsRound(y)
	}
	return jsval.Num(y)
}

// Invert is scale.invert(y).
func (r *Radial) Invert(y jsval.Value) jsval.Value {
	return jsval.Num(r.squared.InvertNumber(square(jsval.ToNumber(y))))
}

// Domain returns the domain as numbers.
func (r *Radial) Domain() []jsval.Value { return r.squared.Domain() }

// SetDomain is scale.domain(d): every element goes through Number().
func (r *Radial) SetDomain(d []jsval.Value) { r.squared.SetDomain(d) }

// Range returns a copy of the range.
func (r *Radial) Range() []jsval.Value { return numsToValues(r.rng) }

// SetRange is scale.range(r): every element goes through Number().
func (r *Radial) SetRange(v []jsval.Value) {
	r.rng = valuesToNums(v)
	r.setSquaredRange()
}

// SetRangeRound is scale.rangeRound(r): sets the range and rounds results.
func (r *Radial) SetRangeRound(v []jsval.Value) {
	r.SetRange(v)
	r.round = true
}

// Round reports whether results are rounded.
func (r *Radial) Round() bool { return r.round }

// SetRound is scale.round(b).
func (r *Radial) SetRound(b bool) { r.round = b }

// Clamp reports whether clamping is on.
func (r *Radial) Clamp() bool { return r.squared.Clamp() }

// SetClamp is scale.clamp(b).
func (r *Radial) SetClamp(b bool) { r.squared.SetClamp(b) }

// Unknown returns the value produced for undefined/NaN inputs.
func (r *Radial) Unknown() jsval.Value { return r.unknown }

// SetUnknown is scale.unknown(v).
func (r *Radial) SetUnknown(v jsval.Value) { r.unknown = v }

// Ticks is scale.ticks(count).
func (r *Radial) Ticks(count TickCount) []float64 { return r.squared.Ticks(count) }

// Nice is scale.nice(count).
func (r *Radial) Nice(count TickCount) { r.squared.Nice(count) }

// HasTickFormat reports true: a radial scale is linearish.
func (r *Radial) HasTickFormat() bool { return true }

// TickFormat is scale.tickFormat(count, specifier) with d3's semantics.
func (r *Radial) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	return r.squared.TickFormat(loc, count, specifier)
}

// Copy returns an independent copy (domain, range, round, clamp and unknown).
func (r *Radial) Copy() Scale {
	n := NewRadial()
	n.SetRange(r.Range())
	n.SetDomain(r.Domain())
	n.round = r.round
	n.SetClamp(r.Clamp())
	n.unknown = r.unknown
	return n
}

var (
	_ Scale         = (*Radial)(nil)
	_ Clamper       = (*Radial)(nil)
	_ Rounder       = (*Radial)(nil)
	_ Inverter      = (*Radial)(nil)
	_ Unknowner     = (*Radial)(nil)
	_ Ticker        = (*Radial)(nil)
	_ Niceable      = (*Radial)(nil)
	_ TickFormatter = (*Radial)(nil)
)
