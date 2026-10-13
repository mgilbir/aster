package raster

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
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
	// owned is set on an image the run allocated for itself, charged to the
	// canvas budget until it is freed; one that is the source, SourceAlpha
	// or another result (a primitive that passes its input through) is not.
	owned bool
}

type fresult struct {
	name string
	img  fimage
}

// filterRun is one filter applied to one canvas. Every buffer it allocates,
// results, colour-space copies and blur scratch, is charged to the canvas
// budget (Limits.MaxCanvasBytes, the pixel memory alive at once) before it
// is allocated and given back when it is dropped, so a chain of primitives
// whose results stay alive is bounded by WithMemoryLimit like any layer
// (C30).
//
// Every pass a primitive makes over the region runs a band of rows (or of
// columns) at a time, each band's pixels charged to MaxPixelOps before it
// runs (filterRun.work), which polls the render's context: a deadline
// reaches into a primitive's work, not only the boundaries between
// primitives, as the canvas passes and the image work do (ADR 0010). Once
// the budget or the context stops a pass, it runs no further band, the
// render has failed and the primitive's result is dropped.
type filterRun struct {
	r       *renderer
	w, h    int
	region  irect
	src     []uint8
	ts      matrix
	results []fresult
	alpha   fimage // SourceAlpha, made once a run (C28)
}

// work charges n pixel operations of filter work to MaxPixelOps, polling the
// render's context (chargeOps); it reports whether the run may go on. A run
// without a renderer is never stopped.
func (run *filterRun) work(n int) bool {
	if run.r == nil {
		return true
	}
	return run.r.chargeOps(n)
}

// rows makes one pass over the region, f(y0, y1) a band of rows at a time,
// cost pixel operations a pixel. It reports whether the pass ran to its end.
func (run *filterRun) rows(cost int, f func(y0, y1 int)) bool {
	return bandRows(run.w, run.h, cost, run.work, f)
}

// bandRows runs f over the h rows of an image w pixels wide, a band of about
// imageref.BandPixels pixels at a time, charging work cost pixel operations
// a pixel of the band before it runs (nil work: free). Once work refuses, no
// further band runs, and bandRows reports false.
func bandRows(w, h, cost int, work func(int) bool, f func(y0, y1 int)) bool {
	step := imageref.BandRows(w)
	for y := 0; y < h; y += step {
		y1 := min(y+step, h)
		if work != nil && !work(cost*w*(y1-y)) {
			return false
		}
		f(y, y1)
	}
	return true
}

// unmultiplyCost is what a pixel of a pass that demultiplies, works on
// the colour and multiplies again (a colour-space conversion, a colour
// matrix, a transfer function) is charged: three passes' work, made in one.
const unmultiplyCost = 3

// rowsOf is rows [y0, y1) of pix, an image w pixels wide.
func rowsOf(pix []uint8, w, y0, y1 int) []uint8 { return pix[y0*w*4 : y1*w*4] }

// alloc returns a zeroed buffer of n bytes charged to the canvas budget. Past
// the budget it stops the render (budget.Stop), as the primitives that
// allocate have no error to return.
func (run *filterRun) alloc(n int) []uint8 {
	run.charge(n)
	return make([]uint8, n)
}

// charge accounts for n bytes of filter memory about to be allocated.
func (run *filterRun) charge(n int) {
	r := run.r
	if r == nil {
		return
	}
	if n > r.lim.MaxCanvasBytes-r.canvasBytes {
		r.fail(fmt.Errorf("raster: a filter needs more than %d MiB of pixel memory at once (raise Limits.MaxCanvasBytes or WithMemoryLimit): %w",
			r.lim.MaxCanvasBytes>>20, errLimit))
		panic(&budget.Stop{Err: r.err})
	}
	r.canvasBytes += n
}

// uncharge gives back n bytes charged with charge.
func (run *filterRun) uncharge(n int) {
	if run.r != nil {
		run.r.canvasBytes -= n
	}
}

// free gives back an owned image's bytes; img must not be used again.
func (run *filterRun) free(img fimage) {
	if img.owned {
		run.uncharge(len(img.pix))
	}
}

// end gives back everything the run still holds.
func (run *filterRun) end() {
	for i := range run.results {
		run.free(run.results[i].img)
		run.results[i].img = fimage{}
	}
	run.free(run.alpha)
	run.alpha = fimage{}
}

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

// newFilterRun is a run of a filter over cv, at ctm-shifted coordinates ts.
func (r *renderer) newFilterRun(cv *canvas, ts matrix) *filterRun {
	return &filterRun{r: r, w: cv.w, h: cv.h, src: cv.pix, ts: ts}
}

func (r *renderer) applyFilter(sp *filterSpec, ts matrix, cv *canvas) bool {
	rc := transformRect32(sp.rect, ts)
	if !(rc.w() > 0 && rc.h() > 0) {
		return false
	}
	run := r.newFilterRun(cv, ts)
	defer run.end()
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
		if !r.poll(true) {
			return false
		}
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
		if !ok || r.err != nil {
			run.free(res)
			return false
		}
		if run.region != sub {
			var sub2 irect
			if p.kind == tagFeOffset {
				sub2 = irect{0, 0, run.region.w(), run.region.h()}
			} else {
				sub2 = irect{sub.x0 - run.region.x0, sub.y0 - run.region.y0, sub.x1 - run.region.x0, sub.y1 - run.region.y0}
			}
			var from []uint8
			if !res.owned {
				// An input passed through: clear a copy, not the input.
				from = res.pix
				res = fimage{pix: run.alloc(len(res.pix)), region: res.region, linear: res.linear, owned: true}
			}
			keep := sub2.intersect(irect{0, 0, run.w, run.h})
			if !run.rows(1, func(y0, y1 int) {
				if from != nil {
					copy(rowsOf(res.pix, run.w, y0, y1), rowsOf(from, run.w, y0, y1))
				}
				clearOutside(res.pix, run.w, keep, y0, y1)
			}) {
				run.free(res)
				return false
			}
			res.region = sub
		}
		run.results = append(run.results, fresult{name: p.result, img: res})
		// Drop results nobody reads any more (the last one is the output).
		if i != last {
			run.release(i, lastUse)
		}
	}
	if len(run.results) == 0 {
		return false
	}
	out := run.toSpace(run.results[len(run.results)-1].img, false)
	done := r.err == nil && run.rows(1, func(y0, y1 int) {
		copy(rowsOf(cv.pix, run.w, y0, y1), rowsOf(out.pix, run.w, y0, y1))
	})
	if done && len(out.pix) < len(cv.pix) {
		clear(cv.pix[len(out.pix):])
	}
	if out.owned && !run.held(out.pix) {
		run.free(out)
	}
	return done
}

// release drops the earlier results whose last reader has run; the newest
// is kept. A dropped result that another still live one passes through
// hands its bytes over to it.
func (run *filterRun) release(i int, lastUse map[string]int) {
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
		if res.img.owned {
			if k := run.alias(j); k >= 0 {
				run.results[k].img.owned = true
			} else {
				run.free(res.img)
			}
		}
		res.img = fimage{}
	}
}

// alias is the index of a live result other than j that holds result j's
// pixels, or -1.
func (run *filterRun) alias(j int) int {
	p := run.results[j].img.pix
	for k := range run.results {
		q := run.results[k].img.pix
		if k != j && len(q) > 0 && len(p) > 0 && &q[0] == &p[0] {
			return k
		}
	}
	return -1
}

// held reports whether pix is the pixels of a stored result.
func (run *filterRun) held(pix []uint8) bool {
	for i := range run.results {
		p := run.results[i].img.pix
		if len(p) > 0 && len(pix) > 0 && &p[0] == &pix[0] {
			return true
		}
	}
	return false
}

// clearOutside clears rows [y0, y1) of pix, an image w pixels wide, outside
// keep (within the image).
func clearOutside(pix []uint8, w int, keep irect, y0, y1 int) {
	if keep.empty() {
		clear(rowsOf(pix, w, y0, y1))
		return
	}
	for y := y0; y < y1; y++ {
		row := pix[y*w*4 : (y+1)*w*4]
		if y < keep.y0 || y >= keep.y1 {
			clear(row)
			continue
		}
		clear(row[:keep.x0*4])
		clear(row[keep.x1*4:])
	}
}

// input is the image a primitive reads: SourceGraphic, SourceAlpha or a
// named result. It is never owned by the caller, which must not change it.
func (run *filterRun) input(in finput) fimage {
	switch in.kind {
	case inAlpha:
		// Made once a run, however many primitives (or merge nodes) read it.
		if run.alpha.pix == nil {
			pix := run.alloc(len(run.src))
			run.alpha = fimage{pix: pix, region: run.region, owned: true}
			run.rows(1, func(y0, y1 int) {
				for i := y0*run.w*4 + 3; i < y1*run.w*4; i += 4 {
					pix[i] = run.src[i]
				}
			})
		}
		return fimage{pix: run.alpha.pix, region: run.region}
	case inRef:
		for j := len(run.results) - 1; j >= 0; j-- {
			if run.results[j].name == in.name && run.results[j].img.pix != nil {
				img := run.results[j].img
				img.owned = false
				return img
			}
		}
	}
	return fimage{pix: run.src, region: run.region}
}

func (run *filterRun) blank() []uint8 { return run.alloc(run.w * run.h * 4) }

// toSpace converts img to the requested colour space: img itself when it is
// in it already, otherwise a new image the caller owns (and frees).
func (run *filterRun) toSpace(img fimage, linear bool) fimage {
	if img.linear == linear {
		return img
	}
	pix := run.alloc(len(img.pix))
	tbl := &linearToSRGBTable
	if linear {
		tbl = &srgbToLinearTable
	}
	// Each pixel is converted on its own, so a band at a time is the same.
	run.rows(unmultiplyCost, func(y0, y1 int) {
		band := rowsOf(pix, run.w, y0, y1)
		copy(band, rowsOf(img.pix, run.w, y0, y1))
		demultiply(band)
		for i := 0; i < len(band); i += 4 {
			band[i] = tbl[band[i]]
			band[i+1] = tbl[band[i+1]]
			band[i+2] = tbl[band[i+2]]
		}
		multiply(band)
	})
	return fimage{pix: pix, region: img.region, linear: linear, owned: true}
}

// owned returns img's pixels as a buffer the caller owns: img's own when it
// owns them (a colour-space copy), a charged copy otherwise.
func (run *filterRun) ownCopy(img fimage) []uint8 {
	if img.owned {
		return img.pix
	}
	pix := run.alloc(len(img.pix))
	run.rows(1, func(y0, y1 int) { copy(rowsOf(pix, run.w, y0, y1), rowsOf(img.pix, run.w, y0, y1)) })
	return pix
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

// drawOver composites src onto rows [y0, y1) of dst (both w*h) at offset
// (dx,dy), source-over.
func drawOver(dst, src []uint8, w, h, dx, dy, y0, y1 int) {
	for y := y0; y < y1; y++ {
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
		run.rows(1, func(y0, y1 int) {
			for i := y0 * w * 4; i < y1*w*4; i += 4 {
				pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, satU8(a8)
			}
		})
		return fimage{pix: pix, region: run.region, owned: true}, true
	case tagFeOffset:
		in := run.input(p.in1)
		sx, sy := tsScale(run.ts)
		dx, dy := float32(p.dx)*float32(sx), float32(p.dy)*float32(sy)
		if math.Abs(float64(dx)) < 1e-6 && math.Abs(float64(dy)) < 1e-6 {
			return in, true
		}
		pix := run.blank()
		run.rows(1, func(y0, y1 int) { drawOver(pix, in.pix, w, h, satInt(dx), satInt(dy), y0, y1) })
		return fimage{pix: pix, region: run.region, linear: in.linear, owned: true}, true
	case tagFeGaussianBlur:
		in := run.input(p.in1)
		sx, sy, box, ok := run.stdDev(p.stdX, p.stdY)
		if !ok {
			return in, true
		}
		pix := run.ownCopy(run.toSpace(in, p.linear))
		run.blur(sx, sy, box, pix)
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
	case tagFeDropShadow:
		return run.dropShadow(p)
	case tagFeBlend:
		in1 := run.toSpace(run.input(p.in1), p.linear)
		defer run.free(in1)
		pix := run.ownCopy(run.toSpace(run.input(p.in2), p.linear))
		run.rows(1, func(y0, y1 int) {
			for i := y0 * w * 4; i < y1*w*4; i += 4 {
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
		})
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
	case tagFeComposite:
		in1 := run.toSpace(run.input(p.in1), p.linear)
		defer run.free(in1)
		in2 := run.toSpace(run.input(p.in2), p.linear)
		defer run.free(in2)
		pix := run.blank()
		if p.op == opArithmetic {
			run.rows(1, func(y0, y1 int) {
				arithmetic(p.k, rowsOf(in1.pix, w, y0, y1), rowsOf(in2.pix, w, y0, y1), rowsOf(pix, w, y0, y1))
			})
			return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
		}
		run.rows(1, func(y0, y1 int) {
			compositeOp(p.op, rowsOf(in1.pix, w, y0, y1), rowsOf(in2.pix, w, y0, y1), rowsOf(pix, w, y0, y1))
		})
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
	case tagFeMerge:
		pix := run.blank()
		// Each node is a pass over the region, polled a band at a time as a
		// primitive is (C28).
		for _, in := range p.merge {
			img := run.toSpace(run.input(in), p.linear)
			ok := run.r == nil || run.r.err == nil
			ok = ok && run.rows(1, func(y0, y1 int) { drawOver(pix, img.pix, w, h, 0, 0, y0, y1) })
			run.free(img)
			if !ok {
				run.free(fimage{pix: pix, owned: true})
				return fimage{}, false
			}
		}
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
	case tagFeColorMatrix:
		pix := run.ownCopy(run.toSpace(run.input(p.in1), p.linear))
		// Each pixel is its own: a band at a time is the same.
		run.rows(unmultiplyCost, func(y0, y1 int) {
			band := rowsOf(pix, w, y0, y1)
			demultiply(band)
			colorMatrix(p, band)
			multiply(band)
		})
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
	case tagFeComponentTransfer:
		pix := run.ownCopy(run.toSpace(run.input(p.in1), p.linear))
		run.rows(unmultiplyCost, func(y0, y1 int) {
			band := rowsOf(pix, w, y0, y1)
			demultiply(band)
			componentTransfer(p, band)
			multiply(band)
		})
		return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
	}
	// Unsupported primitive: transparent black.
	return fimage{pix: run.blank(), region: run.region, owned: true}, true
}

// compositeOp is feComposite's operator op (any but arithmetic): s1 composited
// onto a copy of s2, into dst.
func compositeOp(op uint8, s1, s2, dst []uint8) {
	copy(dst, s2)
	for i := 0; i < len(dst); i += 4 {
		s := s1[i : i+4]
		d := dst[i : i+4]
		sa, da := uint32(s[3]), uint32(d[3])
		switch op {
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
}

// blur blurs pix in place (resvg's box blur from sigma 2, IIR below it),
// charging its scratch to the canvas budget while it lives. Each of a blur's
// passes visits every channel of every pixel, and is charged a pixel
// operation a channel: a blur pass costs about four times a primitive's.
func (run *filterRun) blur(sx, sy float64, box bool, pix []uint8) {
	w, h := run.w, run.h
	if box {
		// A copy of the image, and four prefix-sum lines of one row or
		// column at a time.
		n := len(pix) + 16*(max(w, h)+1)
		run.charge(n)
		boxBlur(sx, sy, pix, w, h, run.work)
		run.uncharge(n)
		return
	}
	n := 8 * w * h // a float64 a pixel
	run.charge(n)
	iirBlur(sx, sy, pix, w, h, run.work)
	run.uncharge(n)
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
	inp := run.toSpace(in, p.linear)
	defer run.free(inp)
	pix := run.blank()
	shadow := run.alloc(len(inp.pix))
	run.rows(1, func(y0, y1 int) { copy(rowsOf(shadow, w, y0, y1), rowsOf(inp.pix, w, y0, y1)) })
	if sx, sy, box, ok := run.stdDev(p.stdX, p.stdY); ok {
		run.blur(sx, sy, box, shadow)
	}
	// Flood the blurred alpha with the shadow colour.
	col := [3]float32{float32(p.color.r), float32(p.color.g), float32(p.color.b)}
	a8 := float32(math.Round(float64(float32(p.alpha) * 255)))
	base := a8 / 255
	run.rows(1, func(y0, y1 int) {
		for i := y0 * w * 4; i < y1*w*4; i += 4 {
			ca := float32(shadow[i+3]) / 255.0
			a := float32(base * ca)
			shadow[i] = satU8(float32(col[0]*a) + 0.5)
			shadow[i+1] = satU8(float32(col[1]*a) + 0.5)
			shadow[i+2] = satU8(float32(col[2]*a) + 0.5)
			shadow[i+3] = satU8(a*255 + 0.5)
		}
	})
	sh := fimage{pix: shadow, linear: false, owned: true}
	if conv := run.toSpace(sh, p.linear); conv.owned && &conv.pix[0] != &shadow[0] {
		run.free(sh)
		sh = conv
	}
	sh.linear = p.linear
	run.rows(1, func(y0, y1 int) { drawOver(pix, sh.pix, w, h, satInt(dx), satInt(dy), y0, y1) })
	run.free(sh)
	run.rows(1, func(y0, y1 int) { drawOver(pix, inp.pix, w, h, 0, 0, y0, y1) })
	return fimage{pix: pix, region: run.region, linear: p.linear, owned: true}, true
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

// boxBlur is resvg's box blur, each of its passes a band of lines at a time,
// charged to work (nil: free) a pixel operation a channel; it stops, pix
// partly blurred, once work refuses.
func boxBlur(sigmaX, sigmaY float64, pix []uint8, w, h int, work func(int) bool) {
	bh := createBoxGauss(float32(sigmaX))
	bv := createBoxGauss(float32(sigmaY))
	tmp := make([]uint8, len(pix))
	for i := 0; i < boxSteps; i++ {
		rh := (bh[i] - 1) / 2
		rv := (bv[i] - 1) / 2
		// vertical: pix -> tmp, then horizontal: tmp -> pix
		if !boxPass(tmp, pix, w, h, rv, w*4, 4, h, w, work) || !boxPass(pix, tmp, w, h, rh, 4, w*4, w, h, work) {
			return
		}
	}
}

// boxPass blurs lines of length n along the given step, for `lines` lines whose
// starts are lineStep bytes apart; zero padded, output rounded to nearest even.
// It runs a band of lines (about imageref.BandPixels pixels) at a time, each
// charged to work first, and reports whether it ran to its end.
func boxPass(dst, src []uint8, w, h, radius, step, lineStep, n, lines int, work func(int) bool) bool {
	if radius <= 0 {
		// A copy, a band of rows at a time.
		return bandRows(w, h, 4, work, func(y0, y1 int) { copy(rowsOf(dst, w, y0, y1), rowsOf(src, w, y0, y1)) })
	}
	iarr := float32(1) / float32(radius+radius+1)
	var pre [4][]int32
	for c := range pre {
		pre[c] = make([]int32, n+1)
	}
	per := imageref.BandRows(n)
	for l0 := 0; l0 < lines; l0 += per {
		l1 := min(l0+per, lines)
		if work != nil && !work(4*n*(l1-l0)) {
			return false
		}
		for l := l0; l < l1; l++ {
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
	return true
}

// iirBlur is resvg's IIR blur, a channel at a time; each pass over a
// channel runs a band of rows or columns at a time, charged to work (nil:
// free) a pixel operation a pixel a sweep. It stops, pix partly blurred,
// once work refuses.
func iirBlur(sigmaX, sigmaY float64, pix []uint8, w, h int, work func(int) bool) {
	buf := make([]float64, w*h)
	for ch := 0; ch < 4; ch++ {
		if !bandRows(w, h, 1, work, func(y0, y1 int) {
			for i := y0 * w; i < y1*w; i++ {
				buf[i] = float64(pix[i*4+ch]) / 255.0
			}
		}) {
			return
		}
		if !gaussianIIR2D(buf, w, h, sigmaX, sigmaY, 4, work) {
			return
		}
		if !bandRows(w, h, 1, work, func(y0, y1 int) {
			for i := y0 * w; i < y1*w; i++ {
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
		}) {
			return
		}
	}
}

func iirCoefficients(sigma float64, steps int) (float64, float64) {
	lambda := (sigma * sigma) / (2.0 * float64(steps))
	dnu := (1.0 + 2.0*lambda - math.Sqrt(1.0+4.0*lambda)) / (2.0 * lambda)
	return lambda, dnu
}

// gaussianIIR2D filters buf (w x h) in place, rows then columns, steps
// sweeps each way, and scales it. Each pass runs a band of rows or columns
// at a time, charged to work first (a pixel operation a pixel a sweep); it
// reports whether it ran to its end.
func gaussianIIR2D(buf []float64, w, h int, sigmaX, sigmaY float64, steps int, work func(int) bool) bool {
	lambdaX, dnuX := 1.0, 1.0
	if sigmaX > 0 {
		lambdaX, dnuX = iirCoefficients(sigmaX, steps)
		if !bandRows(w, h, 2*steps, work, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
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
		}) {
			return false
		}
	}
	lambdaY, dnuY := 1.0, 1.0
	if sigmaY > 0 {
		lambdaY, dnuY = iirCoefficients(sigmaY, steps)
		// A band of columns at a time (the image transposed, h wide).
		if !bandRows(h, w, 2*steps, work, func(x0, x1 int) {
			for x := x0; x < x1; x++ {
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
		}) {
			return false
		}
	}
	post := math.Pow(math.Sqrt(dnuX*dnuY)/math.Sqrt(lambdaX*lambdaY), float64(2*steps))
	return bandRows(w, h, 1, work, func(y0, y1 int) {
		for i := y0 * w; i < y1*w; i++ {
			buf[i] *= post
		}
	})
}
