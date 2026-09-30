package contour

import (
	"context"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Image is a non-premultiplied RGBA raster (canvas ImageData layout), 4 bytes
// per pixel, row-major.
type Image struct {
	Width, Height int
	Pix           []uint8
	url           string // DataURL, once computed
}

// Pixel is the per-cell input to heatmap color and opacity functions: the
// `$x`, `$y`, `$value` and `$max` fields upstream exposes to expressions.
type Pixel struct {
	X, Y, Value, Max float64
}

// RGB holds color channels as d3-color produces them (0..255, possibly
// fractional or NaN); they are rounded and clamped like a Uint8ClampedArray.
type RGB struct{ R, G, B float64 }

// HeatmapParams are the parameters of the vega-geo heatmap transform.
type HeatmapParams struct {
	// Field reads the grid from a tuple; nil means the tuple itself.
	Field Accessor
	// Color is a constant color; the zero RGB{} is replaced by upstream's
	// default mid-grey (#888) only when ColorSet is false.
	Color    RGB
	ColorSet bool
	// ColorFn, if set, computes a color per pixel and takes precedence.
	ColorFn func(datum jsval.Value, px Pixel) (RGB, error)
	// Opacity is a constant opacity; zero (falsy upstream) selects the
	// default [0, max] gradient value/max. OpacityFn takes precedence.
	Opacity   float64
	OpacityFn func(datum jsval.Value, px Pixel) (float64, error)
	Shared    bool // resolve: "shared" (one global maximum)
}

// Heatmap renders each source tuple's grid to an RGBA image, one per tuple in
// input order. The caller decides where to store them (upstream writes a canvas
// into the tuple's `as` field).
func Heatmap(ctx context.Context, source []jsval.Value, p HeatmapParams) ([]*Image, error) {
	field := p.Field
	if field == nil {
		field = func(v jsval.Value) jsval.Value { return v }
	}
	grids := make([]Grid, len(source))
	hasValues := make([]bool, len(source))
	for i, t := range source {
		fv := field(t)
		g, err := GridFromValue(fv)
		if err != nil {
			return nil, err
		}
		grids[i] = g
		hasValues[i] = fv.Get("values").IsArr()
	}
	globalMax := math.NaN()
	if p.Shared {
		for _, g := range grids {
			if _, m := extent(g.Values); !math.IsNaN(m) && !(m <= globalMax) {
				globalMax = m
			}
		}
	}
	out := make([]*Image, len(source))
	for i, t := range source {
		max := globalMax
		if !p.Shared {
			_, max = extent(grids[i].Values)
		}
		img, err := p.render(ctx, grids[i], hasValues[i], t, max)
		if err != nil {
			return nil, err
		}
		out[i] = img
	}
	return out, nil
}

func (p *HeatmapParams) pixelColor(datum jsval.Value, px Pixel) (RGB, error) {
	switch {
	case p.ColorFn != nil:
		return p.ColorFn(datum, px)
	case p.ColorSet:
		return p.Color, nil
	}
	return RGB{136, 136, 136}, nil
}

func (p *HeatmapParams) pixelOpacity(datum jsval.Value, px Pixel) (float64, error) {
	switch {
	case p.OpacityFn != nil:
		return p.OpacityFn(datum, px)
	case p.Opacity != 0 && !math.IsNaN(p.Opacity):
		return p.Opacity, nil
	}
	o := px.Value / px.Max
	if o == 0 || math.IsNaN(o) {
		return 0, nil
	}
	return o, nil
}

func (p *HeatmapParams) render(ctx context.Context, g Grid, hasValues bool, datum jsval.Value, max float64) (*Image, error) {
	n := g.Width
	// The region comes from the data: bound it in floating point before any
	// integer conversion or allocation.
	fx2, fy2 := g.X2, g.Y2
	if fx2 == 0 {
		fx2 = float64(n)
	}
	if fy2 == 0 {
		fy2 = float64(g.Height)
	}
	fw, fh := math.Trunc(fx2)-math.Trunc(g.X1), math.Trunc(fy2)-math.Trunc(g.Y1)
	const far = 1 << 40
	if math.Abs(g.X1) > far || math.Abs(g.Y1) > far || math.Abs(fx2) > far || math.Abs(fy2) > far ||
		math.IsNaN(fw) || math.IsNaN(fh) || fw < 0 || fh < 0 || fw*fh > MaxGridCells {
		return nil, errTooLarge
	}
	x1, y1 := int(g.X1), int(g.Y1)
	x2, y2 := int(fx2), int(fy2)
	w, h := x2-x1, y2-y1
	if err := budget.From(ctx).Canvas(4 * int64(w) * int64(h)); err != nil {
		return nil, err
	}
	img := &Image{Width: w, Height: h, Pix: make([]uint8, 4*w*h)}
	k := 0
	for j := y1; j < y2; j++ {
		if err := checkCtx(ctx); err != nil {
			return nil, err
		}
		for i := x1; i < x2; i++ {
			px := Pixel{X: float64(i - x1), Y: float64(j - y1), Max: max}
			if hasValues {
				if idx := i + j*n; idx >= 0 && idx < len(g.Values) {
					px.Value = g.Values[idx]
				} else {
					px.Value = math.NaN()
				}
			}
			c, err := p.pixelColor(datum, px)
			if err != nil {
				return nil, err
			}
			o, err := p.pixelOpacity(datum, px)
			if err != nil {
				return nil, err
			}
			img.Pix[k] = clamp8(c.R)
			img.Pix[k+1] = clamp8(c.G)
			img.Pix[k+2] = clamp8(c.B)
			img.Pix[k+3] = clamp8(float64(toInt32(255 * o))) // ~~(255 * opacity)
			k += 4
		}
	}
	return img, nil
}

// clamp8 stores a double into a Uint8ClampedArray: NaN is 0, out-of-range
// values clamp, and ties round to even.
func clamp8(v float64) uint8 {
	switch {
	case !(v > 0):
		return 0
	case v >= 255:
		return 255
	}
	return uint8(math.RoundToEven(v))
}
