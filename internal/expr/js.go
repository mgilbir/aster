package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale/color"
)

// JavaScript's abstract operations on jsval.Value. They are methods of Scope
// because turning a Date into a string needs the local time zone.

// colourTag marks the objects rgb(), hsl(), lab() and hcl() return: d3
// colours, which are plain objects with d3's channel names ({r, g, b,
// opacity}, {h, s, l, opacity}, ...) that print as d3's "rgb(r, g, b)" and
// inherit the members of their class (C156). An object of the same shape that
// a colour function did not make is a plain object, as upstream's is.
type colourTag struct {
	class string    // d3's class: Rgb, Hsl, Lab or Hcl
	names [3]string // the channels, then opacity
	proto *jsval.Proto
}

var colourTags = map[string]*colourTag{
	"Rgb": {"Rgb", [3]string{"r", "g", "b"}, colourPs["Rgb"]},
	"Hsl": {"Hsl", [3]string{"h", "s", "l"}, colourPs["Hsl"]},
	"Lab": {"Lab", [3]string{"l", "a", "b"}, colourPs["Lab"]},
	"Hcl": {"Hcl", [3]string{"h", "c", "l"}, colourPs["Hcl"]},
}

// colourOf is the tag of a colour value, nil for anything else.
func colourOf(v jsval.Value) *colourTag {
	o := v.ObjValue()
	if o == nil {
		return nil
	}
	t, _ := o.Host().(*colourTag)
	return t
}

// asColor is the colour a colour value holds.
func asColor(v jsval.Value) (color.Color, bool) {
	t := colourOf(v)
	if t == nil {
		return nil, false
	}
	o := v.ObjValue()
	var ch [4]float64
	for i, name := range [4]string{t.names[0], t.names[1], t.names[2], "opacity"} {
		x, _ := o.Get(name)
		if !x.IsNum() {
			return nil, false
		}
		ch[i] = x.NumValue()
	}
	return t.colour(ch), true
}

// colour is the colour of the class with channels ch (the last opacity).
func (t *colourTag) colour(ch [4]float64) color.Color {
	switch t.class {
	case "Rgb":
		return color.RGB{R: ch[0], G: ch[1], B: ch[2], Opacity: ch[3]}
	case "Hsl":
		return color.HSL{H: ch[0], S: ch[1], L: ch[2], Opacity: ch[3]}
	case "Lab":
		return color.Lab{L: ch[0], A: ch[1], B: ch[2], Opacity: ch[3]}
	}
	return color.HCL{H: ch[0], C: ch[1], L: ch[2], Opacity: ch[3]}
}

// colorValue makes the object form of a colour.
func colorValue(c color.Color) jsval.Value {
	var class string
	var ch [4]float64
	switch c := c.(type) {
	case color.RGB:
		class, ch = "Rgb", [4]float64{c.R, c.G, c.B, c.Opacity}
	case color.HSL:
		class, ch = "Hsl", [4]float64{c.H, c.S, c.L, c.Opacity}
	case color.Lab:
		class, ch = "Lab", [4]float64{c.L, c.A, c.B, c.Opacity}
	case color.HCL:
		class, ch = "Hcl", [4]float64{c.H, c.C, c.L, c.Opacity}
	default:
		rgb := c.RGB()
		class, ch = "Rgb", [4]float64{rgb.R, rgb.G, rgb.B, rgb.Opacity}
	}
	t := colourTags[class]
	o := jsval.NewObject(4)
	for i, n := range t.names {
		o.Set(n, jsval.Num(ch[i]))
	}
	o.Set("opacity", jsval.Num(ch[3]))
	o.SetHost(t)
	// Every d3 colour prints as its rgb() form.
	o.SetStringer(colourString)
	return jsval.Obj(o)
}

// colourMethods gives o, a plain object that holds a d3 colour's methods as
// its own (merge copies them), the String they make: JavaScript's String(o)
// calls o.toString, which formats o's own channels. Rgb's toString reads r,
// g and b; Color's (the other classes') formats o.rgb(), which reads the
// channels of the class whose rgb method o holds.
func colourMethods(o *jsval.Object) {
	ts, ok := o.Get("toString")
	if !ok || !jsval.IsFunction(ts) {
		return
	}
	var t *colourTag
	if jsval.SameRef(ts, colourPs["Rgb"].Get("toString")) {
		t = colourTags["Rgb"]
	} else if jsval.SameRef(ts, colorP.Get("toString")) {
		rgb, _ := o.Get("rgb")
		for _, class := range [...]string{"Rgb", "Hsl", "Lab", "Hcl"} {
			if jsval.SameRef(rgb, colourPs[class].Get("rgb")) {
				t = colourTags[class]
			}
		}
	}
	if t == nil {
		return
	}
	o.SetStringer(func(o *jsval.Object) string {
		var ch [4]float64
		for i, name := range [4]string{t.names[0], t.names[1], t.names[2], "opacity"} {
			// A channel that is an array or an object (which merge can put
			// there) is NaN here: its number is its text, a walk this
			// stringer has no call to charge.
			if x := o.Lookup(name); isObjectLike(x) {
				ch[i] = math.NaN()
			} else {
				ch[i] = jsval.ToNumber(x)
			}
		}
		return t.colour(ch).RGB().FormatRgb()
	})
}

func colourString(o *jsval.Object) string {
	if c, ok := asColor(jsval.Obj(o)); ok {
		return c.RGB().FormatRgb()
	}
	return "[object Object]"
}

// isObjectLike is true for the values JavaScript treats as objects.
func isObjectLike(v jsval.Value) bool {
	switch v.Kind() {
	case jsval.KindArr, jsval.KindObj, jsval.KindTimestamp, jsval.KindPattern:
		return true
	}
	return false
}

// str is String(v).
func (s *Scope) str(v jsval.Value) string { return s.strDepth(v, 0) }

// strDepth is str for an array nested depth levels deep. A value built at run
// time can nest as deep as the specification is long, so the descent is
// bounded by jsval.MaxValueDepth. V8 throws a RangeError only past about ten
// thousand frames, so the bound is the engine's own and ends the render.
func (s *Scope) strDepth(v jsval.Value, depth int) string {
	if depth > jsval.MaxValueDepth {
		exceeded("a value nests deeper than %d levels", jsval.MaxValueDepth)
	}
	switch v.Kind() {
	case jsval.KindStr:
		return v.StrValue()
	case jsval.KindTimestamp:
		return format.DateToString(v.NumValue(), s.zone())
	case jsval.KindArr:
		items := v.Items()
		switch len(items) {
		case 0:
			return ""
		case 1:
			if items[0].IsNullish() {
				return ""
			}
			return s.strDepth(items[0], depth+1)
		}
		b := newStrBuilder(s)
		for i, it := range items {
			if i > 0 {
				b.addByte(',')
			}
			if !it.IsNullish() {
				b.add(s.strDepth(it, depth+1))
			}
		}
		return b.String()
	case jsval.KindObj:
		if c, ok := asColor(v); ok {
			return c.String()
		}
	}
	return v.AsString()
}

// primitive is ToPrimitive. Objects have no valueOf of their own, so both hints
// end at toString, except Date, whose default hint is string and whose number
// hint is its time value.
func (s *Scope) primitive(v jsval.Value, hintNumber bool) jsval.Value {
	switch v.Kind() {
	case jsval.KindTimestamp:
		if hintNumber {
			return jsval.Num(v.NumValue())
		}
		return jsval.Str(s.str(v))
	case jsval.KindArr, jsval.KindObj, jsval.KindPattern:
		return jsval.Str(s.str(v))
	}
	return v
}

// num is Number(v).
func (s *Scope) num(v jsval.Value) float64 {
	switch v.Kind() {
	case jsval.KindNum, jsval.KindTimestamp:
		return v.NumValue()
	case jsval.KindBool:
		if v.BoolValue() {
			return 1
		}
		return 0
	case jsval.KindNull:
		return 0
	case jsval.KindUndefined:
		return math.NaN()
	case jsval.KindStr:
		return jsval.StringToNumber(v.StrValue())
	case jsval.KindArr:
		// String(array) of two or more elements holds a comma, which no
		// number literal does: the result is NaN without making the string.
		if v.Len() >= 2 {
			return math.NaN()
		}
	}
	return jsval.StringToNumber(s.str(v))
}

// toInt32 is ToInt32.
func toInt32(f float64) int32 { return jsmath.ToInt32(f) }

func toUint32(f float64) uint32 { return jsmath.ToUint32(f) }

// toInteger is ToIntegerOrInfinity.
func toInteger(f float64) float64 {
	if math.IsNaN(f) {
		return 0
	}
	return math.Trunc(f)
}

// strictEquals is ===. Dates compare by time value: values carry no object
// identity, so two distinct Date objects for the same instant compare equal
// here where JavaScript says false.
func strictEquals(a, b jsval.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case jsval.KindUndefined, jsval.KindNull:
		return true
	case jsval.KindNum:
		return a.NumValue() == b.NumValue()
	case jsval.KindTimestamp:
		x, y := a.NumValue(), b.NumValue()
		return x == y || math.IsNaN(x) && math.IsNaN(y)
	case jsval.KindBool:
		return a.BoolValue() == b.BoolValue()
	case jsval.KindStr:
		return a.StrValue() == b.StrValue()
	}
	return jsval.SameRef(a, b)
}

// looseEquals is ==.
func (s *Scope) looseEquals(a, b jsval.Value) bool {
	ka, kb := a.Kind(), b.Kind()
	if ka == kb {
		return strictEquals(a, b)
	}
	if a.IsNullish() || b.IsNullish() {
		return a.IsNullish() && b.IsNullish()
	}
	switch {
	case ka == jsval.KindNum && kb == jsval.KindStr:
		return a.NumValue() == jsval.StringToNumber(b.StrValue())
	case ka == jsval.KindStr && kb == jsval.KindNum:
		return jsval.StringToNumber(a.StrValue()) == b.NumValue()
	case ka == jsval.KindBool:
		return s.looseEquals(jsval.Num(s.num(a)), b)
	case kb == jsval.KindBool:
		return s.looseEquals(a, jsval.Num(s.num(b)))
	case isObjectLike(a) && !isObjectLike(b):
		return s.looseEquals(s.primitive(a, false), b)
	case !isObjectLike(a) && isObjectLike(b):
		return s.looseEquals(a, s.primitive(b, false))
	}
	return false
}

// less is the abstract relational comparison a < b. defined is false when the
// comparison involves NaN (JavaScript's undefined result, which every operator
// turns into false).
func (s *Scope) less(a, b jsval.Value) (lt, defined bool) {
	if a.IsNum() && b.IsNum() {
		x, y := a.NumValue(), b.NumValue()
		if math.IsNaN(x) || math.IsNaN(y) {
			return false, false
		}
		return x < y, true
	}
	c, ok := jsval.RelatePrimitives(s.primitive(a, true), s.primitive(b, true))
	return ok && c < 0, ok
}

func (s *Scope) lt(a, b jsval.Value) bool { r, _ := s.less(a, b); return r }
func (s *Scope) gt(a, b jsval.Value) bool { r, _ := s.less(b, a); return r }
func (s *Scope) le(a, b jsval.Value) bool {
	r, ok := s.less(b, a)
	return ok && !r
}
func (s *Scope) ge(a, b jsval.Value) bool {
	r, ok := s.less(a, b)
	return ok && !r
}

// add is the + operator: string concatenation if either primitive is a string,
// numeric addition otherwise.
func (s *Scope) add(a, b jsval.Value) jsval.Value {
	if a.IsNum() && b.IsNum() {
		return jsval.Num(a.NumValue() + b.NumValue())
	}
	pa, pb := s.primitive(a, false), s.primitive(b, false)
	if pa.IsStr() || pb.IsStr() {
		return jsval.Str(s.concat(s.str(pa), s.str(pb)))
	}
	return jsval.Num(s.num(pa) + s.num(pb))
}

// jsMod is the % operator: the result takes the sign of the dividend.
func jsMod(a, b float64) float64 {
	return math.Mod(a, b)
}
