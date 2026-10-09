package text

import (
	"fmt"

	"github.com/mgilbir/forme/shape"
)

// GlyphColour says which of its representations PaintGlyph paints glyph gid
// of f from when asked with opts (forme reads its PPEM and Bitmaps): a
// renderer draws a glyph that is shape.ColourNone from its outline, as
// GlyphOutline gives it, and any other through PaintGlyph. A glyph outside
// the face, and every glyph of a nil face, is ColourNone. It never panics on a
// malformed font: a glyph whose colour cannot be read is ColourNone.
func GlyphColour(f *Face, gid int, opts shape.PaintOptions) (c shape.GlyphColour) {
	if f == nil {
		return shape.ColourNone
	}
	defer func() {
		if recover() != nil {
			c = shape.ColourNone
		}
	}()
	defer f.guard()()
	opts.Palette = f.palette
	return f.shape.GlyphColourFor(gid, opts)
}

// PaintGlyph paints glyph gid of f through p, as forme's Face.PaintGlyph
// does: its COLR paints, its SVG document, its CBDT or sbix image, its EBDT
// or bdat image as a mask in the foreground, or failing all of them its
// outline in the foreground. Coordinates are in font units with y pointing
// up, the origin on the baseline at the glyph's pen position. Its colours
// are from the face's palette (WithFontPalette), whatever opts.Palette says.
//
// forme refuses a COLR glyph whose painting runs past its bounds before p is
// called (shape.ErrPaintLimit). A panic, from a malformed font or from p, is
// reported as an error, and p may then have been left inside pushes it was
// not given the pops of. It may be called on one Face from several goroutines.
func PaintGlyph(f *Face, gid int, opts shape.PaintOptions, p shape.Painter) (err error) {
	if f == nil {
		return ErrNoOutline
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("text: painting glyph %d of %q: %v", gid, f.Family, r)
		}
	}()
	defer f.guard()()
	opts.Palette = f.palette
	return f.shape.PaintGlyph(gid, opts, p)
}
