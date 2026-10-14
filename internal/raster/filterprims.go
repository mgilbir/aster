package raster

import (
	"math"
)

// The primitives below port resvg's (crates/resvg/src/filter: mod.rs and
// the module of each) as the others in filter.go do: the same f32
// arithmetic, each pass a band at a time charged to MaxPixelOps by what it
// costs; their inputs, colour spaces and geometry are Filter Effects 1's
// where resvg's differ (ADR 0015).

// floorMod is a mod b in [0, b), b > 0.
func floorMod(a, b int) int {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

// tile is feTile, Filter Effects 1 §9.20: the input's data region (its
// primitive's sub-region, or the filter region) repeated over the region,
// "The top/left corner of each given tile is at location (x + i*width, y +
// j*height), where (x,y) represents the top/left of the input image's
// filter primitive subregion, width and height represent the width and
// height of the input image's filter primitive subregion". The part of a
// tile outside the image is transparent black (the filter region "acts as
// a hard clip clipping rectangle on the filter primitive's input image",
// §9.4); a tile wholly outside it gives a transparent result. resvg cuts
// the tile to the image and repeats the cut part (and fails the filter
// when nothing is left). The result is in the input's colour space, as
// colour-interpolation-filters "has no effect on filter primitives like
// feOffset, feImage, feTile or feFlood" (§10); resvg tags it sRGB.
func (run *filterRun) tile(p *fprim) (fimage, bool) {
	in := run.input(p.in1)
	w, h := run.w, run.h
	pix := run.blank()
	res := fimage{pix: pix, region: run.region, linear: in.linear, owned: true}
	sub := irect{in.region.x0 - run.region.x0, in.region.y0 - run.region.y0, in.region.x1 - run.region.x0, in.region.y1 - run.region.y0}
	if sub.empty() || sub.intersect(irect{0, 0, w, h}).empty() {
		return res, true
	}
	tw, th := sub.w(), sub.h()
	ok := run.rows(1, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			sy := sub.y0 + floorMod(y-sub.y0, th)
			if sy < 0 || sy >= h {
				continue
			}
			from := in.pix[sy*w*4 : (sy+1)*w*4]
			to := pix[y*w*4 : (y+1)*w*4]
			for x := 0; x < w; x++ {
				sx := sub.x0 + floorMod(x-sub.x0, tw)
				if sx < 0 || sx >= w {
					continue
				}
				copy(to[x*4:x*4+4], from[sx*4:sx*4+4])
			}
		}
	})
	return res, ok
}

// morphSize is the kernel size along an axis of n pixels for a radius r > 0
// (device pixels): Filter Effects 1 §9.17's kernel "is a rectangle with a
// width of 2*x-radius", 2*ceil(r) pixels (as resvg's, from half of it
// before the pixel: the specification does not say which pixels a width
// covers), at least 2 and at most n. resvg's u32 arithmetic wraps for a
// radius from 2^31, and a kernel that wraps to zero leaves every pixel the
// operator's identity; a radius that large here is the whole line.
func morphSize(r float32, n int) int {
	k := 2 * math.Ceil(float64(r))
	if !(k >= 2) {
		k = 2 // a positive radius whose device size rounds to zero
	}
	if k > float64(n) {
		return n
	}
	return int(k)
}

// morphology is feMorphology (resvg's apply_morphology), the input in the
// primitive's colour space: each channel's minimum (erode) or maximum
// (dilate) over a window of morphSize pixels each way, from half of it
// before the pixel, cut to the image. resvg visits the whole window for every pixel, w*h*rx*ry
// work for a radius the input chooses; the minimum over a rectangle is the
// minimum over its rows of the minima over its columns, so it is computed
// here as two passes, each a sliding window (van Herk / Gil-Werman) of three
// comparisons a channel a pixel, whatever the radius: the same result.
func (run *filterRun) morphology(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	pix := run.ownCopy(run.toSpace(run.input(p.in1), p.linear))
	res := fimage{pix: pix, region: run.region, linear: p.linear, owned: true}
	sx, sy := tsScale(run.ts)
	// The radii are positive (a radius that is not is a pass through,
	// resolved before).
	rx, ry := float32(p.rx)*float32(sx), float32(p.ry)*float32(sy)
	cols, rows := morphSize(rx, w), morphSize(ry, h)
	init := uint8(0)
	if !p.dilate {
		init = 255
	}
	tmp := run.alloc(len(pix))
	defer run.uncharge(len(tmp))
	// The window scratch: a padded line and its two running extremes, four
	// channels each, for the longer of a row and a column.
	n := max(w+cols, h+rows) * 4 * 3
	run.charge(n)
	defer run.uncharge(n)
	scratch := make([]uint8, n)
	m := &morphLine{dilate: p.dilate, init: init, buf: scratch}
	// Rows: pix -> tmp, a band of rows at a time.
	if !bandRows(w, h, morphCost, run.work, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			m.run(rowsOf(tmp, w, y, y+1), rowsOf(pix, w, y, y+1), 4, w, cols, cols/2)
		}
	}) {
		return res, false
	}
	// Columns: tmp -> pix, a band of columns at a time.
	return res, bandRows(h, w, morphCost, run.work, func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			m.run(pix[x*4:], tmp[x*4:], w*4, h, rows, rows/2)
		}
	})
}

// morphCost is what a pixel of a morphology pass is charged: a pixel
// operation a channel, as a blur pass is.
const morphCost = 4

// morphLine computes one line of a morphology pass.
type morphLine struct {
	dilate bool
	init   uint8 // the operator's identity: 255 for erode, 0 for dilate
	buf    []uint8
	steps  int // the line passes' iterations so far (tests count them)
}

// run sets each of the n pixels of dst (stride bytes apart) to the extreme,
// channel by channel, of src's pixels in [i-t, i-t+k), cut to [0, n). Out of
// the line a pixel counts as the identity, which is the same as skipping it.
func (m *morphLine) run(dst, src []uint8, stride, n, k, t int) {
	l := n + k - 1 // the padded line: index j is src's j-t
	pad := m.buf[:l*4]
	g := m.buf[l*4 : 2*l*4] // extreme from the start of j's block of k to j
	hh := m.buf[2*l*4 : 3*l*4]
	for j := 0; j < l; j++ {
		s := j - t
		d := pad[j*4 : j*4+4]
		if s < 0 || s >= n {
			d[0], d[1], d[2], d[3] = m.init, m.init, m.init, m.init
			continue
		}
		copy(d, src[s*stride:s*stride+4])
	}
	op := func(a, b uint8) uint8 {
		if m.dilate == (a > b) {
			return a
		}
		return b
	}
	for j := 0; j < l; j++ {
		for c := 0; c < 4; c++ {
			v := pad[j*4+c]
			if j%k != 0 {
				v = op(v, g[(j-1)*4+c])
			}
			g[j*4+c] = v
		}
	}
	for j := l - 1; j >= 0; j-- {
		for c := 0; c < 4; c++ {
			v := pad[j*4+c]
			if (j+1)%k != 0 && j+1 < l {
				v = op(v, hh[(j+1)*4+c])
			}
			hh[j*4+c] = v
		}
	}
	for i := 0; i < n; i++ {
		d := dst[i*stride : i*stride+4]
		e := i + k - 1
		for c := 0; c < 4; c++ {
			d[c] = op(hh[i*4+c], g[e*4+c])
		}
	}
	m.steps += 3*l + n
}

// convolve is resvg's apply_convolve_matrix: the input in the primitive's
// colour space, demultiplied first when alpha is preserved, convolved with
// the kernel turned half round, in f32. Each pixel is orderX x orderY taps
// of each channel it computes, charged as such.
func (run *filterRun) convolve(p *fprim) (fimage, bool) {
	cm := p.conv
	w, h := run.w, run.h
	in := run.toSpace(run.input(p.in1), p.linear)
	src := in.pix
	if cm.preserveAlpha {
		src = run.ownCopy(in)
		in = fimage{pix: src, owned: true}
		if !run.rows(unmultiplyCost, func(y0, y1 int) { demultiply(rowsOf(src, w, y0, y1)) }) {
			run.free(in)
			return fimage{}, false
		}
	}
	defer run.free(in)
	pix := run.blank()
	channels := 4
	if cm.preserveAlpha {
		channels = 3
	}
	// kernelUnitLength in device pixels: the distance between the kernel's
	// cells. One pixel (or unspecified) is the plain convolution.
	tx, ty := tsScale(run.ts)
	// At most 1e290 pixels (past every image): a tap's offset, a cell
	// count of up to 2^16 times it, stays finite.
	ux, uy := min(cm.unitX*tx, 1e290), min(cm.unitY*ty, 1e290)
	if cm.unitX == 0 || (ux == 1 && uy == 1) {
		ok := run.rows(cm.cols*cm.rows*channels, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				for x := 0; x < w; x++ {
					convolvePixel(cm, src, pix, w, h, x, y)
				}
			}
		})
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, ok
	}
	// Each tap is a bilinear sample: four pixels.
	ok := run.rows(4*cm.cols*cm.rows*channels, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				convolveSpaced(cm, src, pix, w, h, x, y, ux, uy)
			}
		}
	})
	return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, ok
}

// convolveSpaced is convolvePixel with the kernel's cells kernelUnitLength
// apart, ux by uy device pixels. Filter Effects 1 §9.9: "kernelUnitLength,
// in combination with the other attributes, defines an implicit pixel grid
// in the filter effects coordinate system", the input "temporarily rescaled
// to match its pixels with kernelUnitLength", with "appropriate
// interpolation techniques, for example bilinear", and "implementations
// might choose approaches that minimize or eliminate resampling": each tap
// reads the input at its cell's position, x + (j - targetX) * ux, by
// bilinear interpolation between the four pixels around it, the edge mode
// extending the image. The result is at the image's own pixels, so there
// is nothing to resample back (Firefox computes it so).
func convolveSpaced(cm *convolveSpec, src, dst []uint8, w, h, x, y int, ux, uy float64) {
	var nr, ng, nb, na float32
	for oy := 0; oy < cm.rows; oy++ {
		fy := float64(y) + float64(float64(oy-cm.targetY)*uy)
		for ox := 0; ox < cm.cols; ox++ {
			fx := float64(x) + float64(float64(ox-cm.targetX)*ux)
			k := cm.kernel[(cm.rows-oy-1)*cm.cols+(cm.cols-ox-1)]
			q := bilinear(src, w, h, fx, fy, cm.edge)
			nr += float32(float32(q[0]/255) * k)
			ng += float32(float32(q[1]/255) * k)
			nb += float32(float32(q[2]/255) * k)
			if !cm.preserveAlpha {
				na += float32(float32(q[3]/255) * k)
			}
		}
	}
	convolveStore(cm, src, dst, w, x, y, nr, ng, nb, na)
}

// bilinear is the pixel of src (w x h, RGBA8) at (fx, fy), interpolated
// between the four pixels around it, in f32; an edge mode extends the
// image past its borders as feConvolveMatrix's does.
func bilinear(src []uint8, w, h int, fx, fy float64, edge uint8) [4]float32 {
	x0, tx := axisSample(fx, w, edge)
	y0, ty := axisSample(fy, h, edge)
	var out [4]float32
	for j := 0; j < 2; j++ {
		wy := 1 - ty
		if j == 1 {
			wy = ty
		}
		if wy == 0 {
			continue
		}
		py := edgeIndex(y0+j, h, edge)
		for i := 0; i < 2; i++ {
			wx := 1 - tx
			if i == 1 {
				wx = tx
			}
			if wx == 0 {
				continue
			}
			px := edgeIndex(x0+i, w, edge)
			if px < 0 || py < 0 {
				continue // edgeMode none: zero
			}
			q := src[(py*w+px)*4 : (py*w+px)*4+4]
			f := float32(wx * wy)
			for c := range out {
				out[c] += float32(float32(q[c]) * f)
			}
		}
	}
	return out
}

// axisSample is the pixel at or before position v on an axis of n pixels,
// and the weight of the one after it: v reduced into the image first where
// the edge mode allows (duplicate clamps it to the image, wrap takes it
// modulo n), so that the index is an int whatever v.
func axisSample(v float64, n int, edge uint8) (int, float32) {
	if v != v {
		v = 0 // not reached: the offsets are finite
	}
	switch edge {
	case edgeDuplicate:
		if v <= 0 {
			return 0, 0
		}
		if v >= float64(n-1) {
			return n - 1, 0
		}
	case edgeWrap:
		v = math.Mod(v, float64(n))
		if v < 0 {
			v += float64(n)
		}
	default:
		// none: at least a pixel outside the image is zero, and so is
		// everything further.
		if v <= -1 || v >= float64(n) {
			return -2, 0
		}
	}
	f := math.Floor(v)
	return int(f), float32(v - f)
}

// edgeIndex is pixel i of an axis of n pixels under the edge mode: -1 for
// one that is zero (none, outside).
func edgeIndex(i, n int, edge uint8) int {
	switch edge {
	case edgeDuplicate:
		return min(max(i, 0), n-1)
	case edgeWrap:
		return floorMod(i, n)
	}
	if i < 0 || i >= n {
		return -1
	}
	return i
}

// f32Bound is resvg's f32_bound: v clamped to [lo, hi], NaN to lo.
func f32Bound(lo, v, hi float32) float32 {
	if v > hi {
		return hi
	}
	if v >= lo {
		return v
	}
	return lo
}

// convolvePixel computes pixel (x, y) of dst from src, both w x h.
func convolvePixel(cm *convolveSpec, src, dst []uint8, w, h, x, y int) {
	var nr, ng, nb, na float32
	for oy := 0; oy < cm.rows; oy++ {
		ty := y - cm.targetY + oy
		for ox := 0; ox < cm.cols; ox++ {
			tx := x - cm.targetX + ox
			switch cm.edge {
			case edgeNone:
				if tx < 0 || tx >= w || ty < 0 || ty >= h {
					continue
				}
			case edgeDuplicate:
				tx, ty = min(max(tx, 0), w-1), min(max(ty, 0), h-1)
			case edgeWrap:
				tx, ty = floorMod(tx, w), floorMod(ty, h)
			}
			k := cm.kernel[(cm.rows-oy-1)*cm.cols+(cm.cols-ox-1)]
			q := src[(ty*w+tx)*4 : (ty*w+tx)*4+4]
			// Each product rounded before it is added: f32 operations as
			// Rust performs them, never fused.
			nr += float32(float32(float32(q[0])/255) * k)
			ng += float32(float32(float32(q[1])/255) * k)
			nb += float32(float32(float32(q[2])/255) * k)
			if !cm.preserveAlpha {
				na += float32(float32(float32(q[3])/255) * k)
			}
		}
	}
	convolveStore(cm, src, dst, w, x, y, nr, ng, nb, na)
}

// convolveStore stores pixel (x, y) of dst from the kernel's sums.
func convolveStore(cm *convolveSpec, src, dst []uint8, w, x, y int, nr, ng, nb, na float32) {
	o := dst[(y*w+x)*4 : (y*w+x)*4+4]
	if cm.preserveAlpha {
		na = float32(src[(y*w+x)*4+3]) / 255
	} else {
		na = na/cm.divisor + cm.bias
	}
	ba := f32Bound(0, na, 1)
	calc := func(v float32) uint8 {
		v = v/cm.divisor + float32(cm.bias*na)
		if cm.preserveAlpha {
			v = float32(f32Bound(0, v, 1) * ba)
		} else {
			v = f32Bound(0, v, ba)
		}
		return satU8(float32(v*255) + 0.5)
	}
	o[0], o[1], o[2] = calc(nr), calc(ng), calc(nb)
	o[3] = satU8(float32(ba*255) + 0.5)
}

// displace is feDisplacementMap, Filter Effects 1 §9.11: "P'(x,y) <- P( x
// + scale * (XC(x,y) - .5), y + scale * (YC(x,y) - .5))", the scale once
// (resvg multiplies the offset by it twice: scale_coordinates already
// multiplies the device scale by it, and displacement_map::apply by it
// again), sampled at the nearest pixel (the specification defines no
// interpolation), transparent where that is outside the image. "The input
// image in is to remain premultiplied for this filter primitive. The
// calculations using the pixel values from in2 are performed using
// non-premultiplied color values" (resvg reads the map premultiplied), and
// "The color-interpolation-filters property only applies to the in2 source
// image and does not apply to the in source image. The in source image
// must remain in its current color space" (resvg converts both): the
// result is in in's space.
func (run *filterRun) displace(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	src := run.input(p.in1)
	mp := run.toSpace(run.input(p.in2), p.linear)
	defer run.free(mp)
	pix := run.blank()
	tx, ty := tsScale(run.ts)
	sx, sy := float32(float32(p.scaleX)*float32(tx)), float32(float32(p.scaleY)*float32(ty))
	ok := run.rows(1, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				m := mp.pix[(y*w+x)*4 : (y*w+x)*4+4]
				dx := unpremultiplied(m, p.xch) - 0.5
				dy := unpremultiplied(m, p.ych) - 0.5
				ox := satI32(math.Round(float64(float32(x) + float32(dx*sx))))
				oy := satI32(math.Round(float64(float32(y) + float32(dy*sy))))
				if ox < 0 || int(ox) >= w || oy < 0 || int(oy) >= h {
					continue
				}
				copy(pix[(y*w+x)*4:(y*w+x)*4+4], src.pix[(int(oy)*w+int(ox))*4:])
			}
		}
	})
	return fimage{pix: pix, region: run.region, linear: src.linear, owned: true}, ok
}

// unpremultiplied is channel ch (0-3: R, G, B, A) of the premultiplied
// pixel m as a non-premultiplied value in [0, 1]: a colour channel divided
// by the alpha (0 where the alpha is), the alpha itself.
func unpremultiplied(m []uint8, ch uint8) float32 {
	a := m[3]
	if ch == 3 {
		return float32(a) / 255
	}
	if a == 0 {
		return 0
	}
	return min(float32(m[ch])/float32(a), 1)
}
