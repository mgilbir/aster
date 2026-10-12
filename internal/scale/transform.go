package scale

import (
	"fmt"
	"math"
	"strconv"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"
)

// tkind selects the domain transform of a continuous, sequential or diverging
// scale: the linear/log/pow/symlog "-ish" families of d3-scale.
type tkind uint8

const (
	kindLinear tkind = iota
	kindLog
	kindPow
	kindSymlog
)

// tspec carries the parameters of the transform family. d3 builds log, pow and
// symlog scales by re-parameterising one `transformer`; here the same state is
// a small struct embedded in every scale that supports it.
type tspec struct {
	kind     tkind
	base     float64 // log
	exponent float64 // pow
	constant float64 // symlog
	logs     func(float64) float64
	pows     func(float64) float64
}

func newTspec(kind tkind) tspec {
	t := tspec{kind: kind, base: 10, exponent: 1, constant: 1}
	t.refresh(1)
	return t
}

func transformLog(x float64) float64  { return jsmath.Log(x) }
func transformExp(x float64) float64  { return jsmath.Exp(x) }
func transformLogn(x float64) float64 { return -jsmath.Log(-x) }
func transformExpn(x float64) float64 { return -jsmath.Exp(-x) }

// pow10 is d3's `+("1e" + x)`: exact powers of ten (a correctly rounded decimal
// parse, unlike math.Pow), and NaN when x is not an integer since "1e2.5" is
// not a number.
func pow10(x float64) float64 {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		if x < 0 {
			return 0
		}
		return x
	}
	if x != math.Trunc(x) || math.Abs(x) >= 1e21 {
		return math.NaN()
	}
	switch {
	case x > 308: // "1e309" and beyond parse as Infinity
		return math.Inf(1)
	case x < -323: // and "1e-324" and below as 0
		return 0
	}
	return pow10tab[int(x)+323]
}

func powp(base float64) func(float64) float64 {
	switch base {
	case 10:
		return pow10
	case math.E:
		return jsmath.Exp // d3-scale's Math.exp
	}
	return func(x float64) float64 { return jsPow(base, x) }
}

func logp(base float64) func(float64) float64 {
	switch base {
	case math.E:
		return jsmath.Log // d3-scale's Math.log
	case 10:
		return log10
	case 2:
		return log2
	}
	lb := jsmath.Log(base)
	return func(x float64) float64 { return jsmath.Log(x) / lb }
}

func reflectFn(f func(float64) float64) func(float64) float64 {
	return func(x float64) float64 { return -f(-x) }
}

func transformPow(e float64) func(float64) float64 {
	return func(x float64) float64 {
		if x < 0 {
			return -jsPow(-x, e)
		}
		return jsPow(x, e)
	}
}

func transformSqrt(x float64) float64 {
	if x < 0 {
		return -math.Sqrt(-x)
	}
	return math.Sqrt(x)
}

func transformSquare(x float64) float64 {
	if x < 0 {
		return -x * x
	}
	return x * x
}

func transformSymlog(c float64) func(float64) float64 {
	return func(x float64) float64 { return jsSign(x) * jsmath.Log1p(math.Abs(x/c)) }
}

func transformSymexp(c float64) func(float64) float64 {
	return func(x float64) float64 { return jsSign(x) * jsmath.Expm1(math.Abs(x)) * c }
}

// refresh recomputes logs/pows for the current base (log kind). It is the
// `logs = logp(base), pows = powp(base)` half of loggish's rescale; the sign
// dependent reflection is applied by transform.
func (t *tspec) refresh(domain0 float64) {
	if t.kind != kindLog {
		return
	}
	t.logs, t.pows = logp(t.base), powp(t.base)
	if domain0 < 0 {
		t.logs, t.pows = reflectFn(t.logs), reflectFn(t.pows)
	}
}

// transform returns the (forward, inverse) pair for the given first domain
// value (only the log family looks at it: a domain starting below zero is a
// reflected log).
func (t *tspec) transform(domain0 float64) transform {
	switch t.kind {
	case kindLog:
		t.refresh(domain0)
		if domain0 < 0 {
			return transform{transformLogn, transformExpn}
		}
		return transform{transformLog, transformExp}
	case kindPow:
		switch t.exponent {
		case 1:
			return transform{}
		case 0.5:
			return transform{transformSqrt, transformSquare}
		}
		return transform{transformPow(t.exponent), transformPow(1 / t.exponent)}
	case kindSymlog:
		return transform{transformSymlog(t.constant), transformSymexp(t.constant)}
	}
	return transform{}
}

// maxLogIter bounds the steps of log tick generation, which skips the
// multiples below the domain without visiting them: past it the ticks would
// be too many to keep anyway.
const maxLogIter = 1 << 22

// ticks is the tick generator of the family: d3's linearish ticks, or the
// loggish variant that emits 1..base-1 multiples of each power.
// More than max ticks (at most maxTickValues) is an error wrapping
// budget.ErrLimit, raised before they are all made.
func (t *tspec) ticks(d []float64, count TickCount, max int) ([]float64, error) {
	u, v := endpoints(d)
	if t.kind != kindLog {
		return ticksN(u, v, count.countOrDefault(), max)
	}
	return t.logTicks(u, v, count.countOrDefault(), min(max, maxTickValues))
}

func endpoints(d []float64) (float64, float64) {
	if len(d) == 0 {
		return math.NaN(), math.NaN()
	}
	return d[0], d[len(d)-1]
}

// errLogTicks is the error past maxLogIter steps.
var errLogTicks = fmt.Errorf("%w: log ticks take more than %d steps", budget.ErrLimit, maxLogIter)

// logTicks is d3's loggish ticks. Its loops over the multiples k of each
// power start at the first multiple that can reach the domain (firstMultiple)
// rather than at 1: the multiples skipped are all below the domain, which
// d3's loop passes over with `continue`, so the ticks are the same.
func (t *tspec) logTicks(u, v, n float64, max int) ([]float64, error) {
	r := v < u
	if r {
		u, v = v, u
	}
	i, j := t.logs(u), t.logs(v)
	var z []float64
	m := math.Mod(t.base, 1)
	if (m == 0 || m != m) && j-i < n {
		i, j = math.Floor(i), math.Ceil(j)
		steps := maxLogIter
		if u > 0 {
			for ; i <= j; i++ {
				var p float64 // x is k*p, or k/p for a negative power
				if i < 0 {
					p = t.pows(-i)
				} else {
					p = t.pows(i)
				}
				for k := firstMultiple(u, p, i < 0, t.base); k < t.base; k++ {
					if steps--; steps < 0 {
						return nil, errLogTicks
					}
					var x float64
					if i < 0 {
						x = k / p
					} else {
						x = k * p
					}
					if x < u {
						continue
					}
					if x > v {
						break
					}
					if len(z) == max {
						return nil, fmt.Errorf("%w: more than %d log ticks", budget.ErrLimit, max)
					}
					z = append(z, x)
				}
			}
		} else {
			for ; i <= j; i++ {
				for k := t.base - 1; k >= 1; k-- {
					if steps--; steps < 0 {
						return nil, errLogTicks
					}
					var x float64
					if i > 0 {
						x = k / t.pows(-i)
					} else {
						x = k * t.pows(i)
					}
					if x < u {
						continue
					}
					if x > v {
						break
					}
					if len(z) == max {
						return nil, fmt.Errorf("%w: more than %d log ticks", budget.ErrLimit, max)
					}
					z = append(z, x)
				}
			}
		}
		if float64(len(z))*2 < n {
			var err error
			if z, err = ticksN(u, v, n, max); err != nil {
				return nil, err
			}
		}
	} else {
		var err error
		if z, err = ticksN(i, j, math.Min(j-i, n), max); err != nil {
			return nil, err
		}
		for k, x := range z {
			z[k] = t.pows(x)
		}
	}
	if r {
		for a, b := 0, len(z)-1; a < b; a, b = a+1, b-1 {
			z[a], z[b] = z[b], z[a]
		}
	}
	return z, nil
}

// firstMultiple is where logTicks' loop over k = 1, 2, ... base-1 may start
// for the power p (x = k/p when div, else k*p) without skipping a tick: two
// below the multiple nearest u, so that every k before it gives an x below u
// however the quotient u/p and the product round. A positive p is assumed
// (the u > 0 branch). It is 1 when u/p is not a finite number.
func firstMultiple(u, p float64, div bool, base float64) float64 {
	q := u / p
	if div {
		q = u * p
	}
	k := math.Floor(q) - 2
	if !(k > 1) || math.IsInf(q, 0) {
		return 1
	}
	return math.Min(k, base)
}

// nice returns the niced domain, or nil when d3 would leave the domain alone
// (linearish nice can fail to converge and then does nothing).
func (t *tspec) nice(d []float64, count TickCount) []float64 {
	if len(d) == 0 {
		return nil
	}
	if t.kind == kindLog {
		return t.logNice(d)
	}
	return linearNice(d, count.countOrDefault())
}

// linearNice is linearish's nice: rounds the domain end points to the tick
// step, repeating until the step stops changing (at most 10 rounds).
func linearNice(d []float64, count float64) []float64 {
	if len(d) == 0 {
		return nil // undefined end points give a NaN step: d3 changes nothing
	}
	d = append([]float64(nil), d...)
	i0, i1 := 0, len(d)-1
	start, stop := d[i0], d[i1]
	if stop < start {
		start, stop = stop, start
		i0, i1 = i1, i0
	}
	prestep := math.NaN() // undefined upstream: never equal to a step
	first := true
	for maxIter := 10; maxIter > 0; maxIter-- {
		step := TickIncrement(start, stop, count)
		if !first && step == prestep {
			d[i0], d[i1] = start, stop
			return d
		} else if step > 0 {
			start = math.Floor(start/step) * step
			stop = math.Ceil(stop/step) * step
		} else if step < 0 {
			start = math.Ceil(start*step) / step
			stop = math.Floor(stop*step) / step
		} else {
			break
		}
		prestep = step
		first = false
	}
	return nil
}

// logNice floors/ceils the end points to whole powers of the base.
func (t *tspec) logNice(d []float64) []float64 {
	d = append([]float64(nil), d...)
	i0, i1 := 0, len(d)-1
	x0, x1 := d[i0], d[i1]
	if x1 < x0 {
		i0, i1 = i1, i0
		x0, x1 = x1, x0
	}
	d[i0] = t.pows(math.Floor(t.logs(x0)))
	d[i1] = t.pows(math.Ceil(t.logs(x1)))
	return d
}

// tickFilterLog is d3's loggish tickFormat filter: for a value d it reports
// whether the label is dense enough to show (i <= k).
func (t *tspec) logKeep(count float64, tickCount int) func(d float64) bool {
	k := math.Max(1, t.base*count/float64(tickCount))
	return func(d float64) bool {
		i := d / t.pows(jsRound(t.logs(d)))
		if i*t.base < t.base-0.5 {
			i *= t.base
		}
		return i <= k
	}
}

// pow10tab holds the correctly rounded powers of ten 1e-323..1e308: what
// JavaScript's "1e" + n string parse gives.
var pow10tab = func() [632]float64 {
	var t [632]float64
	for i := range t {
		t[i], _ = strconv.ParseFloat("1e"+strconv.Itoa(i-323), 64)
	}
	return t
}()
