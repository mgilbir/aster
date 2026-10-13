package raster

import "strings"

// feImage is resvg's apply_image and usvg's convert_image: an element of the
// document (href="#id") or an image (a data: URI, or bytes Options.Images
// fetched, as for <image>) drawn into the filter region. resvg draws it
// with the device scale only, from the corner of the primitive's
// sub-region: an element at its own user coordinates measured from there
// (not where it would be drawn in the document), an image fitted
// (preserveAspectRatio) to the sub-region's size. The result is sRGB.

// feImageSpec is a resolved feImage.
type feImageSpec struct {
	fe     *node   // the feImage: its image-rendering
	target *node   // href="#id": the element drawn
	href   string  // otherwise: the image's href
	w, h   float64 // the sub-region's size, user space (an image's viewport)
}

// feImageOf resolves fe as usvg's convert_image: nil (the dummy, a
// transparent result) without an href, or with a reference to no element.
func (r *renderer) feImageOf(fe *node, sub rect) *feImageSpec {
	href := strings.TrimSpace(fe.str(aHref))
	if href == "" {
		return nil
	}
	s := &feImageSpec{fe: fe, w: sub.w(), h: sub.h()}
	if href[0] == '#' {
		if s.target = r.doc.ids[href[1:]]; s.target == nil {
			return nil
		}
		return s
	}
	s.href = href
	return s
}

// feImage draws the primitive's element or image into a transparent image
// the size of the canvas. The drawing is the render's own: each element it
// draws is counted, polled and charged as any other (MaxRenderNodes,
// MaxPixelOps), and it is one more layer deep (MaxLayerDepth), which bounds
// a chain of feImages drawing elements whose filters draw others. A filter
// being applied is not applied again inside its own feImage: an element
// that would be is not drawn there (usvg's tree recurses until its stack
// overflows; the element that names the feImage's own filter has had its
// filter set to none at parse, as usvg does: fixRecursiveFeImage).
func (run *filterRun) feImage(p *fprim) (fimage, bool) {
	pix := run.blank()
	res := fimage{pix: pix, region: run.region, owned: true}
	r := run.r
	if r == nil {
		return res, true
	}
	sub := toIntRect(transformRect32(p.rect, run.ts))
	sx, sy := tsScale(run.ts)
	ts := matrix{sx, 0, 0, sy, float64(sub.x0), float64(sub.y0)}
	fi := p.img
	var img *rasterImage
	if fi.target == nil {
		// One that cannot be read is not drawn (and reported) as an
		// <image> is not; a limit fails the render.
		if img, _ = r.imageHref(fi.href); img == nil {
			return res, r.err == nil
		}
	}
	cv := &canvas{w: run.w, h: run.h, pix: pix}
	return res, r.drawInto(cv, func() {
		if fi.target != nil {
			st := r.rootState
			if fi.target.parent != nil {
				st = *r.inheritedState(fi.target.parent)
			}
			st.ctm, st.clip = ts, nil
			r.renderElement(fi.target, &st)
			return
		}
		st := *r.inheritedState(fi.fe)
		st.ctm, st.clip, st.visible = ts, nil, true
		r.drawImage(img, 0, 0, fi.w, fi.h, fi.fe.str(aPreserveAspectRatio), &st, 1, blendNormal)
	})
}

// drawInto draws fn onto cv (charged by its owner), which replaces the
// current canvas meanwhile, one layer deeper. Filter layers drawn there are
// bounded by cv, as resvg bounds them by the feImage's region. It reports
// whether the render may go on.
func (r *renderer) drawInto(cv *canvas, fn func()) bool {
	if r.depth >= r.lim.MaxLayerDepth {
		r.fail(errRasterLimit)
		return false
	}
	parent, cw, ch, pool, bound := r.cv, r.cw, r.ch, r.pool, r.inFeImage
	r.cv, r.cw, r.ch, r.pool, r.inFeImage = cv, cv.w, cv.h, nil, true
	r.depth++
	fn()
	r.depth--
	r.releasePool(r.pool)
	r.cv, r.cw, r.ch, r.pool, r.inFeImage = parent, cw, ch, pool, bound
	return r.err == nil
}
