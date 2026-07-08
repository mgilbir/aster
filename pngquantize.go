package aster

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"slices"
)

// Quality guard for lossy quantization: when the quantized image deviates
// from the render by more than these bounds, quantization is rejected and
// the caller falls back to the lossless recode. Charts — flat areas that map
// exactly plus antialiased edges — sit far inside them; images that need
// more colors than the budget (photographic content, dense gradients) fail
// them instead of shipping visibly degraded.
const (
	// maxMeanChannelError bounds the average per-channel deviation across
	// all pixels (0-255 scale).
	maxMeanChannelError = 1.5
	// maxPeakChannelError bounds the worst single-channel deviation of any
	// pixel. Dithered edge pixels stay well below this; structural changes
	// (a color that lost its identity entirely) exceed it.
	maxPeakChannelError = 64
)

// quantizeOrRecodePNG lossily quantizes data to at most maxColors colors,
// falling back to the lossless recode when quantization cannot maintain the
// output within the quality guard (or cannot shrink the image). The fallback
// is logged at debug level: it is expected for non-chart-like content, and a
// library should not write to the host's default log output for documented,
// safe behavior — raise the log level to observe it.
func quantizeOrRecodePNG(data []byte, maxColors int) []byte {
	out, ok, reason := quantizePNG(data, maxColors)
	if ok {
		return out
	}
	slog.Debug("aster: png quantization fell back to lossless recode",
		"reason", reason, "max_colors", maxColors, "bytes", len(data))
	return recodePNG(data)
}

// quantizePNG re-encodes a PNG as 8-bit indexed with at most maxColors
// colors (clamped to 2..256): a weighted median-cut palette over the image's
// color histogram, then Floyd–Steinberg error diffusion when mapping pixels.
// Dimensions and rendering are untouched — only colors move, and only on
// pixels whose exact color did not earn a palette slot (typically
// antialiased edges; flat chart areas map exactly).
//
// Images already within the palette budget take a lossless path identical to
// recodePNG's indexed case. The boolean reports whether the result honours
// the quality guard and is smaller than the input; when false, the returned
// data is the input and reason says why.
func quantizePNG(data []byte, maxColors int) (_ []byte, ok bool, reason string) {
	if maxColors < 2 {
		maxColors = 2
	}
	if maxColors > 256 {
		maxColors = 256
	}

	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false, "decode failed"
	}
	bounds := src.Bounds()
	nrgba, isNRGBA := src.(*image.NRGBA)
	if !isNRGBA {
		nrgba = image.NewNRGBA(bounds)
		draw.Draw(nrgba, bounds, src, bounds.Min, draw.Src)
	}

	// Histogram of exact colors in first-seen scan order: deterministic (no
	// map iteration order involved) and spatially local, which the PNG
	// filters reward with better compression.
	hist := make(map[uint32]int, 1024)
	order := make([]colorCount, 0, 1024)
	for i := 0; i < len(nrgba.Pix); i += 4 {
		key := packNRGBA(nrgba.Pix[i:])
		if hist[key] == 0 {
			order = append(order, colorCount{key: key})
		}
		hist[key]++
	}
	for i := range order {
		order[i].count = hist[order[i].key]
	}

	exact := make(map[uint32]uint8, len(order))
	if len(order) <= maxColors {
		// Lossless: every color gets its own slot; no dithering needed.
		palette := make(color.Palette, 0, len(order))
		for i, e := range order {
			exact[e.key] = uint8(i)
			palette = append(palette, unpackNRGBA(e.key))
		}
		out := encodePNG(indexExact(nrgba, palette, exact))
		if out == nil || len(out) > len(data)+len(data)/10 {
			// The input is already the cheapest form; that still maintains
			// the output, so it is a success, not a fallback.
			return data, true, ""
		}
		return out, true, ""
	}

	// medianCut sorts its input in place; hand it a copy so `order` keeps
	// the first-seen invariant the renumbering below depends on.
	boxPalette, boxOf := medianCut(append([]colorCount(nil), order...), maxColors)
	// Re-number the boxes by first pixel occurrence so palette indices keep
	// scan-order locality (same rationale as above).
	remap := make([]int, len(boxPalette))
	for i := range remap {
		remap[i] = -1
	}
	palette := make(color.Palette, 0, len(boxPalette))
	for _, e := range order {
		b := int(boxOf[e.key])
		if remap[b] < 0 {
			remap[b] = len(palette)
			palette = append(palette, boxPalette[b])
		}
		exact[e.key] = uint8(remap[b])
	}

	dst, mean, peak := ditherIndex(nrgba, palette, exact)
	if mean > maxMeanChannelError || peak > maxPeakChannelError {
		return data, false, "quality guard exceeded"
	}
	out := encodePNG(dst)
	if out == nil || len(out) > len(data)+len(data)/10 {
		// The point of quantization is the ~4x cheaper *decode* in embedders,
		// so a slightly larger file can still be a win — but dithering noise
		// that bloats the encoding beyond ~10% signals content this palette
		// cannot represent economically.
		return data, false, "encoded size grew beyond tolerance"
	}
	return out, true, ""
}

// packNRGBA packs the first four bytes (R,G,B,A) into a map-friendly key.
func packNRGBA(pix []uint8) uint32 {
	return uint32(pix[0])<<24 | uint32(pix[1])<<16 | uint32(pix[2])<<8 | uint32(pix[3])
}

func unpackNRGBA(key uint32) color.NRGBA {
	return color.NRGBA{R: uint8(key >> 24), G: uint8(key >> 16), B: uint8(key >> 8), A: uint8(key)}
}

// colorCount is one histogram entry: a packed RGBA color and its pixel count.
type colorCount struct {
	key   uint32
	count int
}

func channelOf(key uint32, ch int) int {
	return int(uint8(key >> (24 - 8*ch)))
}

// medianCut splits the histogram into maxColors boxes and returns the
// weighted-average palette plus each source color's box index.
func medianCut(entries []colorCount, maxColors int) (color.Palette, map[uint32]uint8) {
	type box struct {
		entries []colorCount
		pixels  int
	}
	sum := func(es []colorCount) int {
		total := 0
		for _, e := range es {
			total += e.count
		}
		return total
	}
	boxes := []box{{entries: entries, pixels: sum(entries)}}

	widest := func(b box) (channel int, span int) {
		var lo, hi [4]int
		for ch := 0; ch < 4; ch++ {
			lo[ch], hi[ch] = 255, 0
		}
		for _, e := range b.entries {
			for ch := 0; ch < 4; ch++ {
				v := channelOf(e.key, ch)
				if v < lo[ch] {
					lo[ch] = v
				}
				if v > hi[ch] {
					hi[ch] = v
				}
			}
		}
		for ch := 0; ch < 4; ch++ {
			if hi[ch]-lo[ch] > span {
				span = hi[ch] - lo[ch]
				channel = ch
			}
		}
		return channel, span
	}

	for len(boxes) < maxColors {
		// Split the most populous box that still has a color spread.
		best, bestSpan, bestPixels := -1, 0, 0
		for i, b := range boxes {
			if len(b.entries) < 2 {
				continue
			}
			_, span := widest(b)
			if span == 0 {
				continue
			}
			if b.pixels > bestPixels || (b.pixels == bestPixels && span > bestSpan) {
				best, bestSpan, bestPixels = i, span, b.pixels
			}
		}
		if best < 0 {
			break
		}
		b := boxes[best]
		channel, _ := widest(b)
		slices.SortStableFunc(b.entries, func(x, y colorCount) int {
			return channelOf(x.key, channel) - channelOf(y.key, channel)
		})
		// Split at the weighted median so both halves hold ~half the pixels.
		half, acc, cut := b.pixels/2, 0, 0
		for i, e := range b.entries {
			acc += e.count
			if acc >= half {
				cut = i + 1
				break
			}
		}
		if cut <= 0 || cut >= len(b.entries) {
			cut = len(b.entries) / 2
		}
		left := box{entries: b.entries[:cut]}
		right := box{entries: b.entries[cut:]}
		left.pixels = sum(left.entries)
		right.pixels = sum(right.entries)
		boxes[best] = left
		boxes = append(boxes, right)
	}

	palette := make(color.Palette, 0, len(boxes))
	boxOf := make(map[uint32]uint8, len(entries))
	for i, b := range boxes {
		var r, g, bl, a, n int
		for _, e := range b.entries {
			r += channelOf(e.key, 0) * e.count
			g += channelOf(e.key, 1) * e.count
			bl += channelOf(e.key, 2) * e.count
			a += channelOf(e.key, 3) * e.count
			n += e.count
			boxOf[e.key] = uint8(i)
		}
		if n == 0 {
			n = 1
		}
		palette = append(palette, color.NRGBA{
			R: uint8((r + n/2) / n), G: uint8((g + n/2) / n),
			B: uint8((bl + n/2) / n), A: uint8((a + n/2) / n),
		})
	}
	return palette, boxOf
}

// indexExact maps pixels whose colors all have palette slots (lossless path).
func indexExact(src *image.NRGBA, palette color.Palette, exact map[uint32]uint8) *image.Paletted {
	dst := image.NewPaletted(src.Bounds(), palette)
	for i, j := 0, 0; i < len(src.Pix); i, j = i+4, j+1 {
		dst.Pix[j] = exact[packNRGBA(src.Pix[i:])]
	}
	return dst
}

// ditherIndex maps pixels with Floyd–Steinberg error diffusion, returning
// the indexed image together with the mean and peak per-channel deviation
// from the source (0-255 scale) for the quality guard. Pixels whose
// error-adjusted color exactly matches a source color take the precomputed
// slot; the rest do a nearest-palette search memoized per adjusted color.
func ditherIndex(src *image.NRGBA, palette color.Palette, exact map[uint32]uint8) (_ *image.Paletted, mean float64, peak int) {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewPaletted(bounds, palette)

	pal := make([][4]int, len(palette))
	for i, c := range palette {
		n := c.(color.NRGBA)
		pal[i] = [4]int{int(n.R), int(n.G), int(n.B), int(n.A)}
	}
	nearestCache := make(map[uint32]uint8, 4096)
	nearest := func(key uint32) uint8 {
		if idx, ok := exact[key]; ok {
			return idx
		}
		if idx, ok := nearestCache[key]; ok {
			return idx
		}
		kr, kg, kb, ka := channelOf(key, 0), channelOf(key, 1), channelOf(key, 2), channelOf(key, 3)
		best, bestDist := 0, 1<<62
		for i, p := range pal {
			dr, dg, db, da := kr-p[0], kg-p[1], kb-p[2], ka-p[3]
			d := dr*dr + dg*dg + db*db + da*da
			if d < bestDist {
				best, bestDist = i, d
			}
		}
		nearestCache[key] = uint8(best)
		return uint8(best)
	}

	clamp := func(v int) uint32 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint32(v)
	}

	// Error buffers for the current and next row, 4 channels per pixel,
	// scaled by 16 (the Floyd-Steinberg denominator).
	var devSum int64
	cur := make([]int, (w+2)*4)
	next := make([]int, (w+2)*4)
	for y := 0; y < h; y++ {
		rowOff := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		for x := 0; x < w; x++ {
			si := rowOff + x*4
			eo := (x + 1) * 4
			adj := clamp(int(src.Pix[si])+cur[eo]/16)<<24 |
				clamp(int(src.Pix[si+1])+cur[eo+1]/16)<<16 |
				clamp(int(src.Pix[si+2])+cur[eo+2]/16)<<8 |
				clamp(int(src.Pix[si+3])+cur[eo+3]/16)
			idx := nearest(adj)
			dst.Pix[y*dst.Stride+x] = idx
			p := pal[idx]
			for ch := 0; ch < 4; ch++ {
				e := channelOf(adj, ch) - p[ch]
				cur[eo+4+ch] += e * 7
				next[eo-4+ch] += e * 3
				next[eo+ch] += e * 5
				next[eo+4+ch] += e * 1
				// Deviation is measured against the source pixel, not the
				// error-adjusted one the diffusion works with.
				d := int(src.Pix[si+ch]) - p[ch]
				if d < 0 {
					d = -d
				}
				devSum += int64(d)
				if d > peak {
					peak = d
				}
			}
		}
		cur, next = next, cur
		for i := range next {
			next[i] = 0
		}
	}
	if total := int64(w) * int64(h) * 4; total > 0 {
		mean = float64(devSum) / float64(total)
	}
	return dst, mean, peak
}

func encodePNG(img image.Image) []byte {
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil
	}
	return out.Bytes()
}
