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
//
// It reads trusted input only: tests and fuzz targets load upstream's
// serialized scenegraphs with it, and no public API or render path calls it,
// so its parse is not metered (jsval.ParseJSONContext) and has no context to
// poll. A caller that hands it untrusted JSON needs one first.
func FromJSON(data []byte) (*Scenegraph, error) {
	v, err := jsval.ParseJSON(data)
	if err != nil {
		return nil, err
	}
	// A serialized scene is trusted input (see above) and has no call: its
	// values are walked with the detached Walker.
	l := &jsonLoader{w: jsval.Walker{}}
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
func pathFuncFromValue(w jsval.Walker, v jsval.Value) (PathFunc, error) {
	d := strOf(w, v.Get("path"))
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
			n := func(i int) float64 { return numOf(decodeRecorded(op.Index(i))).Val() } // an operand left out is undefined
			name, _ := a[0].PropertyKey(1)
			switch name {
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

// jsonLoader reads the encodings of the serialized scenegraphs, which no live
// value is read with (Item.Set is JavaScript's reading of a value): a number
// JSON cannot hold is {"$num": "NaN"|"Infinity"|"-Infinity"}, a gradient is
// defined once with `$gid` and later occurrences are `{"$gref": id}`
// references to the same object, and a clip or shape generator is the
// `{"path", "ops"}` it was recorded drawing.
type jsonLoader struct {
	grads map[int]*Gradient
	w     jsval.Walker
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
	name, _ := o.Lookup("marktype").PropertyKey(16)
	mt, ok := ParseMarkType(name)
	if !ok {
		return nil, fmt.Errorf("scene: unknown marktype %q", jsval.StringPrefix(o.Lookup("marktype"), jsval.MessageBytes))
	}
	m.Type = mt
	m.Name = strOf(l.w, o.Lookup("name"))
	m.Role = strOf(l.w, o.Lookup("role"))
	m.Description = strOf(l.w, o.Lookup("description"))
	m.Aria = FalseOf(o.Lookup("aria"))
	m.NonInteractive = o.Lookup("interactive").Kind() == jsval.KindBool && !o.Lookup("interactive").BoolValue()
	m.Zindex = numOf(decodeRecorded(o.Lookup("zindex"))).Zero()
	m.GuideCaption = strOf(l.w, o.Lookup("guideCaption"))
	switch c := o.Lookup("clip"); {
	case c.IsObj():
		fn, err := pathFuncFromValue(l.w, c)
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

// paint is paintOf with the loader's gradient encoding: a gradient recorded
// with `$gid` is made once, and `{"$gref": id}` is that same object again
// (upstream's gradient ids are per object); its coordinates may be {$num}.
func (l *jsonLoader) paint(v jsval.Value) Paint {
	if v.IsObj() {
		if ref := v.Get("$gref"); !ref.IsUndefined() {
			return GradientPaint(l.grads[int(ref.NumValue())])
		}
		if v.Get("gradient").IsTruthy() {
			g := gradientOf(l.w, v, decodeRecorded)
			if id := v.Get("$gid"); id.IsNum() {
				if l.grads == nil {
					l.grads = map[int]*Gradient{}
				}
				l.grads[int(id.NumValue())] = g
			}
			return GradientPaint(g)
		}
	}
	return paintOf(l.w, v)
}

// decodeRecorded reads the loader's encoding of a number JSON cannot hold,
// {"$num": "NaN"|"Infinity"|"-Infinity"}; any other value is returned as is.
func decodeRecorded(v jsval.Value) jsval.Value {
	if v.IsObj() {
		name, _ := v.Get("$num").PropertyKey(16)
		switch name {
		case "NaN":
			return jsval.Num(math.NaN())
		case "Infinity":
			return jsval.Num(math.Inf(1))
		case "-Infinity":
			return jsval.Num(math.Inf(-1))
		}
	}
	return v
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
		val = decodeRecorded(val)
		switch {
		case k == "fill":
			it.setRawStr(k, val)
			it.Fill = l.paint(val)
		case k == "stroke":
			it.setRawStr(k, val)
			it.Stroke = l.paint(val)
		case k == "shape" && val.IsObj():
			it.setRawStr(k, val)
			fn, err := pathFuncFromValue(l.w, val)
			if err != nil {
				return nil, err
			}
			it.Shape.Func = func(ctx PathContext, _ *Item) string { return fn(ctx) }
		case k == "clip" && val.IsObj():
			fn, err := pathFuncFromValue(l.w, val)
			if err != nil {
				return nil, err
			}
			it.Clip, it.ClipPath = Yes, fn
		default:
			if _, err := it.setProp(k, val); err != nil {
				return nil, err
			}
		}
	}
	if m.Type == MarkGroup {
		it.Items = make([]*Mark, 0, childMarks.Len()) // a group item always has its items array
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
