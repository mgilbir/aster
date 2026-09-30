package contour

import (
	"context"
	"errors"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
)

// Accessor reads a value from a tuple.
type Accessor = func(jsval.Value) jsval.Value

// Grid is a raster of values in row-major order, the field KDE2D produces and
// Isocontour/Heatmap consume. X1..Y2 delimit the region of interest inside the
// padded raster (zero X2/Y2 mean "the full width/height", as upstream's
// `x2 || n`). Scale is the factor mapping raster cells to output pixels; zero
// means unset.
type Grid struct {
	Values         []float64
	Scale          float64
	Width, Height  int
	X1, Y1, X2, Y2 float64
	Translate      []float64
	// ScaleXY is a data grid's scale given as an array, [sx, sy] (missing or
	// non-numeric entries are NaN, which the transform reads as 1).
	ScaleXY []float64
}

// DensityParams configures 2-D kernel density estimation.
type DensityParams struct {
	// X, Y and Weight read pixel coordinates and weights; nil defaults are
	// d[0], d[1] and 1.
	X, Y, Weight Accessor
	// Size is the output extent [width, height] in pixels; nil means the
	// upstream default of 960 by 500.
	Size []float64
	// CellSize is the raster resolution in pixels, rounded down to a power
	// of two; zero means the default 4.
	CellSize float64
	// Bandwidth holds one or two kernel bandwidths in pixels; nil or a
	// negative entry selects Scott's normal-reference rule.
	Bandwidth []float64
}

// Density estimates a 2-D density raster: points are binned into cells, then
// blurred by three box-filter passes (an approximate Gaussian). With counts the
// result is smoothed counts per square pixel, otherwise a probability density.
func Density(ctx context.Context, data []jsval.Value, p DensityParams, counts bool) (Grid, error) {
	x, y, weight := p.X, p.Y, p.Weight
	if x == nil {
		x = func(d jsval.Value) jsval.Value { return d.Index(0) }
	}
	if y == nil {
		y = func(d jsval.Value) jsval.Value { return d.Index(1) }
	}
	dx, dy := 960.0, 500.0
	if p.Size != nil {
		if len(p.Size) != 2 {
			return Grid{}, errors.New("contour: size must have two entries")
		}
		dx, dy = p.Size[0], p.Size[1]
	}
	if !(dx >= 0 && dy >= 0) {
		return Grid{}, errors.New("contour: invalid size")
	}
	k := 2
	if p.CellSize != 0 {
		if !(p.CellSize >= 1) {
			return Grid{}, errors.New("contour: invalid cell size")
		}
		kf := math.Floor(jsmath.Log(p.CellSize) / math.Ln2)
		if kf > 30 {
			return Grid{}, errors.New("contour: cell size too large")
		}
		k = int(kf)
	}
	bw := [2]float64{-1, -1}
	switch len(p.Bandwidth) {
	case 0:
	case 1:
		bw = [2]float64{p.Bandwidth[0], p.Bandwidth[0]}
	case 2:
		bw = [2]float64{p.Bandwidth[0], p.Bandwidth[1]}
	default:
		return Grid{}, errors.New("contour: invalid bandwidth")
	}

	rx := int(toInt32(radius(bw[0], data, x))) >> k // blur x-radius
	ry := int(toInt32(radius(bw[1], data, y))) >> k // blur y-radius
	ox, oy := 0, 0                                  // padding so the blur does not clip
	if rx != 0 {
		ox = rx + 2
	}
	if ry != 0 {
		oy = ry + 2
	}
	gw, gh := int(toInt32(dx))>>k, int(toInt32(dy))>>k
	n := int64(2*ox + gw)
	m := int64(2*oy + gh)
	if n < 0 || m < 0 || n*m > MaxGridCells {
		return Grid{}, errTooLarge
	}
	nn, mm := int(n), int(m)
	values0 := make([]float32, nn*mm)
	values1 := make([]float32, nn*mm)
	values := values0

	for i, d := range data {
		if i&0xfff == 0 {
			if err := checkCtx(ctx); err != nil {
				return Grid{}, err
			}
		}
		xi := ox + int(toInt32(jsval.ToNumber(x(d))))>>k
		yi := oy + int(toInt32(jsval.ToNumber(y(d))))>>k
		if xi >= 0 && xi < nn && yi >= 0 && yi < mm {
			w := 1.0
			if weight != nil {
				w = jsval.ToNumber(weight(d))
			}
			values0[xi+yi*nn] = float32(float64(values0[xi+yi*nn]) + w)
		}
	}

	switch {
	case rx > 0 && ry > 0:
		blurX(nn, mm, values0, values1, rx)
		blurY(nn, mm, values1, values0, ry)
		blurX(nn, mm, values0, values1, rx)
		blurY(nn, mm, values1, values0, ry)
		blurX(nn, mm, values0, values1, rx)
		blurY(nn, mm, values1, values0, ry)
	case rx > 0:
		blurX(nn, mm, values0, values1, rx)
		blurX(nn, mm, values1, values0, rx)
		blurX(nn, mm, values0, values1, rx)
		values = values1
	case ry > 0:
		blurY(nn, mm, values0, values1, ry)
		blurY(nn, mm, values1, values0, ry)
		blurY(nn, mm, values0, values1, ry)
		values = values1
	}

	// Scale to points per square pixel (counts) or to a probability density.
	var s float64
	if counts {
		s = jsmath.Pow(2, float64(-2*k))
	} else {
		var sum float64
		for _, v := range values {
			if f := float64(v); f != 0 && !math.IsNaN(f) {
				sum += f
			}
		}
		s = 1 / sum
	}
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = float64(float32(float64(v) * s))
	}
	return Grid{
		Values: out,
		Scale:  float64(int(1) << k),
		Width:  nn, Height: mm,
		X1: float64(ox), Y1: float64(oy),
		X2: float64(ox + gw), Y2: float64(oy + gh),
	}, nil
}

// radius converts a bandwidth into the integer box-blur radius whose three-pass
// variance matches it.
func radius(bw float64, data []jsval.Value, f Accessor) float64 {
	v := bw
	if !(bw >= 0) {
		v = bandwidthNRD(data, f)
	}
	return jsRound((math.Sqrt(float64(4*v*v)+1) - 1) / 2)
}

// bandwidthNRD is Scott's normal reference rule (vega-statistics bandwidth).
// Deviation and quartiles filter their inputs differently, as upstream does:
// quartiles also drop empty strings.
func bandwidthNRD(data []jsval.Value, f Accessor) float64 {
	n := float64(len(data))
	var count int
	var mean, sum float64
	nums := make([]float64, 0, len(data))
	for _, d := range data {
		v := f(d)
		if v.IsNullish() {
			continue
		}
		x := jsval.ToNumber(v)
		if math.IsNaN(x) {
			continue
		}
		count++
		delta := x - mean
		mean += delta / float64(count)
		sum += float64(delta * (x - mean))
		if !(v.IsStr() && v.StrValue() == "") {
			nums = append(nums, x)
		}
	}
	dev := math.NaN() // undefined
	if count > 1 {
		if variance := sum / float64(count-1); variance != 0 {
			dev = math.Sqrt(variance)
		} else {
			dev = variance
		}
	}
	sort.Float64s(nums)
	q0, q2 := quantileSorted(nums, 0.25), quantileSorted(nums, 0.75)
	h := (q2 - q0) / 1.34
	// Upstream: Math.min(d, h) || d || Math.abs(q[0]) || 1, where every
	// falsy value (NaN, undefined, 0) falls through.
	v := math.Min(dev, h)
	if v == 0 || math.IsNaN(v) {
		v = dev
	}
	if v == 0 || math.IsNaN(v) {
		v = math.Abs(q0)
	}
	if v == 0 || math.IsNaN(v) {
		v = 1
	}
	return 1.06 * v * jsmath.Pow(n, -0.2)
}

func quantileSorted(v []float64, p float64) float64 {
	n := len(v)
	if n == 0 {
		return math.NaN()
	}
	if p <= 0 || n < 2 {
		return v[0]
	}
	if p >= 1 {
		return v[n-1]
	}
	i := float64(float64(n-1) * p)
	i0 := int(math.Floor(i))
	return v[i0] + float64((v[i0+1]-v[i0])*(i-float64(i0)))
}

func blurX(n, m int, source, target []float32, r int) {
	w := r<<1 + 1
	for j := 0; j < m; j++ {
		row := j * n
		sr := 0.0
		for i := 0; i < n+r; i++ {
			if i < n {
				sr += float64(source[i+row])
			}
			if i >= r {
				if i >= w {
					sr -= float64(source[i-w+row])
				}
				target[i-r+row] = float32(sr / float64(min(i+1, n-1+w-i, w)))
			}
		}
	}
}

func blurY(n, m int, source, target []float32, r int) {
	w := r<<1 + 1
	for i := 0; i < n; i++ {
		sr := 0.0
		for j := 0; j < m+r; j++ {
			if j < m {
				sr += float64(source[i+j*n])
			}
			if j >= r {
				if j >= w {
					sr -= float64(source[i+(j-w)*n])
				}
				target[i+(j-r)*n] = float32(sr / float64(min(j+1, m-1+w-j, w)))
			}
		}
	}
}
