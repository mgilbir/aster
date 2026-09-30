package scale

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// pmap is d3-scale's bimap/polymap: a piecewise mapping from a monotonic domain
// to range values. Each segment normalises x into t and interpolates.
//
// Two flavours share one struct. The numeric flavour (num set) holds plain
// float64 endpoints and never allocates or boxes; it is used whenever the
// interpolation is d3's default number interpolation (and always for invert).
// The generic flavour holds one interpolator closure per segment.
type pmap struct {
	poly  bool
	j     int       // number of segments (poly only); bisect upper bound
	d     []float64 // domain used for bisection (poly only), ascending
	norm  []normalizer
	num   bool
	round bool // interpolateRound: round every numeric result
	nr    []numPair
	gr    []func(float64) jsval.Value
}

type numPair struct{ a, b float64 }

// normalizer is d3's normalize(a, b): (x-a)/(b-a), or a constant when the span
// is zero (0.5) or NaN (NaN).
type normalizer struct {
	a, span float64
	c       float64
	isConst bool
}

func newNormalizer(a, b float64) normalizer {
	span := b - a
	if span != 0 && span == span {
		return normalizer{a: a, span: span}
	}
	if span != span {
		return normalizer{isConst: true, c: math.NaN()}
	}
	return normalizer{isConst: true, c: 0.5}
}

func (n normalizer) at(x float64) float64 {
	if n.isConst {
		return n.c
	}
	return (x - n.a) / n.span
}

func numAt(s []float64, i int) float64 {
	if i < 0 || i >= len(s) {
		return math.NaN()
	}
	return s[i]
}

// newNumMap builds a numeric pmap from a domain and a numeric range.
func newNumMap(domain, rng []float64) *pmap {
	n := min(len(domain), len(rng))
	if n > 2 {
		return newPolyNum(domain, rng, n)
	}
	d0, d1 := numAt(domain, 0), numAt(domain, 1)
	r0, r1 := numAt(rng, 0), numAt(rng, 1)
	m := &pmap{num: true, norm: make([]normalizer, 1), nr: make([]numPair, 1)}
	if d1 < d0 {
		m.norm[0] = newNormalizer(d1, d0)
		m.nr[0] = numPair{r1, r0}
	} else {
		m.norm[0] = newNormalizer(d0, d1)
		m.nr[0] = numPair{r0, r1}
	}
	return m
}

func newPolyNum(domain, rng []float64, n int) *pmap {
	j := n - 1
	domain, rng = domain[:n], rng[:n]
	if domain[j] < domain[0] {
		domain = reversedCopy(domain)
		rng = reversedCopy(rng)
	}
	m := &pmap{poly: true, j: j, d: domain, num: true,
		norm: make([]normalizer, j), nr: make([]numPair, j)}
	for i := 0; i < j; i++ {
		m.norm[i] = newNormalizer(domain[i], domain[i+1])
		m.nr[i] = numPair{rng[i], rng[i+1]}
	}
	return m
}

func reversedCopy(s []float64) []float64 {
	out := make([]float64, len(s))
	for i, x := range s {
		out[len(s)-1-i] = x
	}
	return out
}

// newValueMap builds the map for a range of arbitrary values. When interp is
// nil the default d3.interpolate applies, and if every segment's end value is a
// number the numeric flavour is used (interpolateNumber is what d3.interpolate
// chooses for those).
func newValueMap(domain []float64, rng []jsval.Value, interp Interpolator, round bool) *pmap {
	n := min(len(domain), len(rng))
	var pairs [][2]jsval.Value
	var doms []float64
	m := &pmap{}
	if n > 2 {
		j := n - 1
		domain, rng = domain[:n], rng[:n]
		if domain[j] < domain[0] {
			domain = reversedCopy(domain)
			r := make([]jsval.Value, n)
			for i, v := range rng {
				r[n-1-i] = v
			}
			rng = r
		}
		m.poly, m.j, m.d = true, j, domain
		m.norm = make([]normalizer, j)
		pairs = make([][2]jsval.Value, j)
		for i := 0; i < j; i++ {
			m.norm[i] = newNormalizer(domain[i], domain[i+1])
			pairs[i] = [2]jsval.Value{rng[i], rng[i+1]}
		}
	} else {
		doms = []float64{numAt(domain, 0), numAt(domain, 1)}
		r0, r1 := valAt(rng, 0), valAt(rng, 1)
		m.norm = make([]normalizer, 1)
		pairs = make([][2]jsval.Value, 1)
		if doms[1] < doms[0] {
			m.norm[0] = newNormalizer(doms[1], doms[0])
			pairs[0] = [2]jsval.Value{r1, r0}
		} else {
			m.norm[0] = newNormalizer(doms[0], doms[1])
			pairs[0] = [2]jsval.Value{r0, r1}
		}
	}
	if interp == nil || round {
		allNum := true
		for _, p := range pairs {
			if !p[1].IsNum() {
				allNum = false
				break
			}
		}
		if allNum || round {
			m.num = true
			m.nr = make([]numPair, len(pairs))
			for i, p := range pairs {
				m.nr[i] = numPair{jsval.ToNumber(p[0]), jsval.ToNumber(p[1])}
			}
			m.round = round
			return m
		}
	}
	m.gr = make([]func(float64) jsval.Value, len(pairs))
	for i, p := range pairs {
		if interp == nil {
			m.gr[i] = InterpolateValue(p[0], p[1])
		} else {
			m.gr[i] = interp(p[0], p[1])
		}
	}
	return m
}

func valAt(s []jsval.Value, i int) jsval.Value {
	if i < 0 || i >= len(s) {
		return jsval.Undefined
	}
	return s[i]
}

// segment returns the segment index for x.
func (m *pmap) segment(x float64) int {
	if !m.poly {
		return 0
	}
	return bisectRight(m.d, x, 1, m.j) - 1
}

// value evaluates the map at x.
func (m *pmap) value(x float64) jsval.Value {
	i := m.segment(x)
	t := m.norm[i].at(x)
	if m.num {
		p := m.nr[i]
		v := lerp(p.a, p.b, t)
		if m.round {
			v = jsRound(v)
		}
		return jsval.Num(v)
	}
	return m.gr[i](t)
}

// number evaluates a numeric map at x with no boxing. Meant for maps built with
// the numeric flavour; for generic maps it unboxes the interpolated value.
func (m *pmap) number(x float64) float64 {
	i := m.segment(x)
	t := m.norm[i].at(x)
	if m.num {
		p := m.nr[i]
		v := lerp(p.a, p.b, t)
		if m.round {
			v = jsRound(v)
		}
		return v
	}
	return m.gr[i](t).AsDouble()
}

// numberAtHi evaluates like number, but for an input that d3's bisect treats as
// not comparable (null/undefined), which selects the last segment. t is
// computed from x's numeric coercion.
func (m *pmap) numberAtHi(x float64) float64 {
	i := 0
	if m.poly {
		i = m.j - 1
	}
	t := m.norm[i].at(x)
	p := m.nr[i]
	return lerp(p.a, p.b, t)
}
