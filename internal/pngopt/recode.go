package pngopt

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"

	"github.com/mgilbir/aster/internal/budget"
)

// RecodeImage losslessly re-encodes nrgba, the image data encodes, as the
// renderer holds it, into its cheapest equivalent color format: 8-bit
// indexed when the image has at most 256 distinct colors (unless that is
// more than a tenth larger), 24-bit truecolor when it is fully opaque (data
// itself when it already is), unchanged otherwise. The pixels are kept
// exactly; only the storage format changes. It polls ctx.
//
// The rasterizer emits 8-bit RGB or RGBA; an image with an alpha channel
// that is fully opaque for charts drawn on a solid background, or of few
// colours, still costs consumers that decode and re-compress the pixel
// stream (PDF embedders such as dvipdfmx, office formats) per decoded byte,
// so indexed output cuts their work roughly fourfold and avoids a separate
// alpha (soft mask) stream.
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

	if !paletteOK && opaque && isRGB8(data) {
		// Already 24-bit truecolor: the cheapest form an opaque image of
		// more than 256 colours has, and the one Encode writes, so encoding
		// it again would give the same bytes (C158, audit 2026-10-18).
		return data, nil
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
		if out.Len() > len(data)+len(data)/10 {
			// As quantization's lossless path: indexed pixels are cheaper to
			// decode, but not at any size (a tiny image's palette outweighs
			// its pixels).
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

// isRGB8 reports whether data is a PNG of 8-bit RGB, not interlaced: what
// Encode writes for an opaque image.
func isRGB8(data []byte) bool {
	return len(data) > 28 && string(data[:8]) == "\x89PNG\r\n\x1a\n" && string(data[12:16]) == "IHDR" &&
		data[24] == 8 && data[25] == 2 && data[28] == 0
}
