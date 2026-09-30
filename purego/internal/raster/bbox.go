package raster

import "math"

// tightBounds returns the exact bounding box of p, including the extrema of
// curve segments (usvg's "object bounding box" is computed the same way).
func tightBounds(p *path) (rect, bool) {
	var b bbox
	pi := 0
	var cur point
	for _, v := range p.verbs {
		switch v {
		case vMove, vLine:
			cur = p.pts[pi]
			pi++
			b.add(cur)
		case vQuad:
			c, e := p.pts[pi], p.pts[pi+1]
			pi += 2
			b.add(e)
			for _, t := range quadExtrema(cur.x, c.x, e.x) {
				b.add(quadAt(cur, c, e, t))
			}
			for _, t := range quadExtrema(cur.y, c.y, e.y) {
				b.add(quadAt(cur, c, e, t))
			}
			cur = e
		case vCubic:
			c1, c2, e := p.pts[pi], p.pts[pi+1], p.pts[pi+2]
			pi += 3
			b.add(e)
			for _, t := range cubicExtrema(cur.x, c1.x, c2.x, e.x) {
				b.add(cubicAt(cur, c1, c2, e, t))
			}
			for _, t := range cubicExtrema(cur.y, c1.y, c2.y, e.y) {
				b.add(cubicAt(cur, c1, c2, e, t))
			}
			cur = e
		}
	}
	return b.r, b.ok
}

func quadExtrema(a, c, e float64) []float64 {
	d := a - 2*c + e
	if d == 0 {
		return nil
	}
	t := (a - c) / d
	if t > 0 && t < 1 {
		return []float64{t}
	}
	return nil
}

func quadAt(a, c, e point, t float64) point {
	u := 1 - t
	return point{u*u*a.x + 2*u*t*c.x + t*t*e.x, u*u*a.y + 2*u*t*c.y + t*t*e.y}
}

func cubicExtrema(p0, p1, p2, p3 float64) []float64 {
	// Derivative: 3[(p1-p0)(1-t)^2 + 2(p2-p1)t(1-t) + (p3-p2)t^2].
	a := -p0 + 3*p1 - 3*p2 + p3
	b := 2 * (p0 - 2*p1 + p2)
	c := p1 - p0
	var out []float64
	add := func(t float64) {
		if t > 0 && t < 1 {
			out = append(out, t)
		}
	}
	if math.Abs(a) < 1e-12 {
		if b != 0 {
			add(-c / b)
		}
		return out
	}
	disc := b*b - 4*a*c
	if disc < 0 {
		return nil
	}
	sq := math.Sqrt(disc)
	add((-b + sq) / (2 * a))
	add((-b - sq) / (2 * a))
	return out
}

func cubicAt(a, b, c, d point, t float64) point {
	u := 1 - t
	w0, w1, w2, w3 := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return point{w0*a.x + w1*b.x + w2*c.x + w3*d.x, w0*a.y + w1*b.y + w2*c.y + w3*d.y}
}

// transformRect returns the bounding box of rc mapped through m.
func transformRect(rc rect, m matrix) rect {
	var b bbox
	b.add(m.apply(point{rc.x0, rc.y0}))
	b.add(m.apply(point{rc.x1, rc.y0}))
	b.add(m.apply(point{rc.x1, rc.y1}))
	b.add(m.apply(point{rc.x0, rc.y1}))
	return b.r
}

// inheritedState returns the computed style of n from its document ancestors
// (mask, pattern, marker and filter content inherits from where it is defined,
// not from the element referencing it). The transform is not applied.
func (r *renderer) inheritedState(n *node) *state {
	if s, ok := r.docStates[n]; ok {
		return s
	}
	var s state
	if n.parent == nil {
		s = r.rootState
	} else {
		s = *r.inheritedState(n.parent)
	}
	s.applyProps(n)
	s.ctm = identity
	s.clip = nil
	if r.docStates == nil {
		r.docStates = map[*node]*state{}
	}
	if len(r.docStates) > 1<<16 {
		clear(r.docStates)
	}
	r.docStates[n] = &s
	return &s
}

// elemTransform returns the element's transform attribute (identity when
// absent or invalid).
func elemTransform(n *node) matrix {
	if tf, ok := n.get(aTransform); ok {
		if m, valid := parseTransform(tf); valid {
			return m
		}
	}
	return identity
}

// childrenBBox is the union of the children's boxes in the coordinate system
// of the container whose inner state is st.
func (r *renderer) childrenBBox(n *node, st *state, acc *bbox) {
	for _, k := range n.kids {
		if r.err != nil {
			return
		}
		r.addElemBBox(k, st, acc)
	}
}

// addElemBBox adds element k's box, in its parent's coordinates (so including
// k's own transform), to acc.
func (r *renderer) addElemBBox(k *node, parent *state, acc *bbox) {
	switch k.tag {
	case tagSVG, tagG, tagSwitch, tagUse, tagPath, tagRect, tagCircle, tagEllipse,
		tagLine, tagPolyline, tagPolygon, tagText, tagImage:
	default:
		return
	}
	if isDisplayNone(k) || !r.budget() {
		return
	}
	r.nest++
	defer func() { r.nest-- }()
	if r.nest > r.lim.MaxDepth*2 {
		r.fail(errLimit)
		return
	}
	ks := *parent
	ks.applyProps(k)
	tm := elemTransform(k)
	lb, ok := r.contentBBox(k, &ks)
	if !ok {
		return
	}
	tb := transformRect(lb, tm)
	acc.add(point{tb.x0, tb.y0})
	acc.add(point{tb.x1, tb.y1})
}

// contentBBox returns the object bounding box of k's content in k's own
// coordinate system (inside its transform attribute).
func (r *renderer) contentBBox(k *node, ks *state) (rect, bool) {
	switch k.tag {
	case tagPath, tagRect, tagCircle, tagEllipse, tagLine, tagPolyline, tagPolygon:
		var p path
		if !r.buildShape(k, ks, &p) {
			return rect{}, false
		}
		return tightBounds(&p)
	case tagText:
		gs := r.layoutTextNode(k, ks)
		return r.glyphsBBox(gs)
	case tagImage:
		href := k.str(aHref)
		if href == "" {
			return rect{}, false
		}
		x := ks.length(k.str(aX), 0, 0)
		y := ks.length(k.str(aY), 1, 0)
		w := ks.length(k.str(aWidth), 0, 0)
		h := ks.length(k.str(aHeight), 1, 0)
		if !(w > 0 && h > 0) {
			// Size from the decoded image when unspecified.
			img, _ := r.imageFor(k)
			if img == nil {
				return rect{}, false
			}
			iw, ih := float64(img.w), float64(img.h)
			switch {
			case k.str(aWidth) == "" && k.str(aHeight) == "":
				w, h = iw, ih
			case k.str(aWidth) == "":
				w = h * iw / ih
			default:
				h = w * ih / iw
			}
		}
		if !(w > 0 && h > 0) {
			return rect{}, false
		}
		return rect{x, y, x + w, y + h}, true
	case tagG:
		var b bbox
		r.childrenBBox(k, ks, &b)
		return b.r, b.ok
	case tagSwitch:
		for _, c := range k.kids {
			if c.tag == tagChars || !switchAccepts(c) {
				continue
			}
			var b bbox
			r.addElemBBox(c, ks, &b)
			return b.r, b.ok
		}
		return rect{}, false
	case tagSVG:
		if k.parent == nil {
			var b bbox
			r.childrenBBox(k, ks, &b)
			return b.r, b.ok
		}
		x := ks.length(k.str(aX), 0, 0)
		y := ks.length(k.str(aY), 1, 0)
		w := ks.length(k.str(aWidth), 0, ks.vw)
		h := ks.length(k.str(aHeight), 1, ks.vh)
		return r.viewportBBox(k, ks, x, y, w, h)
	case tagUse:
		href := k.str(aHref)
		if len(href) < 2 || href[0] != '#' {
			return rect{}, false
		}
		target := r.doc.ids[href[1:]]
		if target == nil {
			return rect{}, false
		}
		for p := k; p != nil; p = p.parent {
			if p == target {
				return rect{}, false
			}
		}
		x := ks.length(k.str(aX), 0, 0)
		y := ks.length(k.str(aY), 1, 0)
		var b bbox
		switch target.tag {
		case tagSymbol:
			s2 := *ks
			s2.applyProps(target)
			w := s2.length(k.str(aWidth), 0, ks.vw)
			h := s2.length(k.str(aHeight), 1, ks.vh)
			vr, ok := r.viewportBBox(target, &s2, 0, 0, w, h)
			if !ok {
				return rect{}, false
			}
			return transformRect(vr, translate(x, y)), true
		case tagSVG:
			s2 := *ks
			s2.applyProps(target)
			w := s2.length(target.str(aWidth), 0, s2.vw)
			h := s2.length(target.str(aHeight), 1, s2.vh)
			if v := k.str(aWidth); v != "" {
				w = s2.length(v, 0, w)
			}
			if v := k.str(aHeight); v != "" {
				h = s2.length(v, 1, h)
			}
			vr, ok := r.viewportBBox(target, &s2, s2.length(target.str(aX), 0, 0), s2.length(target.str(aY), 1, 0), w, h)
			if !ok {
				return rect{}, false
			}
			return transformRect(vr, translate(x, y)), true
		default:
			r.addElemBBox(target, ks, &b)
			if !b.ok {
				return rect{}, false
			}
			return transformRect(b.r, translate(x, y)), true
		}
	}
	return rect{}, false
}

// viewportBBox is the box of an svg/symbol element's children in the parent's
// coordinates (viewport clipping is ignored, as in usvg's object bbox).
func (r *renderer) viewportBBox(n *node, st *state, x, y, w, h float64) (rect, bool) {
	if !(w > 0 && h > 0) {
		return rect{}, false
	}
	m := translate(x, y)
	inner := *st
	if vb, ok := parseViewBox(n.str(aViewBox)); ok {
		m = m.mul(viewBoxTransform(vb, w, h, n.str(aPreserveAspectRatio)))
		inner.vw, inner.vh = vb.w(), vb.h()
	} else {
		inner.vw, inner.vh = w, h
	}
	var b bbox
	r.childrenBBox(n, &inner, &b)
	if !b.ok {
		return rect{}, false
	}
	return transformRect(b.r, m), true
}

// nonZero reports whether rc has a positive width and height.
func nonZero(rc rect, ok bool) (rect, bool) {
	if !ok || !(rc.w() > 0 && rc.h() > 0) || math.IsInf(rc.w(), 0) || math.IsInf(rc.h(), 0) {
		return rect{}, false
	}
	return rc, true
}

// glyphsBBox is the tight box of the glyph outlines (and decorations).
func (r *renderer) glyphsBBox(gs []glyphPos) (rect, bool) {
	var b bbox
	for _, g := range gs {
		if g.g.Face == nil {
			continue
		}
		p := r.glyphOutline(g.g.Face, g.g.ID)
		if p == nil {
			continue
		}
		gb, ok := tightBounds(p)
		if !ok {
			continue
		}
		gr := transformRect(gb, g.matrix())
		b.add(point{gr.x0, gr.y0})
		b.add(point{gr.x1, gr.y1})
	}
	return b.r, b.ok
}
