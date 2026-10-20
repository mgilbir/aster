package contour

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/budget"
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
	// TranslateText holds the text of a translate entry that is a non-empty
	// string (TranslateIsText marks which): `coordinate * s + t` then
	// concatenates, and the coordinate is that text.
	// ValuesNullish is "undefined" or "null" when the grid object has no
	// values array (nullish): the contour reads values[0] of it.
	ValuesNullish   string
	TranslateText   [2]string
	TranslateIsText [2]bool
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
		return Grid{}, errInvalidSize
	}
	k := 2
	if p.CellSize != 0 {
		if !(p.CellSize >= 1) {
			return Grid{}, errors.New("invalid cell size")
		}
		kf := math.Floor(jsmath.Log(p.CellSize) / math.Ln2)
		if kf > 30 {
			return Grid{}, fmt.Errorf("contour: cell size too large: %w", budget.ErrLimit)
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
		return Grid{}, errors.New("invalid bandwidth")
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
	// The two float32 rasters are scratch; the float64 values are the grid,
	// which the caller gives back (releaseGrid) when it drops it.
	if err := checkCtx(ctx); err != nil {
		return Grid{}, err
	}
	led := budget.LedgerFrom(ctx)
	scratch := 2 * 4 * n * m
	if err := led.Alloc(scratch + gridValueBytes*n*m); err != nil {
		return Grid{}, err
	}
	defer led.Free(scratch)
	values0 := make([]float32, nn*mm)
	values1 := make([]float32, nn*mm)
	values := values0
	meter := budget.NewMeter(ctx)

	for _, d := range data {
		if err := meter.Work(1); err != nil {
			led.Free(gridValueBytes * n * m)
			return Grid{}, err
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

	// Each pass is a sweep of the raster, a line at a time, each line
	// charged to the meter as it is swept (ADR 0010).
	var err error
	pass := func(blur func(n, m int, source, target []float32, r int, meter *budget.Meter) error, src, dst []float32, r int) {
		if err == nil {
			err = blur(nn, mm, src, dst, r, &meter)
		}
	}
	switch {
	case rx > 0 && ry > 0:
		pass(blurX, values0, values1, rx)
		pass(blurY, values1, values0, ry)
		pass(blurX, values0, values1, rx)
		pass(blurY, values1, values0, ry)
		pass(blurX, values0, values1, rx)
		pass(blurY, values1, values0, ry)
	case rx > 0:
		pass(blurX, values0, values1, rx)
		pass(blurX, values1, values0, rx)
		pass(blurX, values0, values1, rx)
		values = values1
	case ry > 0:
		pass(blurY, values0, values1, ry)
		pass(blurY, values1, values0, ry)
		pass(blurY, values0, values1, ry)
		values = values1
	}
	if err != nil {
		led.Free(gridValueBytes * n * m)
		return Grid{}, err
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

func blurX(n, m int, source, target []float32, r int, meter *budget.Meter) error {
	w := r<<1 + 1
	for j := 0; j < m; j++ {
		if err := meter.Work(int64(n + r)); err != nil {
			return err
		}
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
	return nil
}

func blurY(n, m int, source, target []float32, r int, meter *budget.Meter) error {
	w := r<<1 + 1
	for i := 0; i < n; i++ {
		if err := meter.Work(int64(m + r)); err != nil {
			return err
		}
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
	return nil
}
