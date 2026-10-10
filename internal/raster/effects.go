package raster

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
)

// Group effects: filter, mask, opacity and mix-blend-mode follow resvg's
// render_group order (content, filter, clip-path, mask, then opacity and blend
// when the layer is composited onto its parent).

// urlTarget resolves a url(#id) reference held by attribute id of n.
// present is false when the attribute is absent or not a local reference.
func (r *renderer) urlTarget(n *node, id attrID) (target *node, present bool) {
	v, ok := n.get(id)
	if !ok {
		return nil, false
	}
	ref, isURL := parseURLRef(v)
	if !isURL {
		return nil, false
	}
	return r.doc.ids[ref], true
}

// effectRefs resolves the mask and filter references of n. ok is false when
// the element must not be rendered (broken filter link, mask that is not a
// mask element, ...), following usvg.
func (r *renderer) effectRefs(n *node) (mk *node, fls []*node, ok bool) {
	if t, present := r.urlTarget(n, aMask); present && t != nil {
		if t.tag != tagMask {
			return nil, nil, false
		}
		if !r.active[t] {
			mk = t
		}
	}
	if v, has := n.get(aFilter); has {
		v = strings.TrimSpace(v)
		if v != "" && v != "none" {
			invalid := false
			any := false
			for _, item := range splitFilterList(v) {
				any = true
				ref, isURL := parseURLRef(item)
				if !isURL {
					continue // CSS filter functions are not supported
				}
				t := r.doc.ids[ref]
				if t == nil || t.tag != tagFilter {
					invalid = true
					continue
				}
				fls = append(fls, t)
			}
			if any && len(fls) == 0 && invalid {
				return nil, nil, false
			}
		}
	}
	return mk, fls, true
}

// splitFilterList splits a filter value into its top-level items.
func splitFilterList(v string) []string {
	var out []string
	for {
		v = strings.TrimSpace(v)
		if v == "" {
			return out
		}
		depth := 0
		i := 0
		for ; i < len(v); i++ {
			c := v[i]
			if c == '(' {
				depth++
			} else if c == ')' {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
		}
		out = append(out, v[:i])
		v = v[i:]
		if len(out) > 32 {
			return out
		}
	}
}

// maskSpec is a resolved mask for one referencing element.
type maskSpec struct {
	node    *node
	rect    rect
	alpha   bool
	obbBox  *rect // set when maskContentUnits is objectBoundingBox
	nested  *maskSpec
	maskAll bool
}

func (r *renderer) resolveMask(mk *node, st *state, bb rect, hasBB bool, depth int) (*maskSpec, bool) {
	if mk == nil || mk.tag != tagMask || depth > 4 {
		return nil, false
	}
	obb := true
	if v := strings.TrimSpace(mk.str(aMaskUnits)); v == "userSpaceOnUse" {
		obb = false
	}
	contentOBB := strings.TrimSpace(mk.str(aMaskContentUnits)) == "objectBoundingBox"
	get := func(id attrID, def float64, axis int) float64 {
		if v, ok := mk.get(id); ok {
			if f, ok := st.unitValue(v, obb, axis); ok {
				return f
			}
		}
		return def
	}
	var x, y, w, h float64
	if obb {
		x, y, w, h = get(aX, -0.1, 0), get(aY, -0.1, 1), get(aWidth, 1.2, 0), get(aHeight, 1.2, 1)
	} else {
		x, y = get(aX, -0.1*st.vw, 0), get(aY, -0.1*st.vh, 1)
		w, h = get(aWidth, 1.2*st.vw, 0), get(aHeight, 1.2*st.vh, 1)
	}
	if !(w > 0 && h > 0) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return nil, false
	}
	rc := rect32(x, y, w, h)
	ms := &maskSpec{node: mk, rect: rc}
	if obb {
		if !hasBB {
			ms.maskAll = true
			return ms, true
		}
		ms.rect = bboxTransformRect(rc, bb)
	}
	if t, present := r.urlTarget(mk, aMask); present && t != nil && t != mk && !r.active[t] {
		nested, ok := r.resolveMask(t, st, bb, hasBB, depth+1)
		if !ok {
			return nil, false
		}
		ms.nested = nested
	}
	ms.alpha = strings.TrimSpace(mk.str(aMaskType)) == "alpha"
	if contentOBB {
		if !hasBB {
			return nil, false
		}
		b := bb
		ms.obbBox = &b
	}
	return ms, true
}

// applyMask multiplies the current canvas by the mask's coverage. st.ctm maps
// user space to the current canvas.
func (r *renderer) applyMask(ms *maskSpec, st *state) {
	content := r.cv
	if content.dirty.empty() {
		return
	}
	prev := r.pushLayer()
	if prev == nil {
		return
	}
	layer := r.cv
	base := *r.inheritedState(ms.node)
	base.ctm = st.ctm
	base.visible = true
	base.clip = r.rectClip(&state{}, st.ctm, ms.rect)
	if r.active == nil {
		r.active = map[*node]bool{}
	}
	if !base.clip.r.empty() {
		if ms.obbBox != nil {
			b := *ms.obbBox
			base.ctm = st.ctm.mul(matrix{b.w(), 0, 0, b.h(), b.x0, b.y0})
		}
		r.active[ms.node] = true
		r.renderChildren(ms.node, &base)
		delete(r.active, ms.node)
	}
	r.cv = prev
	r.depth--
	if ms.nested != nil {
		r.applyMask(ms.nested, st)
	}
	if d := content.dirty.intersect(content.bounds()); !d.empty() && r.chargeOps(d.w()*d.h()) {
		multiplyByMask(content, layer, ms.alpha)
	}
	layer.clearDirty()
	r.pool = append(r.pool, layer)
}

// multiplyByMask scales dst's pixels by the luminance (or alpha) coverage of
// mk, tiny-skia's Mask::from_pixmap followed by Pixmap::apply_mask.
func multiplyByMask(dst, mk *canvas, alpha bool) {
	reg := dst.dirty.intersect(dst.bounds())
	for y := reg.y0; y < reg.y1; y++ {
		for x := reg.x0; x < reg.x1; x++ {
			i := (y*dst.w + x) * 4
			d := dst.pix[i : i+4 : i+4]
			if d[3] == 0 && d[0] == 0 {
				continue
			}
			m := mk.pix[i : i+4 : i+4]
			var cov uint32
			if alpha {
				cov = uint32(m[3])
			} else if m[3] != 0 {
				ca := float32(m[3]) / 255
				rr, gg, bb := float32(m[0])/255, float32(m[1])/255, float32(m[2])/255
				rr, gg, bb = rr/ca, gg/ca, bb/ca
				luma := float32(rr*0.2126) + float32(gg*0.7152) + float32(bb*0.0722)
				v := float32(float32(luma*ca) * 255)
				if v > 255 {
					v = 255
				} else if v < 0 || v != v {
					v = 0
				}
				cov = uint32(math.Ceil(float64(v)))
			}
			if cov == 255 {
				continue
			}
			d[0] = uint8(div255(uint32(d[0]) * cov))
			d[1] = uint8(div255(uint32(d[1]) * cov))
			d[2] = uint8(div255(uint32(d[2]) * cov))
			d[3] = uint8(div255(uint32(d[3]) * cov))
		}
	}
}

// renderEffects draws n through its mask and/or filters.
func (r *renderer) renderEffects(n *node, st *state, opacity float64, blend blendMode, mk *node, fls []*node) {
	bb, hasBB := nonZero(r.contentBBox(n, st))
	if r.err != nil {
		return
	}
	var ms *maskSpec
	if mk != nil {
		var ok bool
		ms, ok = r.resolveMask(mk, st, bb, hasBB, 0)
		if !ok || ms.maskAll {
			return
		}
	}
	var specs []*filterSpec
	for _, f := range fls {
		if r.active[f] {
			continue
		}
		if sp := r.resolveFilter(f, st, bb, hasBB); sp != nil {
			specs = append(specs, sp)
		}
	}
	if len(fls) > 0 && len(specs) == 0 {
		return
	}
	if len(specs) == 0 {
		prev := r.pushLayer()
		if prev == nil {
			return
		}
		s2 := *st
		r.drawContent(n, &s2, 1, blendNormal)
		if ms != nil {
			r.applyMask(ms, st)
		}
		r.popLayer(prev, opacity, blend)
		return
	}

	// Filter layer: the union of the filter regions, in device pixels.
	ur := specs[0].rect
	for _, sp := range specs[1:] {
		ur.x0, ur.y0 = math.Min(ur.x0, sp.rect.x0), math.Min(ur.y0, sp.rect.y0)
		ur.x1, ur.y1 = math.Max(ur.x1, sp.rect.x1), math.Max(ur.y1, sp.rect.y1)
	}
	dr := transformRect32(ur, st.ctm)
	if !(dr.w() > 0 && dr.h() > 0) {
		return
	}
	ib := toIntRect(dr).intersect(irect{-2 * r.cw, -2 * r.ch, 3 * r.cw, 3 * r.ch})
	if ib.empty() {
		return
	}
	area := budget.MulInt(ib.w(), ib.h())
	if area > r.lim.MaxFilterPixels {
		return // filter region too large to process
	}
	if r.depth >= r.lim.MaxLayerDepth {
		r.fail(errLimit)
		return
	}
	nPrims := 1
	for _, sp := range specs {
		nPrims += len(sp.prims)
	}
	if !r.chargePixels(budget.MulInt(area, nPrims)) {
		return
	}
	sub := r.allocCanvas(ib.w(), ib.h())
	if sub == nil {
		return
	}
	parent := r.cv
	saved := struct {
		cw, ch int
		pool   []*canvas
	}{r.cw, r.ch, r.pool}
	r.cv, r.cw, r.ch, r.pool = sub, sub.w, sub.h, nil
	r.depth++
	s2 := *st
	s2.ctm = translate(float64(-ib.x0), float64(-ib.y0)).mul(st.ctm)
	s2.clip = nil
	r.drawContent(n, &s2, 1, blendNormal)
	if r.err == nil {
		r.applyFilters(specs, s2.ctm, sub)
		if ms != nil {
			sub.dirty = sub.bounds()
			r.applyMask(ms, &s2)
		}
	}
	r.depth--
	r.releasePool(r.pool)
	r.cv, r.cw, r.ch, r.pool = parent, saved.cw, saved.ch, saved.pool
	if r.err == nil && r.chargeOps(ib.w()*ib.h()) {
		compositeLayerAt(parent, sub, ib.x0, ib.y0, opacity, st.clip, blend)
	}
	r.releaseCanvas(sub)
}
