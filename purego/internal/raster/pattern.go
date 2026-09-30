package raster

import (
	"math"
	"strings"
)

// Pattern fills follow resvg: the tile is rendered once into a pixmap of
// round(w*sx) x round(h*sy) pixels (sx, sy being the scale of the path
// transform combined with patternTransform), then sampled with repeat tiling.
// tiny-skia samples with the Mitchell bicubic filter unless the resulting
// mapping is a pure translation, in which case it uses nearest neighbour.

type patternKey struct {
	n       *node
	pw, ph  int
	sx, sy  float64
	content matrix
	tw, th  float64
}

type patternTile struct {
	w, h int
	pix  []uint8 // premultiplied RGBA
}

// subRender renders into a fresh w x h canvas that temporarily replaces the
// current one, returning it (nil when the layer budget is exhausted).
func (r *renderer) subRender(w, h int, fn func()) *canvas {
	if r.depth >= r.lim.MaxLayerDepth {
		r.fail(errLimit)
		return nil
	}
	parent := r.cv
	cw, ch, pool := r.cw, r.ch, r.pool
	sub := r.allocCanvas(w, h)
	if sub == nil {
		return nil
	}
	r.cv, r.cw, r.ch, r.pool = sub, w, h, nil
	r.depth++
	fn()
	r.depth--
	r.releasePool(r.pool)
	r.releaseCanvas(sub) // the tile outlives the render budget; patternBytes caps the cache
	r.cv, r.cw, r.ch, r.pool = parent, cw, ch, pool
	return sub
}

func (r *renderer) patternAttr(pn *node, id attrID) (string, bool) {
	for i := 0; pn != nil && i < 8; i++ {
		if pn.tag != tagPattern {
			return "", false
		}
		if v, ok := pn.get(id); ok {
			return v, true
		}
		pn = r.hrefTarget(pn)
	}
	return "", false
}

// patternPaint resolves a pattern fill. handled is false when the pattern is
// invalid (the fallback colour then applies); ok is false when nothing is drawn.
func (r *renderer) patternPaint(pn *node, opacity float64, st *state, bbox func() (rect, bool)) (src paintSrc, ok, handled bool) {
	if r.active[pn] {
		return paintSrc{}, false, true // recursive pattern
	}
	// Content comes from the first pattern in the href chain that has children.
	var owner *node
	for p, i := pn, 0; p != nil && i < 8; i++ {
		if p.tag != tagPattern {
			return paintSrc{}, false, false
		}
		if len(p.kids) > 0 {
			owner = p
			break
		}
		p = r.hrefTarget(p)
	}
	if owner == nil {
		return paintSrc{}, false, false
	}
	pst := r.inheritedState(pn)
	unitsOBB := true
	if v, has := r.patternAttr(pn, aPatternUnits); has && strings.TrimSpace(v) == "userSpaceOnUse" {
		unitsOBB = false
	}
	contentOBB := false
	if v, has := r.patternAttr(pn, aPatternContentUnits); has && strings.TrimSpace(v) == "objectBoundingBox" {
		contentOBB = true
	}
	num := func(id attrID, axis int) float64 {
		if v, has := r.patternAttr(pn, id); has {
			if f, good := pst.unitValue(v, unitsOBB, axis); good {
				return f
			}
		}
		return 0
	}
	x, y, w, h := num(aX, 0), num(aY, 1), num(aWidth, 0), num(aHeight, 1)
	if !(w > 0 && h > 0) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return paintSrc{}, false, false
	}
	tile := rect32(x, y, w, h)
	var vb rect
	hasVB := false
	if v, has := r.patternAttr(pn, aViewBox); has {
		vb, hasVB = parseViewBox(v)
	}
	var bb rect
	if unitsOBB || contentOBB {
		b, good := nonZero(bbox())
		if !good {
			return paintSrc{}, false, true // pattern on a zero-sized shape
		}
		bb = b
	}
	if unitsOBB {
		tile = bboxTransformRect(tile, bb)
	}
	content := identity
	switch {
	case hasVB:
		par := ""
		if v, has := r.patternAttr(pn, aPreserveAspectRatio); has {
			par = v
		}
		content = viewBoxTransform(vb, tile.w(), tile.h(), par)
	case contentOBB:
		content = scaleM(bb.w(), bb.h())
	}
	ptm := identity
	if v, has := r.patternAttr(pn, aPatternTransform); has {
		if t, good := parseTransform(v); good {
			ptm = t
		}
	}
	if !ptm.isFinite() || !ptm.invertible() {
		return paintSrc{}, false, true
	}
	sx, sy := tsScale(st.ctm.mul(ptm))
	pw := int(math.Round(float64(float32(tile.w()) * float32(sx))))
	ph := int(math.Round(float64(float32(tile.h()) * float32(sy))))
	if pw < 1 || ph < 1 {
		return paintSrc{}, false, true
	}
	if pw > r.lim.MaxDimension || ph > r.lim.MaxDimension || pw*ph > r.lim.MaxPixels {
		return paintSrc{}, false, true
	}
	key := patternKey{pn, pw, ph, sx, sy, content, bb.w(), bb.h()}
	img := r.patterns[key]
	if img == nil {
		if !r.chargePixels(pw * ph) {
			return paintSrc{}, false, true
		}
		var sub *canvas
		func() {
			r.active[pn] = true
			defer delete(r.active, pn)
			base := *r.inheritedState(owner)
			base.ctm = scaleM(sx, sy).mul(content)
			base.clip = nil
			base.visible = true
			sub = r.subRender(pw, ph, func() { r.renderChildren(owner, &base) })
		}()
		if sub == nil || r.err != nil {
			return paintSrc{}, false, true
		}
		img = &patternTile{w: pw, h: ph, pix: sub.pix}
		if r.patterns == nil || r.patternBytes+len(sub.pix) > 256<<20 || len(r.patterns) >= 64 {
			r.patterns = map[patternKey]*patternTile{}
			r.patternBytes = 0
		}
		r.patterns[key] = img
		r.patternBytes += len(sub.pix)
	}
	// Shader transform: pattern space -> device.
	full := st.ctm.mul(ptm).mul(translate(tile.x0, tile.y0)).mul(scaleM(1/sx, 1/sy))
	inv, good := full.invert()
	if !good || !full.isFinite() {
		return paintSrc{}, false, true
	}
	nearest := math.Abs(full.a-1) < 1e-6 && math.Abs(full.d-1) < 1e-6 && math.Abs(full.b) < 1e-9 && math.Abs(full.c) < 1e-9
	return paintSrc{sh: &patternShader{tile: img, inv: inv, nearest: nearest, opacity: float32(opacity)}}, true, true
}

type patternShader struct {
	tile    *patternTile
	inv     matrix
	nearest bool
	opacity float32
}

func wrapIdx(i, n int) int {
	i %= n
	if i < 0 {
		i += n
	}
	return i
}

// bicubic weights of the Mitchell filter as used by tiny-skia.
func bicubicNear(t float32) float32 {
	return ((float32(-21.0/18)*t+27.0/18)*t+9.0/18)*t + 1.0/18
}

func bicubicFar(t float32) float32 { return t * t * (float32(7.0/18)*t - 6.0/18) }

func (s *patternShader) shadeRow(y, x0 int, dst []uint8) {
	n := len(dst) / 4
	t := s.tile
	py := float64(y) + 0.5
	for i := 0; i < n; i++ {
		px := float64(x0+i) + 0.5
		u := s.inv.a*px + s.inv.c*py + s.inv.e
		v := s.inv.b*px + s.inv.d*py + s.inv.f
		var c [4]float32
		if s.nearest {
			ix := wrapIdx(int(math.Floor(u)), t.w)
			iy := wrapIdx(int(math.Floor(v)), t.h)
			p := t.pix[(iy*t.w+ix)*4 : (iy*t.w+ix)*4+4]
			c = [4]float32{float32(p[0]), float32(p[1]), float32(p[2]), float32(p[3])}
		} else {
			fx := float32(u + 0.5 - math.Floor(u+0.5))
			fy := float32(v + 0.5 - math.Floor(v+0.5))
			wx := [4]float32{bicubicFar(1 - fx), bicubicNear(1 - fx), bicubicNear(fx), bicubicFar(fx)}
			wy := [4]float32{bicubicFar(1 - fy), bicubicNear(1 - fy), bicubicNear(fy), bicubicFar(fy)}
			for j := 0; j < 4; j++ {
				iy := wrapIdx(int(math.Floor(v-1.5+float64(j))), t.h)
				for k := 0; k < 4; k++ {
					ix := wrapIdx(int(math.Floor(u-1.5+float64(k))), t.w)
					p := t.pix[(iy*t.w+ix)*4 : (iy*t.w+ix)*4+4]
					wgt := wx[k] * wy[j]
					c[0] += float32(p[0]) * wgt
					c[1] += float32(p[1]) * wgt
					c[2] += float32(p[2]) * wgt
					c[3] += float32(p[3]) * wgt
				}
			}
			for k := 0; k < 3; k++ {
				if c[k] > c[3] {
					c[k] = c[3]
				}
				if c[k] < 0 {
					c[k] = 0
				}
			}
			if c[3] < 0 {
				c[3] = 0
			} else if c[3] > 255 {
				c[3] = 255
			}
		}
		for k := 0; k < 4; k++ {
			dst[i*4+k] = uint8(c[k]*s.opacity + 0.5)
		}
	}
}
