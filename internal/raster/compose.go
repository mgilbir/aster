package raster

import (
	"math"

	"github.com/mgilbir/forme/shape"
)

// separable maps COLR's separable blend modes to the CSS ones.
var separable = map[shape.CompositeMode]blendMode{
	shape.CompositeScreen:     blendScreen,
	shape.CompositeOverlay:    blendOverlay,
	shape.CompositeDarken:     blendDarken,
	shape.CompositeLighten:    blendLighten,
	shape.CompositeColorDodge: blendColorDodge,
	shape.CompositeColorBurn:  blendColorBurn,
	shape.CompositeHardLight:  blendHardLight,
	shape.CompositeSoftLight:  blendSoftLight,
	shape.CompositeDifference: blendDifference,
	shape.CompositeExclusion:  blendExclusion,
	shape.CompositeMultiply:   blendMultiply,
}

// compositeMode combines layer into dst over region r in one of COLR's
// composite modes (the W3C compositing spec's Porter-Duff operators and blend
// modes), the result weighted by the clip mask m where it is not nil. Unlike
// compositeLayer, it touches pixels the layer leaves transparent, as an
// operator such as SrcIn clears what it does not cover.
func compositeMode(dst, layer *canvas, r irect, m *mask, mode shape.CompositeMode) {
	r = r.intersect(dst.bounds()).intersect(layer.bounds())
	if m != nil {
		r = r.intersect(m.r)
	}
	if r.empty() {
		return
	}
	dst.markDirty(r)
	for y := r.y0; y < r.y1; y++ {
		for x := r.x0; x < r.x1; x++ {
			cov := 1.0
			if m != nil {
				c := m.at(x, y)
				if c == 0 {
					continue
				}
				cov = float64(c) / 255
			}
			i := (y*dst.w + x) * 4
			d := dst.pix[i : i+4 : i+4]
			s := layer.pix[i : i+4 : i+4]
			if mode == shape.CompositeSrcOver && cov == 1 {
				if s[3] != 0 {
					ia := 255 - uint32(s[3])
					for k := range 4 {
						d[k] = uint8(uint32(s[k]) + mul255(uint32(d[k]), ia))
					}
				}
				continue
			}
			var sf, df [4]float64
			for k := range 4 {
				sf[k], df[k] = float64(s[k])/255, float64(d[k])/255
			}
			out := composite(sf, df, mode)
			for k := range 4 {
				d[k] = clampByte((df[k] + (out[k]-df[k])*cov) * 255)
			}
		}
	}
}

// composite is one premultiplied pixel s composited onto d.
func composite(s, d [4]float64, mode shape.CompositeMode) [4]float64 {
	as, ab := s[3], d[3]
	var fa, fb float64
	switch mode {
	case shape.CompositeClear:
		return [4]float64{}
	case shape.CompositeSrc:
		return s
	case shape.CompositeDest:
		return d
	case shape.CompositeSrcOver:
		fa, fb = 1, 1-as
	case shape.CompositeDestOver:
		fa, fb = 1-ab, 1
	case shape.CompositeSrcIn:
		fa, fb = ab, 0
	case shape.CompositeDestIn:
		fa, fb = 0, as
	case shape.CompositeSrcOut:
		fa, fb = 1-ab, 0
	case shape.CompositeDestOut:
		fa, fb = 0, 1-as
	case shape.CompositeSrcAtop:
		fa, fb = ab, 1-as
	case shape.CompositeDestAtop:
		fa, fb = 1-ab, as
	case shape.CompositeXor:
		fa, fb = 1-ab, 1-as
	case shape.CompositePlus:
		var out [4]float64
		for k := range 4 {
			out[k] = math.Min(1, s[k]+d[k])
		}
		return out
	default:
		return blend(s, d, mode)
	}
	var out [4]float64
	for k := range 4 {
		out[k] = fa*s[k] + fb*d[k]
	}
	return out
}

// blend is s blended onto d in a separable or non-separable blend mode, then
// composited source-over: co = cs(1-ab) + cb(1-as) + as ab B(Cb, Cs).
func blend(s, d [4]float64, mode shape.CompositeMode) [4]float64 {
	as, ab := s[3], d[3]
	var cs, cb [3]float64
	for k := range 3 {
		if as > 0 {
			cs[k] = s[k] / as
		}
		if ab > 0 {
			cb[k] = d[k] / ab
		}
	}
	var b [3]float64
	switch mode {
	case shape.CompositeHSLHue:
		b = setLum(setSat(cs, sat(cb)), lum(cb))
	case shape.CompositeHSLSaturation:
		b = setLum(setSat(cb, sat(cs)), lum(cb))
	case shape.CompositeHSLColor:
		b = setLum(cs, lum(cb))
	case shape.CompositeHSLLuminosity:
		b = setLum(cb, lum(cs))
	default:
		bm, ok := separable[mode]
		if !ok {
			bm = blendNormal
		}
		for k := range 3 {
			b[k] = blendChannel(bm, cb[k], cs[k])
		}
	}
	var out [4]float64
	for k := range 3 {
		out[k] = s[k]*(1-ab) + d[k]*(1-as) + as*ab*b[k]
	}
	out[3] = as + ab - as*ab
	return out
}

// The non-separable blend modes' helpers, as the W3C compositing spec
// defines them.

func lum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

func clipColor(c [3]float64) [3]float64 {
	l := lum(c)
	n := math.Min(c[0], math.Min(c[1], c[2]))
	x := math.Max(c[0], math.Max(c[1], c[2]))
	for k := range 3 {
		if n < 0 && l-n != 0 {
			c[k] = l + (c[k]-l)*l/(l-n)
		}
		if x > 1 && x-l != 0 {
			c[k] = l + (c[k]-l)*(1-l)/(x-l)
		}
	}
	return c
}

func setLum(c [3]float64, l float64) [3]float64 {
	d := l - lum(c)
	return clipColor([3]float64{c[0] + d, c[1] + d, c[2] + d})
}

func sat(c [3]float64) float64 {
	return math.Max(c[0], math.Max(c[1], c[2])) - math.Min(c[0], math.Min(c[1], c[2]))
}

func setSat(c [3]float64, s float64) [3]float64 {
	// Order the channels: lo, mid, hi.
	lo, mid, hi := 0, 1, 2
	if c[lo] > c[mid] {
		lo, mid = mid, lo
	}
	if c[mid] > c[hi] {
		mid, hi = hi, mid
	}
	if c[lo] > c[mid] {
		lo, mid = mid, lo
	}
	var out [3]float64
	if c[hi] > c[lo] {
		out[mid] = (c[mid] - c[lo]) * s / (c[hi] - c[lo])
		out[hi] = s
	}
	return out
}
