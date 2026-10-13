package raster

import (
	"math"
)

// The primitives below port resvg's (crates/resvg/src/filter: mod.rs and
// the module of each) as the others in filter.go do: the same inputs,
// colour spaces and f32 arithmetic, each pass a band at a time charged to
// MaxPixelOps by what it costs.

// floorMod is a mod b in [0, b), b > 0.
func floorMod(a, b int) int {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

// tile is resvg's apply_tile: the input's data region (its primitive's
// sub-region, or the filter region), cut to the image, repeated over the
// region from the sub-region's corner. A sub-region wholly outside the image
// fails the filter, as resvg's copy_region does. The result is tagged sRGB
// whatever the input's space, as resvg's is.
func (run *filterRun) tile(p *fprim) (fimage, bool) {
	in := run.input(p.in1)
	w := run.w
	sub := irect{in.region.x0 - run.region.x0, in.region.y0 - run.region.y0, in.region.x1 - run.region.x0, in.region.y1 - run.region.y0}
	src := sub.intersect(irect{0, 0, run.w, run.h})
	if src.empty() {
		return fimage{}, false
	}
	pix := run.blank()
	tw, th := src.w(), src.h()
	ok := run.rows(1, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			sy := src.y0 + floorMod(y-sub.y0, th)
			from := in.pix[sy*w*4 : (sy+1)*w*4]
			to := pix[y*w*4 : (y+1)*w*4]
			for x := 0; x < w; x++ {
				sx := (src.x0 + floorMod(x-sub.x0, tw)) * 4
				copy(to[x*4:x*4+4], from[sx:sx+4])
			}
		}
	})
	return fimage{pix: pix, region: run.region, owned: true}, ok
}

// morphSize is resvg's kernel size along an axis of n pixels for a radius r
// (device pixels): 2*ceil(r), u32 arithmetic that wraps as Rust's release
// builds do, at most n.
func morphSize(r float32, n int) int {
	k := satU32(math.Ceil(float64(r))) * 2
	return min(int(k), n)
}

// morphology is resvg's apply_morphology, the input in the primitive's
// colour space: each channel's minimum (erode) or maximum (dilate) over a
// window of morphSize pixels each way, from half of it before the pixel,
// cut to the image. resvg visits the whole window for every pixel, w*h*rx*ry
// work for a radius the input chooses; the minimum over a rectangle is the
// minimum over its rows of the minima over its columns, so it is computed
// here as two passes, each a sliding window (van Herk / Gil-Werman) of three
// comparisons a channel a pixel, whatever the radius: the same result.
func (run *filterRun) morphology(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	pix := run.ownCopy(run.toSpace(run.input(p.in1), p.linear))
	res := fimage{pix: pix, region: run.region, linear: p.linear, owned: true}
	sx, sy := tsScale(run.ts)
	rx, ry := float32(p.rx)*float32(sx), float32(p.ry)*float32(sy)
	if !(rx > 0 && ry > 0) {
		return res, run.rows(1, func(y0, y1 int) { clear(rowsOf(pix, w, y0, y1)) })
	}
	cols, rows := morphSize(rx, w), morphSize(ry, h)
	init := uint8(0)
	if !p.dilate {
		init = 255
	}
	if cols == 0 || rows == 0 {
		// A size that wrapped to zero: resvg visits no pixel, and every
		// result is the operator's identity.
		return res, run.rows(1, func(y0, y1 int) {
			band := rowsOf(pix, w, y0, y1)
			for i := range band {
				band[i] = init
			}
		})
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
	ok := run.rows(cm.cols*cm.rows*channels, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				convolvePixel(cm, src, pix, w, h, x, y)
			}
		}
	})
	return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, ok
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

// displace is resvg's apply_displacement_map: both inputs in the
// primitive's colour space, each pixel of the result the pixel of in1 the
// map (in2, premultiplied as resvg leaves it) points to, transparent where
// that is outside the image. resvg scales the offset by the primitive's
// scale twice (scale_coordinates already multiplies the device scale by it,
// and displacement_map::apply multiplies by it again); so does this, being
// its output that is matched.
func (run *filterRun) displace(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	src := run.toSpace(run.input(p.in1), p.linear)
	defer run.free(src)
	mp := run.toSpace(run.input(p.in2), p.linear)
	defer run.free(mp)
	pix := run.blank()
	tx, ty := tsScale(run.ts)
	k := float32(p.scale)
	sx, sy := float32(k*float32(tx)), float32(k*float32(ty))
	ok := run.rows(1, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				m := mp.pix[(y*w+x)*4 : (y*w+x)*4+4]
				dx := float32(m[p.xch])/255 - 0.5
				dy := float32(m[p.ych])/255 - 0.5
				ox := satI32(math.Round(float64(float32(x) + float32(float32(dx*sx)*k))))
				oy := satI32(math.Round(float64(float32(y) + float32(float32(dy*sy)*k))))
				if ox < 0 || int(ox) >= w || oy < 0 || int(oy) >= h {
					continue
				}
				copy(pix[(y*w+x)*4:(y*w+x)*4+4], src.pix[(int(oy)*w+int(ox))*4:])
			}
		}
	})
	return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, ok
}
