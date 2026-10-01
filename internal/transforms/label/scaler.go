package label

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
)

// scaler maps layout coordinates to bitmap cells. Large layouts are
// downsampled so the bitmap stays near one million cells.
type scaler struct {
	width, height float64
	padding       float64
	ratio         float64
	w, h          int // bitmap dimensions
}

func newScaler(width, height, padding float64) (*scaler, error) {
	ratio := math.Max(1, math.Sqrt(width*height/1e6))
	w := toInt32((width + 2*padding + ratio) / ratio)
	h := toInt32((height + 2*padding + ratio) / ratio)
	if w < 0 || h < 0 {
		// new Uint32Array of a negative length
		return nil, fmt.Errorf("RangeError: Invalid typed array length: %d", (w*h+31)>>5)
	}
	if w*h > maxBitmapBits {
		return nil, fmt.Errorf("%w: label: layout bitmap %dx%d is too large (check size and padding)", budget.ErrLimit, w, h)
	}
	return &scaler{width: width, height: height, padding: padding, ratio: ratio, w: w, h: h}, nil
}

func (s *scaler) scale(v float64) int  { return toInt32((v + s.padding) / s.ratio) }
func (s *scaler) invert(v int) float64 { return float64(float64(v)*s.ratio) - s.padding }
func (s *scaler) bitmap() *bitmap      { return newBitmap(s.w, s.h) }

// toInt32 is JavaScript's `~~x`: truncation with 32-bit wraparound, NaN and
// infinities becoming 0.
func toInt32(f float64) int {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	f = math.Trunc(f)
	if math.Abs(f) < 1<<31 {
		return int(f)
	}
	f = math.Mod(f, 1<<32)
	if f < 0 {
		f += 1 << 32
	}
	if f >= 1<<31 {
		f -= 1 << 32
	}
	return int(f)
}

// markBitmaps rasterizes the base and avoided marks and folds them into the
// interior layer (marks to keep clear of) and, when labels may sit inside
// their mark or on an area, the border layer.
func markBitmaps(ctx context.Context, sc *scaler, r Rasterizer, baseMark []any, avoidMarks [][]any, labelInside, isGroupArea bool) ([2]*bitmap, error) {
	width, height := int(sc.width), int(sc.height)
	if width*height > maxMaskPixels {
		return [2]*bitmap{}, fmt.Errorf("%w: label: layout %dx%d is too large to rasterize", budget.ErrLimit, width, height)
	}
	border := labelInside || isGroupArea
	newMask := func() *Mask { return &Mask{Width: width, Height: height, Pix: make([]bool, width*height)} }
	avoid, base := newMask(), newMask()
	var stroke *Mask
	if border {
		stroke = newMask()
	}
	if r != nil {
		for _, items := range avoidMarks {
			if len(items) > 0 {
				r.Draw(avoid, items, false)
			}
		}
		if len(baseMark) > 0 {
			r.Draw(base, baseMark, false)
			if border {
				r.Draw(stroke, baseMark, true)
			}
		}
	}
	layer1 := sc.bitmap()
	var layer2 *bitmap
	if border {
		layer2 = sc.bitmap()
	}
	for y := 0; y < height; y++ {
		if y&255 == 0 {
			if err := ctx.Err(); err != nil {
				return [2]*bitmap{}, err
			}
		}
		for x := 0; x < width; x++ {
			i := y*width + x
			alpha, baseAlpha := avoid.Pix[i], base.Pix[i]
			strokeAlpha := border && stroke.Pix[i]
			if !(alpha || strokeAlpha || baseAlpha) {
				continue
			}
			u, v := sc.scale(float64(x)), sc.scale(float64(y))
			if !isGroupArea && (alpha || baseAlpha) {
				layer1.set(u, v)
			}
			if border && (alpha || strokeAlpha) {
				layer2.set(u, v)
			}
		}
	}
	return [2]*bitmap{layer1, layer2}, nil
}
