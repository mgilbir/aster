package raster

import "strings"

// feImage is Filter Effects 1 §9.15: an element of the document
// (href="#id") or an image (a data: URI, or bytes Options.Images fetched,
// as for <image>) drawn into the filter region, its result sRGB. "If the
// href references a stand-alone image resource such as a JPEG, PNG or SVG
// file, then the image resource is rendered according to the behavior of
// the image element; otherwise, the referenced resource is rendered
// according to the behavior of the use element. In either case, the current
// user coordinate system depends on the value of attribute primitiveUnits
// on the filter element. The processing of the preserveAspectRatio
// attribute on the feImage element is identical to that of the image
// element." An image is drawn as an <image> whose x, y, width and height
// are the primitive's sub-region; an element as a <use> draws it, at its
// own coordinates in the user space of the filtered element (under
// primitiveUnits="objectBoundingBox", the space of its bounding box), with
// the filtered element's whole transform. resvg (and Chromium) draw an
// element with the device scale alone, moved to the sub-region's corner,
// whatever the units, and an image with the device scale and translation
// alone.

// feImageSpec is a resolved feImage.
type feImageSpec struct {
	fe     *node  // the feImage: its image-rendering
	target *node  // href="#id": the element drawn
	href   string // otherwise: the image's href
	sub    rect   // the sub-region, user space: an image's viewport
	// units maps the primitiveUnits coordinate system an element is drawn
	// in to user space: the bounding box's under objectBoundingBox.
	units matrix
}

// feImageOf resolves fe: nil (a transparent result) without an href, or
// with a reference to no element ("A href reference that is an empty image
// (zero width or zero height), that fails to download, is non-existent, or
// that cannot be displayed (...) fills the filter primitive subregion with
// transparent black").
func (r *renderer) feImageOf(fe *node, sub rect, u primUnits) *feImageSpec {
	href := strings.TrimSpace(fe.str(aHref))
	if href == "" {
		return nil
	}
	s := &feImageSpec{fe: fe, sub: sub, units: identity}
	if u.obb {
		s.units = matrix{u.bb.w(), 0, 0, u.bb.h(), u.bb.x0, u.bb.y0}
	}
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
// a chain of feImages drawing elements whose filters draw others.
//
// A reference that comes back to a filter being applied is circular (the
// feImage draws an element whose filter draws the feImage again, directly
// or through others): SVG 2 §16.1.2, "URL references that directly or
// indirectly reference themselves are treated as invalid circular
// references", and an invalid reference fills the sub-region with
// transparent black (§9.15), so every feImage on the circle gives a
// transparent result. usvg instead sets the filter of an element that
// names the feImage's own filter to none, where the feImage draws it and
// wherever else the document does (fix_recursive_fe_image), and its own
// tree recurses until its stack overflows on a longer circle.
func (run *filterRun) feImage(p *fprim) (fimage, bool) {
	pix := run.blank()
	res := fimage{pix: pix, region: run.region, owned: true}
	r := run.r
	if r == nil {
		return res, true
	}
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
	depth := r.feDepth + 1
	ok := r.drawInto(cv, func() {
		if fi.target != nil {
			st := r.rootState
			if fi.target.parent != nil {
				st = *r.inheritedState(fi.target.parent)
			}
			st.ctm, st.clip = run.ts.mul(fi.units), nil
			r.renderElement(fi.target, &st)
			return
		}
		st := *r.inheritedState(fi.fe)
		st.ctm, st.clip, st.visible = run.ts, nil, true
		r.drawImage(img, fi.sub.x0, fi.sub.y0, fi.sub.w(), fi.sub.h(), fi.fe.str(aPreserveAspectRatio), &st, 1, blendNormal)
	})
	if r.cycleFloor != 0 && r.cycleFloor <= depth {
		// On a circle of references: transparent black.
		if !run.rows(1, func(y0, y1 int) { clear(rowsOf(pix, run.w, y0, y1)) }) {
			ok = false
		}
		if r.cycleFloor == depth {
			r.cycleFloor = 0 // the circle closes here: the feImages outside it are not on it
		}
	}
	return res, ok
}

// drawInto draws fn onto cv (charged by its owner), which replaces the
// current canvas meanwhile, one layer and one feImage deeper. It reports
// whether the render may go on.
func (r *renderer) drawInto(cv *canvas, fn func()) bool {
	if r.depth >= r.lim.MaxLayerDepth {
		r.fail(errRasterLimit)
		return false
	}
	parent, cw, ch, pool := r.cv, r.cw, r.ch, r.pool
	r.cv, r.cw, r.ch, r.pool = cv, cv.w, cv.h, nil
	r.depth++
	r.feDepth++
	fn()
	r.feDepth--
	r.depth--
	r.releasePool(r.pool)
	r.cv, r.cw, r.ch, r.pool = parent, cw, ch, pool
	return r.err == nil
}
