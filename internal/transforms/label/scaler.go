package label

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// scaler maps layout coordinates to bitmap cells. Large layouts are
// downsampled so the bitmap stays near one million cells.
type scaler struct {
	width, height float64
	padding       float64
	ratio         float64
	w, h          int        // bitmap dimensions
	text          [2]*string // a size given as text (Options.SizeText)

	// The layout's bitmaps, masks and flood-fill stacks are charged to the
	// call's ledger before they are made, and given back (release) when the
	// layout is done.
	ledger  *budget.Ledger
	charged int64
}

// alloc charges n bytes the layout is about to allocate.
func (s *scaler) alloc(n int64) error {
	if err := s.ledger.Alloc(n); err != nil {
		return err
	}
	s.charged += n
	return nil
}

// release gives back what the layout charged.
func (s *scaler) release() {
	s.ledger.Free(s.charged)
	s.charged = 0
}

func newScaler(ctx context.Context, width, height, padding float64, text [2]*string) (*scaler, error) {
	ratio := math.Max(1, math.Sqrt(width*height/1e6))
	w := toInt32(sizeSum(width, text[0], padding, ratio) / ratio)
	h := toInt32(sizeSum(height, text[1], padding, ratio) / ratio)
	if n := toInt32(float64(w*h+32) / 32); n < 0 {
		// new Uint32Array of a negative length
		return nil, fmt.Errorf("RangeError: Invalid typed array length: %d", n)
	}
	if budget.MulInt(w, h) > maxBitmapBits {
		return nil, fmt.Errorf("%w: label: layout bitmap %dx%d is too large (check size and padding)", budget.ErrLimit, w, h)
	}
	return &scaler{width: width, height: height, padding: padding, ratio: ratio, w: w, h: h, text: text, ledger: budget.LedgerFrom(ctx)}, nil
}

func (s *scaler) scale(v float64) int { return toInt32((v + s.padding) / s.ratio) }

// scaleJS is scale of a coordinate that can be text: `v + padding`
// concatenates then.
func (s *scaler) scaleJS(v jsv) int {
	if v.str {
		return toInt32(jsval.StringToNumber(v.s+jsval.JSNumberString(s.padding)) / s.ratio)
	}
	return s.scale(v.f)
}

func (s *scaler) invert(v int) float64 { return float64(float64(v)*s.ratio) - s.padding }

// bitmap is a new layout bitmap, charged.
func (s *scaler) bitmap() (*bitmap, error) {
	if err := s.alloc(int64((s.w*s.h+32)/32) * 4); err != nil {
		return nil, err
	}
	return newBitmap(s.w, s.h), nil
}

// sizeSum is `size + 2 * padding + ratio`: a size that is text makes the sum
// a concatenation, which the division then reads as a number.
func sizeSum(size float64, text *string, padding, ratio float64) float64 {
	if text == nil {
		return size + 2*padding + ratio
	}
	return jsval.StringToNumber(*text + jsval.JSNumberString(2*padding) + jsval.JSNumberString(ratio))
}

// toInt32 is JavaScript's `~~x`.
func toInt32(f float64) int { return int(jsmath.ToInt32(f)) }

// markBitmaps rasterizes the base and avoided marks and folds them into the
// interior layer (marks to keep clear of) and, when labels may sit inside
// their mark or on an area, the border layer.
func markBitmaps(ctx context.Context, sc *scaler, r Rasterizer, baseMark []any, avoidMarks [][]any, labelInside, isGroupArea bool) ([2]*bitmap, error) {
	// canvas(width, height) is node-canvas's: a side past 32767 (a negative
	// one is read as unsigned) is refused by the constructor, and getImageData
	// refuses a side that truncates to less than one pixel (NaN, infinity,
	// zero, a fraction)
	for i, name := range [2]string{"width", "height"} {
		if v := [2]float64{sc.width, sc.height}[i]; v < 0 || v > 32767 {
			return [2]*bitmap{}, &jsval.Thrown{Name: "Error", Msg: "Canvas " + name + " cannot exceed 32767"}
		}
	}
	for i, name := range [2]string{"width", "height"} {
		if v := [2]float64{sc.width, sc.height}[i]; !(v >= 1) || math.IsInf(v, 0) {
			return [2]*bitmap{}, &jsval.Thrown{Name: "IndexSizeError", Msg: "The source " + name + " is 0."}
		}
	}
	// Each side within the limit before it is an int, and their product
	// saturating, where int is 32 bits.
	width, height := int(min(sc.width, maxMaskPixels+1)), int(min(sc.height, maxMaskPixels+1))
	if budget.MulInt(width, height) > maxMaskPixels || width > maxMaskPixels || height > maxMaskPixels {
		return [2]*bitmap{}, fmt.Errorf("%w: label: layout %dx%d is too large to rasterize", budget.ErrLimit, width, height)
	}
	// canvas(width, height) of a size that is no number has the size 0
	// (node-canvas reads numbers only), which getImageData refuses
	for i, name := range [2]string{"width", "height"} {
		if sc.text[i] != nil {
			return [2]*bitmap{}, &jsval.Thrown{Name: "TypeError", Msg: "Canvas " + name + " is 0"}
		}
	}
	border := labelInside || isGroupArea
	// The canvas masks are as large as the layout, one bit a pixel.
	masks := int64(2)
	if border {
		masks++
	}
	if err := sc.alloc(masks * int64(maskWords(width, height)) * 8); err != nil {
		return [2]*bitmap{}, err
	}
	avoid, base := newMask(width, height), newMask(width, height)
	var stroke *Mask
	if border {
		stroke = newMask(width, height)
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
	layer1, err := sc.bitmap()
	if err != nil {
		return [2]*bitmap{}, err
	}
	var layer2 *bitmap
	if border {
		if layer2, err = sc.bitmap(); err != nil {
			return [2]*bitmap{}, err
		}
	}
	for y := 0; y < height; y++ {
		if y&255 == 0 {
			if err := ctx.Err(); err != nil {
				return [2]*bitmap{}, err
			}
		}
		for x := 0; x < width; x++ {
			alpha, baseAlpha := avoid.Get(x, y), base.Get(x, y)
			strokeAlpha := border && stroke.Get(x, y)
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
