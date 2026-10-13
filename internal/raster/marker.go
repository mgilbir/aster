package raster

import (
	"math"
	"strconv"
	"strings"
)

// Markers follow usvg's marker module: vertex positions and orientation come
// from the path segments (quadratics promoted to cubics), the marker content is
// drawn with translate(vertex) rotate(angle) scale translate(-ref), and the
// viewport clip is the viewBox (or 0,0,markerWidth,markerHeight) unless
// overflow is visible.

const (
	segMove uint8 = iota
	segLine
	segCubic
	segClose
)

type segment struct {
	kind      uint8
	p1, p2, p point // cubic controls / end point; p for move and line
	// start is the point of the last move before this segment (the origin
	// when there is none): where a close returns to. Kept with each segment
	// so that a vertex finds it at once rather than by walking back, which
	// made marker-mid quadratic in a path's segments.
	start point
}

func pathSegments(p *path) []segment {
	out := make([]segment, 0, len(p.verbs))
	pi := 0
	var prev, prevMove point
	for _, v := range p.verbs {
		switch v {
		case vMove:
			pt := p.pts[pi]
			pi++
			out = append(out, segment{kind: segMove, p: pt})
			prev, prevMove = pt, pt
		case vLine:
			pt := p.pts[pi]
			pi++
			out = append(out, segment{kind: segLine, p: pt})
			prev = pt
		case vQuad:
			c, e := p.pts[pi], p.pts[pi+1]
			pi += 2
			c1 := point{(prev.x + c.x*2) / 3, (prev.y + c.y*2) / 3}
			c2 := point{(e.x + c.x*2) / 3, (e.y + c.y*2) / 3}
			out = append(out, segment{kind: segCubic, p1: c1, p2: c2, p: e})
			prev = e
		case vCubic:
			out = append(out, segment{kind: segCubic, p1: p.pts[pi], p2: p.pts[pi+1], p: p.pts[pi+2]})
			prev = p.pts[pi+2]
			pi += 3
		case vClose:
			out = append(out, segment{kind: segClose})
			prev = prevMove
		}
	}
	// Each segment's subpath start: the last move before it.
	var start point
	for i := range out {
		out[i].start = start
		if out[i].kind == segMove {
			start = out[i].p
		}
	}
	return out
}

func approxEq(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// markerPath is a path's segments as marker placement reads them.
type markerPath struct {
	segs []segment
	// reads counts the segments read to find the subpath starts of the
	// vertices. Nothing reads it but TestMarkerMidLinear, which checks that
	// placing markers on n vertices reads O(n) of them by counting, not
	// timing; a lookup that walks segments must count each one it reads.
	reads int
}

func (mp *markerPath) subpathStart(idx int) point {
	mp.reads++
	return mp.segs[idx].start
}

func (mp *markerPath) prevVertex(idx int) point {
	s := mp.segs[idx-1]
	if s.kind == segClose {
		return mp.subpathStart(idx)
	}
	return s.p
}

func normAngle(rad float64) float64 {
	v := math.Mod(rad, 2*math.Pi)
	if v < 0 {
		return v + 2*math.Pi
	}
	return v
}

func vectorAngle(vx, vy float64) float64 {
	rad := math.Atan2(vy, vx)
	if rad != rad {
		return 0
	}
	return normAngle(rad)
}

func calcAngle(x1, y1, x2, y2, x3, y3, x4, y4 float64) float64 {
	in := vectorAngle(x2-x1, y2-y1)
	out := vectorAngle(x4-x3, y4-y3)
	d := (out - in) * 0.5
	angle := in + d
	if math.Pi/2 < math.Abs(d) {
		angle -= math.Pi
	}
	return normAngle(angle) * 180 / math.Pi
}

func calcLineAngle(x1, y1, x2, y2 float64) float64 { return calcAngle(x1, y1, x2, y2, x1, y1, x2, y2) }

func calcCurvesAngle(px, py, cx1, cy1, x, y, cx2, cy2, nx, ny float64) float64 {
	switch {
	case approxEq(cx1, x) && approxEq(cy1, y):
		return calcAngle(px, py, x, y, x, y, cx2, cy2)
	case approxEq(x, cx2) && approxEq(y, cy2):
		return calcAngle(cx1, cy1, x, y, x, y, nx, ny)
	}
	return calcAngle(cx1, cy1, x, y, x, y, cx2, cy2)
}

func (mp *markerPath) vertexAngle(idx int) float64 {
	segs := mp.segs
	n := len(segs)
	if idx == 0 {
		if n < 2 {
			return 0
		}
		s1, s2 := segs[0], segs[1]
		switch {
		case s1.kind == segMove && s2.kind == segLine:
			return calcLineAngle(s1.p.x, s1.p.y, s2.p.x, s2.p.y)
		case s1.kind == segMove && s2.kind == segCubic:
			if approxEq(s1.p.x, s2.p1.x) && approxEq(s1.p.y, s2.p1.y) {
				return calcLineAngle(s1.p.x, s1.p.y, s2.p.x, s2.p.y)
			}
			return calcLineAngle(s1.p.x, s1.p.y, s2.p1.x, s2.p1.y)
		}
		return 0
	}
	if idx == n-1 {
		s1, s2 := segs[idx-1], segs[idx]
		switch s2.kind {
		case segMove:
			return 0
		case segLine:
			pv := mp.prevVertex(idx)
			return calcLineAngle(pv.x, pv.y, s2.p.x, s2.p.y)
		case segCubic:
			if approxEq(s2.p2.x, s2.p.x) && approxEq(s2.p2.y, s2.p.y) {
				return calcLineAngle(s2.p1.x, s2.p1.y, s2.p.x, s2.p.y)
			}
			return calcLineAngle(s2.p2.x, s2.p2.y, s2.p.x, s2.p.y)
		case segClose:
			switch s1.kind {
			case segLine:
				next := mp.subpathStart(idx)
				return calcLineAngle(s1.p.x, s1.p.y, next.x, next.y)
			case segCubic:
				pv := mp.prevVertex(idx)
				next := mp.subpathStart(idx)
				return calcCurvesAngle(pv.x, pv.y, s1.p2.x, s1.p2.y, s1.p.x, s1.p.y, next.x, next.y, next.x, next.y)
			}
		}
		return 0
	}
	s1, s2 := segs[idx], segs[idx+1]
	switch {
	case s1.kind == segMove && s2.kind == segLine:
		return calcLineAngle(s1.p.x, s1.p.y, s2.p.x, s2.p.y)
	case s1.kind == segMove && s2.kind == segCubic:
		return calcLineAngle(s1.p.x, s1.p.y, s2.p1.x, s2.p1.y)
	case s1.kind == segLine && s2.kind == segLine:
		pv := mp.prevVertex(idx)
		return calcAngle(pv.x, pv.y, s1.p.x, s1.p.y, s1.p.x, s1.p.y, s2.p.x, s2.p.y)
	case s1.kind == segCubic && s2.kind == segCubic:
		pv := mp.prevVertex(idx)
		return calcCurvesAngle(pv.x, pv.y, s1.p2.x, s1.p2.y, s1.p.x, s1.p.y, s2.p1.x, s2.p1.y, s2.p.x, s2.p.y)
	case s1.kind == segLine && s2.kind == segCubic:
		pv := mp.prevVertex(idx)
		return calcCurvesAngle(pv.x, pv.y, pv.x, pv.y, s1.p.x, s1.p.y, s2.p1.x, s2.p1.y, s2.p.x, s2.p.y)
	case s1.kind == segCubic && s2.kind == segLine:
		pv := mp.prevVertex(idx)
		return calcCurvesAngle(pv.x, pv.y, s1.p2.x, s1.p2.y, s1.p.x, s1.p.y, s2.p.x, s2.p.y, s2.p.x, s2.p.y)
	case s1.kind == segLine && s2.kind == segMove:
		pv := mp.prevVertex(idx)
		return calcLineAngle(pv.x, pv.y, s1.p.x, s1.p.y)
	case s1.kind == segCubic && s2.kind == segMove:
		if approxEq(s1.p.x, s1.p2.x) && approxEq(s1.p.y, s1.p2.y) {
			pv := mp.prevVertex(idx)
			return calcLineAngle(pv.x, pv.y, s1.p.x, s1.p.y)
		}
		return calcLineAngle(s1.p2.x, s1.p2.y, s1.p.x, s1.p.y)
	case s1.kind == segLine && s2.kind == segClose:
		pv := mp.prevVertex(idx)
		next := mp.subpathStart(idx)
		return calcAngle(pv.x, pv.y, s1.p.x, s1.p.y, s1.p.x, s1.p.y, next.x, next.y)
	case s2.kind == segClose && s1.kind != segClose:
		pv := mp.prevVertex(idx)
		next := mp.subpathStart(idx)
		return calcLineAngle(pv.x, pv.y, next.x, next.y)
	}
	return 0
}

// parseAngle parses an SVG angle (deg, grad, rad, turn or unitless degrees).
func parseAngle(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	unit := 1.0
	for _, u := range []struct {
		suffix string
		k      float64
	}{{"deg", 1}, {"grad", 0.9}, {"rad", 180 / math.Pi}, {"turn", 360}} {
		if strings.HasSuffix(s, u.suffix) {
			s, unit = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.k
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v * unit, true
}

// hasMarkers reports whether the shape n draws markers under state st.
func (r *renderer) hasMarkers(n *node, st *state) bool {
	if st.markers == nil || !st.visible {
		return false
	}
	for p := n; p != nil; p = p.parent {
		if p.tag == tagClipPath {
			return false
		}
	}
	for _, id := range st.markers {
		if id != "" {
			if m := r.doc.ids[id]; m != nil && m.tag == tagMarker {
				return true
			}
		}
	}
	return false
}

// drawMarkers draws the marker-start/mid/end symbols of path p (local
// coordinates of the shape whose state is st).
func (r *renderer) drawMarkers(p *path, st *state) {
	r.holdPoints(4 * len(p.verbs)) // a segment is four points' bytes
	mp := &markerPath{segs: pathSegments(p)}
	if len(mp.segs) == 0 {
		return
	}
	for kind := 0; kind < 3; kind++ {
		id := st.markers[kind]
		if id == "" {
			continue
		}
		mn := r.doc.ids[id]
		if mn == nil || mn.tag != tagMarker || r.active[mn] {
			continue
		}
		r.drawMarker(mn, mp, kind, st)
		if r.err != nil {
			return
		}
	}
}

func (r *renderer) drawMarker(mn *node, mp *markerPath, kind int, st *state) {
	mst := r.inheritedState(mn)
	strokeScale := 1.0
	if strings.TrimSpace(mn.str(aMarkerUnits)) != "userSpaceOnUse" {
		strokeScale = st.strokeWidth
		if !(strokeScale > 0) {
			return
		}
	}
	refX := mst.length(mn.str(aRefX), 0, 0)
	refY := mst.length(mn.str(aRefY), 1, 0)
	mw := mst.length(mn.str(aMarkerWidth), 0, 3)
	mh := mst.length(mn.str(aMarkerHeight), 1, 3)
	if !(mw > 0 && mh > 0) {
		return
	}
	vb, hasVB := parseViewBox(mn.str(aViewBox))
	ov := strings.TrimSpace(mn.str(aOverflow))
	clipped := ov == "" || ov == "hidden" || ov == "scroll"
	clipRect := rect{0, 0, mw, mh}
	if hasVB {
		clipRect = vb
	}
	orient := strings.TrimSpace(mn.str(aOrient))

	// draw places the marker at a vertex; false ends the placement (the
	// render has failed).
	draw := func(pt point, idx int) bool {
		if !r.budget() {
			return r.err == nil
		}
		ts := translate(pt.x, pt.y)
		var angle float64
		switch {
		case orient == "auto-start-reverse" && idx == 0:
			angle = math.Mod(mp.vertexAngle(idx)+180, 360)
		case orient == "auto" || orient == "auto-start-reverse":
			angle = mp.vertexAngle(idx)
		default:
			if a, ok := parseAngle(orient); ok {
				angle = a
			}
		}
		if math.Abs(angle) > 1e-9 {
			sn, cs := math.Sincos(angle * math.Pi / 180)
			ts = ts.mul(matrix{cs, sn, -sn, cs, 0, 0})
		}
		if hasVB {
			vt := viewBoxTransform(vb, mw*strokeScale, mh*strokeScale, mn.str(aPreserveAspectRatio))
			sx := math.Hypot(vt.a, vt.c)
			sy := math.Hypot(vt.b, vt.d)
			ts = ts.mul(scaleM(sx, sy))
		} else {
			ts = ts.mul(scaleM(strokeScale, strokeScale))
		}
		ts = ts.mul(translate(-refX, -refY))
		cs := *mst
		cs.ctm = st.ctm.mul(ts)
		cs.clip = st.clip
		cs.vw, cs.vh = st.vw, st.vh
		if !cs.ctm.isFinite() || !cs.ctm.invertible() {
			return r.err == nil
		}
		if clipped {
			cs.clip = r.rectClip(&state{clip: st.clip}, cs.ctm, clipRect)
			if cs.clip.r.empty() {
				return r.err == nil
			}
		}
		r.active[mn] = true
		r.renderChildren(mn, &cs)
		delete(r.active, mn)
		return r.err == nil
	}
	if r.active == nil {
		r.active = map[*node]bool{}
	}

	mp.eachVertex(kind, draw)
}

// eachVertex calls draw with each vertex a marker of kind (0 start, 1 mid,
// 2 end) is placed on, and the index of its segment, until draw returns
// false.
func (mp *markerPath) eachVertex(kind int, draw func(pt point, idx int) bool) {
	segs := mp.segs
	switch kind {
	case 0:
		if segs[0].kind == segMove {
			draw(segs[0].p, 0)
		}
	case 1:
		total := len(segs) - 1
		for i := 1; i < total; i++ {
			if segs[i].kind == segClose {
				continue
			}
			if !draw(segs[i].p, i) {
				return
			}
		}
	case 2:
		idx := len(segs) - 1
		switch segs[idx].kind {
		case segLine, segCubic:
			draw(segs[idx].p, idx)
		case segClose:
			draw(mp.subpathStart(idx), idx)
		}
	}
}
