package imageref

import (
	"encoding/binary"
	"image"
	"image/color"

	"github.com/mgilbir/aster/internal/budget"
)

// The standard decoders' own state, whatever the image's size: a PNG's
// inflater (its window and tables) and reader, a JPEG decoder's Huffman and
// quantization tables, a GIF's LZW tables. Measured with Go 1.27 at about
// 66, 32 and 39 KiB (TestDecodeBytesCoversTheDecoder); rounded up.
const (
	pngOverhead  = 96 << 10
	jpegOverhead = 48 << 10
	gifOverhead  = 64 << 10
)

// DecodeBytes is what decoding data (an image Config accepted, as cfg and
// format) allocates with the standard decoders, at most: the decoded image,
// and what the decoder makes on the way to it (C20, audit 2026-10-18). A
// caller charges it before it calls Decode. Beyond the pixels:
//
//   - a JPEG's planes are padded to whole MCUs; a progressive one keeps
//     every coefficient until the last scan, 64 int32 a block of each
//     component (four bytes a sample: four times a baseline image's
//     planes); one in RGB or CMYK is converted into an RGBA or CMYK image
//     after it is decoded;
//   - an interlaced PNG decodes each of its seven passes into an image of
//     its own before merging it into the whole one; every PNG reads two rows
//     of up to eight bytes a pixel;
//   - each decoder's own state (the overheads above).
func DecodeBytes(data []byte, cfg image.Config, format string) int64 {
	px := budget.Mul(int64(cfg.Width), int64(cfg.Height))
	switch format {
	case "jpeg":
		if n, ok := jpegDecodeBytes(data, cfg.Width, cfg.Height); ok {
			return n + jpegOverhead
		}
		// A header the scan below cannot read is one the decoder refuses
		// early; charge the worst a JPEG can cost: four components,
		// progressive, converted (4 + 16 + 4 bytes a sample, at 4:4:4).
		return budget.Mul(px, 24) + jpegOverhead
	case "png":
		n := budget.Mul(px, modelBytes(cfg.ColorModel))
		if len(data) > 28 && data[28] != 0 {
			n += n // the passes: as many pixels as the image again
		}
		return n + budget.Mul(int64(cfg.Width), 16) + pngOverhead
	}
	return budget.Mul(px, modelBytes(cfg.ColorModel)) + gifOverhead
}

// modelBytes is what the standard decoders hold a pixel of colour model m
// in: a byte for a palette or grey, two for 16-bit grey, three for JPEG's
// YCbCr (with its chroma planes at most), eight for 16-bit colour, four
// otherwise.
func modelBytes(m color.Model) int64 {
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	switch m {
	case color.GrayModel:
		return 1
	case color.Gray16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	return 4
}

// jpegDecodeBytes reads data's markers up to its first scan, as
// image/jpeg's decoder does, and sizes what it will allocate: the planes
// (makeImg), the black plane of a four-component image, the progressive
// coefficients (processSOS), and the RGBA or CMYK image an RGB or CMYK one
// is converted into (convertToRGB, applyBlack). ok is false where the
// header cannot be read.
func jpegDecodeBytes(data []byte, w, h int) (int64, bool) {
	var (
		comps       []jpegComp
		progressive bool
		jfif        bool
		adobe       = -1 // the APP14 transform, -1 without one
	)
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 0, false
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 0, false
		}
		m := data[i+1]
		if m == 0xff { // fill byte
			i++
			continue
		}
		if m == 0xd8 || (m >= 0xd0 && m <= 0xd7) || m == 0x01 {
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return 0, false
		}
		seg := data[i+4 : i+2+n]
		switch m {
		case 0xc0, 0xc1, 0xc2:
			progressive = m == 0xc2
			if len(seg) < 6 {
				return 0, false
			}
			nc := int(seg[5])
			if (nc != 1 && nc != 3 && nc != 4) || len(seg) < 6+3*nc {
				return 0, false
			}
			comps = comps[:0]
			for k := range nc {
				hv := seg[7+3*k]
				ch, cv := int(hv>>4), int(hv&15)
				if nc == 1 || ch < 1 || cv < 1 {
					ch, cv = 1, 1 // the decoder takes one component as 1x1
				}
				comps = append(comps, jpegComp{int(seg[6+3*k]), min(ch, 4), min(cv, 4)})
			}
		case 0xe0:
			jfif = jfif || (len(seg) >= 5 && string(seg[:5]) == "JFIF\x00")
		case 0xee:
			if len(seg) >= 12 && string(seg[:5]) == "Adobe" {
				adobe = int(seg[11])
			}
		case 0xda:
			return jpegPlanes(comps, progressive, jfif, adobe, w, h)
		}
		i += 2 + n
	}
	return 0, false
}

// jpegComp is a component of a JPEG's frame: its identifier and sampling
// factors.
type jpegComp struct{ id, h, v int }

// jpegPlanes is jpegDecodeBytes for the header read: see there.
func jpegPlanes(comps []jpegComp, progressive, jfif bool, adobe, w, h int) (int64, bool) {
	if len(comps) == 0 {
		return 0, false
	}
	maxH, maxV := 1, 1
	for _, c := range comps {
		maxH, maxV = max(maxH, c.h), max(maxV, c.v)
	}
	mxx := int64((w + 8*maxH - 1) / (8 * maxH))
	myy := int64((h + 8*maxV - 1) / (8 * maxV))
	pw, ph := 8*int64(maxH)*mxx, 8*int64(maxV)*myy // the planes' padded size
	px := budget.Mul(int64(w), int64(h))
	var n int64
	if len(comps) == 1 {
		n = budget.Mul(pw, ph)
	} else {
		// The luma plane and two chroma planes at their subsampling
		// (makeImg); at full size in the decoder's flex mode, which makes
		// them 4:4:4.
		c1, c2 := comps[1], comps[2]
		cw, ch := pw, ph
		if c1.h == c2.h && c1.v == c2.v && comps[0].h == maxH && comps[0].v == maxV {
			switch (maxH/c1.h)<<4 | maxV/c1.v {
			case 0x12, 0x21, 0x22, 0x41, 0x42:
				cw, ch = pw*int64(c1.h)/int64(maxH), ph*int64(c1.v)/int64(maxV)
			}
		}
		n = budget.Mul(pw, ph) + 2*budget.Mul(cw, ch)
		if len(comps) == 4 {
			n += budget.Mul(budget.Mul(8*int64(comps[3].h), mxx), budget.Mul(8*int64(comps[3].v), myy))
			n += budget.Mul(px, 4) // the CMYK (or RGBA) image
		} else if !jfif && (adobe == 0 || (comps[0].id == 'R' && comps[1].id == 'G' && comps[2].id == 'B')) {
			n += budget.Mul(px, 4) // converted to RGBA
		}
	}
	if progressive {
		for _, c := range comps {
			n += budget.Mul(budget.Mul(mxx, myy), int64(c.h*c.v)*256) // 64 int32 a block
		}
	}
	return n, true
}
