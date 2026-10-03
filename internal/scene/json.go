package scene

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// FromJSON builds a scenegraph from vega's serialized form (the JSON that
// Scenegraph.toJSON produces): a root mark object whose `items` are item
// objects, with group items holding child marks in their own `items`.
//
// Values are coerced to the field types the way the renderer would read them
// (`+x` for numbers, `String(x)` for strings). Function-valued properties have
// no JSON form; two extensions carry their output instead: a mark or group
// `clip` of `{"path": "M..."}` and a shape item's `shape` of `{"path": "M..."}`
// become PathFunc/ShapeFunc that replay the path data. The bounds and
// z-order caches are not read; use Bounder to compute bounds.
func FromJSON(data []byte) (*Scenegraph, error) {
	v, err := jsval.ParseJSON(data)
	if err != nil {
		return nil, err
	}
	l := &jsonLoader{}
	root, err := l.mark(v, nil, 0)
	if err != nil {
		return nil, err
	}
	return &Scenegraph{Root: root}, nil
}

// PathFuncFromString returns a PathFunc that replays SVG path data: with a nil
// context it returns the string as given, otherwise it draws the parsed commands
// (parsing lazily, so that generator output that is not plain SVG path syntax,
// such as "MNaN,NaN", only matters when drawn; a parse failure draws nothing).
func PathFuncFromString(d string) (PathFunc, error) {
	var (
		cmds []PathCmd
		done bool
	)
	return func(ctx PathContext) string {
		if ctx == nil {
			return d
		}
		if !done {
			cmds, _ = ParsePath(d)
			done = true
		}
		_ = RenderPath(ctx, cmds, 0, 0, 1, 1)
		return ""
	}, nil
}

// pathFuncFromValue builds a PathFunc from `{"path": "...", "ops": [...]}`. The
// ops, when present, are the canvas calls the generator made (`["M",x,y]`,
// `["L",x,y]`, `["Q",..]`, `["C",..]`, `["A",x,y,r,a0,a1,ccw]`, `["R",x,y,w,h]`,
// `["Z"]`) and are replayed for context drawing at full precision.
func pathFuncFromValue(v jsval.Value) (PathFunc, error) {
	d := strOf(v.Get("path"))
	ops := v.Get("ops")
	if !ops.IsArr() {
		return PathFuncFromString(d)
	}
	list := ops.Items()
	return func(ctx PathContext) string {
		if ctx == nil {
			return d
		}
		for _, op := range list {
			a := op.Items()
			if len(a) == 0 {
				continue
			}
			n := func(i int) float64 { return numOf(op.Index(i)).Val() } // an operand left out is undefined
			switch a[0].AsString() {
			case "M":
				ctx.MoveTo(n(1), n(2))
			case "L":
				ctx.LineTo(n(1), n(2))
			case "Q":
				ctx.QuadraticCurveTo(n(1), n(2), n(3), n(4))
			case "C":
				ctx.BezierCurveTo(n(1), n(2), n(3), n(4), n(5), n(6))
			case "A":
				ctx.Arc(n(1), n(2), n(3), n(4), n(5), n(6) != 0)
			case "R":
				ctx.Rect(n(1), n(2), n(3), n(4))
			case "Z":
				ctx.ClosePath()
			}
		}
		return ""
	}, nil
}

// jsonLoader keeps the gradient identity table: a `$gid` object is defined once
// and later occurrences are `{"$gref": id}` references to the same object.
type jsonLoader struct {
	grads map[int]*Gradient
}

func (l *jsonLoader) mark(v jsval.Value, group *Item, depth int) (*Mark, error) {
	if depth > MaxDepth {
		return nil, ErrTooDeep
	}
	o := v.ObjValue()
	if o == nil {
		return nil, fmt.Errorf("scene: mark must be an object")
	}
	m := &Mark{Group: group, Bounds: NewBounds()}
	mt, ok := ParseMarkType(o.Lookup("marktype").AsString())
	if !ok {
		return nil, fmt.Errorf("scene: unknown marktype %q", o.Lookup("marktype").AsString())
	}
	m.Type = mt
	m.Name = strOf(o.Lookup("name"))
	m.Role = strOf(o.Lookup("role"))
	m.Description = strOf(o.Lookup("description"))
	m.Aria = triOf(o.Lookup("aria"))
	m.NonInteractive = o.Lookup("interactive").Kind() == jsval.KindBool && !o.Lookup("interactive").BoolValue()
	m.Zindex = numOf(o.Lookup("zindex")).Zero()
	m.GuideCaption = strOf(o.Lookup("guideCaption"))
	switch c := o.Lookup("clip"); {
	case c.IsObj():
		fn, err := pathFuncFromValue(c)
		if err != nil {
			return nil, err
		}
		m.Clip, m.ClipPath = true, fn
	default:
		m.Clip = c.IsTruthy()
	}
	items := o.Lookup("items")
	for _, iv := range items.Items() {
		it, err := l.item(iv, m, depth)
		if err != nil {
			return nil, err
		}
		m.Items = append(m.Items, it)
	}
	return m, nil
}

func strOf(v jsval.Value) string {
	if v.IsNullish() {
		return ""
	}
	if v.IsStr() {
		return v.StrValue()
	}
	return v.AsString()
}

// strOfTruthy is strOf for properties that upstream reads as `value || default`
// (interpolate, orient): every falsy value (0, NaN, false, null) is unset.
func strOfTruthy(v jsval.Value) string {
	if !v.IsTruthy() {
		return ""
	}
	return strOf(v)
}

func numOf(v jsval.Value) Num {
	if v.IsNullish() {
		return Num{}
	}
	if v.IsObj() {
		// {"$num": "NaN"|"Infinity"|"-Infinity"}: non-finite numbers, which
		// JSON cannot carry.
		switch v.Get("$num").AsString() {
		case "NaN":
			return N(math.NaN())
		case "Infinity":
			return N(math.Inf(1))
		case "-Infinity":
			return N(math.Inf(-1))
		}
	}
	return N(jsval.ToNumber(v))
}

func triOf(v jsval.Value) Tri {
	if v.IsNullish() {
		return Unset
	}
	return B(v.IsTruthy())
}

func (l *jsonLoader) paint(v jsval.Value) Paint {
	switch {
	case v.IsUndefined():
		return Paint{}
	case v.IsNull():
		return NullPaint()
	case v.IsObj():
		if ref := v.Get("$gref"); l != nil && !ref.IsUndefined() {
			return GradientPaint(l.grads[int(ref.NumValue())])
		}
		if v.Get("gradient").IsTruthy() {
			g := gradientOf(v)
			if id := v.Get("$gid"); l != nil && id.IsNum() {
				if l.grads == nil {
					l.grads = map[int]*Gradient{}
				}
				l.grads[int(id.NumValue())] = g
			}
			return GradientPaint(g)
		}
		return Color(v.AsString())
	case v.IsStr():
		return Color(v.StrValue())
	}
	return Color(v.AsString())
}

func gradientOf(v jsval.Value) *Gradient {
	g := &Gradient{
		Radial: v.Get("gradient").AsString() == "radial",
		ID:     strOf(v.Get("id")),
		X1:     numOf(v.Get("x1")), Y1: numOf(v.Get("y1")),
		X2: numOf(v.Get("x2")), Y2: numOf(v.Get("y2")),
		R1: numOf(v.Get("r1")), R2: numOf(v.Get("r2")),
	}
	for _, s := range v.Get("stops").Items() {
		g.Stops = append(g.Stops, GradientStop{
			Offset: jsval.ToNumber(s.Get("offset")),
			Color:  strOf(s.Get("color")),
		})
	}
	return g
}

func (l *jsonLoader) item(v jsval.Value, m *Mark, depth int) (*Item, error) {
	o := v.ObjValue()
	if o == nil {
		return nil, fmt.Errorf("scene: item must be an object")
	}
	it := &Item{Mark: m, Bounds: NewBounds()}
	var childMarks jsval.Value
	for i := 0; i < o.Len(); i++ {
		k, val := o.KeyAt(i), o.ValueAt(i)
		if k == "items" {
			childMarks = val
			continue
		}
		if _, err := it.setProp(l, k, val); err != nil {
			return nil, err
		}
	}
	if m.Type == MarkGroup {
		for _, cv := range childMarks.Items() {
			child, err := l.mark(cv, it, depth+1)
			if err != nil {
				return nil, err
			}
			it.Items = append(it.Items, child)
		}
	}
	return it, nil
}
