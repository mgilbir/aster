package raster

import (
	"context"
	"math"
	"slices"
)

// The scan converter samples each pixel row at ssN evenly spaced sub-scanlines
// and quantises edge crossings to 1/subN of a pixel horizontally. That is the
// same sampling grid tiny-skia's supersampler uses (4x4), which is what makes
// edge pixels agree with resvg output; the winding rule is evaluated exactly
// per sub-scanline so overlapping geometry (self-intersecting strokes, text
// with overlapping contours) composes correctly under both nonzero and
// even-odd.

type edge struct {
	x0, dxdy float64 // x at y0 and dx/dy, in pixel units
	y0       float64 // start y in sub-scanline units
	ks, ke   int32   // first and last+1 sub-scanline sampled by this edge
	dir      int8
}

type crossing struct {
	x   int32
	dir int8
}

// spanSink receives one pixel row of coverage at a time: cov[i] is the
// coverage (0-255) of pixel x0+i on row y.
type spanSink interface {
	blitRow(y, x0 int, cov []uint8)
}

type rasterizer struct {
	clip       irect
	ssShift    uint // log2 sub-scanlines per pixel
	subShift   uint // log2 horizontal subdivisions per pixel
	edges      []edge
	minK, maxK int32
	sorted     []int32
	bucket     []int32
	active     []int32
	cross      []crossing
	acc, diff  []int32
	cov        []uint8
	covTable   []uint8
	aaTable    []uint8
	crispTable []uint8
	fx0, fx1   float64
	qx0, qx1   int32
	aa         bool
	haveEdges  bool
	minX, maxX float64

	ctx     context.Context // polled every few rows; nil = never cancelled
	work    int             // pixels processed by fill since the caller last reset it
	workCap int             // fill stops once work exceeds this (0 = no cap)
	stopped bool            // the last fill was cut short (cancelled or over the cap)
}

func newRasterizer() *rasterizer {
	r := &rasterizer{}
	r.setAA(true)
	return r
}

func (r *rasterizer) setAA(aa bool) {
	r.aa = aa
	if aa {
		r.ssShift, r.subShift = aaSS, aaSub
	} else {
		r.ssShift, r.subShift = 0, 0
	}
	total := 1 << (r.ssShift + r.subShift)
	if aa {
		if len(r.aaTable) != total+1 {
			r.aaTable = makeCovTable(total)
		}
		r.covTable = r.aaTable
	} else {
		if len(r.crispTable) != total+1 {
			r.crispTable = makeCovTable(total)
		}
		r.covTable = r.crispTable
	}
}

func makeCovTable(total int) []uint8 {
	t := make([]uint8, total+1)
	for i := range t {
		t[i] = uint8((i*255 + total/2) / total)
	}
	return t
}

// begin resets the rasterizer for a new shape clipped to clip.
func (r *rasterizer) begin(clip irect) {
	r.clip = clip
	r.edges = r.edges[:0]
	r.minK, r.maxK = math.MaxInt32, math.MinInt32
	r.fx0, r.fx1 = float64(clip.x0), float64(clip.x1)
	r.minX, r.maxX = math.Inf(1), math.Inf(-1)
}

// addLine adds one polygon edge in device coordinates.
func (r *rasterizer) addLine(p, q point) {
	if p.y == q.y || p.y != p.y || q.y != q.y || p.x != p.x || q.x != q.x {
		return
	}
	dir := int8(1)
	if p.y > q.y {
		p, q = q, p
		dir = -1
	}
	ss := float64(int(1) << r.ssShift)
	lo := float64(int32(r.clip.y0) << r.ssShift)
	hi := float64(int32(r.clip.y1) << r.ssShift)
	ksf := math.Ceil(p.y*ss - 0.5)
	kef := math.Ceil(q.y*ss - 0.5)
	if ksf < lo {
		ksf = lo
	}
	if kef > hi {
		kef = hi
	}
	if ksf >= kef {
		return
	}
	ks, ke := int32(ksf), int32(kef)
	dxdy := (q.x - p.x) / (q.y - p.y)
	r.edges = append(r.edges, edge{x0: p.x, dxdy: dxdy, y0: p.y * ss, ks: ks, ke: ke, dir: dir})
	if ks < r.minK {
		r.minK = ks
	}
	if ke > r.maxK {
		r.maxK = ke
	}
}

// addPolys adds every subpath of f as a closed polygon.
func (r *rasterizer) addPolys(f *flat) {
	for _, s := range f.subs {
		pts := f.pts[s.start:s.end]
		if len(pts) < 2 {
			continue
		}
		for i := 0; i+1 < len(pts); i++ {
			r.addLine(pts[i], pts[i+1])
		}
		r.addLine(pts[len(pts)-1], pts[0])
	}
}

// fill scan-converts the accumulated edges and sends coverage to sink.
func (r *rasterizer) fill(evenOdd bool, sink spanSink) {
	r.stopped = false
	if len(r.edges) == 0 || r.minK >= r.maxK {
		return
	}
	ssN := int32(1) << r.ssShift
	subN := int32(1) << r.subShift
	W := r.clip.w()
	if cap(r.acc) < W+2 {
		r.acc = make([]int32, W+2)
		r.diff = make([]int32, W+2)
		r.cov = make([]uint8, W+2)
	}
	acc := r.acc[:W+2]
	diff := r.diff[:W+2]
	cov := r.cov[:W+2]
	cx0 := int32(r.clip.x0)
	qmin := int32(0)
	qmax := int32(W) << r.subShift

	// Bucket edges by starting sub-scanline (counting sort).
	nk := int(r.maxK - r.minK)
	if cap(r.bucket) < nk+1 {
		r.bucket = make([]int32, nk+1)
	}
	bucket := r.bucket[:nk+1]
	for i := range bucket {
		bucket[i] = 0
	}
	for i := range r.edges {
		bucket[r.edges[i].ks-r.minK+1]++
	}
	for i := 1; i <= nk; i++ {
		bucket[i] += bucket[i-1]
	}
	if cap(r.sorted) < len(r.edges) {
		r.sorted = make([]int32, len(r.edges))
	}
	sorted := r.sorted[:len(r.edges)]
	for i := range r.edges {
		b := r.edges[i].ks - r.minK
		sorted[bucket[b]] = int32(i)
		bucket[b]++
	}
	active := r.active[:0]
	cross := r.cross[:0]

	rowStart := r.minK >> r.ssShift
	rowEnd := (r.maxK - 1) >> r.ssShift
	next := 0 // next index into sorted
	fx := float64(subN)
	invSS := 1 / float64(ssN)
	xoff := float64(cx0)

	for row := rowStart; row <= rowEnd; row++ {
		if row&31 == 0 {
			if r.ctx != nil && r.ctx.Err() != nil {
				r.stopped = true
				break
			}
		}
		minPx, maxPx := int32(math.MaxInt32), int32(-1)
		for s := int32(0); s < ssN; s++ {
			k := row<<r.ssShift + s
			if k < r.minK || k >= r.maxK {
				continue
			}
			// Retire finished edges, admit new ones.
			for i := 0; i < len(active); {
				if r.edges[active[i]].ke <= k {
					active[i] = active[len(active)-1]
					active = active[:len(active)-1]
				} else {
					i++
				}
			}
			for next < len(sorted) && r.edges[sorted[next]].ks <= k {
				active = append(active, sorted[next])
				next++
			}
			if len(active) == 0 {
				continue
			}
			yc := (float64(k) + 0.5) * invSS
			cross = cross[:0]
			for _, ei := range active {
				e := &r.edges[ei]
				x := e.x0 + (yc-e.y0*invSS)*e.dxdy
				qf := math.Floor((x-xoff)*fx + 0.5)
				var q int32
				if qf <= float64(qmin) {
					q = qmin
				} else if qf >= float64(qmax) {
					q = qmax
				} else {
					q = int32(qf)
				}
				cross = append(cross, crossing{q, e.dir})
			}
			if len(cross) > 1 {
				if len(cross) <= 24 {
					for i := 1; i < len(cross); i++ {
						c := cross[i]
						j := i - 1
						for j >= 0 && cross[j].x > c.x {
							cross[j+1] = cross[j]
							j--
						}
						cross[j+1] = c
					}
				} else {
					slices.SortFunc(cross, func(a, b crossing) int { return int(a.x) - int(b.x) })
				}
			}
			w := int32(0)
			var start int32
			for _, c := range cross {
				inBefore := w != 0
				if evenOdd {
					inBefore = w&1 != 0
				}
				w += int32(c.dir)
				inAfter := w != 0
				if evenOdd {
					inAfter = w&1 != 0
				}
				if !inBefore && inAfter {
					start = c.x
				} else if inBefore && !inAfter {
					qa, qb := start, c.x
					if qb <= qa {
						continue
					}
					pa := qa >> r.subShift
					pb := qb >> r.subShift
					if pa < minPx {
						minPx = pa
					}
					if pb > maxPx {
						maxPx = pb
					}
					if pa == pb {
						acc[pa] += qb - qa
					} else {
						acc[pa] += subN - (qa & (subN - 1))
						acc[pb] += qb & (subN - 1)
						if pb > pa+1 {
							diff[pa+1] += subN
							diff[pb] -= subN
						}
					}
				}
			}
		}
		if maxPx < 0 {
			continue
		}
		if maxPx >= int32(W) {
			maxPx = int32(W) - 1
		}
		if minPx > maxPx {
			// Only the zero-width tail beyond the clip was touched.
			acc[minPx] = 0
			diff[minPx] = 0
			continue
		}
		run := int32(0)
		n := int(maxPx-minPx) + 1
		r.work += n
		if r.workCap > 0 && r.work > r.workCap {
			// Over the pixel budget: drop this row's accumulators and stop.
			for i := 0; i < n; i++ {
				acc[minPx+int32(i)] = 0
				diff[minPx+int32(i)] = 0
			}
			acc[maxPx+1] = 0
			diff[maxPx+1] = 0
			r.stopped = true
			break
		}
		total := int32(ssN * subN)
		for i := 0; i < n; i++ {
			px := minPx + int32(i)
			run += diff[px]
			v := acc[px] + run
			if v > total {
				v = total
			}
			cov[i] = r.covTable[v]
			acc[px] = 0
			diff[px] = 0
		}
		// Clear residue one past the end (pb entries at the right edge).
		acc[maxPx+1] = 0
		diff[maxPx+1] = 0
		sink.blitRow(int(row), int(cx0+minPx), cov[:n])
	}
	r.active = active[:0]
	r.cross = cross[:0]
}

// Sampling grid: 2^aaSS sub-scanlines per pixel and 2^aaSub horizontal
// positions. tiny-skia (resvg) supersamples 4x4; finer grids measurably
// disagree with it on edge pixels, so this matches its grid.
var (
	aaSS  uint = 2
	aaSub uint = 2
)
