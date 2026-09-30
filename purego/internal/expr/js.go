package expr

import (
	"math"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scale/color"
)

// JavaScript's abstract operations on jsval.Value. They are methods of Scope
// because turning a Date into a string needs the local time zone.

// asColor recognises the objects rgb(), hsl(), lab() and hcl() return: d3
// colours, whose toString is "rgb(r, g, b)". They are represented as plain
// objects with the d3 channel names, and are recognised by that exact shape.
func asColor(v jsval.Value) (color.Color, bool) {
	o := v.ObjValue()
	if o == nil || o.Len() != 4 || o.KeyAt(3) != "opacity" {
		return nil, false
	}
	var ch [4]float64
	for i := range ch {
		x := o.ValueAt(i)
		if !x.IsNum() {
			return nil, false
		}
		ch[i] = x.NumValue()
	}
	switch o.KeyAt(0) + o.KeyAt(1) + o.KeyAt(2) {
	case "rgb":
		return color.RGB{R: ch[0], G: ch[1], B: ch[2], Opacity: ch[3]}, true
	case "hsl":
		return color.HSL{H: ch[0], S: ch[1], L: ch[2], Opacity: ch[3]}, true
	case "lab":
		return color.Lab{L: ch[0], A: ch[1], B: ch[2], Opacity: ch[3]}, true
	case "hcl":
		return color.HCL{H: ch[0], C: ch[1], L: ch[2], Opacity: ch[3]}, true
	}
	return nil, false
}

// colorValue makes the object form of a colour.
func colorValue(c color.Color) jsval.Value {
	var names [3]string
	var ch [4]float64
	switch c := c.(type) {
	case color.RGB:
		names, ch = [3]string{"r", "g", "b"}, [4]float64{c.R, c.G, c.B, c.Opacity}
	case color.HSL:
		names, ch = [3]string{"h", "s", "l"}, [4]float64{c.H, c.S, c.L, c.Opacity}
	case color.Lab:
		names, ch = [3]string{"l", "a", "b"}, [4]float64{c.L, c.A, c.B, c.Opacity}
	case color.HCL:
		names, ch = [3]string{"h", "c", "l"}, [4]float64{c.H, c.C, c.L, c.Opacity}
	default:
		rgb := c.RGB()
		names, ch = [3]string{"r", "g", "b"}, [4]float64{rgb.R, rgb.G, rgb.B, rgb.Opacity}
	}
	o := jsval.NewObject(4)
	for i, n := range names {
		o.Set(n, jsval.Num(ch[i]))
	}
	o.Set("opacity", jsval.Num(ch[3]))
	return jsval.Obj(o)
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
func (s *Scope) str(v jsval.Value) string {
	switch v.Kind() {
	case jsval.KindStr:
		return v.StrValue()
	case jsval.KindTimestamp:
		return dateToString(v.NumValue(), s.zone())
	case jsval.KindArr:
		items := v.Items()
		switch len(items) {
		case 0:
			return ""
		case 1:
			if items[0].IsNullish() {
				return ""
			}
			return s.str(items[0])
		}
		var b []byte
		for i, it := range items {
			if i > 0 {
				b = append(b, ',')
			}
			if !it.IsNullish() {
				b = append(b, s.str(it)...)
			}
		}
		return string(b)
	case jsval.KindObj:
		if c, ok := asColor(v); ok {
			return c.String()
		}
		return "[object Object]"
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
	}
	return jsval.StringToNumber(s.str(v))
}

// toInt32 is ToInt32.
func toInt32(f float64) int32 {
	if f >= -2147483648 && f <= 2147483647 {
		return int32(f) // truncates toward zero; NaN fails the range test
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int32(uint32(int64(math.Mod(math.Trunc(f), 4294967296))))
}

func toUint32(f float64) uint32 { return uint32(toInt32(f)) }

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
	pa, pb := s.primitive(a, true), s.primitive(b, true)
	if pa.IsStr() && pb.IsStr() {
		return compareUTF16(pa.StrValue(), pb.StrValue()) < 0, true
	}
	x, y := s.num(pa), s.num(pb)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false, false
	}
	return x < y, true
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
		return jsval.Str(s.str(pa) + s.str(pb))
	}
	return jsval.Num(s.num(pa) + s.num(pb))
}

// jsMod is the % operator: the result takes the sign of the dividend.
func jsMod(a, b float64) float64 {
	return math.Mod(a, b)
}

// jsRound is Math.round: halves round toward +Infinity, and -0 is preserved.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == math.Trunc(x) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		f++
	}
	if f == 0 && x < 0 {
		return math.Copysign(0, -1)
	}
	return f
}
