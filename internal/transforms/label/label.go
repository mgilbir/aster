// Package label implements vega-label: label placement that annotates marks.
//
// Labels are placed greedily, in priority order, on a coarse occupancy bitmap
// that already contains the marks to avoid and every label placed so far.
// The package is independent of the scenegraph: callers describe each label
// and its base mark with plain structs (Label, Base), measure text through
// Options.TextWidth and, when marks must be avoided, supply a Rasterizer that
// paints them.
package label

import (
	"context"
	"errors"
	"fmt"
	"github.com/mgilbir/aster/internal/jssort"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// Box is an axis-aligned bounding box.
type Box struct{ X1, Y1, X2, Y2 float64 }

// Point is one sample of a line or area mark. X2/Y2 are only meaningful when
// the matching Has flag is set (area marks with a second baseline).
type Point struct {
	X, Y, X2, Y2 float64
	HasX2, HasY2 bool
}

// SubMark is a mark nested in a group base item.
type SubMark struct {
	MarkType string // "line", "area", ...
	Points   []Point
}

// Base is the item a label annotates (upstream: the text item's datum).
type Base struct {
	// MarkType is the base mark's type; empty when labels annotate bare data
	// points rather than a mark.
	MarkType string
	X, Y     float64 // position, used for line and area marks
	Bounds   Box
	// Marks are the marks nested in a group base item, indexed by
	// Options.MarkIndex.
	Marks []SubMark
	// Ref is an opaque handle handed back to the Rasterizer.
	Ref any
}

// Label is a text item to place.
type Label struct {
	Text     string
	FontSize float64
	X, Y     float64 // the text item's own position (used without a base mark)
	Base     *Base   // nil when there is no base mark
	// Tuple is the data tuple Apply writes results to; unused by Layout.
	Tuple jsval.Value
	// Ref is an opaque handle for the caller, handed back in Placement.Label
	// and to Options.Sort and Options.TextWidth.
	Ref any
}

// Mask is a width x height coverage grid painted by a Rasterizer. A set
// pixel means non-zero alpha.
type Mask struct {
	Width, Height int
	Pix           []bool
}

// Set marks pixel (x, y); out-of-range pixels are ignored.
func (m *Mask) Set(x, y int) {
	if x >= 0 && y >= 0 && x < m.Width && y < m.Height {
		m.Pix[y*m.Width+x] = true
	}
}

// Rasterizer paints marks into a Mask, standing in for the canvas upstream
// draws to. items are the item handles of one mark (Base.Ref for the base
// mark); groups must be flattened by the implementation. When outline is
// true the marks must be drawn as a 1px opaque black stroke with no fill
// (only for items that have a fill or stroke); otherwise drawn normally.
type Rasterizer interface {
	Draw(m *Mask, items []any, outline bool)
}

// Options are the transform parameters.
type Options struct {
	Size    [2]float64 // layout width and height (required)
	Sort    func(a, b *Label) float64
	Anchor  []string  // default: DefaultAnchors
	Offset  []float64 // default: [1]
	Padding float64
	// UnboundedPadding (upstream: null padding) or an infinite Padding lets
	// labels leave the layout area without limit.
	UnboundedPadding bool
	LineAnchor       string // "start" or "end" (default)
	MarkIndex        int
	AvoidMarks       [][]any // item handles of extra marks to avoid
	NoAvoidBaseMark  bool    // upstream avoidBaseMark=false
	Method           string  // "naive" (default), "reduced-search", "floodfill"

	TextWidth  func(l *Label) float64
	Rasterizer Rasterizer
}

// DefaultAnchors are the anchors tried when Options.Anchor is empty.
var DefaultAnchors = []string{"top-left", "left", "bottom-left", "top", "bottom", "top-right", "right", "bottom-right"}

// Placement is the layout result for one label.
type Placement struct {
	Label *Label
	// X, Y are valid when HasPos. Align and Baseline are empty when upstream
	// leaves them undefined (label not placed).
	X, Y     float64
	HasPos   bool
	Opacity  float64 // 1 when placed, else 0
	Align    string
	Baseline string
}

// Limits on spec-driven allocation: the label bitmap (bits) and the canvas
// mask (pixels).
const (
	maxBitmapBits = 1 << 28
	maxMaskPixels = 1 << 26
)

// anchor codes: vertical in bits 2-3, horizontal in bits 0-1.
var anchorCode = map[string]int8{
	"top-left": 0x0, "top": 0x1, "top-right": 0x2,
	"left": 0x4, "middle": 0x5, "right": 0x6,
	"bottom-left": 0x8, "bottom": 0x9, "bottom-right": 0xa,
}

// Layout places labels. The result is in placement priority order (Sort
// applied), except for group-area labels with the naive method, which keep
// input order as upstream does.
func Layout(ctx context.Context, labels []Label, o Options) ([]Placement, error) {
	if math.IsNaN(o.Size[0]) || math.IsNaN(o.Size[1]) || o.Size[0] < 0 || o.Size[1] < 0 ||
		math.IsInf(o.Size[0], 0) || math.IsInf(o.Size[1], 0) {
		return nil, errors.New("label: size must be a finite non-negative [width, height]")
	}
	if len(labels) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	offset := o.Offset
	if offset == nil {
		offset = []float64{1}
	}
	anchor := o.Anchor
	if anchor == nil {
		anchor = DefaultAnchors
	}
	lineAnchor, method := o.LineAnchor, o.Method
	if lineAnchor == "" {
		lineAnchor = "end"
	}
	if method == "" {
		method = "naive"
	}
	avoidBaseMark := !o.NoAvoidBaseMark

	positions := max(len(offset), len(anchor))
	offsets := make([]float64, positions)
	for i := range offsets {
		switch {
		case i < len(offset):
			offsets[i] = offset[i] // upstream: `_[i] || 0` (NaN -> 0)
			if math.IsNaN(offsets[i]) {
				offsets[i] = 0
			}
		case len(offset) > 0:
			offsets[i] = offsets[len(offset)-1]
		default:
			offsets[i] = math.NaN()
		}
	}
	anchors := make([]int8, positions)
	for i := range anchors {
		switch {
		case i < len(anchor):
			anchors[i] = anchorCode[anchor[i]] // unknown anchors act as top-left
		case len(anchor) > 0:
			anchors[i] = anchors[len(anchor)-1]
		}
	}

	base0 := labels[0].Base
	marktype, grouptype := "", ""
	if base0 != nil {
		marktype = base0.MarkType
		if marktype == "group" && o.MarkIndex >= 0 && o.MarkIndex < len(base0.Marks) {
			grouptype = base0.Marks[o.MarkIndex].MarkType
		}
	}
	isGroupArea := grouptype == "area"
	infPadding := o.UnboundedPadding || math.IsInf(o.Padding, 1)
	isNaiveGroupArea := isGroupArea && method == "naive"

	data := make([]*item, len(labels))
	maxTextWidth, maxTextHeight := -1.0, -1.0
	for i := range labels {
		l := &labels[i]
		d := &item{label: l, boundary: boundaryOf(l, marktype, grouptype, lineAnchor, o.MarkIndex)}
		if infPadding && o.TextWidth != nil {
			d.textWidth = o.TextWidth(l)
		}
		maxTextWidth = jsMax(maxTextWidth, d.textWidth)
		maxTextHeight = jsMax(maxTextHeight, l.FontSize)
		data[i] = d
	}

	padding := o.Padding
	if infPadding {
		maxOff := math.Inf(-1)
		for _, v := range offset {
			maxOff = jsMax(maxOff, v)
		}
		padding = jsMax(maxTextWidth, maxTextHeight) + maxOff
	}
	sc, err := newScaler(o.Size[0], o.Size[1], padding)
	if err != nil {
		return nil, err
	}

	var bms [2]*bitmap
	if !isNaiveGroupArea {
		if o.Sort != nil {
			jssort.Sort(data, func(a, b *item) int { return jssort.Sign(o.Sort(a.label, b.label)) })
		}
		labelInside := false
		for i := 0; i < len(anchors) && !labelInside; i++ {
			labelInside = anchors[i] == 0x5 || offsets[i] < 0
		}
		hasBase := (marktype != "" && avoidBaseMark) || isGroupArea
		if len(o.AvoidMarks) > 0 || hasBase {
			var baseItems []any
			if hasBase {
				baseItems = make([]any, len(labels))
				for i := range labels {
					if b := labels[i].Base; b != nil {
						baseItems[i] = b.Ref
					}
				}
			}
			bms, err = markBitmaps(ctx, sc, o.Rasterizer, baseItems, o.AvoidMarks, labelInside, isGroupArea)
			if err != nil {
				return nil, err
			}
		} else {
			bms[0] = sc.bitmap()
			if avoidBaseMark {
				for _, d := range data {
					bms[0].set(sc.scale(d.boundary[0]), sc.scale(d.boundary[3]))
				}
			}
		}
	}

	var place func(*item) (bool, error)
	if isGroupArea {
		p := &areaPlacer{ctx: ctx, sc: sc, bm0: bms[0], bm1: bms[1], avoidBaseMark: avoidBaseMark, markIndex: o.MarkIndex, textWidth: o.TextWidth}
		switch method {
		case "reduced-search":
			place = p.reducedSearch
		case "floodfill":
			p.bm2 = sc.bitmap()
			place = p.floodFill
		default:
			if method != "naive" {
				return nil, fmt.Errorf("label: unknown method %q", method)
			}
			place = p.naive
		}
	} else {
		place = markPlacer(sc, bms, anchors, offsets, o.TextWidth)
	}

	out := make([]Placement, len(data))
	for i, d := range data {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ok, err := place(d)
		if err != nil {
			return nil, err
		}
		p := Placement{Label: d.label, X: d.x, Y: d.y, HasPos: d.hasPos, Align: d.align, Baseline: d.baseline}
		if ok {
			p.Opacity = 1
		}
		out[i] = p
	}
	return out, nil
}

// item is the per-label working record (upstream's layout datum).
type item struct {
	label     *Label
	boundary  [6]float64 // x1, xc, x2, y1, yc, y2 of the base mark
	textWidth float64    // 0 when not pre-measured
	x, y      float64
	hasPos    bool
	align     string
	baseline  string
}

func (d *item) setPos(x, y float64) { d.x, d.y, d.hasPos = x, y, true }

// boundaryOf mirrors upstream markBoundary: with no base mark, or a line or
// area base mark, the boundary is a single point; a group of lines uses the
// first or last point of the labelled line; anything else uses its bounds.
func boundaryOf(l *Label, marktype, grouptype, lineAnchor string, markIndex int) [6]float64 {
	xy := func(x, y float64) [6]float64 { return [6]float64{x, x, x, y, y, y} }
	switch {
	case marktype == "" || l.Base == nil:
		return xy(l.X, l.Y)
	case marktype == "line" || marktype == "area":
		return xy(l.Base.X, l.Base.Y)
	case grouptype == "line":
		var pts []Point
		if markIndex >= 0 && markIndex < len(l.Base.Marks) {
			pts = l.Base.Marks[markIndex].Points
		}
		if len(pts) == 0 {
			return xy(math.NaN(), math.NaN())
		}
		p := pts[len(pts)-1]
		if lineAnchor == "start" {
			p = pts[0]
		}
		return xy(p.X, p.Y)
	default:
		b := l.Base.Bounds
		return [6]float64{b.X1, (b.X1 + b.X2) / 2, b.X2, b.Y1, (b.Y1 + b.Y2) / 2, b.Y2}
	}
}

// jsMax is Math.max for two operands: NaN is contagious.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}
