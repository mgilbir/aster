package raster

import "math"

type lineCap uint8

const (
	capButt lineCap = iota
	capRound
	capSquare
)

type lineJoin uint8

const (
	joinMiter lineJoin = iota
	joinRound
	joinBevel
	joinMiterClip
)

type strokeStyle struct {
	width      float64
	cap        lineCap
	join       lineJoin
	miterLimit float64
	dash       []float64
	dashOffset float64
	// hairline makes the stroke one device pixel wide along the minor axis of
	// each segment (tiny-skia's hairline model), so diagonal lines are thinner
	// than a true 1px stroke. lin is the local->device linear map.
	hairline bool
	lin      matrix
}

// stroker converts flattened centre lines into fillable outlines. Every subpath
// yields one polygon (open) or two loops (closed); pieces overlap freely and
// rely on the nonzero winding rule, exactly like an offsetting stroker in a
// graphics library.
type stroker struct {
	hw      float64 // half width
	st      strokeStyle
	arcStep float64 // max angle between round join/cap vertices
	out     *flat
	tmp     []point
	rev     []point
	dirs    []point
	left    []point // side outlines, reused across subpaths (emitPoly copies them)
	right   []point
	dashed  flat
}

const strokeTolDev = 0.05 // device-space arc tolerance in pixels

// stroke strokes src (local space) into dst. devScale is the approximate
// local->device scale used to size round joins.
func (s *stroker) stroke(src *flat, st strokeStyle, devScale float64, dst *flat) {
	dst.reset()
	s.st = st
	s.hw = st.width / 2
	s.out = dst
	r := s.hw * devScale
	if r <= strokeTolDev {
		s.arcStep = math.Pi / 2
	} else {
		s.arcStep = 2 * math.Acos(1-strokeTolDev/r)
		if s.arcStep < 0.02 {
			s.arcStep = 0.02
		}
		if s.arcStep > math.Pi/2 {
			s.arcStep = math.Pi / 2
		}
	}
	in := src
	if len(st.dash) > 0 {
		if s.applyDash(src, st, devScale) {
			in = &s.dashed
		}
	}
	for _, sub := range in.subs {
		s.strokeSub(in.pts[sub.start:sub.end], sub.closed)
	}
}

func (s *stroker) emitPoly(pts []point) {
	start := len(s.out.pts)
	s.out.pts = append(s.out.pts, pts...)
	s.out.subs = append(s.out.subs, polyline{start, len(s.out.pts), true})
}

func (s *stroker) strokeSub(pts []point, closed bool) {
	// Remove near-duplicate points to get well-defined directions.
	clean := s.tmp[:0]
	for i, p := range pts {
		if i > 0 {
			q := clean[len(clean)-1]
			if math.Abs(p.x-q.x) < 1e-12 && math.Abs(p.y-q.y) < 1e-12 {
				continue
			}
		}
		clean = append(clean, p)
	}
	if closed && len(clean) > 1 {
		p, q := clean[0], clean[len(clean)-1]
		if math.Abs(p.x-q.x) < 1e-12 && math.Abs(p.y-q.y) < 1e-12 {
			clean = clean[:len(clean)-1]
		}
	}
	s.tmp = clean
	if len(clean) == 1 {
		s.dot(clean[0])
		return
	}
	if len(clean) < 2 {
		return
	}
	if closed && len(clean) == 2 {
		closed = false // a two-point loop is a line traversed twice
	}
	// Left offset chain (forward), then the same on the reversed points.
	left := s.side(clean, closed, s.left[:0])
	s.left = left
	rev := s.rev[:0]
	for i := len(clean) - 1; i >= 0; i-- {
		rev = append(rev, clean[i])
	}
	s.rev = rev
	right := s.side(rev, closed, s.right[:0])
	s.right = right
	if closed {
		s.emitPoly(left)
		s.emitPoly(right)
		return
	}
	poly := left
	// End cap: from the last left point around to the first right point.
	n := len(clean)
	d := unit(clean[n-1].x-clean[n-2].x, clean[n-1].y-clean[n-2].y)
	poly = s.cap(poly, clean[n-1], d)
	poly = append(poly, right...)
	d0 := unit(clean[0].x-clean[1].x, clean[0].y-clean[1].y)
	poly = s.cap(poly, clean[0], d0)
	s.left = poly
	s.emitPoly(poly)
}

func unit(x, y float64) point {
	l := math.Hypot(x, y)
	if l == 0 {
		return point{1, 0}
	}
	return point{x / l, y / l}
}

// dot draws a zero-length subpath: round caps give a disc, square caps an
// axis-aligned square, butt caps nothing (SVG 2, "zero length subpaths").
func (s *stroker) dot(p point) {
	hw := s.hw
	switch s.st.cap {
	case capSquare:
		s.emitPoly([]point{{p.x - hw, p.y - hw}, {p.x + hw, p.y - hw}, {p.x + hw, p.y + hw}, {p.x - hw, p.y + hw}})
	case capRound:
		var pts []point
		pts = append(pts, point{p.x + hw, p.y})
		pts = s.arc(pts, p, point{hw, 0}, 2*math.Pi)
		s.emitPoly(pts)
	}
}

// arc appends intermediate points of an arc around c starting at offset a
// sweeping by the signed angle sweep (endpoints excluded).
func (s *stroker) arc(dst []point, c, a point, sweep float64) []point {
	steps := int(math.Ceil(math.Abs(sweep) / s.arcStep))
	if steps < 1 {
		steps = 1
	}
	if steps > 2000 {
		steps = 2000
	}
	for i := 1; i < steps; i++ {
		sn, cs := math.Sincos(sweep * float64(i) / float64(steps))
		dst = append(dst, point{c.x + a.x*cs - a.y*sn, c.y + a.x*sn + a.y*cs})
	}
	return dst
}

// cap appends the end cap at v for a path arriving in direction d, going from
// v+n around to v-n where n is d rotated +90 degrees, scaled to the half width.
func (s *stroker) cap(dst []point, v, d point) []point {
	hw := s.hw
	n := point{-d.y * hw, d.x * hw}
	switch s.st.cap {
	case capSquare:
		dst = append(dst, point{v.x + n.x + d.x*hw, v.y + n.y + d.y*hw}, point{v.x - n.x + d.x*hw, v.y - n.y + d.y*hw})
	case capRound:
		dst = s.arc(dst, v, n, -math.Pi)
	}
	return dst
}

// side builds the left-offset outline of pts traversed in order.
func (s *stroker) side(pts []point, closed bool, dst []point) []point {
	n := len(pts)
	hw := s.hw
	segs := n - 1
	if closed {
		segs = n
	}
	if cap(s.dirs) < segs {
		s.dirs = make([]point, segs)
	}
	dirs := s.dirs[:segs]
	for i := 0; i < segs; i++ {
		a, b := pts[i], pts[(i+1)%n]
		dirs[i] = unit(b.x-a.x, b.y-a.y)
	}
	norm := func(d point) point {
		k := hw
		if s.st.hairline {
			k *= s.hairFactor(d)
		}
		return point{-d.y * k, d.x * k}
	}
	if !closed {
		p := pts[0]
		nm := norm(dirs[0])
		dst = append(dst, point{p.x + nm.x, p.y + nm.y})
	}
	joinAt := func(i int) { // vertex i between dirs[i-1] and dirs[i]
		prev := dirs[(i-1+segs)%segs]
		next := dirs[i%segs]
		dst = s.join(dst, pts[i%n], prev, next)
	}
	start := 1
	if closed {
		start = 0
	}
	for i := start; i < segs; i++ {
		joinAt(i)
	}
	if closed {
		return dst
	}
	p := pts[n-1]
	nm := norm(dirs[segs-1])
	return append(dst, point{p.x + nm.x, p.y + nm.y})
}

// join appends the offset points around vertex v between unit directions d0
// and d1 on the left side.
func (s *stroker) join(dst []point, v, d0, d1 point) []point {
	hw := s.hw
	k0, k1 := hw, hw
	if s.st.hairline {
		k0 *= s.hairFactor(d0)
		k1 *= s.hairFactor(d1)
	}
	n0 := point{-d0.y * k0, d0.x * k0}
	n1 := point{-d1.y * k1, d1.x * k1}
	cross := d0.x*d1.y - d0.y*d1.x
	dot := d0.x*d1.x + d0.y*d1.y
	a := point{v.x + n0.x, v.y + n0.y}
	b := point{v.x + n1.x, v.y + n1.y}
	if math.Abs(cross) < 1e-9 && dot > 0 {
		return append(dst, a)
	}
	if cross > 0 {
		// Left turn: this side is the inside; connect through the pivot.
		return append(dst, a, v, b)
	}
	// Outer side.
	switch s.st.join {
	case joinRound:
		dst = append(dst, a)
		sweep := math.Atan2(cross, dot)
		if math.Abs(cross) < 1e-9 {
			sweep = -math.Pi
		}
		dst = s.arc(dst, v, n0, sweep)
		return append(dst, b)
	case joinMiter, joinMiterClip:
		if !s.st.hairline && (1+dot)/2 >= 1/(s.st.miterLimit*s.st.miterLimit) && 1+dot > 1e-12 {
			k := 1 / (1 + dot)
			m := point{v.x + (n0.x+n1.x)*k, v.y + (n0.y+n1.y)*k}
			return append(dst, a, m, b)
		}
	}
	return append(dst, a, b)
}

// applyDash splits src into dashes in s.dashed. It reports false when the dash
// array is unusable (the caller then strokes solid).
func (s *stroker) applyDash(src *flat, st strokeStyle, devScale float64) bool {
	dash := st.dash
	sum := 0.0
	for _, d := range dash {
		if d < 0 || d != d || math.IsInf(d, 0) {
			return false
		}
		sum += d
	}
	if sum <= 0 || sum*devScale < 1e-3 {
		return false
	}
	pat := dash
	if len(pat)%2 == 1 {
		pat = append(append([]float64(nil), pat...), pat...)
		sum *= 2
	}
	// Bound the dash count.
	total := 0.0
	for _, sub := range src.subs {
		pts := src.pts[sub.start:sub.end]
		for i := 0; i+1 < len(pts); i++ {
			total += math.Hypot(pts[i+1].x-pts[i].x, pts[i+1].y-pts[i].y)
		}
		if sub.closed && len(pts) > 1 {
			total += math.Hypot(pts[0].x-pts[len(pts)-1].x, pts[0].y-pts[len(pts)-1].y)
		}
	}
	if total/sum*float64(len(pat)) > 2_000_000 {
		return false
	}
	off := math.Mod(st.dashOffset, sum)
	if off < 0 {
		off += sum
	}
	out := &s.dashed
	out.reset()
	for _, sub := range src.subs {
		pts := src.pts[sub.start:sub.end]
		if len(pts) < 2 {
			// Zero-length subpaths draw as a dot if the pattern starts "on".
			out.subs = append(out.subs, polyline{len(out.pts), len(out.pts) + len(pts), false})
			out.pts = append(out.pts, pts...)
			continue
		}
		// Initialise the pattern position from the offset.
		idx := 0
		rem := pat[0]
		o := off
		for o > 0 {
			if o >= rem {
				o -= rem
				idx = (idx + 1) % len(pat)
				rem = pat[idx]
				if o == 0 {
					break
				}
			} else {
				rem -= o
				o = 0
			}
		}
		on := idx%2 == 0
		firstStart := len(out.subs)
		var cur []point
		startedOn := on
		flush := func() {
			if len(cur) >= 2 {
				st := len(out.pts)
				out.pts = append(out.pts, cur...)
				out.subs = append(out.subs, polyline{st, len(out.pts), false})
			}
			cur = cur[:0]
		}
		npts := len(pts)
		nsegs := npts - 1
		if sub.closed {
			nsegs = npts
		}
		if on {
			cur = append(cur, pts[0])
		}
		for i := 0; i < nsegs; i++ {
			a, b := pts[i], pts[(i+1)%npts]
			segLen := math.Hypot(b.x-a.x, b.y-a.y)
			if segLen == 0 {
				continue
			}
			d := point{(b.x - a.x) / segLen, (b.y - a.y) / segLen}
			pos := 0.0
			for segLen-pos > rem {
				pos += rem
				p := point{a.x + d.x*pos, a.y + d.y*pos}
				if on {
					if len(cur) == 1 && cur[0] == p {
						// Zero-length dash: keep direction with a tiny extension.
						p = point{p.x + d.x*1e-7, p.y + d.y*1e-7}
					}
					cur = append(cur, p)
					flush()
				} else {
					cur = append(cur[:0], p)
				}
				on = !on
				idx = (idx + 1) % len(pat)
				rem = pat[idx]
			}
			rem -= segLen - pos
			if on {
				cur = append(cur, b)
			}
		}
		if on && len(cur) >= 2 {
			// A closed shape whose last dash runs into a first "on" dash joins them.
			if sub.closed && startedOn && firstStart < len(out.subs) {
				first := out.subs[firstStart]
				merged := append([]point(nil), cur...)
				merged = append(merged, out.pts[first.start+1:first.end]...)
				out.subs[firstStart] = polyline{len(out.pts), len(out.pts) + len(merged), false}
				out.pts = append(out.pts, merged...)
				cur = cur[:0]
			} else {
				flush()
			}
		}
	}
	return true
}

// hairFactor scales the half width for a segment with local direction d so the
// device-space thickness measured along the minor axis is constant.
func (s *stroker) hairFactor(d point) float64 {
	m := s.st.lin
	dx := m.a*d.x + m.c*d.y
	dy := m.b*d.x + m.d*d.y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return 1
	}
	return math.Max(math.Abs(dx), math.Abs(dy)) / l
}
