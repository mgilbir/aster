package pngopt

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"

	"github.com/mgilbir/aster/internal/budget"
)

// Recode losslessly re-encodes a PNG into its cheapest equivalent color
// format: 8-bit indexed when the image has at most 256 distinct colors,
// 24-bit truecolor when it is fully opaque, unchanged otherwise. The 8-bit
// pixels it decodes to are kept exactly; only the storage format changes (a
// 16-bit PNG is reduced to 8 bits a channel).
//
// The rasterizer emits 8-bit RGB or RGBA; an image with an alpha channel
// that is fully opaque for charts drawn on a solid background, or of few
// colours, still costs consumers that decode and re-compress the pixel
// stream (PDF embedders such as dvipdfmx, office formats) per decoded byte,
// so indexed output cuts their work roughly fourfold and avoids a separate
// alpha (soft mask) stream.
//
// On any decode failure the input is returned unchanged. It runs without a
// context; the engine calls RecodeImage.
func Recode(data []byte) []byte {
	img, ok := decodeNRGBA(data)
	if !ok {
		return data
	}
	out, err := RecodeImage(context.Background(), img, data)
	if err != nil {
		return data
	}
	return out
}

// RecodeImage is Recode for nrgba, the image data encodes, as the renderer
// holds it, under ctx, which it polls.
func RecodeImage(ctx context.Context, nrgba *image.NRGBA, data []byte) ([]byte, error) {
	bounds := nrgba.Bounds()
	// One scan collects both facts that pick the target format: full opacity
	// and the distinct-color count (abandoned past 256).
	const maxPaletteSize = 256
	opaque := true
	colorIndex := make(map[[4]uint8]uint8, maxPaletteSize)
	palette := make([]color.Color, 0, maxPaletteSize)
	paletteOK := true
	scan := budget.NewMeter(ctx)
	for i := 0; i < len(nrgba.Pix); i += 4 {
		if err := scan.Work(1); err != nil {
			return nil, err
		}
		var key [4]uint8
		copy(key[:], nrgba.Pix[i:i+4])
		if key[3] != 0xff {
			opaque = false
		}
		if !paletteOK {
			if !opaque {
				break
			}
			continue
		}
		if _, seen := colorIndex[key]; !seen {
			if len(palette) == maxPaletteSize {
				paletteOK = false
				continue
			}
			colorIndex[key] = uint8(len(palette))
			palette = append(palette, color.NRGBA{R: key[0], G: key[1], B: key[2], A: key[3]})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Sized to the input once: growing by doubling copies the output whole,
	// each copy a stretch without a poll as long as the output is large.
	var out bytes.Buffer
	out.Grow(len(data))
	switch {
	case paletteOK:
		l := budget.LedgerFrom(ctx)
		n := int64(len(nrgba.Pix) / 4)
		if err := l.Alloc(n); err != nil {
			return nil, fmt.Errorf("pngopt: recoding: %w", err)
		}
		defer l.Free(n)
		paletted := image.NewPaletted(bounds, palette)
		m := budget.NewMeter(ctx)
		for i, j := 0, 0; i < len(nrgba.Pix); i, j = i+4, j+1 {
			if err := m.Work(1); err != nil {
				return nil, err
			}
			var key [4]uint8
			copy(key[:], nrgba.Pix[i:i+4])
			paletted.Pix[j] = colorIndex[key]
		}
		if err := EncodePaletted(ctx, &out, paletted); err != nil {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			return data, nil
		}
	case opaque:
		// Fully opaque: 24-bit truecolor (no alpha channel), as image/png
		// writes it.
		if err := Encode(ctx, &out, nrgba); err != nil {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			return data, nil
		}
	default:
		return data, nil
	}
	return out.Bytes(), nil
}
