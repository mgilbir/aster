package aster

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// recodePNG losslessly re-encodes a PNG into its cheapest equivalent color
// format: 8-bit indexed when the image has at most 256 distinct colors,
// 24-bit truecolor when it is fully opaque, unchanged otherwise. Every pixel
// is preserved exactly; only the storage format changes.
//
// The resvg renderer always emits 8-bit RGBA — four bytes per pixel plus an
// alpha channel that is fully opaque for charts drawn on a solid background.
// Consumers that decode and re-compress the pixel stream (PDF embedders such
// as dvipdfmx, office formats) pay per decoded byte, so indexed output cuts
// their work roughly fourfold and avoids a separate alpha (soft mask) stream.
//
// On any decode failure the input is returned unchanged.
func recodePNG(data []byte) []byte {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}

	bounds := src.Bounds()
	nrgba, ok := src.(*image.NRGBA)
	if !ok {
		nrgba = image.NewNRGBA(bounds)
		draw.Draw(nrgba, bounds, src, bounds.Min, draw.Src)
	}

	// One scan collects both facts that pick the target format: full opacity
	// and the distinct-color count (abandoned past 256).
	const maxPaletteSize = 256
	opaque := true
	colorIndex := make(map[[4]uint8]uint8, maxPaletteSize)
	palette := make([]color.Color, 0, maxPaletteSize)
	paletteOK := true
	for i := 0; i < len(nrgba.Pix); i += 4 {
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

	var out bytes.Buffer
	switch {
	case paletteOK:
		paletted := image.NewPaletted(bounds, palette)
		for i, j := 0, 0; i < len(nrgba.Pix); i, j = i+4, j+1 {
			var key [4]uint8
			copy(key[:], nrgba.Pix[i:i+4])
			paletted.Pix[j] = colorIndex[key]
		}
		if err := png.Encode(&out, paletted); err != nil {
			return data
		}
	case opaque:
		// The stdlib encoder detects a fully opaque image and writes 24-bit
		// truecolor (no alpha channel).
		if err := png.Encode(&out, nrgba); err != nil {
			return data
		}
	default:
		return data
	}
	return out.Bytes()
}
