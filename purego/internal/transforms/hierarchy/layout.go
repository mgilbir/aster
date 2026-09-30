package hierarchy

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Compare orders two nodes; negative means a sorts first. It is the compiled
// form of a vega `compare` parameter (field/order lists). Upstream applies the
// comparator to hierarchy nodes, not tuples, so fields are node fields:
// "value" is the summed value and tuple fields are reached as "data.name".
type Compare func(a, b *Node) int

// NodeField reads a value from a node, as vega field accessors do when they
// are applied to hierarchy nodes (e.g. the pack `radius` parameter). Use
// [NodeFieldPath] to build one from a field path such as "data.size".
type NodeField func(*Node) jsval.Value

// Common holds the parameters shared by all layouts.
type Common struct {
	// Field sizes nodes: values are summed up the tree. When nil the leaves
	// are counted instead.
	Field Accessor
	// Sort orders siblings, after Field has been applied.
	Sort Compare
	// TupleID, if set, returns a tuple's dataflow id (NaN when it has none).
	// vega breaks comparator ties by ascending tuple id; without it ties keep
	// their current order, which is the same for data in ingestion order.
	TupleID func(jsval.Value) float64
	// As overrides the output field names, one per output slot (see each
	// layout). Missing trailing entries fall back to the defaults.
	As []string
}

// Optional numbers: nil means "parameter not given", in which case d3's own
// default applies. vega only forwards parameters present in the spec.
func Float(f float64) *float64 { return &f }

var errNoTree = errors.New("hierarchy layout transform requires a backing tree data source")

func prepare(root *Node, c Common) {
	if c.Field != nil {
		f := c.Field
		root.sum(func(n *Node) float64 {
			v := jsval.ToNumber(f(n.Data))
			if v != v {
				return 0
			}
			return v
		})
	} else {
		root.count()
	}
	if c.Sort != nil {
		cmp, id := c.Sort, c.TupleID
		root.sortChildren(func(a, b *Node) int {
			if r := cmp(a, b); r != 0 {
				return r
			}
			if id != nil {
				if d := id(a.Data) - id(b.Data); d < 0 {
					return -1
				} else if d > 0 {
					return 1
				}
			}
			return 0
		})
	}
}

type prop uint8

const (
	pX prop = iota
	pY
	pR
	pX0
	pY0
	pX1
	pY1
	pDepth
	pHeight
	pValue
	pChildren
)

func (p prop) get(n *Node) jsval.Value {
	switch p {
	case pX:
		return jsval.Num(n.X)
	case pY:
		return jsval.Num(n.Y)
	case pR:
		return jsval.Num(n.R)
	case pX0:
		return jsval.Num(n.X0)
	case pY0:
		return jsval.Num(n.Y0)
	case pX1:
		return jsval.Num(n.X1)
	case pY1:
		return jsval.Num(n.Y1)
	case pDepth:
		return jsval.Int(n.Depth)
	case pHeight:
		return jsval.Int(n.Height)
	case pValue:
		return jsval.Num(n.Value)
	}
	return jsval.Int(len(n.Children))
}

// writeFields copies the layout result of every node into its tuple: the
// slot i property goes to as[i]; the last slot is always the child count.
func writeFields(root *Node, props []prop, defaults, as []string) {
	names := make([]string, len(props))
	for i := range names {
		names[i] = defaults[i]
		if i < len(as) && as[i] != "" {
			names[i] = as[i]
		}
	}
	root.each(func(n *Node) {
		if !n.Data.IsObj() {
			return
		}
		o := n.Data.ObjValue()
		for i, p := range props {
			o.Set(names[i], p.get(n))
		}
	})
}

func size(dx, dy float64, s []float64) (float64, float64) {
	if len(s) >= 2 {
		return s[0], s[1]
	}
	return dx, dy
}

func toNum(v jsval.Value) float64 { return jsval.ToNumber(v) }

// TreeParams configures [LayoutTree].
type TreeParams struct {
	Common
	// Method is "tidy" (default) or "cluster".
	Method string
	// Size is the [width, height] of the layout; NodeSize the fixed
	// [dx, dy] between nodes. NodeSize wins when both are given, since
	// vega applies it last.
	Size, NodeSize []float64
	// UniformSeparation mirrors `separation: false`: all neighbours are one
	// unit apart instead of siblings 1 and cousins 2.
	UniformSeparation bool
}

var (
	treeProps = []prop{pX, pY, pDepth, pChildren}
	treeNames = []string{"x", "y", "depth", "children"}
	packProps = []prop{pX, pY, pR, pDepth, pChildren}
	packNames = []string{"x", "y", "r", "depth", "children"}
	rectProps = []prop{pX0, pY0, pX1, pY1, pDepth, pChildren}
	rectNames = []string{"x0", "y0", "x1", "y1", "depth", "children"}
	sepFuncs  = [2]separation{defaultSeparation, unitSeparation}
)

// LayoutTree runs vega's Tree transform: Reingold-Tilford (tidy) or
// dendrogram (cluster) layout. Output slots: x, y, depth, children.
func LayoutTree(ctx context.Context, t *Tree, p TreeParams) error {
	if t == nil || t.Root == nil {
		return errNoTree
	}
	method := p.Method
	if method == "" {
		method = "tidy"
	}
	if method != "tidy" && method != "cluster" {
		return fmt.Errorf("Unrecognized Tree layout method: %s", method)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepare(t.Root, p.Common)
	sz := sizing{dx: 1, dy: 1, nodeSize: false}
	if len(p.Size) >= 2 {
		sz.dx, sz.dy, sz.nodeSize = p.Size[0], p.Size[1], false
	}
	if len(p.NodeSize) >= 2 {
		sz.dx, sz.dy, sz.nodeSize = p.NodeSize[0], p.NodeSize[1], true
	}
	sep := sepFuncs[0]
	if p.UniformSeparation {
		sep = sepFuncs[1]
	}
	if method == "tidy" {
		tidy(t.Root, sep, sz)
	} else {
		cluster(t.Root, sep, sz)
	}
	writeFields(t.Root, treeProps, treeNames, p.As)
	return nil
}

// PackParams configures [LayoutPack].
type PackParams struct {
	Common
	// Radius sizes leaves directly; when nil radii are sqrt(value) and the
	// layout is scaled to fit Size.
	Radius  NodeField
	Size    []float64
	Padding *float64
}

// LayoutPack runs vega's Pack transform. Output slots: x, y, r, depth, children.
func LayoutPack(ctx context.Context, t *Tree, p PackParams) error {
	if t == nil || t.Root == nil {
		return errNoTree
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepare(t.Root, p.Common)
	dx, dy := size(1, 1, p.Size)
	pad := 0.0
	if p.Padding != nil {
		pad = *p.Padding
	}
	var radius func(*Node) float64
	if p.Radius != nil {
		f := p.Radius
		radius = func(n *Node) float64 { return toNum(f(n)) }
	}
	if err := pack(ctx, t.Root, radius, dx, dy, pad); err != nil {
		return err
	}
	writeFields(t.Root, packProps, packNames, p.As)
	return nil
}

// PartitionParams configures [LayoutPartition].
type PartitionParams struct {
	Common
	Size    []float64
	Padding *float64
	Round   bool
}

// LayoutPartition runs vega's Partition transform.
// Output slots: x0, y0, x1, y1, depth, children.
func LayoutPartition(ctx context.Context, t *Tree, p PartitionParams) error {
	if t == nil || t.Root == nil {
		return errNoTree
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepare(t.Root, p.Common)
	dx, dy := size(1, 1, p.Size)
	pad := 0.0
	if p.Padding != nil {
		pad = *p.Padding
	}
	partition(t.Root, dx, dy, pad, p.Round)
	writeFields(t.Root, rectProps, rectNames, p.As)
	return nil
}

// TreemapParams configures [LayoutTreemap]. Padding parameters are applied in
// vega's order, so a later, more specific one overrides an earlier general one:
// Padding sets inner and outer; PaddingInner, PaddingOuter, then the four
// sides follow.
type TreemapParams struct {
	Common
	// Method is squarify (default), resquarify, binary, dice, slice or
	// slicedice.
	Method string
	// Ratio is the squarify target aspect ratio (default the golden ratio,
	// values below 1 become 1). Ignored by the other methods.
	Ratio *float64
	Size  []float64
	Round bool

	Padding, PaddingInner, PaddingOuter                  *float64
	PaddingTop, PaddingRight, PaddingBottom, PaddingLeft *float64
}

// LayoutTreemap runs vega's Treemap transform.
// Output slots: x0, y0, x1, y1, depth, children.
func LayoutTreemap(ctx context.Context, t *Tree, p TreemapParams) error {
	if t == nil || t.Root == nil {
		return errNoTree
	}
	cfg := treemapConfig{dx: 1, dy: 1, round: p.Round}
	ratio := phi
	squarifying := true
	resquarify := false
	switch p.Method {
	case "", "squarify":
	case "resquarify":
		resquarify = true
	case "binary":
		cfg.tile, squarifying = tileBinary, false
	case "dice":
		cfg.tile, squarifying = tileDice, false
	case "slice":
		cfg.tile, squarifying = tileSlice, false
	case "slicedice":
		cfg.tile, squarifying = tileSliceDice, false
	default:
		return fmt.Errorf("Unrecognized Treemap layout method: %s", p.Method)
	}
	if squarifying && p.Ratio != nil {
		if r := *p.Ratio; r > 1 { // NaN and <1 fall back to 1
			ratio = r
		} else {
			ratio = 1
		}
	}
	if squarifying {
		if resquarify {
			cfg.tile = tileResquarify(ratio)
		} else {
			cfg.tile = tileSquarify(ratio)
		}
	}
	cfg.dx, cfg.dy = size(1, 1, p.Size)
	set := func(dst *float64, v *float64) {
		if v != nil {
			*dst = *v
		}
	}
	set(&cfg.paddingInner, p.Padding)
	for _, d := range []*float64{&cfg.paddingTop, &cfg.paddingRight, &cfg.paddingBottom, &cfg.paddingLeft} {
		set(d, p.Padding)
	}
	set(&cfg.paddingInner, p.PaddingInner)
	for _, d := range []*float64{&cfg.paddingTop, &cfg.paddingRight, &cfg.paddingBottom, &cfg.paddingLeft} {
		set(d, p.PaddingOuter)
	}
	set(&cfg.paddingTop, p.PaddingTop)
	set(&cfg.paddingRight, p.PaddingRight)
	set(&cfg.paddingBottom, p.PaddingBottom)
	set(&cfg.paddingLeft, p.PaddingLeft)

	if err := ctx.Err(); err != nil {
		return err
	}
	prepare(t.Root, p.Common)
	cfg.layout(t.Root)
	writeFields(t.Root, rectProps, rectNames, p.As)
	return nil
}

// NodeFieldPath builds a NodeField from a dotted path resolved against the
// node: "data.x" reads the tuple's field x, "value", "depth", "height", "x",
// "y", "r", "x0", "y0", "x1", "y1" read layout state.
func NodeFieldPath(path string) NodeField {
	parts := jsval.ParseFieldPath(path)
	return func(n *Node) jsval.Value {
		if len(parts) == 0 {
			return jsval.Undefined
		}
		var v jsval.Value
		switch parts[0] {
		case "data":
			v = n.Data
		case "value":
			v = jsval.Num(n.Value)
		case "depth":
			v = jsval.Int(n.Depth)
		case "height":
			v = jsval.Int(n.Height)
		case "x":
			v = jsval.Num(n.X)
		case "y":
			v = jsval.Num(n.Y)
		case "r":
			v = jsval.Num(n.R)
		case "x0":
			v = jsval.Num(n.X0)
		case "y0":
			v = jsval.Num(n.Y0)
		case "x1":
			v = jsval.Num(n.X1)
		case "y1":
			v = jsval.Num(n.Y1)
		default:
			return jsval.Undefined
		}
		for _, k := range parts[1:] {
			v = v.Get(k)
		}
		return v
	}
}

// TreeLinks generates the {source, target} tuples of vega's TreeLinks
// transform: one per parent-child edge whose both ends satisfy valid (nil
// accepts every tuple). Edges are produced breadth-first, as tree.each does.
func TreeLinks(t *Tree, valid func(jsval.Value) bool) ([]jsval.Value, error) {
	if t == nil || t.Root == nil {
		return nil, errors.New("TreeLinks transform requires a tree data source.")
	}
	var out []jsval.Value
	t.Root.each(func(n *Node) {
		p := n.Parent
		if p == nil || (valid != nil && !(valid(n.Data) && valid(p.Data))) {
			return
		}
		out = append(out, jsval.Obj(jsval.ObjectOf("source", p.Data, "target", n.Data)))
	})
	return out, nil
}
