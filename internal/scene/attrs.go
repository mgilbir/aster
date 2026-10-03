package scene

import "github.com/mgilbir/aster/internal/jsval"

// The property groups of an item (see Item). Each is allocated when one of its
// properties is first set, from a slab its mark keeps, so that the items of a
// mark that all set the same group share a few allocations.

type textAttrs struct {
	Text        jsval.Value
	Font        string
	FontWeight  string
	FontStyle   string
	FontVariant string
	LineBreak   string
	Ellipsis    string
	Dir         string
	FontSize    Num
	Dx, Dy      Num
	Radius      Num
	Theta       Num
	Limit       Num
	LineHeight  Num
}

type strokeAttrs struct {
	// StrokeDash is the dash pattern as the encoder wrote it: upstream keeps
	// any value and its SVG renderer writes String(value) (an array joins with
	// commas), so a number, a string or an empty array is written as given.
	// A nullish value (the zero Value is undefined) means no pattern.
	StrokeDash       jsval.Value
	StrokeCap        string
	StrokeJoin       string
	Blend            string
	StrokeMiterLimit Num
	StrokeDashOffset Num
	StrokeOffset     Num
	StrokeForeground Tri
}

// geomAttrs are the rect and group corners and the arc geometry.
type geomAttrs struct {
	CornerRadius            Num
	CornerRadiusTopLeft     Num
	CornerRadiusTopRight    Num
	CornerRadiusBottomLeft  Num
	CornerRadiusBottomRight Num
	StartAngle, EndAngle    Num
	PadAngle                Num
	InnerRadius             Num
	OuterRadius             Num
}

// pathAttrs are the path string, and the scale path marks draw it at.
type pathAttrs struct {
	Path           Path
	ScaleX, ScaleY Num
}

type lineAttrs struct {
	Interpolate string
	Orient      string
	Tension     Num
}

type imageAttrs struct {
	URL    string
	Aspect Tri
	Smooth Tri
	// imgW, imgH are the natural size of the loaded picture.
	imgW, imgH float64
}

type linkAttrs struct {
	Tooltip jsval.Value
	Cursor  string
	Href    string
}

// attrSlabs are the slabs a mark allocates its items' property groups from.
type attrSlabs struct {
	text   slab[textAttrs]
	stroke slab[strokeAttrs]
	geom   slab[geomAttrs]
	path   slab[pathAttrs]
	line   slab[lineAttrs]
	image  slab[imageAttrs]
	link   slab[linkAttrs]
}

// slab hands out zero values of T from chunks that double from 16 to 1024.
type slab[T any] struct {
	free []T
	n    int
}

func (s *slab[T]) take() *T {
	if len(s.free) == 0 {
		s.n = min(max(2*s.n, 16), 1024)
		s.free = make([]T, s.n)
	}
	p := &s.free[0]
	s.free = s.free[1:]
	return p
}

// alloc returns a new group for an item of mark m: from m's slab, or on its own
// for an item outside any mark.
func alloc[T any](m *Mark, pick func(*attrSlabs) *slab[T]) *T {
	if m == nil {
		return new(T)
	}
	if m.attrs == nil {
		m.attrs = new(attrSlabs)
	}
	return pick(m.attrs).take()
}

func (it *Item) textW() *textAttrs {
	if it.text == nil {
		it.text = alloc(it.Mark, func(s *attrSlabs) *slab[textAttrs] { return &s.text })
	}
	return it.text
}

func (it *Item) strokeW() *strokeAttrs {
	if it.stroke == nil {
		it.stroke = alloc(it.Mark, func(s *attrSlabs) *slab[strokeAttrs] { return &s.stroke })
	}
	return it.stroke
}

func (it *Item) geomW() *geomAttrs {
	if it.geom == nil {
		it.geom = alloc(it.Mark, func(s *attrSlabs) *slab[geomAttrs] { return &s.geom })
	}
	return it.geom
}

func (it *Item) pathW() *pathAttrs {
	if it.path == nil {
		it.path = alloc(it.Mark, func(s *attrSlabs) *slab[pathAttrs] { return &s.path })
	}
	return it.path
}

func (it *Item) lineW() *lineAttrs {
	if it.line == nil {
		it.line = alloc(it.Mark, func(s *attrSlabs) *slab[lineAttrs] { return &s.line })
	}
	return it.line
}

func (it *Item) imageW() *imageAttrs {
	if it.image == nil {
		it.image = alloc(it.Mark, func(s *attrSlabs) *slab[imageAttrs] { return &s.image })
	}
	return it.image
}

func (it *Item) linkW() *linkAttrs {
	if it.link == nil {
		it.link = alloc(it.Mark, func(s *attrSlabs) *slab[linkAttrs] { return &s.link })
	}
	return it.link
}

var (
	noText   textAttrs
	noStroke strokeAttrs
	noGeom   geomAttrs
	noPath   pathAttrs
	noLine   lineAttrs
	noImage  imageAttrs
	noLink   linkAttrs
)

// The read-only views of the groups: the group, or its zero value when absent.
// Nothing may write through them.

func (it *Item) textR() *textAttrs {
	if it.text == nil {
		return &noText
	}
	return it.text
}

func (it *Item) strokeR() *strokeAttrs {
	if it.stroke == nil {
		return &noStroke
	}
	return it.stroke
}

func (it *Item) geomR() *geomAttrs {
	if it.geom == nil {
		return &noGeom
	}
	return it.geom
}

func (it *Item) pathR() *pathAttrs {
	if it.path == nil {
		return &noPath
	}
	return it.path
}

func (it *Item) lineR() *lineAttrs {
	if it.line == nil {
		return &noLine
	}
	return it.line
}

func (it *Item) imageR() *imageAttrs {
	if it.image == nil {
		return &noImage
	}
	return it.image
}

func (it *Item) linkR() *linkAttrs {
	if it.link == nil {
		return &noLink
	}
	return it.link
}

// Text is the text of a text item, as the encoder gave it.
func (it *Item) Text() jsval.Value            { return it.textR().Text }
func (it *Item) Font() string                 { return it.textR().Font }
func (it *Item) FontSize() Num                { return it.textR().FontSize }
func (it *Item) FontWeight() string           { return it.textR().FontWeight }
func (it *Item) FontStyle() string            { return it.textR().FontStyle }
func (it *Item) FontVariant() string          { return it.textR().FontVariant }
func (it *Item) Dx() Num                      { return it.textR().Dx }
func (it *Item) Dy() Num                      { return it.textR().Dy }
func (it *Item) Radius() Num                  { return it.textR().Radius }
func (it *Item) Theta() Num                   { return it.textR().Theta }
func (it *Item) Limit() Num                   { return it.textR().Limit }
func (it *Item) LineBreak() string            { return it.textR().LineBreak }
func (it *Item) LineHeight() Num              { return it.textR().LineHeight }
func (it *Item) Ellipsis() string             { return it.textR().Ellipsis }
func (it *Item) Dir() string                  { return it.textR().Dir }
func (it *Item) StrokeDash() jsval.Value      { return it.strokeR().StrokeDash }
func (it *Item) StrokeCap() string            { return it.strokeR().StrokeCap }
func (it *Item) StrokeJoin() string           { return it.strokeR().StrokeJoin }
func (it *Item) Blend() string                { return it.strokeR().Blend }
func (it *Item) StrokeMiterLimit() Num        { return it.strokeR().StrokeMiterLimit }
func (it *Item) StrokeDashOffset() Num        { return it.strokeR().StrokeDashOffset }
func (it *Item) StrokeOffset() Num            { return it.strokeR().StrokeOffset }
func (it *Item) StrokeForeground() Tri        { return it.strokeR().StrokeForeground }
func (it *Item) CornerRadius() Num            { return it.geomR().CornerRadius }
func (it *Item) CornerRadiusTopLeft() Num     { return it.geomR().CornerRadiusTopLeft }
func (it *Item) CornerRadiusTopRight() Num    { return it.geomR().CornerRadiusTopRight }
func (it *Item) CornerRadiusBottomLeft() Num  { return it.geomR().CornerRadiusBottomLeft }
func (it *Item) CornerRadiusBottomRight() Num { return it.geomR().CornerRadiusBottomRight }
func (it *Item) StartAngle() Num              { return it.geomR().StartAngle }
func (it *Item) EndAngle() Num                { return it.geomR().EndAngle }
func (it *Item) PadAngle() Num                { return it.geomR().PadAngle }
func (it *Item) InnerRadius() Num             { return it.geomR().InnerRadius }
func (it *Item) OuterRadius() Num             { return it.geomR().OuterRadius }

// Path is the path string of a path item (with its parse cache).
func (it *Item) Path() Path           { return it.pathR().Path }
func (it *Item) ScaleX() Num          { return it.pathR().ScaleX }
func (it *Item) ScaleY() Num          { return it.pathR().ScaleY }
func (it *Item) Interpolate() string  { return it.lineR().Interpolate }
func (it *Item) Orient() string       { return it.lineR().Orient }
func (it *Item) Tension() Num         { return it.lineR().Tension }
func (it *Item) URL() string          { return it.imageR().URL }
func (it *Item) Aspect() Tri          { return it.imageR().Aspect }
func (it *Item) Smooth() Tri          { return it.imageR().Smooth }
func (it *Item) Tooltip() jsval.Value { return it.linkR().Tooltip }
func (it *Item) Cursor() string       { return it.linkR().Cursor }
func (it *Item) Href() string         { return it.linkR().Href }

// SetPath sets the item's path string (the `path` property).
func (it *Item) SetPath(p Path) {
	if it.path == nil && !p.Set && p.D == "" {
		return
	}
	it.pathW().Path = p
}
