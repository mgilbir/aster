package scale

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// BandSpace is vega-scale's bandSpace: the number of steps a band scale divides
// its range into.
func BandSpace(count, paddingInner, paddingOuter float64) float64 {
	space := count - paddingInner + paddingOuter*2
	if count != 0 {
		if space > 0 {
			return space
		}
		return 1
	}
	return 0
}

// Band is vega-scale's band scale (and, with point set, its point scale): an
// ordinal scale whose range is computed from a [start, stop] extent, padding
// and alignment. vega-scale re-implements d3.scaleBand (clamping every padding
// to [0, 1] and using bandSpace); this is that version.
type Band struct {
	meta
	ord          *Ordinal
	r0, r1       float64
	step         float64
	bandwidth    float64
	round        bool
	paddingInner float64
	paddingOuter float64
	align        float64
	point        bool
}

// NewBand is vega-scale's band scale.
func NewBand() *Band {
	b := &Band{ord: NewOrdinal(), r1: 1, align: 0.5}
	b.ord.SetUnknown(jsval.Undefined)
	b.rescale()
	return b
}

// NewPoint is vega-scale's point scale: a band scale with paddingInner 1, whose
// padding() is the outer padding.
func NewPoint() *Band {
	b := NewBand()
	b.SetPaddingInner(1)
	b.point = true
	return b
}

// IsPoint reports whether this is a point scale.
func (b *Band) IsPoint() bool { return b.point }

func (b *Band) rescale() {
	n := float64(len(b.ord.domain))
	reverse := b.r1 < b.r0
	start, stop := b.r0, b.r1
	if reverse {
		start, stop = b.r1, b.r0
	}
	space := BandSpace(n, b.paddingInner, b.paddingOuter)
	if space == 0 {
		space = 1 // `space || 1`
	}
	step := (stop - start) / space
	if b.round {
		step = math.Floor(step)
	}
	start += float64(((stop - start) - float64(step*(n-b.paddingInner))) * b.align)
	bandwidth := step * (1 - b.paddingInner)
	if b.round {
		start = jsRound(start)
		bandwidth = jsRound(bandwidth)
	}
	b.step, b.bandwidth = step, bandwidth
	count := len(b.ord.domain)
	values := make([]jsval.Value, count)
	for i := 0; i < count; i++ {
		v := start + float64(step*float64(i))
		if reverse {
			values[count-1-i] = jsval.Num(v)
		} else {
			values[i] = jsval.Num(v)
		}
	}
	b.ord.rng = values
}

// Apply is scale(x): the start of x's band, or undefined for an unknown value.
func (b *Band) Apply(x jsval.Value) jsval.Value { return b.ord.Apply(x) }

// Position is scale(x) as a number, NaN when x is not in the domain.
func (b *Band) Position(x jsval.Value) float64 {
	i, ok := b.ord.lookup(x)
	if !ok || i >= len(b.ord.rng) {
		return math.NaN()
	}
	return b.ord.rng[i].NumValue()
}

// Domain returns a copy of the domain.
func (b *Band) Domain() []jsval.Value { return b.ord.Domain() }

// SetDomain is scale.domain(d).
func (b *Band) SetDomain(d []jsval.Value) {
	b.ord.SetDomain(d)
	b.rescale()
}

// Range returns [start, stop].
func (b *Band) Range() []jsval.Value { return []jsval.Value{jsval.Num(b.r0), jsval.Num(b.r1)} }

// SetRange is scale.range([start, stop]); missing entries read as NaN.
func (b *Band) SetRange(r []jsval.Value) {
	b.r0, b.r1 = numFromValues(r, 0), numFromValues(r, 1)
	b.rescale()
}

// SetRangeRound is scale.rangeRound([start, stop]).
func (b *Band) SetRangeRound(r []jsval.Value) {
	b.r0, b.r1 = numFromValues(r, 0), numFromValues(r, 1)
	b.round = true
	b.rescale()
}

// Bandwidth is the width of each band.
func (b *Band) Bandwidth() float64 { return b.bandwidth }

// Step is the distance between the starts of adjacent bands.
func (b *Band) Step() float64 { return b.step }

// Round reports whether step and offsets are rounded to integers.
func (b *Band) Round() bool { return b.round }

// SetRound is scale.round(bool).
func (b *Band) SetRound(v bool) { b.round = v; b.rescale() }

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// Padding is scale.padding(): the inner padding for band, the outer for point.
func (b *Band) Padding() float64 {
	if b.point {
		return b.paddingOuter
	}
	return b.paddingInner
}

// SetPadding is scale.padding(p): both paddings for band, the outer one only
// for point (whose paddingInner is fixed at 1).
func (b *Band) SetPadding(p float64) {
	if b.point {
		b.paddingOuter = clamp01(p)
	} else {
		b.paddingOuter = clamp01(p)
		b.paddingInner = b.paddingOuter
	}
	b.rescale()
}

// PaddingInner is the fraction of the step between bands.
func (b *Band) PaddingInner() float64 { return b.paddingInner }

// SetPaddingInner is scale.paddingInner(p), clamped to [0, 1].
func (b *Band) SetPaddingInner(p float64) { b.paddingInner = clamp01(p); b.rescale() }

// PaddingOuter is the padding at both ends, in steps.
func (b *Band) PaddingOuter() float64 { return b.paddingOuter }

// SetPaddingOuter is scale.paddingOuter(p), clamped to [0, 1].
func (b *Band) SetPaddingOuter(p float64) { b.paddingOuter = clamp01(p); b.rescale() }

// Align is the alignment of the bands within the range.
func (b *Band) Align() float64 { return b.align }

// SetAlign is scale.align(a), clamped to [0, 1].
func (b *Band) SetAlign(a float64) { b.align = clamp01(a); b.rescale() }

// InvertRange is vega-scale's band invertRange: the domain values whose bands
// intersect [lo, hi]. ok is false where upstream answers undefined.
func (b *Band) InvertRange(loV, hiV jsval.Value) (jsval.Value, bool) {
	if loV.IsNullish() || hiV.IsNullish() {
		return jsval.Undefined, false
	}
	lo, hi := jsval.ToNumber(loV), jsval.ToNumber(hiV)
	if lo != lo || hi != hi {
		return jsval.Undefined, false
	}
	reverse := b.r1 < b.r0
	rng := b.ord.rng
	values := make([]float64, len(rng))
	for i, v := range rng {
		if reverse {
			values[len(rng)-1-i] = v.NumValue()
		} else {
			values[i] = v.NumValue()
		}
	}
	n := len(values) - 1
	if hi < lo {
		lo, hi = hi, lo
	}
	far := b.r1
	if reverse {
		far = b.r0
	}
	if (len(values) > 0 && hi < values[0]) || lo > far {
		return jsval.Undefined, false
	}
	a := max(0, bisectRight(values, lo, 0, len(values))-1)
	var e int
	if lo == hi {
		e = a
	} else {
		e = bisectRight(values, hi, 0, len(values)) - 1
	}
	if a < len(values) && lo-values[a] > b.bandwidth+1e-10 {
		a++
	}
	if reverse {
		a, e = n-e, n-a
	}
	if a > e {
		return jsval.Undefined, false
	}
	dom := b.ord.domain
	lo2, hi2 := min(a, len(dom)), min(e+1, len(dom))
	if lo2 < 0 {
		lo2 = 0
	}
	if hi2 < lo2 {
		hi2 = lo2
	}
	return jsval.Arr(cloneValues(dom[lo2:hi2])), true
}

// Invert is scale.invert(y): the domain value whose band contains y.
func (b *Band) Invert(y jsval.Value) jsval.Value {
	v, ok := b.InvertRange(y, y)
	if !ok || v.Len() == 0 {
		return jsval.Undefined
	}
	return v.Index(0)
}

// Copy is vega-scale's band copy (a point scale stays a point scale).
func (b *Band) Copy() Scale {
	n := NewBand()
	n.typ = b.typ
	n.point = b.point
	n.ord.SetDomain(b.ord.domain)
	n.r0, n.r1 = b.r0, b.r1
	n.round = b.round
	n.paddingInner, n.paddingOuter, n.align = b.paddingInner, b.paddingOuter, b.align
	n.rescale()
	return n
}

var (
	_ Scale         = (*Band)(nil)
	_ Bander        = (*Band)(nil)
	_ Rounder       = (*Band)(nil)
	_ Inverter      = (*Band)(nil)
	_ RangeInverter = (*Band)(nil)
)
