package text

import (
	"errors"
	"fmt"
	"sync"

	"github.com/mgilbir/forme/shape"
)

// SegmentKind is the operation of an outline Segment.
type SegmentKind uint8

const (
	// MoveTo starts a new contour at P[0].
	MoveTo SegmentKind = iota
	// LineTo draws a line from the current point to P[0].
	LineTo
	// QuadTo draws a quadratic Bezier to P[1] with control point P[0].
	QuadTo
	// CubicTo draws a cubic Bezier to P[2] with control points P[0], P[1].
	CubicTo
	// Close ends the current contour with a straight line back to its
	// starting point. Every contour of an outline ends with Close.
	Close
)

// Point is a position in user-space units.
type Point struct{ X, Y float64 }

// Segment is one path element of a glyph outline. Which entries of P are
// meaningful depends on Kind (see the SegmentKind constants); the rest are
// zero.
type Segment struct {
	Kind SegmentKind
	P    [3]Point
}

// Outline errors.
var (
	// ErrNoOutline reports a glyph with no monochrome vector outline: a
	// colour or bitmap glyph, or a face whose outline format cannot be read
	// (CFF2). Blank glyphs such as the space are not errors; they have an
	// empty outline.
	ErrNoOutline = errors.New("text: glyph has no vector outline")
	// ErrGlyphRange reports a glyph index outside the face.
	ErrGlyphRange = errors.New("text: glyph index out of range")
)

// maxOutlineCache bounds the per-face cache of unscaled outlines.
const maxOutlineCache = 4096

// outlineCache holds a face's outlines in font units, y down, so that a
// repeated glyph costs only the scaling (forme keeps its own copy in font
// units, y up, but handing it out means a callback per segment).
type outlineCache struct {
	mu sync.RWMutex
	m  map[int][]Segment
}

// GlyphOutline returns the outline of glyph gid of face f scaled to the given
// font size (the em is size user-space units), as path segments with the y
// axis pointing down (screen orientation), the origin on the baseline at the
// glyph's pen position. Add the glyph's pen and offset to place it.
//
// TrueType (glyf) and CFF outlines are supported, including composite glyphs.
// TrueType quadratics are returned as QuadTo, CFF cubics as CubicTo, and every
// contour is explicitly closed. Font hinting is never applied.
//
// A variable font is read at its default instance. The result is a fresh slice
// owned by the caller; the unscaled outline is cached per face, so repeated
// calls for the same glyph only cost the scaling. It returns ErrGlyphRange or
// ErrNoOutline where there is nothing to draw, and never panics on a malformed
// font.
func GlyphOutline(f *Face, gid int, size float64) (segs []Segment, err error) {
	if f == nil {
		return nil, ErrNoOutline
	}
	units, err := f.outlineUnits(gid)
	if err != nil {
		return nil, err
	}
	k := size / float64(f.upem)
	segs = make([]Segment, len(units))
	for i, s := range units {
		out := Segment{Kind: s.Kind}
		for j := range s.P {
			out.P[j] = Point{s.P[j].X * k, s.P[j].Y * k}
		}
		segs[i] = out
	}
	return segs, nil
}

// outlineUnits returns the outline of gid in font units, y down. The slice is
// shared and must not be modified.
func (f *Face) outlineUnits(gid int) (segs []Segment, err error) {
	c := &f.outlines
	c.mu.RLock()
	segs, ok := c.m[gid]
	c.mu.RUnlock()
	if ok {
		return segs, nil
	}
	defer func() {
		if r := recover(); r != nil {
			segs, err = nil, fmt.Errorf("%w: %v", ErrNoOutline, r)
		}
	}()
	if gid < 0 || gid >= f.shape.NumGlyphs() {
		return nil, ErrGlyphRange
	}
	// forme's outline is in font units with y up; flip it.
	conv := func(p shape.Point) Point { return Point{p.X, 0 - p.Y} }
	open := false
	err = f.shape.GlyphOutline(gid, func(s shape.Segment) bool {
		switch s.Op {
		case shape.MoveTo:
			if open {
				segs = append(segs, Segment{Kind: Close})
			}
			segs = append(segs, Segment{Kind: MoveTo, P: [3]Point{conv(s.Pts[0])}})
			open = true
		case shape.LineTo:
			segs = append(segs, Segment{Kind: LineTo, P: [3]Point{conv(s.Pts[0])}})
		case shape.QuadTo:
			segs = append(segs, Segment{Kind: QuadTo, P: [3]Point{conv(s.Pts[0]), conv(s.Pts[1])}})
		case shape.CubicTo:
			segs = append(segs, Segment{Kind: CubicTo, P: [3]Point{conv(s.Pts[0]), conv(s.Pts[1]), conv(s.Pts[2])}})
		}
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoOutline, err)
	}
	if open {
		segs = append(segs, Segment{Kind: Close})
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[int][]Segment)
	}
	if len(c.m) >= maxOutlineCache {
		clear(c.m)
	}
	c.m[gid] = segs
	c.mu.Unlock()
	return segs, nil
}
