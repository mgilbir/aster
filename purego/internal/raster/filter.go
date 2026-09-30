package raster

import (
	"math"
)

// The filter pipeline is a port of resvg's (crates/resvg/src/filter): images
// are premultiplied RGBA8 the size of the filter region, colour-space
// conversion goes through the same u8 tables, blurs are resvg's box blur
// (sigma >= 2) and IIR blur, and the compositing arithmetic uses the same
// integer / f32 formulas, so results agree to within rounding.

type fimage struct {
	pix    []uint8
	region irect // where the data is valid, in filter coordinates
	linear bool
}

type fresult struct {
	name string
	img  fimage
}

type filterRun struct {
	w, h    int
	region  irect
	src     []uint8
	ts      matrix
	results []fresult
	live    int // bytes held by results
	limit   int
	failed  bool
}

const maxFilterLiveBytes = 1 << 30

func f32(v float64) float32 { return float32(v) }

// toIntRect is usvg's NonZeroRect::to_int_rect (f32 arithmetic).
func toIntRect(rc rect) irect {
	x0, y0 := float32(clampCoord(rc.x0)), float32(clampCoord(rc.y0))
	w, h := float32(clampCoord(rc.x1))-x0, float32(clampCoord(rc.y1))-y0
	iw := int(math.Ceil(math.Min(float64(w), coordLimit)))
	ih := int(math.Ceil(math.Min(float64(h), coordLimit)))
	if iw < 1 {
		iw = 1
	}
	if ih < 1 {
		ih = 1
	}
	x := int(clampCoord(math.Floor(float64(x0))))
	y := int(clampCoord(math.Floor(float64(y0))))
	return irect{x, y, x + iw, y + ih}
}

func tsScale(m matrix) (float64, float64) {
	// tiny-skia's Transform::get_scale uses the row norms.
	sx := float64(float32(math.Sqrt(float64(float32(m.a*m.a + m.c*m.c)))))
	sy := float64(float32(math.Sqrt(float64(float32(m.b*m.b + m.d*m.d)))))
	return sx, sy
}

// applyFilters runs the specs over cv (whose pixels are the source graphic,
// at ctm-shifted coordinates ts). On error the canvas is cleared.
func (r *renderer) applyFilters(specs []*filterSpec, ts matrix, cv *canvas) {
	for _, sp := range specs {
		if !r.applyFilter(sp, ts, cv) {
			clear(cv.pix)
			break
		}
	}
	cv.dirty = cv.bounds()
}

func (r *renderer) applyFilter(sp *filterSpec, ts matrix, cv *canvas) bool {
	rc := transformRect32(sp.rect, ts)
	if !(rc.w() > 0 && rc.h() > 0) {
		return false
	}
	run := &filterRun{w: cv.w, h: cv.h, src: cv.pix, ts: ts, limit: maxFilterLiveBytes}
	run.region = toIntRect(rc)
	last := len(sp.prims) - 1
	// Last use of each result name, to free buffers early.
	lastUse := map[string]int{}
	for i := range sp.prims {
		p := &sp.prims[i]
		for _, in := range []finput{p.in1, p.in2} {
			if in.kind == inRef {
				lastUse[in.name] = i
			}
		}
		for _, in := range p.merge {
			if in.kind == inRef {
				lastUse[in.name] = i
			}
		}
	}
	for i := range sp.prims {
		p := &sp.prims[i]
		sub := toIntRect(transformRect32(p.rect, ts))
		if !(p.rect.w() > 0 && p.rect.h() > 0) {
			return false
		}
		if p.kind == tagFeOffset && p.in1.kind == inRef {
			for j := len(run.results) - 1; j >= 0; j-- {
				if run.results[j].name == p.in1.name {
					sub = run.results[j].img.region
					break
				}
			}
		}
		res, ok := run.apply(p)
		if !ok || run.failed {
			return false
		}
		if run.region != sub {
			var sub2 irect
			if p.kind == tagFeOffset {
				sub2 = irect{0, 0, run.region.w(), run.region.h()}
			} else {
				sub2 = irect{sub.x0 - run.region.x0, sub.y0 - run.region.y0, sub.x1 - run.region.x0, sub.y1 - run.region.y0}
			}
			pix := res.pix
			if run.shared(pix) {
				pix = append([]uint8(nil), pix...)
			}
			clearOutside(pix, run.w, run.h, sub2)
			res = fimage{pix: pix, region: sub, linear: res.linear}
		}
		run.push(fresult{name: p.result, img: res})
		if run.failed {
			return false
		}
		// Drop results nobody reads any more (the last one is the output).
		if i != last {
			run.release(sp, i, lastUse)
		}
	}
	if len(run.results) == 0 {
		return false
	}
	out := run.results[len(run.results)-1].img
	out = toSpace(out, false)
	copy(cv.pix, out.pix)
	if len(out.pix) < len(cv.pix) {
		clear(cv.pix[len(out.pix):])
	}
	return true
}

func (run *filterRun) push(res fresult) {
	run.results = append(run.results, res)
	run.live += len(res.img.pix)
	if run.live > run.limit {
		run.failed = true
	}
}

func (run *filterRun) release(sp *filterSpec, i int, lastUse map[string]int) {
	// Free earlier results whose last reader has run; keep the newest.
	for j := 0; j < len(run.results)-1; j++ {
		res := &run.results[j]
		if res.img.pix == nil {
			continue
		}
		if lu, ok := lastUse[res.name]; ok && lu > i {
			continue
		}
		// A later primitive may reuse the name; the most recent one wins for
		// lookups, so only free when a newer result shadows or nobody reads it.
		run.live -= len(res.img.pix)
		res.img.pix = nil
	}
}

// shared reports whether pix is held by a stored result or is the source.
func (run *filterRun) shared(pix []uint8) bool {
	if len(pix) > 0 && len(run.src) > 0 && &pix[0] == &run.src[0] {
		return true
	}
	for i := range run.results {
		p := run.results[i].img.pix
		if len(p) > 0 && len(pix) > 0 && &p[0] == &pix[0] {
			return true
		}
	}
	return false
}

func clearOutside(pix []uint8, w, h int, keep irect) {
	keep = keep.intersect(irect{0, 0, w, h})
	if keep.empty() {
		clear(pix)
		return
	}
	for y := 0; y < h; y++ {
		row := pix[y*w*4 : (y+1)*w*4]
		if y < keep.y0 || y >= keep.y1 {
			clear(row)
			continue
		}
		clear(row[:keep.x0*4])
		clear(row[keep.x1*4:])
	}
}

func (run *filterRun) input(in finput) fimage {
	switch in.kind {
	case inAlpha:
		pix := make([]uint8, len(run.src))
		for i := 3; i < len(pix); i += 4 {
			pix[i] = run.src[i]
		}
		return fimage{pix: pix, region: run.region}
	case inRef:
		for j := len(run.results) - 1; j >= 0; j-- {
			if run.results[j].name == in.name && run.results[j].img.pix != nil {
				return run.results[j].img
			}
		}
	}
	return fimage{pix: run.src, region: run.region}
}

func (run *filterRun) blank() []uint8 { return make([]uint8, run.w*run.h*4) }

// toSpace converts img to the requested colour space (returns img itself when
// unchanged; the result never aliases the input otherwise).
func toSpace(img fimage, linear bool) fimage {
	if img.linear == linear {
		return img
	}
	pix := append([]uint8(nil), img.pix...)
	demultiply(pix)
	tbl := &linearToSRGBTable
	if linear {
		tbl = &srgbToLinearTable
	}
	for i := 0; i < len(pix); i += 4 {
		pix[i] = tbl[pix[i]]
		pix[i+1] = tbl[pix[i+1]]
		pix[i+2] = tbl[pix[i+2]]
	}
	multiply(pix)
	return fimage{pix: pix, region: img.region, linear: linear}
}

func satU8(v float32) uint8 {
	if v != v || v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

func demultiply(pix []uint8) {
	for i := 0; i < len(pix); i += 4 {
		a := float32(pix[i+3]) / 255.0
		for c := 0; c < 3; c++ {
			pix[i+c] = satU8(float32(float32(pix[i+c])/a) + 0.5)
		}
	}
}

func multiply(pix []uint8) {
	for i := 0; i < len(pix); i += 4 {
		a := float32(pix[i+3]) / 255.0
		for c := 0; c < 3; c++ {
			pix[i+c] = satU8(float32(float32(pix[i+c])*a) + 0.5)
		}
	}
}

func div255(v uint32) uint32 { return (v + 255) >> 8 }

// drawOver composites src onto dst (both w*h) at offset (dx,dy), source-over.
func drawOver(dst, src []uint8, w, h, dx, dy int) {
	for y := 0; y < h; y++ {
		sy := y - dy
		if sy < 0 || sy >= h {
			continue
		}
		for x := 0; x < w; x++ {
			sx := x - dx
			if sx < 0 || sx >= w {
				continue
			}
			s := src[(sy*w+sx)*4 : (sy*w+sx)*4+4]
			if s[3] == 0 {
				continue
			}
			d := dst[(y*w+x)*4 : (y*w+x)*4+4]
			ia := 255 - uint32(s[3])
			d[0] = uint8(uint32(s[0]) + div255(uint32(d[0])*ia))
			d[1] = uint8(uint32(s[1]) + div255(uint32(d[1])*ia))
			d[2] = uint8(uint32(s[2]) + div255(uint32(d[2])*ia))
			d[3] = uint8(uint32(s[3]) + div255(uint32(d[3])*ia))
		}
	}
}

func (run *filterRun) apply(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	switch p.kind {
	case tagFeFlood:
		pix := run.blank()
		a := f32(p.alpha)
		a8 := float32(math.Round(float64(a * 255)))
		af := a8 / 255
		r := satU8(float32(float32(p.color.r)*af) + 0.5)
		g := satU8(float32(float32(p.color.g)*af) + 0.5)
		b := satU8(float32(float32(p.color.b)*af) + 0.5)
		for i := 0; i < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, uint8(a8)
		}
		return fimage{pix: pix, region: run.region}, true
	case tagFeOffset:
		in := run.input(p.in1)
		sx, sy := tsScale(run.ts)
		dx, dy := float32(p.dx)*float32(sx), float32(p.dy)*float32(sy)
		if math.Abs(float64(dx)) < 1e-6 && math.Abs(float64(dy)) < 1e-6 {
			return in, true
		}
		pix := run.blank()
		drawOver(pix, in.pix, w, h, satInt(dx), satInt(dy))
		return fimage{pix: pix, region: run.region, linear: in.linear}, true
	case tagFeGaussianBlur:
		in := run.input(p.in1)
		sx, sy, box, ok := run.stdDev(p.stdX, p.stdY)
		if !ok {
			return in, true
		}
		in = toSpace(in, p.linear)
		pix := append([]uint8(nil), in.pix...)
		if box {
			boxBlur(sx, sy, pix, w, h)
		} else {
			iirBlur(sx, sy, pix, w, h)
		}
		return fimage{pix: pix, region: run.region, linear: p.linear}, true
	case tagFeDropShadow:
		return run.dropShadow(p)
	case tagFeBlend:
		in1 := toSpace(run.input(p.in1), p.linear)
		in2 := toSpace(run.input(p.in2), p.linear)
		pix := append([]uint8(nil), in2.pix...)
		for i := 0; i < len(pix); i += 4 {
			s := in1.pix[i : i+4]
			if s[3] == 0 {
				continue
			}
			d := pix[i : i+4]
			if p.mode == blendNormal {
				ia := 255 - uint32(s[3])
				d[0] = uint8(uint32(s[0]) + div255(uint32(d[0])*ia))
				d[1] = uint8(uint32(s[1]) + div255(uint32(d[1])*ia))
				d[2] = uint8(uint32(s[2]) + div255(uint32(d[2])*ia))
				d[3] = uint8(uint32(s[3]) + div255(uint32(d[3])*ia))
			} else {
				blendPixel(d, uint32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3]), p.mode)
			}
		}
		return fimage{pix: pix, region: run.region, linear: p.linear}, true
	case tagFeComposite:
		in1 := toSpace(run.input(p.in1), p.linear)
		in2 := toSpace(run.input(p.in2), p.linear)
		pix := run.blank()
		if p.op == opArithmetic {
			arithmetic(p.k, in1.pix, in2.pix, pix)
			return fimage{pix: pix, region: run.region, linear: p.linear}, true
		}
		copy(pix, in2.pix)
		for i := 0; i < len(pix); i += 4 {
			s := in1.pix[i : i+4]
			d := pix[i : i+4]
			sa, da := uint32(s[3]), uint32(d[3])
			switch p.op {
			case opOver:
				if sa == 0 {
					continue
				}
				ia := 255 - sa
				for c := 0; c < 4; c++ {
					d[c] = uint8(uint32(s[c]) + div255(uint32(d[c])*ia))
				}
			case opIn:
				for c := 0; c < 4; c++ {
					d[c] = uint8(div255(uint32(s[c]) * da))
				}
			case opOut:
				for c := 0; c < 4; c++ {
					d[c] = uint8(div255(uint32(s[c]) * (255 - da)))
				}
			case opAtop:
				for c := 0; c < 4; c++ {
					d[c] = uint8(div255(uint32(s[c])*da + uint32(d[c])*(255-sa)))
				}
			case opXor:
				for c := 0; c < 4; c++ {
					d[c] = uint8(div255(uint32(s[c])*(255-da) + uint32(d[c])*(255-sa)))
				}
			}
		}
		return fimage{pix: pix, region: run.region, linear: p.linear}, true
	case tagFeMerge:
		pix := run.blank()
		for _, in := range p.merge {
			img := toSpace(run.input(in), p.linear)
			drawOver(pix, img.pix, w, h, 0, 0)
		}
		return fimage{pix: pix, region: run.region, linear: p.linear}, true
	case tagFeColorMatrix:
		in := toSpace(run.input(p.in1), p.linear)
		pix := append([]uint8(nil), in.pix...)
		demultiply(pix)
		colorMatrix(p, pix)
		multiply(pix)
		return fimage{pix: pix, region: run.region, linear: p.linear}, true
	case tagFeComponentTransfer:
		in := toSpace(run.input(p.in1), p.linear)
		pix := append([]uint8(nil), in.pix...)
		demultiply(pix)
		componentTransfer(p, pix)
		multiply(pix)
		return fimage{pix: pix, region: run.region, linear: p.linear}, true
	}
	// Unsupported primitive: transparent black.
	return fimage{pix: run.blank(), region: run.region}, true
}

func satInt(v float32) int {
	if v != v {
		return 0
	}
	if v > 1e9 {
		return 1e9
	}
	if v < -1e9 {
		return -1e9
	}
	return int(v) // truncation toward zero, like Rust's `as i32`
}

// stdDev is resvg's resolve_std_dev: device-space sigmas and whether the box
// blur applies. ok is false when the blur is a no-op.
func (run *filterRun) stdDev(sx, sy float64) (float64, float64, bool, bool) {
	tx, ty := tsScale(run.ts)
	dx, dy := float32(sx)*float32(tx), float32(sy)*float32(ty)
	if dx == 0 && dy == 0 {
		return 0, 0, false, false
	}
	if dx < 0.05 {
		dx = 0
	}
	if dy < 0.05 {
		dy = 0
	}
	box := dx >= 2 || dy >= 2
	// A sigma beyond any plausible region averages everything anyway; keep the
	// kernel sizes in int range.
	const maxSigma = 1e6
	return math.Min(float64(dx), maxSigma), math.Min(float64(dy), maxSigma), box, true
}

func (run *filterRun) dropShadow(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	in := run.input(p.in1)
	tx, ty := tsScale(run.ts)
	dx, dy := float32(p.dx)*float32(tx), float32(p.dy)*float32(ty)
	inp := toSpace(in, p.linear)
	pix := run.blank()
	shadow := append([]uint8(nil), inp.pix...)
	if sx, sy, box, ok := run.stdDev(p.stdX, p.stdY); ok {
		if box {
			boxBlur(sx, sy, shadow, w, h)
		} else {
			iirBlur(sx, sy, shadow, w, h)
		}
	}
	// Flood the blurred alpha with the shadow colour.
	col := [3]float32{float32(p.color.r), float32(p.color.g), float32(p.color.b)}
	a8 := float32(math.Round(float64(float32(p.alpha) * 255)))
	base := a8 / 255
	for i := 0; i < len(shadow); i += 4 {
		ca := float32(shadow[i+3]) / 255.0
		a := float32(base * ca)
		shadow[i] = satU8(float32(col[0]*a) + 0.5)
		shadow[i+1] = satU8(float32(col[1]*a) + 0.5)
		shadow[i+2] = satU8(float32(col[2]*a) + 0.5)
		shadow[i+3] = satU8(a*255 + 0.5)
	}
	sh := fimage{pix: shadow, linear: false}
	sh = toSpace(sh, p.linear)
	sh.linear = p.linear
	drawOver(pix, sh.pix, w, h, satInt(dx), satInt(dy))
	drawOver(pix, inp.pix, w, h, 0, 0)
	return fimage{pix: pix, region: run.region, linear: p.linear}, true
}

func arithmetic(k [4]float64, s1, s2, dst []uint8) {
	k1, k2, k3, k4 := float32(k[0]), float32(k[1]), float32(k[2]), float32(k[3])
	calc := func(i1, i2 uint8, max float32) float32 {
		a := float32(i1) / 255.0
		b := float32(i2) / 255.0
		v := float32(float32(float32(k1*a)*b)+float32(k2*a)) + float32(k3*b) + k4
		if v > max {
			return max
		}
		if v < 0 {
			return 0
		}
		return v
	}
	for i := 0; i < len(dst); i += 4 {
		a := calc(s1[i+3], s2[i+3], 1)
		if a > -1e-6 && a < 1e-6 {
			continue
		}
		dst[i] = satU8(float32(calc(s1[i], s2[i], a)) * 255)
		dst[i+1] = satU8(float32(calc(s1[i+1], s2[i+1], a)) * 255)
		dst[i+2] = satU8(float32(calc(s1[i+2], s2[i+2], a)) * 255)
		dst[i+3] = satU8(a * 255)
	}
}

func fromNorm(c float32) uint8 {
	if c > 1 {
		c = 1
	} else if c < 0 || c != c {
		c = 0
	}
	return uint8(float32(c * 255))
}

func colorMatrix(p *fprim, pix []uint8) {
	switch p.cmKind {
	case cmMatrix:
		var m [20]float32
		for i, v := range p.cmValues {
			if i < 20 {
				m[i] = float32(v)
			}
		}
		for i := 0; i < len(pix); i += 4 {
			r, g, b, a := float32(pix[i])/255, float32(pix[i+1])/255, float32(pix[i+2])/255, float32(pix[i+3])/255
			nr := float32(r*m[0]) + float32(g*m[1]) + float32(b*m[2]) + float32(a*m[3]) + m[4]
			ng := float32(r*m[5]) + float32(g*m[6]) + float32(b*m[7]) + float32(a*m[8]) + m[9]
			nb := float32(r*m[10]) + float32(g*m[11]) + float32(b*m[12]) + float32(a*m[13]) + m[14]
			na := float32(r*m[15]) + float32(g*m[16]) + float32(b*m[17]) + float32(a*m[18]) + m[19]
			pix[i], pix[i+1], pix[i+2], pix[i+3] = fromNorm(nr), fromNorm(ng), fromNorm(nb), fromNorm(na)
		}
	case cmSaturate:
		v := float32(math.Max(0, p.cmValues[0]))
		m := [9]float32{
			0.213 + 0.787*v, 0.715 - 0.715*v, 0.072 - 0.072*v,
			0.213 - 0.213*v, 0.715 + 0.285*v, 0.072 - 0.072*v,
			0.213 - 0.213*v, 0.715 - 0.715*v, 0.072 + 0.928*v,
		}
		apply3(m, pix)
	case cmHueRotate:
		ang := float64(float32(p.cmValues[0])) * math.Pi / 180
		a1, a2 := float32(math.Cos(ang)), float32(math.Sin(ang))
		m := [9]float32{
			0.213 + 0.787*a1 - 0.213*a2, 0.715 - 0.715*a1 - 0.715*a2, 0.072 - 0.072*a1 + 0.928*a2,
			0.213 - 0.213*a1 + 0.143*a2, 0.715 + 0.285*a1 + 0.140*a2, 0.072 - 0.072*a1 - 0.283*a2,
			0.213 - 0.213*a1 - 0.787*a2, 0.715 - 0.715*a1 + 0.715*a2, 0.072 + 0.928*a1 + 0.072*a2,
		}
		apply3(m, pix)
	case cmLuminance:
		for i := 0; i < len(pix); i += 4 {
			r, g, b := float32(pix[i])/255, float32(pix[i+1])/255, float32(pix[i+2])/255
			na := float32(r*0.2125) + float32(g*0.7154) + float32(b*0.0721)
			pix[i], pix[i+1], pix[i+2], pix[i+3] = 0, 0, 0, fromNorm(na)
		}
	}
}

func apply3(m [9]float32, pix []uint8) {
	for i := 0; i < len(pix); i += 4 {
		r, g, b := float32(pix[i])/255, float32(pix[i+1])/255, float32(pix[i+2])/255
		pix[i] = fromNorm(float32(r*m[0]) + float32(g*m[1]) + float32(b*m[2]))
		pix[i+1] = fromNorm(float32(r*m[3]) + float32(g*m[4]) + float32(b*m[5]))
		pix[i+2] = fromNorm(float32(r*m[6]) + float32(g*m[7]) + float32(b*m[8]))
	}
}

func (f *transferFn) dummy() bool {
	switch f.kind {
	case 0:
		return true
	case 1, 2:
		return len(f.table) == 0
	}
	return false
}

func (f *transferFn) apply(v uint8) uint8 {
	c := float32(v) / 255
	switch f.kind {
	case 1:
		n := len(f.table) - 1
		k := int(math.Floor(float64(c * float32(n))))
		if k > n {
			k = n
		}
		if k == n {
			c = float32(f.table[k])
		} else {
			vk, vk1 := float32(f.table[k]), float32(f.table[k+1])
			c = vk + float32(float32(c-float32(k)/float32(n))*float32(n))*(vk1-vk)
		}
	case 2:
		n := len(f.table)
		k := int(math.Floor(float64(c * float32(n))))
		if k > n-1 {
			k = n - 1
		}
		c = float32(f.table[k])
	case 3:
		c = float32(float32(f.a)*c) + float32(f.b)
	case 4:
		c = float32(float32(f.a)*float32(math.Pow(float64(c), f.b))) + float32(f.off)
	}
	return fromNorm(c)
}

func componentTransfer(p *fprim, pix []uint8) {
	for i := 0; i < len(pix); i += 4 {
		if !p.funcs[0].dummy() {
			pix[i] = p.funcs[0].apply(pix[i])
		}
		if !p.funcs[2].dummy() {
			pix[i+2] = p.funcs[2].apply(pix[i+2])
		}
		if !p.funcs[1].dummy() {
			pix[i+1] = p.funcs[1].apply(pix[i+1])
		}
		if !p.funcs[3].dummy() {
			pix[i+3] = p.funcs[3].apply(pix[i+3])
		}
	}
}

// ---- blurs ---------------------------------------------------------------------

const boxSteps = 5

func createBoxGauss(sigma float32) [boxSteps]int {
	var sizes [boxSteps]int
	if sigma > 0 {
		n := float32(boxSteps)
		wIdeal := float32(math.Sqrt(float64(float32(12*sigma*sigma)/n))) + 1
		wl := int(math.Floor(float64(wIdeal)))
		if wl%2 == 0 {
			wl--
		}
		wu := wl + 2
		wlf := float32(wl)
		mIdeal := (float32(12*sigma*sigma) - float32(n*wlf*wlf) - float32(4*n*wlf) - 3*n) / (-4*wlf - 4)
		m := int(math.Round(float64(mIdeal)))
		for i := range sizes {
			if i < m {
				sizes[i] = wl
			} else {
				sizes[i] = wu
			}
		}
		return sizes
	}
	for i := range sizes {
		sizes[i] = 1
	}
	return sizes
}

func boxBlur(sigmaX, sigmaY float64, pix []uint8, w, h int) {
	bh := createBoxGauss(float32(sigmaX))
	bv := createBoxGauss(float32(sigmaY))
	tmp := make([]uint8, len(pix))
	for i := 0; i < boxSteps; i++ {
		rh := (bh[i] - 1) / 2
		rv := (bv[i] - 1) / 2
		// vertical: pix -> tmp, then horizontal: tmp -> pix
		boxPass(tmp, pix, w, h, rv, w*4, 4, h, w)
		boxPass(pix, tmp, w, h, rh, 4, w*4, w, h)
	}
}

// boxPass blurs lines of length n along the given step, for `lines` lines whose
// starts are lineStep bytes apart; zero padded, output rounded to nearest even.
func boxPass(dst, src []uint8, w, h, radius, step, lineStep, n, lines int) {
	if radius <= 0 {
		copy(dst, src)
		return
	}
	iarr := float32(1) / float32(radius+radius+1)
	var pre [4][]int32
	for c := range pre {
		pre[c] = make([]int32, n+1)
	}
	for l := 0; l < lines; l++ {
		base := l * lineStep
		for c := 0; c < 4; c++ {
			pc := pre[c]
			for i := 0; i < n; i++ {
				pc[i+1] = pc[i] + int32(src[base+i*step+c])
			}
		}
		for i := 0; i < n; i++ {
			lo, hi := i-radius, i+radius+1
			if lo < 0 {
				lo = 0
			}
			if hi > n {
				hi = n
			}
			for c := 0; c < 4; c++ {
				sum := pre[c][hi] - pre[c][lo]
				v := float32(sum) * iarr
				dst[base+i*step+c] = satU8(float32(math.RoundToEven(float64(v))))
			}
		}
	}
}

func iirBlur(sigmaX, sigmaY float64, pix []uint8, w, h int) {
	buf := make([]float64, w*h)
	for ch := 0; ch < 4; ch++ {
		for i := 0; i < w*h; i++ {
			buf[i] = float64(pix[i*4+ch]) / 255.0
		}
		gaussianIIR2D(buf, w, h, sigmaX, sigmaY, 4)
		for i := 0; i < w*h; i++ {
			v := buf[i] * 255.0
			switch {
			case v != v || v <= 0:
				pix[i*4+ch] = 0
			case v >= 255:
				pix[i*4+ch] = 255
			default:
				pix[i*4+ch] = uint8(v)
			}
		}
	}
}

func iirCoefficients(sigma float64, steps int) (float64, float64) {
	lambda := (sigma * sigma) / (2.0 * float64(steps))
	dnu := (1.0 + 2.0*lambda - math.Sqrt(1.0+4.0*lambda)) / (2.0 * lambda)
	return lambda, dnu
}

func gaussianIIR2D(buf []float64, w, h int, sigmaX, sigmaY float64, steps int) {
	lambdaX, dnuX := 1.0, 1.0
	if sigmaX > 0 {
		lambdaX, dnuX = iirCoefficients(sigmaX, steps)
		for y := 0; y < h; y++ {
			idx := w * y
			for s := 0; s < steps; s++ {
				for x := 1; x < w; x++ {
					buf[idx+x] += dnuX * buf[idx+x-1]
				}
				for x := w - 1; x > 0; x-- {
					buf[idx+x-1] += dnuX * buf[idx+x]
				}
			}
		}
	}
	lambdaY, dnuY := 1.0, 1.0
	if sigmaY > 0 {
		lambdaY, dnuY = iirCoefficients(sigmaY, steps)
		for x := 0; x < w; x++ {
			idx := x
			for s := 0; s < steps; s++ {
				for y := w; y < len(buf); y += w {
					buf[idx+y] += dnuY * buf[idx+y-w]
				}
				for y := len(buf) - w; y > 0; y -= w {
					buf[idx+y-w] += dnuY * buf[idx+y]
				}
			}
		}
	}
	post := math.Pow(math.Sqrt(dnuX*dnuY)/math.Sqrt(lambdaX*lambdaY), float64(2*steps))
	for i := range buf {
		buf[i] *= post
	}
}
