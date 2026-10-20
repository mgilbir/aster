package hierarchy

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
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
	// Sort orders siblings, after Field has been applied. Ties go by the
	// tuple ids of the nodes' data (`stableCompare(_.sort, d => d.data)`),
	// which is the order they were ingested in, whatever order a sort
	// upstream of the layout left them in.
	Sort Compare
	// AsExact reports that As holds every output slot's name as upstream
	// resolves them (`_.as || fields`, each as[i] the key it names): an
	// empty one is the empty name.
	AsExact bool
	// As overrides the output field names, one per output slot (see each
	// layout). Missing trailing entries fall back to the defaults.
	As []string
}

// Optional numbers: nil means "parameter not given", in which case d3's own
// default applies. vega only forwards parameters present in the spec.
func Float(f float64) *float64 { return &f }

var errNoTree = errors.New("hierarchy layout transform requires a backing tree data source")

// Each node summed and each comparison is a step of work charged to m, which
// polls the call's context: the field and the comparator can be expressions
// (ADR 0010).
func prepare(m *budget.Meter, root *Node, c Common) error {
	var err error
	if c.Field != nil {
		f := c.Field
		root.sum(func(n *Node) float64 {
			if err != nil {
				return 0
			}
			if err = m.Work(1); err != nil {
				return 0
			}
			v := jsval.ToNumber(f(n.Data))
			if v != v {
				return 0
			}
			return v
		})
	} else {
		root.count()
	}
	if err != nil {
		return err
	}
	if c.Sort != nil {
		cmp := c.Sort
		root.sortChildren(func(a, b *Node) int {
			if err != nil {
				return 0 // the sort is abandoned with the call
			}
			if err = m.Work(1); err != nil {
				return 0
			}
			if r := cmp(a, b); r != 0 {
				return r
			}
			return jsval.TupleIDOrder(a.Data, b.Data)
		})
	}
	return err
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
// Each node written is a step of work per field, charged to m.
func writeFields(m *budget.Meter, root *Node, props []prop, defaults, as []string, exact bool) error {
	names := make([]string, len(props))
	for i := range names {
		names[i] = defaults[i]
		if i < len(as) && (as[i] != "" || exact) {
			names[i] = as[i]
		}
	}
	var err error
	root.each(func(n *Node) {
		if err != nil {
			return
		}
		if err = m.Work(int64(len(props))); err != nil {
			return
		}
		o := jsval.Fields(n.Data)
		if o == nil {
			// `t[as[i]] = ...` where the datum is no object (a nest leaf's
			// values can be anything): a primitive is a TypeError; a date
			// or a pattern takes the property, which nothing reads
			err = setOnPrimitive(n.Data, names[0])
			return
		}
		for i, p := range props {
			o.Set(names[i], p.get(n))
		}
	})
	return err
}

// setOnPrimitive is the TypeError of setting the property name on v in
// strict mode (jsval.FieldsFor's), nil for a value that is an object in
// JavaScript.
func setOnPrimitive(v jsval.Value, name string) error {
	switch v.Kind() {
	case jsval.KindNull, jsval.KindUndefined:
		of := "undefined"
		if v.IsNull() {
			of = "null"
		}
		return &jsval.Thrown{Name: "TypeError", Msg: "Cannot set properties of " + of + " (setting '" + name + "')"}
	case jsval.KindStr, jsval.KindNum, jsval.KindBool:
		kind := map[jsval.Kind]string{jsval.KindStr: "string", jsval.KindNum: "number", jsval.KindBool: "boolean"}[v.Kind()]
		return &jsval.Thrown{Name: "TypeError", Msg: "Cannot create property '" + name + "' on " + kind + " '" + jsval.StringPrefix(v, jsval.MessageBytes) + "'"}
	}
	return nil
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
	m := budget.NewMeter(ctx)
	if err := prepare(&m, t.Root, p.Common); err != nil {
		return err
	}
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
	var err error
	if method == "tidy" {
		err = tidy(&m, t.Root, sep, sz)
	} else {
		err = cluster(&m, t.Root, sep, sz)
	}
	if err != nil {
		return err
	}
	return writeFields(&m, t.Root, treeProps, treeNames, p.As, p.AsExact)
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
	m := budget.NewMeter(ctx)
	if err := prepare(&m, t.Root, p.Common); err != nil {
		return err
	}
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
	return writeFields(&m, t.Root, packProps, packNames, p.As, p.AsExact)
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
	m := budget.NewMeter(ctx)
	if err := prepare(&m, t.Root, p.Common); err != nil {
		return err
	}
	dx, dy := size(1, 1, p.Size)
	pad := 0.0
	if p.Padding != nil {
		pad = *p.Padding
	}
	if err := partition(&m, t.Root, dx, dy, pad, p.Round); err != nil {
		return err
	}
	return writeFields(&m, t.Root, rectProps, rectNames, p.As, p.AsExact)
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
	m := budget.NewMeter(ctx)
	if err := prepare(&m, t.Root, p.Common); err != nil {
		return err
	}
	if err := cfg.layout(&m, t.Root); err != nil {
		return err
	}
	return writeFields(&m, t.Root, rectProps, rectNames, p.As, p.AsExact)
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
			// a name the node has no property of: undefined, and a step
			// past it reads undefined
			v = jsval.Undefined
		}
		// The comparator reads the path as a member chain (vega-util's
		// getter): a step into null or undefined is a TypeError.
		for _, k := range parts[1:] {
			if v.IsNullish() {
				nm := "undefined"
				if v.IsNull() {
					nm = "null"
				}
				panic(&jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + nm + " (reading '" + k + "')"})
			}
			v = v.Get(k)
		}
		return v
	}
}

// TreeLinks generates the {source, target} tuples of vega's TreeLinks
// transform: one per parent-child edge whose both ends satisfy valid (nil
// accepts every tuple). Edges are produced breadth-first, as tree.each does.
func TreeLinks(ctx context.Context, t *Tree, valid func(jsval.Value) bool) ([]jsval.Value, error) {
	if t == nil || t.Root == nil {
		return nil, errors.New("TreeLinks transform requires a tree data source.")
	}
	m := budget.NewMeter(ctx)
	var out []jsval.Value
	var err error
	t.Root.each(func(n *Node) {
		if err == nil {
			err = m.Work(1)
		}
		if err != nil {
			return
		}
		p := n.Parent
		if p == nil || (valid != nil && !(valid(n.Data) && valid(p.Data))) {
			return
		}
		out = append(out, jsval.Obj(jsval.ObjectOf("source", p.Data, "target", n.Data)))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
