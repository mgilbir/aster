// Package scene is the scenegraph layer of the engine: the Go counterpart of
// vega-scenegraph. It holds the Scenegraph/Mark/Item data model that the Vega
// runtime produces, the path generators for every mark type (arc, area, line,
// rect, symbol, trail, SVG path strings, d3-shape curves) and the bounds
// calculation that layout depends on. The svg package turns a scenegraph into
// SVG text.
//
// Items are typed structs rather than property bags. Properties whose absence
// matters upstream use Num, Tri and Paint, whose zero values mean "unset".
package scene

import "github.com/mgilbir/aster/internal/jsval"

// MarkType identifies the mark kind. Unknown types are rejected at load time;
// upstream fails with a TypeError on them.
type MarkType uint8

const (
	MarkInvalid MarkType = iota
	MarkArc
	MarkArea
	MarkGroup
	MarkImage
	MarkLine
	MarkPath
	MarkRect
	MarkRule
	MarkShape
	MarkSymbol
	MarkText
	MarkTrail
)

var markTypeNames = [...]string{"", "arc", "area", "group", "image", "line", "path", "rect", "rule", "shape", "symbol", "text", "trail"}

// String returns the Vega mark type name ("rect", "group", ...).
func (t MarkType) String() string {
	if int(t) < len(markTypeNames) {
		return markTypeNames[t]
	}
	return ""
}

// ParseMarkType maps a Vega mark type name to its MarkType.
func ParseMarkType(s string) (MarkType, bool) {
	for i := 1; i < len(markTypeNames); i++ {
		if markTypeNames[i] == s {
			return MarkType(i), true
		}
	}
	return MarkInvalid, false
}

// Nested reports whether the mark type draws all its items as one element
// (area, line, trail): the SVG has a single path built from every item, and the
// bounds are shared by all items.
func (t MarkType) Nested() bool { return t == MarkArea || t == MarkLine || t == MarkTrail }

// PathFunc stands for a d3 path generator (a clip path): called with a context it
// draws into it; called with a nil context it returns the SVG path data string
// (`path.context(null)()` upstream, which formats the string itself). The return
// value is ignored when a context is given.
type PathFunc func(ctx PathContext) string

// ShapeFunc is the same for the geometry of one item of a shape mark (a geoshape's
// path generator): draw into ctx, or return the path data when ctx is nil. An
// empty string means the shape has no geometry and no `d` attribute is written.
type ShapeFunc func(ctx PathContext, item *Item) string

// Scenegraph is the root of a scene. Root is a group mark (role "frame", name
// "root") holding a single group item; the mark hierarchy hangs below it.
type Scenegraph struct {
	Root *Mark
}

// New creates the empty scenegraph vega's `new Scenegraph()` builds.
func New() *Scenegraph {
	root := &Mark{Type: MarkGroup, Name: "root", Role: "frame"}
	root.Items = []*Item{{Mark: root}}
	return &Scenegraph{Root: root}
}

// RootItem is the group item that holds the top-level marks.
func (s *Scenegraph) RootItem() *Item { return s.Root.Items[0] }

// MarkDef describes a mark to add to a group.
type MarkDef struct {
	Type           MarkType
	Name           string
	Role           string
	Clip           bool
	ClipPath       PathFunc
	NonInteractive bool
	Zindex         float64
	Aria           Tri
	Description    string
}

// AddMark appends a new mark to group's child marks at index (or at the end if
// index is out of range or negative) and returns it.
func (s *Scenegraph) AddMark(def MarkDef, group *Item, index int) *Mark {
	if group == nil {
		group = s.RootItem()
	}
	m := &Mark{
		Type:           def.Type,
		Name:           def.Name,
		Role:           def.Role,
		Clip:           def.Clip,
		ClipPath:       def.ClipPath,
		NonInteractive: def.NonInteractive,
		Zindex:         def.Zindex,
		Aria:           def.Aria,
		Description:    def.Description,
		Group:          group,
		Bounds:         NewBounds(),
	}
	if index < 0 || index >= len(group.Items) {
		group.Items = append(group.Items, m)
	} else {
		// Vega assigns group.items[index], replacing any previous mark.
		group.Items[index] = m
	}
	return m
}

// Mark is a collection of items sharing a mark type (vega-scenegraph's
// scenegraph "mark" object).
type Mark struct {
	Type MarkType
	Role string
	Name string
	// Description is the mark-level aria-label; Aria is the mark-level
	// aria toggle (only an explicit false matters).
	Description string
	Aria        Tri
	// NonInteractive is `interactive === false`: the group gets
	// pointer-events="none". (Positive default so that the zero Mark is
	// interactive, as Vega marks are.)
	NonInteractive bool
	// Clip clips the mark to its group's box; ClipPath, when set, is a path
	// generator that overrides the box (Vega's `clip: {path: ...}`).
	Clip     bool
	ClipPath PathFunc
	Zindex   float64
	Bounds   Bounds

	// Items are the visual items. Nested types (area, line, trail) draw all of
	// them as a single path.
	Items []*Item
	// Group is the group item that contains this mark; nil for the root.
	Group *Item
	// Shape is the mark-wide geo shape generator (`mark.shape`), consulted
	// before Item.Shape by shape marks.
	Shape ShapeFunc

	// GuideCaption is the aria-label text for axis and legend marks that have
	// no explicit description; the runtime computes it from the guide's scale
	// (upstream builds it from `item.context`). Ignored for other roles.
	GuideCaption string
	// NoGuideCaption reports that upstream cannot caption this axis or legend
	// mark: its group item has no child marks, so nothing ever bound the
	// item's `context`, and vega-scenegraph's axisCaption/legendCaption throw.
	// Upstream then emits no aria attributes for the mark at all unless it
	// has an explicit description.
	NoGuideCaption bool
}

// AddItem appends a new item (with empty bounds) to the mark and returns it.
func (m *Mark) AddItem() *Item {
	it := &Item{Mark: m, Bounds: NewBounds()}
	m.Items = append(m.Items, it)
	return it
}

// Item is one visual element of a mark. Group items also carry child marks.
//
// Field names follow Vega's item properties. Unset means "the encoding did not
// produce this property".
type Item struct {
	// Mark is the parent mark. Datum is the source tuple (not rendered).
	Mark  *Mark
	Datum jsval.Value
	// Bounds is the item's bounding box (filled by Bounder.BoundMark).
	Bounds Bounds
	// Seq is the item's tuple id: the order the items of a view were created
	// in, which breaks the ties of a mark sort.
	Seq uint64

	// Layout.
	X, Y, X2, Y2  Num
	Width, Height Num
	Align         string
	Baseline      string

	// Paint.
	Fill, Stroke     Paint
	Opacity          Num
	FillOpacity      Num
	StrokeOpacity    Num
	StrokeWidth      Num
	StrokeCap        string
	StrokeJoin       string
	StrokeMiterLimit Num
	// StrokeDash is the dash pattern as the encoder wrote it: upstream keeps
	// any value and its SVG renderer writes String(value) (an array joins with
	// commas), so a number, a string or an empty array is written as given.
	// A nullish value (the zero Value is undefined) means no pattern.
	StrokeDash       jsval.Value
	StrokeDashOffset Num
	StrokeForeground Tri
	StrokeOffset     Num
	Blend            string

	// Rect and group corners.
	CornerRadius            Num
	CornerRadiusTopLeft     Num
	CornerRadiusTopRight    Num
	CornerRadiusBottomLeft  Num
	CornerRadiusBottomRight Num

	// Arc.
	StartAngle, EndAngle Num
	PadAngle             Num
	InnerRadius          Num
	OuterRadius          Num

	// Symbol, shape, path.
	Shape Shape
	Size  Num
	Path  Path

	// Line and area.
	Interpolate string
	Tension     Num
	Orient      string
	Defined     Tri

	// Text.
	Text        jsval.Value
	Font        string
	FontSize    Num
	FontWeight  string
	FontStyle   string
	FontVariant string
	Dx, Dy      Num
	Angle       Num
	Radius      Num
	Theta       Num
	Limit       Num
	LineBreak   string
	LineHeight  Num
	Ellipsis    string
	Dir         string

	// Image.
	URL    string
	Aspect Tri
	Smooth Tri

	// Interaction and accessibility.
	Cursor              string
	Href                string
	Tooltip             jsval.Value
	Description         string
	Aria                Tri
	AriaRole            string
	AriaRoleDescription string
	Zindex              Num

	// Group items: child marks, and group clipping. ClipPath overrides the
	// group box when set.
	Items    []*Mark
	Clip     Tri
	ClipPath PathFunc
	// ScaleX and ScaleY scale path marks.
	ScaleX, ScaleY Num
	// NoBound excludes the group's own box from its bounds (`noBound`).
	NoBound bool

	// Extra holds any property this package does not model.
	Extra *jsval.Object
	// Raw keeps the value given to a numeric property when it was not a
	// number (a string or boolean from an encoder), keyed by property name.
	// Upstream stores such values untouched: arithmetic coerces them (+"3"
	// is 3, +true is 1, +"wide" is NaN), a truthiness guard reads the raw
	// value (`if (item.angle)` passes for "sideways"), and the SVG renderer
	// writes String(value). The numeric field holds the coercion. Nil for
	// nearly every item.
	Raw map[string]jsval.Value

	// imgW, imgH are the natural size of the loaded picture of an image item.
	imgW, imgH float64
}

// Shape is the geometry source of symbol and shape items: a symbol type name /
// custom SVG path (symbol marks) or a geo shape generator (shape marks).
type Shape struct {
	// Name is a symbol type ("circle", "cross", ...) or an SVG path string.
	Name string
	// Func draws the item for shape marks (a geoshape's path generator).
	Func ShapeFunc
}

// Path is an SVG path string plus its lazily parsed form.
type Path struct {
	// D is the path data; Set distinguishes an absent path from "".
	D   string
	Set bool

	parsed   []PathCmd
	parsedD  string
	parsedOK bool
}

// P makes a set Path.
func P(d string) Path { return Path{D: d, Set: true} }

// parsedPath returns the parsed commands of the item's path, caching them
// until the path string changes.
func (it *Item) parsedPath() ([]PathCmd, error) {
	p := &it.Path
	if p.parsedOK && p.parsedD == p.D {
		return p.parsed, nil
	}
	cmds, err := ParsePath(p.D)
	if err != nil {
		return nil, err
	}
	p.parsed, p.parsedD, p.parsedOK = cmds, p.D, true
	return cmds, nil
}
