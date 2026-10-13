// Package raster renders SVG to pixels in pure Go: a scanline rasterizer with
// exact winding-rule handling, a stroker, gradients and patterns, clipping,
// masks, filters, markers, group opacity, text from glyph outlines and PNG
// output. It targets the SVG that Vega's SVG renderer emits plus general-purpose
// SVG (paths, basic shapes, use/symbol/nested svg, clipPath, mask, pattern,
// marker, the common filter primitives, <style> sheets, data: images and
// text/tspan) and matches resvg 0.45's output to within anti-aliasing noise;
// the tests compare both on a corpus and on synthetic documents.
//
// Supported beyond the basics:
//
//   - CSS: <style> elements (type text/css or none) with type, universal,
//     class, id, attribute and :first-child selectors, descendant, child and
//     adjacent-sibling combinators, selector lists, specificity ordering and
//     !important, following resvg's cascade (presentation attributes < style
//     sheet < style attribute). Only presentation attributes are taken from
//     style sheets and style attributes.
//   - mask (luminance and alpha, maskUnits, maskContentUnits, nested masks),
//     clipPath with clipPathUnits=objectBoundingBox and <text> children.
//   - filter: feGaussianBlur, feOffset, feFlood, feColorMatrix, feComposite
//     (all operators), feMerge, feBlend, feDropShadow, feComponentTransfer,
//     filterUnits, primitiveUnits, primitive sub-regions, result/in chaining,
//     color-interpolation-filters. Other primitives yield transparent black.
//   - pattern fills and strokes (patternUnits, patternContentUnits,
//     patternTransform, viewBox, href inheritance) and markers (start, mid,
//     end, orient auto / auto-start-reverse / angle, markerUnits, refX/refY,
//     viewBox, overflow).
//   - shape-rendering and text-rendering (crispEdges / optimizeSpeed turn
//     anti-aliasing off), paint-order, image-rendering, per-character text
//     rotate, textLength with lengthAdjust, and text decorations and baseline
//     shifts placed from the font's own metrics.
//
// Not implemented: CSS filter functions (blur(), drop-shadow(), ...), feImage,
// feTile, feMorphology, feConvolveMatrix, feDisplacementMap, feTurbulence and
// the lighting filters, textPath, context-fill/stroke, external style
// sheets and references into other documents (an <image> fetches its bytes
// through Options.Images; one that is itself SVG is not drawn), and the
// writing-mode / vertical text.
//
// The input is untrusted. Element count, nesting, canvas size, <use>
// expansion, layer depth, embedded image size, style sheet size and matching
// work, filter region size, the pixels spent on filters and pattern tiles,
// the path geometry held and computed, and every pixel buffer (layers, clip
// coverage, filter intermediates) are all bounded (see Limits); decoded
// images and the render's caches are charged to the call's ledger before
// they are allocated; nothing is fetched from the network or the
// filesystem; malformed input yields an error or is skipped, never a panic.
package raster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
	"github.com/mgilbir/aster/internal/pngopt"
)

// Options configures Render.
type Options struct {
	// Scale multiplies the SVG's user-space size; zero means 1. The pixel size
	// is ceil(width*scale) x ceil(height*scale), like resvg.
	Scale float64
	// Fonts are extra fonts (family name + file data) added to the embedded
	// Liberation and Noto Emoji faces. Ignored when Shaper is set. Prefer
	// building a Shaper once with NewShaper when rendering repeatedly.
	Fonts []FontData
	// Shaper turns text into glyphs. nil selects the default shaper over the
	// embedded fonts plus Fonts.
	Shaper Shaper
	// Images fetches the image an <image> element refers to by anything but
	// a data: URI (which is decoded in place). It is called once per distinct
	// href, before drawing, from several goroutines at once. nil leaves such
	// images undrawn: the rasterizer touches neither the network nor the
	// filesystem itself. An image that cannot be fetched is skipped, as a
	// broken image is; an error wrapping budget.ErrLimit fails the render.
	Images func(ctx context.Context, href string) ([]byte, error)
	// ImageError, when non-nil, is told of each <image> left undrawn because
	// it could not be read (not fetched, not a PNG, JPEG or GIF, or not
	// decodable), with its href, on the rendering goroutine. Errors wrapping
	// budget.ErrLimit fail the render instead.
	ImageError func(href string, err error)
	// Background, when non-nil, is painted under the drawing. The default is
	// fully transparent.
	Background color.Color
	// Limits bounds resource use; zero fields take safe defaults.
	Limits Limits
	// Context, when non-nil, is polled while rendering (per element, per
	// block of scanlines, between filter primitives and when compositing
	// layers); Render returns the context's error once it is done.
	Context context.Context
}

// Render rasterizes svg and returns a non-premultiplied image.
func Render(svg []byte, opts Options) (img *image.NRGBA, err error) {
	defer func() {
		if p := recover(); p != nil {
			if stop, ok := p.(*budget.Stop); ok {
				// A bound stopped drawing where it cannot return an error:
				// the shaper's budget or the context in a text run, the
				// geometry budget in flattening or stroking.
				img, err = nil, stop.Err
				return
			}
			img, err = nil, fmt.Errorf("raster: internal error: %v", p)
		}
	}()
	lim := opts.Limits.withDefaults()
	if opts.Context != nil {
		if err := opts.Context.Err(); err != nil {
			return nil, err
		}
	}
	scale := opts.Scale
	if scale == 0 {
		scale = 1
	}
	if !(scale > 0) || math.IsInf(scale, 0) {
		return nil, fmt.Errorf("raster: invalid scale %v (must be a positive, finite number)", opts.Scale)
	}
	if len(svg) == 0 {
		return nil, errors.New("raster: empty SVG input")
	}
	if len(svg) > lim.MaxInputBytes {
		return nil, fmt.Errorf("raster: SVG input is %d bytes, limit is %d: %w", len(svg), lim.MaxInputBytes, errLimit)
	}
	doc, err := parseDocumentCtx(opts.Context, string(svg), lim)
	if err != nil {
		return nil, err
	}
	root := doc.root

	// Document size (usvg semantics: width/height default to the viewBox size).
	vb, hasVB := parseViewBox(root.str(aViewBox))
	rootState := initialState(0, 0)
	if hasVB {
		rootState.vw, rootState.vh = vb.w(), vb.h()
	} else {
		rootState.vw, rootState.vh = 100, 100
	}
	sizeOf := func(id attrID, axis int, vbLen float64) (float64, bool) {
		v := root.str(id)
		if v == "" {
			if hasVB {
				return vbLen, true
			}
			return 0, false
		}
		f, u, ok := parseLength(v)
		if !ok {
			return 0, false
		}
		if u == "%" {
			if !hasVB {
				return 0, false
			}
			return f / 100 * vbLen, true
		}
		return rootState.toPx(f, u, axis), true
	}
	w, okW := sizeOf(aWidth, 0, vb.w())
	h, okH := sizeOf(aHeight, 1, vb.h())
	if !okW || !okH || !(w > 0) || !(h > 0) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return nil, errors.New("raster: SVG has no valid size (width/height or viewBox required)")
	}
	// resvg keeps the size as f32 and takes ceil in f64 after scaling.
	pw := math.Ceil(float64(float32(w)) * scale)
	ph := math.Ceil(float64(float32(h)) * scale)
	if !(pw >= 1) || !(ph >= 1) {
		return nil, errors.New("raster: SVG has zero dimensions")
	}
	if pw > float64(lim.MaxDimension) || ph > float64(lim.MaxDimension) || pw*ph > float64(lim.MaxPixels) {
		return nil, fmt.Errorf("raster: output %.0fx%.0f px (width*height*scale^2) exceeds the limit of %d pixels / %d per side: %w",
			pw, ph, lim.MaxPixels, lim.MaxDimension, errLimit)
	}
	cw, ch := int(pw), int(ph)

	shaper := opts.Shaper
	if shaper == nil {
		var err error
		if len(opts.Fonts) == 0 {
			shaper, err = getDefaultShaper()
		} else {
			shaper, err = NewShaper(opts.Fonts...)
		}
		if err != nil {
			return nil, err
		}
	}
	fetched, err := fetchImages(opts.Context, doc, opts.Images, lim)
	if err != nil {
		return nil, err
	}
	r := &renderer{
		doc: doc, lim: lim, shaper: shaper,
		rast: newRasterizer(), cw: cw, ch: ch, active: map[*node]bool{},
		ctx: opts.Context, fetched: fetched, imageErr: opts.ImageError,
		ledger: budget.LedgerFrom(opts.Context),
	}
	if r.ledger == nil {
		// Called on its own (not through the public API), a render still
		// has its memory bounded: by a ledger of its own.
		r.ledger = budget.NewLedger(opts.Context, budget.LedgerLimits{MaxBytes: defaultLedgerBytes})
	}
	// The kept tiles, bitmaps and images go with the render.
	defer r.dropColourImages()
	defer r.dropPatterns()
	defer r.dropImages()
	r.initCaches()
	defer r.dropCaches()
	r.rast.ctx = opts.Context
	r.fl.r = r
	// The work budget follows the scale and the canvas (see PixelOps), from
	// the limits as given: withDefaults cannot know either.
	r.lim.MaxPixelOps = opts.Limits.PixelOps(scale, cw, ch)
	defer r.reportWork(opts.Context, scale)
	r.rootState = rootState
	if r.cv = r.allocCanvas(cw, ch); r.cv == nil {
		return nil, r.err
	}
	if opts.Background != nil {
		nc := color.NRGBAModel.Convert(opts.Background).(color.NRGBA)
		a := uint32(nc.A)
		pr := [4]uint8{uint8(mul255(uint32(nc.R), a)), uint8(mul255(uint32(nc.G), a)), uint8(mul255(uint32(nc.B), a)), nc.A}
		// A band at a time, polling: the canvas is the output's size.
		for b := 0; b < len(r.cv.pix); b += 4 * imageref.BandPixels {
			if !r.poll(true) {
				return nil, r.err
			}
			band := r.cv.pix[b:min(b+4*imageref.BandPixels, len(r.cv.pix))]
			for i := 0; i < len(band); i += 4 {
				copy(band[i:i+4], pr[:])
			}
		}
		r.cv.dirty = r.cv.bounds()
	}

	st := rootState
	st.ctm = scaleM(scale, scale)
	r.host = st.ctm
	if hasVB {
		st.ctm = st.ctm.mul(viewBoxTransform(vb, w, h, root.str(aPreserveAspectRatio)))
	}
	st.applyProps(root)
	r.renderRoot(root, &st)
	if r.err != nil {
		return nil, r.err
	}
	out := r.cv.toNRGBA(r)
	if out == nil {
		return nil, r.err
	}
	return out, nil
}

func (r *renderer) renderRoot(root *node, st *state) {
	if !st.ctm.isFinite() || !st.ctm.invertible() || isDisplayNone(root) {
		return
	}
	if tf, ok := root.get(aTransform); ok {
		if m, valid := parseTransform(tf); valid {
			st.ctm = st.ctm.mul(m)
		}
	}
	if cp, ok := root.get(aClipPath); ok {
		if id, isURL := parseURLRef(cp); isURL {
			if cn := r.doc.ids[id]; cn != nil {
				if cn.tag != tagClipPath {
					return
				}
				m := r.buildClipFor(cn, root, st)
				if m == nil || m.r.empty() {
					return
				}
				st.clip = m
			}
		}
	}
	opacity, _ := parseOpacity(root.str(aOpacity))
	layered := opacity < 1
	var prev *canvas
	if layered {
		if prev = r.pushLayer(); prev == nil {
			return
		}
	}
	r.renderChildren(root, st)
	if layered {
		r.popLayer(prev, opacity, blendNormal)
	}
}

// toNRGBA converts premultiplied pixels to a non-premultiplied image, in
// place: the canvas is done with, and its pixels become the image's, so the
// output costs no second canvas of memory (C103). c must not be used after.
// It polls r's context a band of pixels at a time (the canvas is as large as
// the output, which a deadline must reach into too), and is nil once r
// stops.
func (c *canvas) toNRGBA(r *renderer) *image.NRGBA {
	out := &image.NRGBA{Pix: c.pix, Stride: 4 * c.w, Rect: image.Rect(0, 0, c.w, c.h)}
	for b := 0; b < len(c.pix); b += 4 * imageref.BandPixels {
		if !r.poll(true) {
			return nil
		}
		unpremultiply(c.pix[b:min(b+4*imageref.BandPixels, len(c.pix))])
	}
	return out
}

// unpremultiply converts premultiplied RGBA pixels to non-premultiplied ones,
// in place.
func unpremultiply(px []uint8) {
	src, dst := px, px
	for i := 0; i < len(src); i += 4 {
		a := uint32(src[i+3])
		switch a {
		case 0:
			dst[i], dst[i+1], dst[i+2] = 0, 0, 0 // as a new image is
		case 255:
		default:
			h := a / 2
			r := (uint32(src[i])*255 + h) / a
			g := (uint32(src[i+1])*255 + h) / a
			b := (uint32(src[i+2])*255 + h) / a
			if r > 255 {
				r = 255
			}
			if g > 255 {
				g = 255
			}
			if b > 255 {
				b = 255
			}
			dst[i], dst[i+1], dst[i+2], dst[i+3] = uint8(r), uint8(g), uint8(b), uint8(a)
		}
	}
}

// EncodePNG encodes img as PNG. Fully opaque images are written as RGB
// (smaller); others as RGBA. The bytes are image/png's.
func EncodePNG(img *image.NRGBA) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(img.Pix) / 8)
	if err := pngopt.Encode(context.Background(), &buf, img); err != nil {
		return nil, fmt.Errorf("raster: encoding PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderPNG renders svg and encodes the result as PNG.
func RenderPNG(svg []byte, opts Options) ([]byte, error) {
	img, err := Render(svg, opts)
	if err != nil {
		return nil, err
	}
	return EncodePNG(img)
}
