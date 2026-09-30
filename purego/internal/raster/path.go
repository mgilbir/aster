package raster

import (
	"math"
)

const (
	vMove uint8 = iota
	vLine
	vQuad
	vCubic
	vClose
)

// path is a sequence of drawing commands in some coordinate space.
type path struct {
	verbs []uint8
	pts   []point
	start point // start of the current subpath
	last  point
}

func (p *path) moveTo(x, y float64) {
	pt := point{clampCoord(x), clampCoord(y)}
	p.verbs = append(p.verbs, vMove)
	p.pts = append(p.pts, pt)
	p.start, p.last = pt, pt
}

func (p *path) lineTo(x, y float64) {
	pt := point{clampCoord(x), clampCoord(y)}
	p.verbs = append(p.verbs, vLine)
	p.pts = append(p.pts, pt)
	p.last = pt
}

func (p *path) quadTo(x1, y1, x, y float64) {
	p.verbs = append(p.verbs, vQuad)
	p.pts = append(p.pts, point{clampCoord(x1), clampCoord(y1)}, point{clampCoord(x), clampCoord(y)})
	p.last = point{clampCoord(x), clampCoord(y)}
}

func (p *path) cubicTo(x1, y1, x2, y2, x, y float64) {
	p.verbs = append(p.verbs, vCubic)
	p.pts = append(p.pts, point{clampCoord(x1), clampCoord(y1)}, point{clampCoord(x2), clampCoord(y2)}, point{clampCoord(x), clampCoord(y)})
	p.last = point{clampCoord(x), clampCoord(y)}
}

func (p *path) close() {
	if len(p.verbs) == 0 || p.verbs[len(p.verbs)-1] == vClose {
		return
	}
	p.verbs = append(p.verbs, vClose)
	p.last = p.start
}

func (p *path) reset() {
	p.verbs = p.verbs[:0]
	p.pts = p.pts[:0]
}

func (p *path) empty() bool { return len(p.verbs) == 0 }

// bounds returns the control-point bounding box (a superset of the true box).
func (p *path) bounds() (rect, bool) {
	var b bbox
	for _, pt := range p.pts {
		b.add(pt)
	}
	return b.r, b.ok
}

// transformed returns a copy of p under m.
func (p *path) transformed(m matrix) *path {
	q := &path{verbs: append([]uint8(nil), p.verbs...), pts: make([]point, len(p.pts))}
	for i, pt := range p.pts {
		q.pts[i] = m.apply(pt)
	}
	return q
}

// arcTo appends an SVG elliptical arc from the current point as cubic
// Béziers (SVG 1.1 implementation notes, F.6).
func (p *path) arcTo(rx, ry, xrot float64, large, sweep bool, x, y float64) {
	x0, y0 := p.last.x, p.last.y
	x, y = clampCoord(x), clampCoord(y)
	if x0 == x && y0 == y {
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 || math.IsInf(rx, 0) || math.IsInf(ry, 0) || rx != rx || ry != ry {
		p.lineTo(x, y)
		return
	}
	phi := xrot * math.Pi / 180
	sinPhi, cosPhi := math.Sincos(phi)
	dx2, dy2 := (x0-x)/2, (y0-y)/2
	x1p := cosPhi*dx2 + sinPhi*dy2
	y1p := -sinPhi*dx2 + cosPhi*dy2
	lambda := (x1p*x1p)/(rx*rx) + (y1p*y1p)/(ry*ry)
	if lambda > 1 {
		s := math.Sqrt(lambda)
		rx *= s
		ry *= s
	}
	rx2, ry2 := rx*rx, ry*ry
	num := rx2*ry2 - rx2*y1p*y1p - ry2*x1p*x1p
	den := rx2*y1p*y1p + ry2*x1p*x1p
	coef := 0.0
	if den != 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := -coef * ry * x1p / rx
	cx := cosPhi*cxp - sinPhi*cyp + (x0+x)/2
	cy := sinPhi*cxp + cosPhi*cyp + (y0+y)/2
	ang := func(ux, uy, vx, vy float64) float64 {
		return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	}
	th1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dth := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dth > 0 {
		dth -= 2 * math.Pi
	} else if sweep && dth < 0 {
		dth += 2 * math.Pi
	}
	nseg := int(math.Ceil(math.Abs(dth) / (math.Pi / 2) * 0.999999))
	if nseg < 1 {
		nseg = 1
	}
	delta := dth / float64(nseg)
	t := 4.0 / 3.0 * math.Tan(delta/4)
	th := th1
	for i := 0; i < nseg; i++ {
		s1, c1 := math.Sincos(th)
		s2, c2 := math.Sincos(th + delta)
		// Points on the unit circle mapped through the ellipse transform.
		e1x, e1y := c1-t*s1, s1+t*c1
		e2x, e2y := c2+t*s2, s2-t*c2
		mp := func(ux, uy float64) (float64, float64) {
			return cosPhi*rx*ux - sinPhi*ry*uy + cx, sinPhi*rx*ux + cosPhi*ry*uy + cy
		}
		ax, ay := mp(e1x, e1y)
		bx, by := mp(e2x, e2y)
		ex, ey := mp(c2, s2)
		if i == nseg-1 {
			ex, ey = x, y
		}
		p.cubicTo(ax, ay, bx, by, ex, ey)
		th += delta
	}
}

// numScanner reads SVG numbers and flags from a string.
type numScanner struct {
	s string
	i int
}

func (n *numScanner) skipWS() {
	for n.i < len(n.s) {
		c := n.s[n.i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			n.i++
		} else {
			break
		}
	}
}

func (n *numScanner) skipSep() {
	n.skipWS()
	if n.i < len(n.s) && n.s[n.i] == ',' {
		n.i++
		n.skipWS()
	}
}

func (n *numScanner) atEnd() bool {
	n.skipWS()
	return n.i >= len(n.s)
}

// number parses one number (SVG grammar: sign, digits, fraction, exponent).
func (n *numScanner) number() (float64, bool) {
	n.skipWS()
	s := n.s
	i := n.i
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	var mant float64
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		mant = mant*10 + float64(s[i]-'0')
		i++
		digits++
	}
	scale := 0
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if digits < 19 {
				mant = mant*10 + float64(s[i]-'0')
				scale--
			}
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0, false
	}
	exp := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		neg := false
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			neg = s[j] == '-'
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			e := 0
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				if e < 10000 {
					e = e*10 + int(s[j]-'0')
				}
				j++
			}
			if neg {
				e = -e
			}
			exp = e
			i = j
		}
	}
	v := mant
	if e := scale + exp; e != 0 {
		v = mant * math.Pow(10, float64(e))
	}
	if s[start] == '-' {
		v = -v
	}
	n.i = i
	if math.IsInf(v, 0) || v != v {
		return 0, false
	}
	return v, true
}

func (n *numScanner) flag() (bool, bool) {
	n.skipWS()
	if n.i < len(n.s) && (n.s[n.i] == '0' || n.s[n.i] == '1') {
		v := n.s[n.i] == '1'
		n.i++
		return v, true
	}
	return false, false
}

// parsePathData parses SVG path data. Per the SVG error-handling rules,
// everything before the first error is kept.
func parsePathData(d string, p *path) {
	sc := numScanner{s: d}
	var cmd byte
	var cx, cy float64   // current point
	var lcx, lcy float64 // last control point for S/T
	var lastCmd byte     // previous effective command (upper-case)
	started := false     // has a moveto been seen
	first := true
	for {
		sc.skipWS()
		if sc.i >= len(sc.s) {
			return
		}
		c := sc.s[sc.i]
		if isCmdLetter(c) {
			cmd = c
			sc.i++
		} else if cmd == 0 || cmd == 'z' || cmd == 'Z' {
			return
		} else if !(c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9')) {
			return
		}
		if first && cmd != 'M' && cmd != 'm' {
			return
		}
		first = false
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		if up != 'Z' && !started && up != 'M' {
			return
		}
		read := func(k int, out *[7]float64) bool {
			for j := 0; j < k; j++ {
				if j > 0 {
					sc.skipSep()
				}
				v, ok := sc.number()
				if !ok {
					return false
				}
				out[j] = v
			}
			return true
		}
		var a [7]float64
		switch up {
		case 'M':
			if !read(2, &a) {
				return
			}
			if rel {
				a[0] += cx
				a[1] += cy
			}
			p.moveTo(a[0], a[1])
			cx, cy = p.last.x, p.last.y
			started = true
			// Subsequent pairs are implicit linetos.
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
			lastCmd = 'M'
		case 'L':
			if !read(2, &a) {
				return
			}
			if rel {
				a[0] += cx
				a[1] += cy
			}
			p.lineTo(a[0], a[1])
			cx, cy = p.last.x, p.last.y
			lastCmd = 'L'
		case 'H':
			if !read(1, &a) {
				return
			}
			if rel {
				a[0] += cx
			}
			p.lineTo(a[0], cy)
			cx = p.last.x
			lastCmd = 'L'
		case 'V':
			if !read(1, &a) {
				return
			}
			if rel {
				a[0] += cy
			}
			p.lineTo(cx, a[0])
			cy = p.last.y
			lastCmd = 'L'
		case 'C':
			if !read(6, &a) {
				return
			}
			if rel {
				for j := 0; j < 6; j += 2 {
					a[j] += cx
					a[j+1] += cy
				}
			}
			p.cubicTo(a[0], a[1], a[2], a[3], a[4], a[5])
			lcx, lcy = a[2], a[3]
			cx, cy = p.last.x, p.last.y
			lastCmd = 'C'
		case 'S':
			if !read(4, &a) {
				return
			}
			if rel {
				for j := 0; j < 4; j += 2 {
					a[j] += cx
					a[j+1] += cy
				}
			}
			x1, y1 := cx, cy
			if lastCmd == 'C' {
				x1, y1 = 2*cx-lcx, 2*cy-lcy
			}
			p.cubicTo(x1, y1, a[0], a[1], a[2], a[3])
			lcx, lcy = a[0], a[1]
			cx, cy = p.last.x, p.last.y
			lastCmd = 'C'
		case 'Q':
			if !read(4, &a) {
				return
			}
			if rel {
				for j := 0; j < 4; j += 2 {
					a[j] += cx
					a[j+1] += cy
				}
			}
			p.quadTo(a[0], a[1], a[2], a[3])
			lcx, lcy = a[0], a[1]
			cx, cy = p.last.x, p.last.y
			lastCmd = 'Q'
		case 'T':
			if !read(2, &a) {
				return
			}
			if rel {
				a[0] += cx
				a[1] += cy
			}
			x1, y1 := cx, cy
			if lastCmd == 'Q' {
				x1, y1 = 2*cx-lcx, 2*cy-lcy
			}
			p.quadTo(x1, y1, a[0], a[1])
			lcx, lcy = x1, y1
			cx, cy = p.last.x, p.last.y
			lastCmd = 'Q'
		case 'A':
			var rx, ry, rot float64
			var ok bool
			if rx, ok = sc.number(); !ok {
				return
			}
			sc.skipSep()
			if ry, ok = sc.number(); !ok {
				return
			}
			sc.skipSep()
			if rot, ok = sc.number(); !ok {
				return
			}
			sc.skipSep()
			large, ok1 := sc.flag()
			sc.skipSep()
			sweep, ok2 := sc.flag()
			sc.skipSep()
			if !ok1 || !ok2 {
				return
			}
			x, ok3 := sc.number()
			sc.skipSep()
			y, ok4 := sc.number()
			if !ok3 || !ok4 {
				return
			}
			if rel {
				x += cx
				y += cy
			}
			p.arcTo(rx, ry, rot, large, sweep, x, y)
			cx, cy = p.last.x, p.last.y
			lastCmd = 'A'
		case 'Z':
			p.close()
			cx, cy = p.start.x, p.start.y
			lastCmd = 'Z'
		default:
			return
		}
		sc.skipSep()
	}
}

func isCmdLetter(c byte) bool {
	switch c {
	case 'M', 'm', 'L', 'l', 'H', 'h', 'V', 'v', 'C', 'c', 'S', 's', 'Q', 'q', 'T', 't', 'A', 'a', 'Z', 'z':
		return true
	}
	return false
}

const kappa = 0.5522847498307936

func (p *path) addRect(x, y, w, h float64) {
	p.moveTo(x, y)
	p.lineTo(x+w, y)
	p.lineTo(x+w, y+h)
	p.lineTo(x, y+h)
	p.close()
}

func (p *path) addRoundRect(x, y, w, h, rx, ry float64) {
	if rx <= 0 || ry <= 0 {
		p.addRect(x, y, w, h)
		return
	}
	kx, ky := rx*kappa, ry*kappa
	p.moveTo(x+rx, y)
	p.lineTo(x+w-rx, y)
	p.cubicTo(x+w-rx+kx, y, x+w, y+ry-ky, x+w, y+ry)
	p.lineTo(x+w, y+h-ry)
	p.cubicTo(x+w, y+h-ry+ky, x+w-rx+kx, y+h, x+w-rx, y+h)
	p.lineTo(x+rx, y+h)
	p.cubicTo(x+rx-kx, y+h, x, y+h-ry+ky, x, y+h-ry)
	p.lineTo(x, y+ry)
	p.cubicTo(x, y+ry-ky, x+rx-kx, y, x+rx, y)
	p.close()
}

func (p *path) addEllipse(cx, cy, rx, ry float64) {
	kx, ky := rx*kappa, ry*kappa
	p.moveTo(cx+rx, cy)
	p.cubicTo(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry)
	p.cubicTo(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy)
	p.cubicTo(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry)
	p.cubicTo(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy)
	p.close()
}

// polyline is one flattened subpath: pts[start:end].
type polyline struct {
	start, end int
	closed     bool
}

// flat is a flattened path.
type flat struct {
	pts  []point
	subs []polyline
}

func (f *flat) reset() {
	f.pts = f.pts[:0]
	f.subs = f.subs[:0]
}

const maxCurveSegs = 400

// flatten converts p, mapped through m, into polylines whose deviation from the
// true curve is at most tol (in output units). Consecutive duplicate points
// are dropped. Subpaths that draw only to their own start point are kept as a
// single point so strokers can honour zero-length caps.
func (f *flat) flatten(p *path, m matrix, tol float64) {
	f.reset()
	f.appendFlatten(p, m, tol)
}

// appendFlatten is flatten without clearing the existing subpaths.
func (f *flat) appendFlatten(p *path, m matrix, tol float64) {
	pi := 0
	var cur point
	subStart := -1
	drew := false
	finish := func(closed bool) {
		if subStart < 0 {
			return
		}
		end := len(f.pts)
		if closed && end-subStart > 1 && f.pts[end-1] == f.pts[subStart] {
			f.pts = f.pts[:end-1]
			end--
		}
		if end-subStart >= 2 || (end-subStart == 1 && drew) {
			f.subs = append(f.subs, polyline{subStart, end, closed})
		} else {
			f.pts = f.pts[:subStart]
		}
		subStart = -1
	}
	add := func(pt point) {
		if n := len(f.pts); n > subStart && f.pts[n-1] == pt {
			return
		}
		f.pts = append(f.pts, pt)
	}
	identityM := m == identity
	tr := func(pt point) point {
		if identityM {
			return pt
		}
		return m.apply(pt)
	}
	var startPt point
	for _, v := range p.verbs {
		switch v {
		case vMove:
			finish(false)
			cur = tr(p.pts[pi])
			pi++
			subStart = len(f.pts)
			drew = false
			startPt = cur
			f.pts = append(f.pts, cur)
		case vLine:
			q := tr(p.pts[pi])
			pi++
			if subStart < 0 {
				subStart = len(f.pts)
				f.pts = append(f.pts, cur)
				startPt = cur
				drew = false
			}
			drew = true
			add(q)
			cur = q
		case vQuad:
			c1 := tr(p.pts[pi])
			q := tr(p.pts[pi+1])
			pi += 2
			if subStart < 0 {
				subStart = len(f.pts)
				f.pts = append(f.pts, cur)
				startPt = cur
			}
			drew = true
			ddx := cur.x - 2*c1.x + q.x
			ddy := cur.y - 2*c1.y + q.y
			dd := math.Hypot(ddx, ddy)
			n := int(math.Ceil(math.Sqrt(dd / (4 * tol))))
			if n < 1 {
				n = 1
			}
			if n > maxCurveSegs {
				n = maxCurveSegs
			}
			for i := 1; i < n; i++ {
				t := float64(i) / float64(n)
				mt := 1 - t
				add(point{mt*mt*cur.x + 2*mt*t*c1.x + t*t*q.x, mt*mt*cur.y + 2*mt*t*c1.y + t*t*q.y})
			}
			add(q)
			cur = q
		case vCubic:
			c1 := tr(p.pts[pi])
			c2 := tr(p.pts[pi+1])
			q := tr(p.pts[pi+2])
			pi += 3
			if subStart < 0 {
				subStart = len(f.pts)
				f.pts = append(f.pts, cur)
				startPt = cur
			}
			drew = true
			d1 := math.Hypot(cur.x-2*c1.x+c2.x, cur.y-2*c1.y+c2.y)
			d2 := math.Hypot(c1.x-2*c2.x+q.x, c1.y-2*c2.y+q.y)
			dd := math.Max(d1, d2)
			n := int(math.Ceil(math.Sqrt(0.75 * dd / tol)))
			if n < 1 {
				n = 1
			}
			if n > maxCurveSegs {
				n = maxCurveSegs
			}
			for i := 1; i < n; i++ {
				t := float64(i) / float64(n)
				mt := 1 - t
				a := mt * mt * mt
				b := 3 * mt * mt * t
				c := 3 * mt * t * t
				d := t * t * t
				add(point{a*cur.x + b*c1.x + c*c2.x + d*q.x, a*cur.y + b*c1.y + c*c2.y + d*q.y})
			}
			add(q)
			cur = q
		case vClose:
			if subStart >= 0 {
				drew = true
				finish(true)
				cur = startPt
			}
		}
	}
	finish(false)
}
