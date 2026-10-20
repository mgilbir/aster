package scale

import (
	"context"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"math"
)

// Scale is implemented by every scale. It is the common surface of a d3 scale
// object as vega-scale uses it: apply, domain, range, copy and the `type` tag
// that vega-scale attaches. Capabilities that only some scales have are
// expressed by the smaller interfaces below (Clamper, Niceable, Ticker, ...).
//
// Scales are not safe for concurrent use: like their d3 counterparts they
// build derived state lazily on first application.
type Scale interface {
	// Type is the vega-scale type name ("linear", "sequential-log", "band", ...).
	Type() string
	// Apply is scale(x). A nullish or NaN input yields the scale's unknown
	// value for scales that define one.
	Apply(x jsval.Value) jsval.Value
	// Domain returns a copy of the domain (dates as timestamps for time scales).
	Domain() []jsval.Value
	// SetDomain replaces the domain, coercing values as the d3 scale does.
	SetDomain(d []jsval.Value)
	// Range returns a copy of the range.
	Range() []jsval.Value
	// SetRange replaces the range.
	SetRange(r []jsval.Value)
	// Copy returns an independent scale with the same configuration and type.
	Copy() Scale
}

// meta holds the properties vega-scale/vega-encode hang on a scale object
// beside d3's own state: its type name and, for binned scales, `bins`.
type meta struct {
	typ  string
	bins []float64
	// work is what SetWork installed: it is told the steps of the scale's
	// loops over its domain, a batch at a time.
	work func(steps int)
	// walk is what SetWalker installed: the call's Walker, which walks the
	// values the scale reads as text or interpolates element by element.
	walk jsval.Walker
}

// SetWalker installs the Walker of the call the scale belongs to (ADR 0018).
// A copy of the scale keeps it. Without one the scale walks with the
// detached Walker.
func (m *meta) SetWalker(w jsval.Walker) { m.walk = w }

// workBatch is how many domain values a scale's loops go between two reports
// of their work.
const workBatch = 256

// SetWork installs a function the scale tells the steps of its loops over a
// domain (a step a value), a batch at a time, so that a caller can charge
// them to a budget and poll its context (ADR 0010): a domain is as long as a
// data set's distinct values. The function may end the caller's work by
// panicking, which leaves the scale part updated. nil installs none.
func (m *meta) SetWork(f func(steps int)) { m.work = f }

// charge reports, at value i of a loop over n, the batch of steps that
// starts there.
func (m *meta) charge(i, n int) {
	if m.work != nil && i&(workBatch-1) == 0 {
		m.work(min(workBatch, n-i))
	}
}

func (m *meta) Type() string { return m.typ }

// SetType records the vega-scale type name; the registry calls it on creation.
func (m *meta) SetType(t string) { m.typ = t }

// Bins returns the bin boundaries attached by vega-encode (scale.bins), or nil.
func (m *meta) Bins() []float64 { return m.bins }

// SetBins attaches bin boundaries (scale.bins = ...). Like upstream, Copy does
// not carry them over.
func (m *meta) SetBins(b []float64) { m.bins = b }

// Typed is the part of Scale that carries the registry type and bins.
type Typed interface {
	Type() string
	SetType(string)
	Bins() []float64
	SetBins([]float64)
}

// Clamper is implemented by continuous scales with clamp().
type Clamper interface {
	Clamp() bool
	SetClamp(bool)
}

// Rounder is implemented by scales with rangeRound(): the continuous scales
// (round the interpolated value) and band/point (round step and offsets).
type Rounder interface {
	SetRangeRound(r []jsval.Value)
}

// Inverter is implemented by scales with invert(y).
type Inverter interface {
	Invert(y jsval.Value) jsval.Value
}

// RangeInverter is vega-scale's invertRange: maps a pair of range values to a
// domain extent (continuous) or list of domain values (band/point). ok is false
// where upstream answers undefined.
type RangeInverter interface {
	InvertRange(lo, hi jsval.Value) (jsval.Value, bool)
}

// ExtentInverter is implemented by discretizing scales with invertExtent(y).
type ExtentInverter interface {
	// InvertExtent returns the domain extent of range value y; entries are
	// undefined (zero Value) where d3 returns undefined.
	InvertExtent(y jsval.Value) [2]jsval.Value
}

// Unknowner is implemented by scales with unknown().
type Unknowner interface {
	Unknown() jsval.Value
	SetUnknown(jsval.Value)
}

// Interpolating is implemented by continuous scales that take interpolate().
type Interpolating interface {
	Interpolate() Interpolator
	SetInterpolate(Interpolator)
}

// Ticker is implemented by scales with ticks(count) (all continuous scales,
// including time). The values are numbers; for temporal scales they are epoch
// milliseconds. Use TickValues for boxed values. Ticks d3 would list but
// that are too many to keep fail with an error wrapping budget.ErrLimit; a
// calendar walk stops with ctx's error once the call is over.
type Ticker interface {
	Ticks(ctx context.Context, count TickCount) ([]float64, error)
}

// Niceable is implemented by scales with nice(count).
type Niceable interface {
	Nice(count TickCount)
}

// TickFormatter marks scales that have a d3 `tickFormat` method. vega-scale only
// tests for its presence and then formats through its locale, so the method's
// implementation lives in the format layer.
type TickFormatter interface {
	HasTickFormat() bool
}

// Bander is implemented by band and point scales.
type Bander interface {
	Bandwidth() float64
	Step() float64
	Round() bool
	SetRound(bool)
	Padding() float64
	SetPadding(float64)
	PaddingInner() float64
	SetPaddingInner(float64)
	PaddingOuter() float64
	SetPaddingOuter(float64)
	Align() float64
	SetAlign(float64)
}

// TickCount is the "count" argument of d3's ticks/nice: unspecified (null or
// undefined upstream, which d3 treats as 10), a number, or - for time scales - a
// calendar interval.
type TickCount struct {
	// N is the count when HasN.
	N    float64
	HasN bool
	// Interval is non-nil for a time interval (time and utc scales).
	Interval *format.Interval
	// Null marks an unspecified count that is null rather than undefined.
	// d3's ticks treat both as 10, but vega-scale also computes with the
	// count, where null is 0 and undefined NaN (tickLog, formatSpan).
	Null bool
	// Opaque marks a count that is a JavaScript object or function: truthy
	// whatever number it reads as (N). vega-encode's `tickCount(...) || null`
	// keeps it (the member of Object.prototype a vega-time unit name finds, a
	// {} or an [] given as the interval), where a NaN or 0 count it reads as
	// would be replaced by null.
	Opaque bool
}

// number is the count as JavaScript arithmetic reads it: N, 0 for null, NaN
// for undefined or an interval.
func (c TickCount) number() float64 {
	switch {
	case c.HasN:
		return c.N
	case c.Null && c.Interval == nil:
		return 0
	}
	return math.NaN()
}

// IntervalCount makes a TickCount that names a calendar interval.
func IntervalCount(iv format.Interval) TickCount { return TickCount{Interval: &iv} }

// Count makes a numeric TickCount.
func Count(n float64) TickCount { return TickCount{N: n, HasN: true} }

// CountOrDefault returns the count, or d3's default of 10.
func (c TickCount) countOrDefault() float64 {
	if c.HasN {
		return c.N
	}
	return 10
}

// DefaultTickCount is d3's implicit tick count (10).
const DefaultTickCount = 10

// transform is the (forward, inverse) pair of a continuous scale; nil functions
// mean identity, which lets the common linear case skip two indirect calls.
type transform struct {
	fwd, inv func(float64) float64
}

func (t transform) apply(x float64) float64 {
	if t.fwd == nil {
		return x
	}
	return t.fwd(x)
}

func (t transform) unapply(x float64) float64 {
	if t.inv == nil {
		return x
	}
	return t.inv(x)
}

func numsToValues(f []float64) []jsval.Value {
	out := make([]jsval.Value, len(f))
	for i, x := range f {
		out[i] = jsval.Num(x)
	}
	return out
}

func numsToTimestamps(f []float64) []jsval.Value {
	out := make([]jsval.Value, len(f))
	for i, x := range f {
		out[i] = jsval.Timestamp(x)
	}
	return out
}

func valuesToNums(v []jsval.Value) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = jsval.ToNumber(x)
	}
	return out
}

func cloneValues(v []jsval.Value) []jsval.Value {
	out := make([]jsval.Value, len(v))
	copy(out, v)
	return out
}
