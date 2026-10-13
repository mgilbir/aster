package raster

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
	"github.com/mgilbir/aster/internal/text"
)

// maxColourImageSide bounds the side of an image ColourGlyphImage paints.
const maxColourImageSide = 4096

// ColourGlyphImage paints colour glyph gid of f, as the PNG writer paints it,
// into an image at ppem pixels per em, its foreground paints in fg. It
// returns the image and the box it covers, in font units with y up, the
// origin on the baseline at the glyph's pen position: the image's top-left
// corner is the box's (XMin, YMax). It is for a writer that cannot draw what
// a glyph paints, as a PDF cannot draw a sweep gradient, and draws its image
// instead. A glyph that paints nothing returns a nil image. Painting polls
// ctx (nil: never done) as a render does, and returns its error once it is
// done: the glyph's painting is the font's to choose, up to a 4096-pixel
// square drawn many times over.
func ColourGlyphImage(ctx context.Context, f *text.Face, gid int, fg shape.Color, ppem int) (img *image.NRGBA, box shape.Rect, err error) {
	defer func() {
		// A bound reached deep in drawing stops it with budget.Stop.
		if p := recover(); p != nil {
			stop, ok := p.(*budget.Stop)
			if !ok {
				panic(p)
			}
			img, box, err = nil, shape.Rect{}, stop.Err
		}
	}()
	if f == nil || ppem <= 0 {
		return nil, box, errors.New("raster: no face or size to paint a glyph at")
	}
	tf := &textFace{f: f}
	b := &colourBounds{face: tf}
	if err := tf.Paint(uint32(gid), shape.PaintOptions{Foreground: fg, PPEM: ppem}, b); err != nil {
		return nil, box, err
	}
	svg := b.svg
	if svg {
		// An SVG glyph's document says nothing of where it draws until it is
		// drawn: it is drawn over two ems about its em square, and cropped.
		u := tf.UnitsPerEm()
		b.r, b.ok = rect{-u / 2, -u / 2, 3 * u / 2, 3 * u / 2}, true
	}
	if !b.ok {
		return nil, box, nil
	}
	k := float64(ppem) / tf.UnitsPerEm()
	// Whole pixels, so that the image's pixels fall on the em's grid.
	x0, x1 := math.Floor(b.r.x0*k), math.Ceil(b.r.x1*k)
	y0, y1 := math.Floor(b.r.y0*k), math.Ceil(b.r.y1*k)
	// The size is checked in float64 before it becomes an int: a paint
	// reaching to an infinity must not convert (implementation-defined).
	fw, fh := x1-x0, y1-y0
	if !(fw >= 1) || !(fh >= 1) {
		return nil, box, nil
	}
	if fw > maxColourImageSide || fh > maxColourImageSide {
		return nil, box, fmt.Errorf("raster: a glyph at %d pixels per em is %.0fx%.0f pixels: %w", ppem, fw, fh, errLimit)
	}
	w, h := int(fw), int(fh)
	lim := Limits{}.withDefaults()
	// The image is drawn at its own pixels, scale 1: the PNG render's budget
	// for an output of its size.
	lim.MaxPixelOps = Limits{}.PixelOps(1, w, h)
	r := &renderer{lim: lim, rast: newRasterizer(), cw: w, ch: h, active: map[*node]bool{}, ctx: ctx}
	defer r.reportWork(ctx, 1)
	r.rast.ctx = ctx
	r.fl.r = r
	r.host = identity
	r.initCaches()
	if r.cv = r.allocCanvas(w, h); r.cv == nil {
		return nil, box, r.err
	}
	st := initialState(0, 0)
	// Font units, y up, to the image's pixels, y down from its top.
	m := matrix{k, 0, 0, -k, -x0, y1}
	if !r.paintColour(tf, uint32(gid), m, &st, fg, ppem, 1) && r.err == nil {
		return nil, box, fmt.Errorf("raster: glyph %d of %q could not be painted", gid, f.Family)
	}
	if r.err != nil {
		return nil, box, r.err
	}
	if img = r.cv.toNRGBA(r); img == nil {
		return nil, box, r.err
	}
	if svg {
		// Cropped to what it painted.
		c := opaqueBounds(img)
		if c.Empty() {
			return nil, box, nil
		}
		// A copy of its own: a sub-image's rows are its parent's.
		crop := image.NewNRGBA(image.Rect(0, 0, c.Dx(), c.Dy()))
		if err := imageref.DrawSrc(ctx, crop, crop.Bounds(), img, c.Min); err != nil {
			return nil, box, err
		}
		img = crop
		x0, x1 = x0+float64(c.Min.X), x0+float64(c.Max.X)
		y0, y1 = y1-float64(c.Max.Y), y1-float64(c.Min.Y)
	}
	return img, shape.Rect{XMin: x0 / k, YMin: y0 / k, XMax: x1 / k, YMax: y1 / k}, nil
}

// opaqueBounds is the box of the pixels of img that are not transparent.
func opaqueBounds(img *image.NRGBA) image.Rectangle {
	b := img.Bounds()
	out := image.Rectangle{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[(x-b.Min.X)*4+3] != 0 {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

// ColourGlyphBounds is the box a colour glyph of f paints within, in font
// units with y up, the origin on the baseline at its pen position: where its
// paints reach, each bounded by the clips around it. ok is false for a glyph
// that paints nothing, or cannot be painted.
func ColourGlyphBounds(f *text.Face, gid int, opts shape.PaintOptions) (box shape.Rect, ok bool) {
	if f == nil {
		return box, false
	}
	tf := &textFace{f: f}
	b := &colourBounds{face: tf}
	if err := tf.Paint(uint32(gid), opts, b); err != nil || !b.ok {
		return box, false
	}
	return shape.Rect{XMin: b.r.x0, YMin: b.r.y0, XMax: b.r.x1, YMax: b.r.y1}, true
}

// colourBounds is a painter that paints nothing and measures where a glyph's
// painting reaches: each paint is bounded by the clips around it, the
// glyphs' outlines and the boxes, as HarfBuzz's paint extents are.
type colourBounds struct {
	face  Face
	m     []matrix // font units to the glyph's, innermost last
	clips []rect   // in the glyph's font units, innermost last
	r     rect
	ok    bool
	svg   bool // it painted an SVG document, which says nothing of its bounds
}

func (b *colourBounds) top() matrix {
	if len(b.m) == 0 {
		return identity
	}
	return b.m[len(b.m)-1]
}

func (b *colourBounds) PushTransform(t shape.Transform) {
	b.m = append(b.m, b.top().mul(matrix{t.XX, t.YX, t.XY, t.YY, t.X0, t.Y0}))
}

func (b *colourBounds) PopTransform() {
	if len(b.m) > 0 {
		b.m = b.m[:len(b.m)-1]
	}
}

// pushBox clips to rc, given in the current transform's units.
func (b *colourBounds) pushBox(rc rect, ok bool) {
	if ok {
		rc = transformRect(rc, b.top())
		if n := len(b.clips); n > 0 {
			rc = intersectRect(rc, b.clips[n-1])
		}
	} else {
		rc = rect{}
	}
	b.clips = append(b.clips, rc)
}

func (b *colourBounds) PushClipGlyph(gid int) {
	var rc rect
	var r renderer
	o := r.glyphOutline(b.face, uint32(gid))
	ok := o != nil && len(o.pts) > 0
	if ok {
		rc = rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for _, pt := range o.pts {
			// The outline is y down.
			rc.x0, rc.x1 = math.Min(rc.x0, pt.x), math.Max(rc.x1, pt.x)
			rc.y0, rc.y1 = math.Min(rc.y0, -pt.y), math.Max(rc.y1, -pt.y)
		}
	}
	b.pushBox(rc, ok)
}

func (b *colourBounds) PushClipRect(rc shape.Rect) {
	b.pushBox(rect{rc.XMin, rc.YMin, rc.XMax, rc.YMax}, true)
}

func (b *colourBounds) PopClip() {
	if len(b.clips) > 0 {
		b.clips = b.clips[:len(b.clips)-1]
	}
}

func (b *colourBounds) PushGroup()                          {}
func (b *colourBounds) PopGroup(shape.CompositeMode)        {}
func (b *colourBounds) Solid(shape.Color, bool)             { b.paint() }
func (b *colourBounds) LinearGradient(shape.LinearGradient) { b.paint() }
func (b *colourBounds) RadialGradient(shape.RadialGradient) { b.paint() }
func (b *colourBounds) SweepGradient(shape.SweepGradient)   { b.paint() }

func (b *colourBounds) Image(img shape.Image) {
	if img.Format == shape.ImageSVG {
		b.svg = true
		return
	}
	b.pushBox(rect{img.Box.XMin, img.Box.YMin, img.Box.XMax, img.Box.YMax}, true)
	b.paint()
	b.PopClip()
}

// paint adds the innermost clip to the bounds. A paint with no clip around
// it would cover the plane; such a glyph is bounded by its clips elsewhere.
func (b *colourBounds) paint() {
	n := len(b.clips)
	if n == 0 {
		return
	}
	rc := b.clips[n-1]
	if !(rc.x1 > rc.x0) || !(rc.y1 > rc.y0) {
		return
	}
	if !b.ok {
		b.r, b.ok = rc, true
		return
	}
	b.r = rect{math.Min(b.r.x0, rc.x0), math.Min(b.r.y0, rc.y0), math.Max(b.r.x1, rc.x1), math.Max(b.r.y1, rc.y1)}
}

func intersectRect(a, b rect) rect {
	return rect{math.Max(a.x0, b.x0), math.Max(a.y0, b.y0), math.Min(a.x1, b.x1), math.Min(a.y1, b.y1)}
}
