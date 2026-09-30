package transforms

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Distribution is a probability distribution as vega-statistics models them.
type Distribution interface {
	PDF(x float64) float64
	CDF(x float64) float64
	// ICDF is the quantile function; kde and mixture do not support it.
	ICDF(p float64) (float64, error)
	// Sample draws one value using r.
	Sample(r Rand) float64
}

// ErrNoICDF is returned by ICDF of distributions that upstream leaves
// unsupported (kde, mixture).
var ErrNoICDF = errors.New("icdf not supported")

const (
	distSQRT2PI = 2.5066282746310002 // math.Sqrt(2 * math.Pi)
	distSQRT2   = math.Sqrt2
)

// distOrZero is JavaScript's `x || 0`.
func distOrZero(x float64) float64 {
	if x == 0 || math.IsNaN(x) {
		return 0
	}
	return x
}

// normalSampler is the Box-Muller sampler: each accepted pair yields two
// normals, the second cached for the next call. Upstream keeps that cache in
// one module-level variable; here each distribution owns its own, which keeps
// results reproducible per distribution.
type normalSampler struct{ next float64 }

func (s *normalSampler) sample(r Rand, mean, stdev float64) float64 {
	var x float64
	if s.next == s.next {
		x = s.next
		s.next = math.NaN()
	} else {
		var y, rds float64
		for {
			x = r()*2 - 1
			y = r()*2 - 1
			rds = float64(x*x) + float64(y*y)
			if !(rds == 0 || rds > 1) {
				break
			}
		}
		c := math.Sqrt(-2 * jsmath.Log(rds) / rds)
		x *= c
		s.next = y * c
	}
	return mean + float64(x*stdev)
}

// DensityNormal is the normal probability density function.
func DensityNormal(value, mean, stdev float64) float64 {
	z := (value - distOrZero(mean)) / stdev
	return jsmath.Exp(-0.5*z*z) / (stdev * distSQRT2PI)
}

// CumulativeNormal is the normal CDF using the approximation of West (2009),
// "Better Approximations to Cumulative Normal Functions".
func CumulativeNormal(value, mean, stdev float64) float64 {
	z := (value - distOrZero(mean)) / stdev
	Z := math.Abs(z)
	var cd float64
	if Z > 37 {
		cd = 0
	} else {
		exp := jsmath.Exp(-Z * Z / 2)
		var sum float64
		if Z < 7.07106781186547 {
			sum = float64(3.52624965998911e-02*Z) + 0.700383064443688
			sum = float64(sum*Z) + 6.37396220353165
			sum = float64(sum*Z) + 33.912866078383
			sum = float64(sum*Z) + 112.079291497871
			sum = float64(sum*Z) + 221.213596169931
			sum = float64(sum*Z) + 220.206867912376
			cd = exp * sum
			sum = float64(8.83883476483184e-02*Z) + 1.75566716318264
			sum = float64(sum*Z) + 16.064177579207
			sum = float64(sum*Z) + 86.7807322029461
			sum = float64(sum*Z) + 296.564248779674
			sum = float64(sum*Z) + 637.333633378831
			sum = float64(sum*Z) + 793.826512519948
			sum = float64(sum*Z) + 440.413735824752
			cd = cd / sum
		} else {
			sum = Z + 0.65
			sum = Z + 4/sum
			sum = Z + 3/sum
			sum = Z + 2/sum
			sum = Z + 1/sum
			cd = exp / sum / 2.506628274631
		}
	}
	if z > 0 {
		return 1 - cd
	}
	return cd
}

// QuantileNormal is the normal quantile function (probit) via the inverse
// error function; NaN outside [0, 1].
func QuantileNormal(p, mean, stdev float64) float64 {
	if p < 0 || p > 1 {
		return math.NaN()
	}
	return distOrZero(mean) + float64(float64(stdev*distSQRT2)*distErfinv(2*p-1))
}

// distErfinv approximates the inverse error function (Giles, "Approximating
// the erfinv function", GPU Computing Gems 2, 2010), ported upstream from
// Apache Commons Math.
func distErfinv(x float64) float64 {
	// The logarithm argument must be (1-x)(1+x), not 1-x*x, to avoid rounding
	// errors near +-1.
	w := -jsmath.Log((1 - x) * (1 + x))
	var p float64
	horner := func(w float64, init float64, cs []float64) float64 {
		p := init
		for _, c := range cs {
			p = c + float64(p*w)
		}
		return p
	}
	switch {
	case w < 6.25:
		w -= 3.125
		p = horner(w, -3.6444120640178196996e-21, []float64{
			-1.685059138182016589e-19, 1.2858480715256400167e-18, 1.115787767802518096e-17,
			-1.333171662854620906e-16, 2.0972767875968561637e-17, 6.6376381343583238325e-15,
			-4.0545662729752068639e-14, -8.1519341976054721522e-14, 2.6335093153082322977e-12,
			-1.2975133253453532498e-11, -5.4154120542946279317e-11, 1.051212273321532285e-09,
			-4.1126339803469836976e-09, -2.9070369957882005086e-08, 4.2347877827932403518e-07,
			-1.3654692000834678645e-06, -1.3882523362786468719e-05, 0.0001867342080340571352,
			-0.00074070253416626697512, -0.0060336708714301490533, 0.24015818242558961693,
			1.6536545626831027356})
	case w < 16.0:
		w = math.Sqrt(w) - 3.25
		p = horner(w, 2.2137376921775787049e-09, []float64{
			9.0756561938885390979e-08, -2.7517406297064545428e-07, 1.8239629214389227755e-08,
			1.5027403968909827627e-06, -4.013867526981545969e-06, 2.9234449089955446044e-06,
			1.2475304481671778723e-05, -4.7318229009055733981e-05, 6.8284851459573175448e-05,
			2.4031110387097893999e-05, -0.0003550375203628474796, 0.00095328937973738049703,
			-0.0016882755560235047313, 0.0024914420961078508066, -0.0037512085075692412107,
			0.005370914553590063617, 1.0052589676941592334, 3.0838856104922207635})
	case !math.IsInf(w, 0) && !math.IsNaN(w):
		w = math.Sqrt(w) - 5.0
		p = horner(w, -2.7109920616438573243e-11, []float64{
			-2.5556418169965252055e-10, 1.5076572693500548083e-09, -3.7894654401267369937e-09,
			7.6157012080783393804e-09, -1.4960026627149240478e-08, 2.9147953450901080826e-08,
			-6.7711997758452339498e-08, 2.2900482228026654717e-07, -9.9298272942317002539e-07,
			4.5260625972231537039e-06, -1.9681778105531670567e-05, 7.5995277030017761139e-05,
			-0.00021503011930044477347, -0.00013871931833623122026, 1.0103004648645343977,
			4.8499064014085844221})
	default:
		p = math.Inf(1)
	}
	return p * x
}

// Normal is the normal (Gaussian) distribution.
type Normal struct {
	Mean, Stdev float64
	s           normalSampler
}

// NewNormal follows randomNormal(mean, stdev): a NaN mean becomes 0. Callers
// wanting upstream's default stdev (used when it is null) pass 1.
func NewNormal(mean, stdev float64) *Normal {
	return &Normal{Mean: distOrZero(mean), Stdev: stdev, s: normalSampler{next: math.NaN()}}
}

func (d *Normal) PDF(x float64) float64 { return DensityNormal(x, d.Mean, d.Stdev) }
func (d *Normal) CDF(x float64) float64 { return CumulativeNormal(x, d.Mean, d.Stdev) }
func (d *Normal) ICDF(p float64) (float64, error) {
	return QuantileNormal(p, d.Mean, d.Stdev), nil
}
func (d *Normal) Sample(r Rand) float64 { return d.s.sample(r, d.Mean, d.Stdev) }

// LogNormal is the log-normal distribution; Mean and Stdev are those of the
// underlying normal.
type LogNormal struct {
	Mean, Stdev float64
	s           normalSampler
}

func NewLogNormal(mean, stdev float64) *LogNormal {
	return &LogNormal{Mean: distOrZero(mean), Stdev: stdev, s: normalSampler{next: math.NaN()}}
}

func (d *LogNormal) PDF(value float64) float64 {
	if value <= 0 {
		return 0
	}
	z := (jsmath.Log(value) - d.Mean) / d.Stdev
	return jsmath.Exp(-0.5*z*z) / (d.Stdev * distSQRT2PI * value)
}
func (d *LogNormal) CDF(value float64) float64 {
	return CumulativeNormal(jsmath.Log(value), d.Mean, d.Stdev)
}
func (d *LogNormal) ICDF(p float64) (float64, error) {
	return jsmath.Exp(QuantileNormal(p, d.Mean, d.Stdev)), nil
}
func (d *LogNormal) Sample(r Rand) float64 {
	return jsmath.Exp(d.Mean + float64(d.s.sample(r, 0, 1)*d.Stdev))
}

// Uniform is the continuous uniform distribution on [Min, Max].
type Uniform struct{ Min, Max float64 }

// NewUniform follows randomUniform(min, max) once both are given (`min || 0`).
func NewUniform(min, max float64) *Uniform { return &Uniform{Min: distOrZero(min), Max: max} }

func (d *Uniform) PDF(v float64) float64 {
	if v >= d.Min && v <= d.Max {
		return 1 / (d.Max - d.Min)
	}
	return 0
}
func (d *Uniform) CDF(v float64) float64 {
	switch {
	case v < d.Min:
		return 0
	case v > d.Max:
		return 1
	}
	return (v - d.Min) / (d.Max - d.Min)
}
func (d *Uniform) ICDF(p float64) (float64, error) {
	if p >= 0 && p <= 1 {
		return d.Min + float64(p*(d.Max-d.Min)), nil
	}
	return math.NaN(), nil
}
func (d *Uniform) Sample(r Rand) float64 { return d.Min + float64((d.Max-d.Min)*r()) }

// Integer is the discrete uniform distribution on the integers [Min, Max)
// (vega-statistics' randomInteger).
type Integer struct{ Min, Max float64 }

func NewInteger(min, max float64) *Integer {
	return &Integer{Min: distOrZero(min), Max: distOrZero(max)}
}

func (d *Integer) span() float64 { return d.Max - d.Min }
func (d *Integer) PDF(x float64) float64 {
	if x == math.Floor(x) && x >= d.Min && x < d.Max {
		return 1 / d.span()
	}
	return 0
}
func (d *Integer) CDF(x float64) float64 {
	v := math.Floor(x)
	switch {
	case v < d.Min:
		return 0
	case v >= d.Max:
		return 1
	}
	return (v - d.Min + 1) / d.span()
}
func (d *Integer) ICDF(p float64) (float64, error) {
	if p >= 0 && p <= 1 {
		return d.Min - 1 + math.Floor(p*d.span()), nil
	}
	return math.NaN(), nil
}
func (d *Integer) Sample(r Rand) float64 { return d.Min + math.Floor(d.span()*r()) }

// Mixture is a weighted mixture of distributions. Weights are normalised to
// sum to one; a missing (NaN) or absent weight counts as 1.
type Mixture struct {
	Dists   []Distribution
	weights []float64
}

// NewMixture builds a mixture. weights may be shorter than dists or nil.
func NewMixture(dists []Distribution, weights []float64) *Mixture {
	m := len(dists)
	w := make([]float64, m)
	sum := 0.0
	for i := range w {
		w[i] = 1
		if i < len(weights) && !math.IsNaN(weights[i]) {
			w[i] = weights[i]
		}
		sum += w[i]
	}
	for i := range w {
		w[i] /= sum
	}
	return &Mixture{Dists: dists, weights: w}
}

func (d *Mixture) PDF(x float64) float64 {
	p := 0.0
	for i, dd := range d.Dists {
		p += float64(d.weights[i] * dd.PDF(x))
	}
	return p
}
func (d *Mixture) CDF(x float64) float64 {
	p := 0.0
	for i, dd := range d.Dists {
		p += float64(d.weights[i] * dd.CDF(x))
	}
	return p
}
func (d *Mixture) ICDF(float64) (float64, error) { return math.NaN(), ErrNoICDF }
func (d *Mixture) Sample(r Rand) float64 {
	m := len(d.Dists)
	if m == 0 {
		return math.NaN()
	}
	u := r()
	pick := d.Dists[m-1]
	v := d.weights[0]
	for i := 0; i < m-1; i++ {
		if u < v {
			pick = d.Dists[i]
			break
		}
		v += d.weights[i+1]
	}
	return pick.Sample(r)
}

// KernelDensity is a Gaussian kernel density estimate over sample points.
type KernelDensity struct {
	support   []jsval.Value
	nums      []float64
	Bandwidth float64
	kernel    normalSampler
}

// NewKernelDensity builds a KDE over raw values (coerced with arithmetic semantics).
// A zero or NaN bandwidth is estimated with EstimateBandwidth.
func NewKernelDensity(support []jsval.Value, bandwidth float64) *KernelDensity {
	d := &KernelDensity{support: support, kernel: normalSampler{next: math.NaN()}}
	d.nums = make([]float64, len(support))
	for i, v := range support {
		d.nums[i] = jsval.ToNumber(v)
	}
	d.Bandwidth = bandwidth
	if bandwidth == 0 || math.IsNaN(bandwidth) {
		d.Bandwidth = EstimateBandwidth(support)
	}
	return d
}

// Data returns the sample points (upstream's dist.data()).
func (d *KernelDensity) Data() []jsval.Value { return d.support }

func (d *KernelDensity) PDF(x float64) float64 {
	y := 0.0
	for _, s := range d.nums {
		y += DensityNormal((x-s)/d.Bandwidth, 0, 1)
	}
	return y / d.Bandwidth / float64(len(d.nums))
}
func (d *KernelDensity) CDF(x float64) float64 {
	y := 0.0
	for _, s := range d.nums {
		y += CumulativeNormal((x-s)/d.Bandwidth, 0, 1)
	}
	return y / float64(len(d.nums))
}
func (d *KernelDensity) ICDF(float64) (float64, error) { return math.NaN(), ErrNoICDF }
func (d *KernelDensity) Sample(r Rand) float64 {
	n := len(d.nums)
	if n == 0 {
		return math.NaN()
	}
	i := int(r() * float64(n))
	return d.nums[i] + float64(d.Bandwidth*d.kernel.sample(r, 0, 1))
}

// EstimateBandwidth is Scott's rule of thumb (1992, Multivariate Density
// Estimation) as vega-statistics' estimateBandwidth computes it, over raw
// values: 1.06 * min(stdev, IQR/1.34) * n^-0.2, falling back to the stdev, then
// |q1|, then 1 when the preferred scale is zero or undefined.
func EstimateBandwidth(values []jsval.Value) float64 {
	n := len(values)
	d := bandwidthDeviation(values)
	q := Quartiles(values, func(v jsval.Value) jsval.Value { return v })
	h := (q[2] - q[0]) / 1.34
	v := math.Min(d, h)
	if v == 0 || math.IsNaN(v) {
		v = d
		if v == 0 || math.IsNaN(v) {
			v = math.Abs(q[0])
			if v == 0 || math.IsNaN(v) {
				v = 1
			}
		}
	}
	return 1.06 * v * jsmath.Pow(float64(n), -0.2)
}

// bandwidthDeviation is d3.deviation: the sample standard deviation of the
// values that are not null and coerce to a number; NaN (upstream undefined)
// when fewer than two, and 0 stays 0.
func bandwidthDeviation(values []jsval.Value) float64 {
	count := 0
	mean, sum := 0.0, 0.0
	for _, raw := range values {
		if raw.IsNullish() {
			continue
		}
		v := jsval.ToNumber(raw)
		if !(v >= v) {
			continue
		}
		delta := v - mean
		count++
		mean += delta / float64(count)
		sum += float64(delta * (v - mean))
	}
	if count <= 1 {
		return math.NaN()
	}
	variance := sum / float64(count-1)
	if variance == 0 {
		return 0
	}
	return math.Sqrt(variance)
}

// SampleCurve adaptively samples f over extent: minSteps points on a uniform
// grid (0 means 25), refined by subdividing wherever the curve bends by more
// than half a degree, up to maxSteps (0 means 200, never below minSteps). When
// minSteps equals maxSteps the grid is used as is. The number of steps is
// bounded by MaxSteps.
func SampleCurve(f func(float64) float64, extent [2]float64, minSteps, maxSteps float64) ([][2]float64, error) {
	if minSteps == 0 || math.IsNaN(minSteps) {
		minSteps = 25
	}
	if maxSteps == 0 || math.IsNaN(maxSteps) {
		maxSteps = 200
	}
	maxSteps = math.Max(minSteps, maxSteps)
	if maxSteps > MaxSteps {
		return nil, limitErr("steps", int(min(maxSteps, math.MaxInt32)), MaxSteps)
	}
	point := func(x float64) [2]float64 { return [2]float64{x, f(x)} }
	minX, maxX := extent[0], extent[1]
	span := maxX - minX
	stop := span / maxSteps
	prev := [][2]float64{point(minX)}
	var next [][2]float64

	if minSteps == maxSteps {
		// no adaptation: sample the uniform grid directly
		for i := 1.0; i < maxSteps; i++ {
			prev = append(prev, point(minX+float64((i/minSteps)*span)))
		}
		return append(prev, point(maxX)), nil
	}
	// sample the minimum points on a uniform grid (stacked so the leftmost is
	// on top), then refine adaptively
	next = append(next, point(maxX))
	for i := minSteps - 1; i > 0; i-- {
		next = append(next, point(minX+float64((i/minSteps)*span)))
	}

	p0 := prev[0]
	sx := 1 / span
	sy := curveScaleY(p0[1], next)
	budget := 8 * MaxSteps
	for len(next) > 0 {
		if budget--; budget < 0 {
			return nil, limitErr("curve samples", 8*MaxSteps, 8*MaxSteps)
		}
		p1 := next[len(next)-1]
		// midpoint for potential curve subdivision
		pm := point((p0[0] + p1[0]) / 2)
		dx := pm[0]-p0[0] >= stop
		if dx && curveAngleDelta(p0, pm, p1, sx, sy) > minRadians {
			// maximum resolution not met and the midpoint differs enough from
			// the endpoint: visit the midpoint next
			next = append(next, pm)
		} else {
			// midpoint close enough: keep the endpoint
			p0 = p1
			prev = append(prev, p1)
			next = next[:len(next)-1]
		}
	}
	return prev, nil
}

// minRadians is 0.5 degrees, the subdivision accuracy.
const minRadians = 0.5 * math.Pi / 180

func curveScaleY(init float64, points [][2]float64) float64 {
	ymin, ymax := init, init
	for _, p := range points {
		if p[1] < ymin {
			ymin = p[1]
		}
		if p[1] > ymax {
			ymax = p[1]
		}
	}
	return 1 / (ymax - ymin)
}

func curveAngleDelta(p, q, r [2]float64, sx, sy float64) float64 {
	a0 := jsmath.Atan2(sy*(r[1]-p[1]), sx*(r[0]-p[0]))
	a1 := jsmath.Atan2(sy*(q[1]-p[1]), sx*(q[0]-p[0]))
	return math.Abs(a0 - a1)
}
