package scene

import (
	"context"
	"errors"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"
)

// MaxDepth bounds group nesting in every recursive walk (bounds, rendering).
// Real charts nest a handful of levels; a specification that nests deeper is
// treated as malicious and fails instead of exhausting the stack.
const MaxDepth = 256

var errNoMark = errors.New("scene: item has no mark")

// ErrTooDeep reports a scenegraph nested deeper than MaxDepth.
var ErrTooDeep = errors.New("scene: group nesting too deep")

// Bounder computes bounding boxes. It owns scratch state and is therefore not
// safe for concurrent use; make one per goroutine.
type Bounder struct {
	Metrics
	ctx  boundContext
	clip Bounds
	// Context, when set, is polled while items are bounded (text bounds
	// shape and measure every string).
	Context context.Context
	n       int
}

// NewBounder returns a Bounder using measurer for text widths (nil selects
// vega's width estimate).
func NewBounder(measurer TextMeasurer) *Bounder {
	return &Bounder{Metrics: Metrics{Measurer: measurer}}
}

// BoundStroke expands b by the item's stroke: half the stroke width (times √2
// for square caps), and for miter joins up to half the miter limit, since a
// miter tip can extend that far past a vertex. Nothing is added for items with
// no stroke or zero opacity.
func BoundStroke(b *Bounds, it *Item, miter bool) *Bounds {
	if it.Stroke.Truthy() && it.Opacity.Val() != 0 && it.StrokeOpacity.Val() != 0 {
		sw := it.StrokeWidth.Or(1)
		k := 1.0
		if it.StrokeCap == "square" {
			k = math.Sqrt2
		}
		e := k * sw / 2
		if miter && (it.StrokeJoin == "" || it.StrokeJoin == "miter") {
			e = jsMax(e, it.StrokeMiterLimit.Or(4)*sw/2)
		}
		b.Expand(e)
	}
	return b
}

// jsMax is Math.max for two values (NaN wins).
func jsMax(a, b float64) float64 { return math.Max(a, b) }

// BoundItem recomputes it.Bounds from scratch. For nested marks (line, area,
// trail) the shared mark bounds are what matter; use BoundMark.
func (bd *Bounder) BoundItem(it *Item) error {
	if it.Mark == nil {
		return errNoMark
	}
	it.Bounds.Clear()
	return bd.itemBounds(it.Mark.Type, &it.Bounds, it)
}

// BoundMark recomputes the bounds of the mark and of all its items (the work of
// vega's Bound transform for one mark), then clips them to the group when the
// mark is clipped. Child marks of group items must already be bounded; use
// BoundTree to do a whole subtree bottom-up.
func (bd *Bounder) BoundMark(m *Mark) error {
	if m.Type.Nested() {
		if len(m.Items) == 0 {
			m.Bounds.Clear()
		} else {
			b := NewBounds()
			if err := bd.multiBounds(m, &b); err != nil {
				return err
			}
			m.Bounds = b
			for _, it := range m.Items {
				it.Bounds = b
			}
		}
	} else {
		m.Bounds.Clear()
		for _, it := range m.Items {
			if bd.Context != nil {
				if bd.n++; bd.n&127 == 0 {
					if err := bd.Context.Err(); err != nil {
						return err
					}
				}
			}
			it.Bounds.Clear()
			if err := bd.itemBounds(m.Type, &it.Bounds, it); err != nil {
				return err
			}
			m.Bounds.Union(&it.Bounds)
		}
	}
	bd.boundClip(m)
	return nil
}

// BoundTree bounds every mark below (and including) m, children first, since a
// group's bounds are the union of its child marks' bounds.
func (bd *Bounder) BoundTree(m *Mark) error { return bd.boundTree(m, 0) }

func (bd *Bounder) boundTree(m *Mark, depth int) error {
	if depth > MaxDepth {
		return ErrTooDeep
	}
	if m.Type == MarkGroup {
		for _, g := range m.Items {
			for _, child := range g.Items {
				if err := bd.boundTree(child, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return bd.BoundMark(m)
}

// boundClip keeps mark bounds within the clipping region (boundClip.js).
func (bd *Bounder) boundClip(m *Mark) {
	switch {
	case m.ClipPath != nil:
		bd.clip.Clear()
		bd.ctx.reset(&bd.clip, false, 0)
		_ = m.ClipPath(&bd.ctx)
	case m.Clip:
		var w, h float64
		if m.Group != nil {
			w, h = m.Group.Width.Val(), m.Group.Height.Val()
		} else {
			w, h = math.NaN(), math.NaN() // group undefined upstream would throw
		}
		bd.clip.Set(0, 0, w, h)
	default:
		return
	}
	m.Bounds.Intersect(&bd.clip)
}

// multiBounds bounds a nested mark from all its items.
func (bd *Bounder) multiBounds(m *Mark, b *Bounds) error {
	bd.ctx.reset(b, false, 0)
	var err error
	switch m.Type {
	case MarkLine:
		err = Line(&bd.ctx, m.Items)
	case MarkArea:
		err = Area(&bd.ctx, m.Items)
	case MarkTrail:
		err = Trail(&bd.ctx, m.Items)
	}
	if err != nil {
		return err
	}
	BoundStroke(b, m.Items[0], true)
	return nil
}

// itemBounds computes b for one item of a non-nested mark.
func (bd *Bounder) itemBounds(t MarkType, b *Bounds, it *Item) error {
	switch t {
	case MarkArc, MarkSymbol, MarkShape:
		bd.ctx.reset(b, it.AngleTruthy(), it.Angle.Val())
		var err error
		switch t {
		case MarkArc:
			err = Arc(&bd.ctx, it)
		case MarkSymbol:
			err = Symbol(&bd.ctx, it)
		default:
			fn := it.Shape.Func
			if it.Mark != nil && it.Mark.Shape != nil {
				fn = it.Mark.Shape
			}
			if fn != nil {
				_ = fn(&bd.ctx, it)
			}
		}
		if err != nil {
			return err
		}
		BoundStroke(b, it, true).Translate(it.X.Zero(), it.Y.Zero())
	case MarkRect:
		x, y := it.X.Zero(), it.Y.Zero()
		// (x + width) || 0: NaN sums collapse to 0.
		x2 := nanZero(x + it.Width.Val())
		y2 := nanZero(y + it.Height.Val())
		b.Set(x, y, x2, y2)
		BoundStroke(b, it, false)
	case MarkRule:
		x1, y1 := it.X.Zero(), it.Y.Zero()
		x2, y2 := x1, y1
		if it.X2.Set() {
			x2 = it.X2.Val()
		}
		if it.Y2.Set() {
			y2 = it.Y2.Val()
		}
		b.Set(x1, y1, x2, y2)
		BoundStroke(b, it, false)
	case MarkImage:
		bd.imageBounds(b, it)
	case MarkText:
		bd.textBounds(b, it, 0)
	case MarkPath:
		return bd.pathBounds(b, it)
	case MarkGroup:
		return bd.groupBounds(b, it)
	}
	return nil
}

func nanZero(v float64) float64 {
	if v != v {
		return 0
	}
	return v
}

func (bd *Bounder) pathBounds(b *Bounds, it *Item) error {
	if !it.Path.Set {
		b.Set(0, 0, 0, 0)
		return nil
	}
	cmds, err := it.parsedPath()
	if err != nil {
		return err
	}
	// The rotation is applied by the context about the origin, after the item
	// offset, exactly as upstream does for path bounds.
	bd.ctx.reset(b, it.AngleTruthy(), it.Angle.Val())
	sx, sy := scaleOf(it.ScaleX), scaleOf(it.ScaleY)
	if err := RenderPath(&bd.ctx, cmds, it.X.Zero(), it.Y.Zero(), sx, sy); err != nil {
		return err
	}
	BoundStroke(b, it, true)
	return nil
}

func (bd *Bounder) groupBounds(b *Bounds, g *Item) error {
	if !g.Clip.IsTrue() && g.ClipPath == nil {
		for _, m := range g.Items {
			b.Union(&m.Bounds)
		}
	}
	if (g.Clip.IsTrue() || g.ClipPath != nil || g.Width.Truthy() || g.Height.Truthy()) && !g.NoBound {
		b.Add(0, 0).Add(g.Width.Zero(), g.Height.Zero())
	}
	BoundStroke(b, g, false)
	b.Translate(g.X.Zero(), g.Y.Zero())
	return nil
}

// ImageSize returns the natural size of an image item's picture, or 0, 0 when
// unknown; the SVG renderer and bounds share it through Item.Image.
func imageWidth(it *Item) float64 {
	switch {
	case it.Width.Set():
		return it.Width.Val()
	case it.imgW == 0 || it.imgW != it.imgW:
		return 0
	case !it.Aspect.IsFalse() && it.Height.Truthy():
		return it.Height.Val() * it.imgW / it.imgH
	}
	return it.imgW
}

func imageHeight(it *Item) float64 {
	switch {
	case it.Height.Set():
		return it.Height.Val()
	case it.imgH == 0 || it.imgH != it.imgH:
		return 0
	case !it.Aspect.IsFalse() && it.Width.Truthy():
		return it.Width.Val() * it.imgH / it.imgW
	}
	return it.imgH
}

func imageXOffset(align string, w float64) float64 {
	switch align {
	case "center":
		return w / 2
	case "right":
		return w
	}
	return 0
}

func imageYOffset(baseline string, h float64) float64 {
	switch baseline {
	case "middle":
		return h / 2
	case "bottom":
		return h
	}
	return 0
}

// ImageGeometry returns the drawn position and size of an image item, given the
// picture's natural size (0, 0 when unknown).
func ImageGeometry(it *Item, naturalW, naturalH float64) (x, y, w, h float64) {
	saveW, saveH := it.imgW, it.imgH
	it.imgW, it.imgH = naturalW, naturalH
	w, h = imageWidth(it), imageHeight(it)
	x = it.X.Zero() - imageXOffset(it.Align, w)
	y = it.Y.Zero() - imageYOffset(it.Baseline, h)
	it.imgW, it.imgH = saveW, saveH
	return
}

func (bd *Bounder) imageBounds(b *Bounds, it *Item) {
	w, h := imageWidth(it), imageHeight(it)
	x := it.X.Zero() - imageXOffset(it.Align, w)
	y := it.Y.Zero() - imageYOffset(it.Baseline, h)
	b.Set(x, y, x+w, y+h)
}

// SetImageSize records the loaded picture's natural size, used for image
// bounds when width or height is not given.
func (it *Item) SetImageSize(w, h float64) { it.imgW, it.imgH = w, h }

func scaleOf(n Num) float64 {
	v := n.Zero()
	if v == 0 {
		return 1
	}
	return v
}

// AnchorPoint is the text anchor: (x, y), displaced by radius/theta when a
// radius is given (polar placement from the origin, theta measured clockwise
// from 12 o'clock).
func AnchorPoint(it *Item) (x, y float64) {
	x, y = it.X.Zero(), it.Y.Zero()
	if r := it.Radius.Zero(); r != 0 {
		t := it.Theta.Zero() - halfPi
		x += float64(r * jsmath.Cos(t))
		y += float64(r * jsmath.Sin(t))
	}
	return
}

// textBounds sets b to the text's box. mode 0 rotates the box about the anchor
// when the item has an angle; mode 1 leaves it unrotated (for hit tests).
func (bd *Bounder) textBounds(b *Bounds, it *Item, mode int) {
	h := bd.Height(it)
	x, y := AnchorPoint(it)
	dx := it.Dx.Zero()
	dy := it.Dy.Zero() + BaselineOffset(it) - jsRound(float64(0.8*h)) // use 4/5 offset
	line, lines := TextLine(it)

	var w float64
	if lines != nil {
		h += float64(LineHeight(it) * float64(len(lines)-1))
		for _, l := range lines {
			w = math.Max(w, bd.Width(it, l))
		}
	} else {
		w = bd.Width(it, line)
	}

	switch it.Align {
	case "center":
		dx -= w / 2
	case "right":
		dx -= w
	}

	dx += x
	dy += y
	b.Set(dx, dy, dx+w, dy+h)
	if it.AngleTruthy() && mode == 0 {
		b.Rotate(it.Angle.Val()*degToRad, x, y)
	}
}
