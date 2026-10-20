package scene

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Set assigns an item property by its Vega name from a dynamic value, coercing
// it to the field's type the way the renderer would read it (`+x` for numbers,
// `String(x)` for text, null/undefined for "unset"). It is how encode results
// (name to value maps) become items. Names this package does not model go into
// Extra and Set reports false for them; the structural keys (items, bounds,
// mark, group) are ignored. Function-valued properties (clip paths, shape
// generators) cannot be expressed as values: set ClipPath and Shape.Func
// directly.
//
// A gradient value is a jsval object with a `gradient` key ("linear" or
// "radial"), optional coordinates and `stops`; each Set creates a new
// gradient identity, as each encode evaluation creates a new object.
func (it *Item) Set(key string, val jsval.Value) (known bool, err error) {
	return it.setProp(nil, key, val)
}

func (it *Item) setProp(l *jsonLoader, k string, val jsval.Value) (bool, error) {
	w := it.Walker()
	if rawNumeric(k) {
		it.setRaw(k, val)
	} else if rawString(k) {
		it.setRawStr(k, val)
	}
	switch k {
	case "x":
		it.X = numOf(val)
	case "y":
		it.Y = numOf(val)
	case "x2":
		it.X2 = numOf(val)
	case "y2":
		it.Y2 = numOf(val)
	case "width":
		it.Width = numOf(val)
	case "height":
		it.Height = numOf(val)
	case "align":
		it.Align = strOf(w, val)
	case "baseline":
		it.Baseline = strOf(w, val)
	case "fill":
		it.Fill = l.paint(w, val)
	case "stroke":
		it.Stroke = l.paint(w, val)
	case "opacity":
		it.Opacity = numOf(val)
	case "fillOpacity":
		it.FillOpacity = numOf(val)
	case "strokeOpacity":
		it.StrokeOpacity = numOf(val)
	case "strokeWidth":
		it.StrokeWidth = numOf(val)
	case "strokeCap":
		it.strokeW().StrokeCap = strOf(w, val)
	case "strokeJoin":
		it.strokeW().StrokeJoin = strOf(w, val)
	case "strokeMiterLimit":
		it.strokeW().StrokeMiterLimit = numOf(val)
	case "strokeDash":
		it.strokeW().StrokeDash = val
	case "strokeDashOffset":
		it.strokeW().StrokeDashOffset = numOf(val)
	case "strokeForeground":
		it.strokeW().StrokeForeground = triOf(val)
		it.setRawTri(k, val)
	case "strokeOffset":
		it.strokeW().StrokeOffset = numOf(val)
	case "blend":
		it.strokeW().Blend = strOf(w, val)
	case "cornerRadius":
		it.geomW().CornerRadius = numOf(val)
	case "cornerRadiusTopLeft":
		it.geomW().CornerRadiusTopLeft = numOf(val)
	case "cornerRadiusTopRight":
		it.geomW().CornerRadiusTopRight = numOf(val)
	case "cornerRadiusBottomLeft":
		it.geomW().CornerRadiusBottomLeft = numOf(val)
	case "cornerRadiusBottomRight":
		it.geomW().CornerRadiusBottomRight = numOf(val)
	case "startAngle":
		it.geomW().StartAngle = numOf(val)
	case "endAngle":
		it.geomW().EndAngle = numOf(val)
	case "padAngle":
		it.geomW().PadAngle = numOf(val)
	case "innerRadius":
		it.geomW().InnerRadius = numOf(val)
	case "outerRadius":
		it.geomW().OuterRadius = numOf(val)
	case "shape":
		switch {
		case l != nil && val.IsObj():
			// A recorded scene carries a shape generator as an object.
			fn, err := pathFuncFromValue(w, val)
			if err != nil {
				return true, err
			}
			it.Shape.Func = func(ctx PathContext, _ *Item) string { return fn(ctx) }
		case shapeFuncOf(val) != nil:
			it.Shape.Func = shapeFuncOf(val)
		default:
			// Any other value is a symbol name or path, as given (see Raw);
			// a shape mark has no generator in it.
			it.Shape.Name = strOf(w, val)
		}
	case "size":
		it.Size = numOf(val)
	case "path":
		it.setRawStr(k, val)
		// item.path = value: a nullish path clears an earlier one (the
		// voronoi transform sets a degenerate cell's path to null on a later
		// run), and the SVG writer then emits no d attribute.
		if val.IsNullish() {
			it.SetPath(Path{})
		} else {
			it.SetPath(P(strOf(w, val)))
		}
	case "scaleX":
		it.pathW().ScaleX = numOf(val)
	case "scaleY":
		it.pathW().ScaleY = numOf(val)
	case "interpolate":
		it.lineW().Interpolate = strOfTruthy(w, val)
	case "tension":
		it.lineW().Tension = numOf(val)
	case "orient":
		it.lineW().Orient = strOfTruthy(w, val)
		it.keepRawOrient(val)
	case "defined":
		it.Defined = FalseOf(val)
		it.setRawTri(k, val)
	case "text":
		it.textW().Text = val
	case "font":
		// An array (Vega-Lite writes [] for a null title font) is truthy: the
		// attribute is its text, with no sans-serif default (see Raw).
		it.textW().Font = strOf(w, val)
	case "fontSize":
		it.textW().FontSize = numOf(val)
	case "fontWeight":
		it.textW().FontWeight = strOf(w, val)
	case "fontStyle":
		it.textW().FontStyle = strOf(w, val)
	case "fontVariant":
		it.textW().FontVariant = strOf(w, val)
	case "dx":
		it.textW().Dx = numOf(val)
	case "dy":
		it.textW().Dy = numOf(val)
	case "angle":
		it.Angle = numOf(val)
	case "radius":
		it.textW().Radius = numOf(val)
	case "theta":
		it.textW().Theta = numOf(val)
	case "limit":
		it.textW().Limit = numOf(val)
	case "lineBreak":
		it.textW().LineBreak = strOf(w, val)
	case "lineHeight":
		it.textW().LineHeight = numOf(val)
	case "ellipsis":
		it.textW().Ellipsis = strOf(w, val)
	case "dir":
		it.textW().Dir = strOf(w, val)
	case "url":
		it.imageW().URL = strOf(w, val)
	case "aspect":
		it.imageW().Aspect = FalseOf(val)
		it.setRawTri(k, val)
	case "smooth":
		it.imageW().Smooth = FalseOf(val)
		it.setRawTri(k, val)
	case "cursor":
		it.linkW().Cursor = strOf(w, val)
	case "href":
		// The renderer sanitizes the href with vega-loader, whose first step
		// is uri.replace(...): only a string can be a link, any other value
		// (a number, a date) rejects and renders none.
		it.setRawStr(k, val)
		it.linkW().Href = ""
		if val.IsStr() {
			it.linkW().Href = val.StrValue()
		}
	case "tooltip":
		it.linkW().Tooltip = val
	case "description":
		it.Description = strOf(w, val)
	case "aria":
		it.Aria = FalseOf(val)
		it.setRawTri(k, val)
	case "ariaRole":
		it.AriaRole = strOf(w, val)
	case "ariaRoleDescription":
		it.AriaRoleDescription = strOf(w, val)
	case "zindex":
		it.Zindex = numOf(val)
	case "datum":
		it.Datum = val
	case "clip":
		switch {
		case l != nil && val.IsObj():
			// A recorded scene carries a clip path generator as an object.
			fn, err := pathFuncFromValue(w, val)
			if err != nil {
				return true, err
			}
			it.Clip, it.ClipPath = Yes, fn
		case shapeFuncOf(val) != nil:
			// isFunction(clip): the path is clip(null).
			fn := shapeFuncOf(val)
			it.Clip, it.ClipPath = Yes, func(ctx PathContext) string { return fn(ctx, nil) }
		default:
			// Any other value clips to the group's box when truthy.
			it.Clip, it.ClipPath = triOf(val), nil
		}
	case "noBound":
		it.NoBound = val.IsTruthy()
	case "bounds", "rbounds", "mark", "group", "exit":
		// Recomputed, never read.
	default:
		if it.Extra == nil {
			it.Extra = jsval.NewObject(0)
		}
		it.Extra.Set(k, val)
		return false, nil
	}
	return true, nil
}

// Get reads an item property by its Vega name as a dynamic value: numbers as
// numbers (unset as undefined), colours as strings, gradients as objects,
// booleans as booleans, and unmodelled names from Extra. Undefined for unknown
// or unset properties. Group items answer "items" with their child marks'
// count only through Items; Get does not expose structure.
func (it *Item) Get(key string) jsval.Value {
	// A numeric property given a word reads back as that word.
	if v, ok := it.Raw[key]; ok {
		return v
	}
	num := func(n Num) jsval.Value {
		if !n.Set() {
			return jsval.Undefined
		}
		return jsval.Num(n.Val())
	}
	str := func(s string) jsval.Value {
		if s == "" {
			return jsval.Undefined
		}
		return jsval.Str(s)
	}
	tri := func(t Tri) jsval.Value {
		if t == Unset {
			return jsval.Undefined
		}
		return jsval.Bool(t == Yes)
	}
	paint := func(p Paint) jsval.Value {
		switch {
		case p.IsNull():
			return jsval.Null
		case p.Gradient() != nil:
			return gradientValue(p.Gradient())
		case p.Present():
			return jsval.Str(p.Str())
		}
		return jsval.Undefined
	}
	switch key {
	case "x":
		return num(it.X)
	case "y":
		return num(it.Y)
	case "x2":
		return num(it.X2)
	case "y2":
		return num(it.Y2)
	case "width":
		return num(it.Width)
	case "height":
		return num(it.Height)
	case "align":
		return str(it.Align)
	case "baseline":
		return str(it.Baseline)
	case "fill":
		return paint(it.Fill)
	case "stroke":
		return paint(it.Stroke)
	case "opacity":
		return num(it.Opacity)
	case "fillOpacity":
		return num(it.FillOpacity)
	case "strokeOpacity":
		return num(it.StrokeOpacity)
	case "strokeWidth":
		return num(it.StrokeWidth)
	case "strokeCap":
		return str(it.StrokeCap())
	case "strokeJoin":
		return str(it.StrokeJoin())
	case "strokeMiterLimit":
		return num(it.StrokeMiterLimit())
	case "strokeDash":
		return it.StrokeDash()
	case "strokeDashOffset":
		return num(it.StrokeDashOffset())
	case "strokeForeground":
		return tri(it.StrokeForeground())
	case "strokeOffset":
		return num(it.StrokeOffset())
	case "blend":
		return str(it.Blend())
	case "cornerRadius":
		return num(it.CornerRadius())
	case "cornerRadiusTopLeft":
		return num(it.CornerRadiusTopLeft())
	case "cornerRadiusTopRight":
		return num(it.CornerRadiusTopRight())
	case "cornerRadiusBottomLeft":
		return num(it.CornerRadiusBottomLeft())
	case "cornerRadiusBottomRight":
		return num(it.CornerRadiusBottomRight())
	case "startAngle":
		return num(it.StartAngle())
	case "endAngle":
		return num(it.EndAngle())
	case "padAngle":
		return num(it.PadAngle())
	case "innerRadius":
		return num(it.InnerRadius())
	case "outerRadius":
		return num(it.OuterRadius())
	case "shape":
		return str(it.Shape.Name)
	case "size":
		return num(it.Size)
	case "path":
		if !it.Path().Set {
			return jsval.Undefined
		}
		return jsval.Str(it.Path().D)
	case "scaleX":
		return num(it.ScaleX())
	case "scaleY":
		return num(it.ScaleY())
	case "interpolate":
		return str(it.Interpolate())
	case "tension":
		return num(it.Tension())
	case "orient":
		return str(it.Orient())
	case "defined":
		return tri(it.Defined)
	case "text":
		return it.Text()
	case "font":
		return str(it.Font())
	case "fontSize":
		return num(it.FontSize())
	case "fontWeight":
		return str(it.FontWeight())
	case "fontStyle":
		return str(it.FontStyle())
	case "fontVariant":
		return str(it.FontVariant())
	case "dx":
		return num(it.Dx())
	case "dy":
		return num(it.Dy())
	case "angle":
		return num(it.Angle)
	case "radius":
		return num(it.Radius())
	case "theta":
		return num(it.Theta())
	case "limit":
		return num(it.Limit())
	case "lineBreak":
		return str(it.LineBreak())
	case "lineHeight":
		return num(it.LineHeight())
	case "ellipsis":
		return str(it.Ellipsis())
	case "dir":
		return str(it.Dir())
	case "url":
		return str(it.URL())
	case "aspect":
		return tri(it.Aspect())
	case "smooth":
		return tri(it.Smooth())
	case "cursor":
		return str(it.Cursor())
	case "href":
		return str(it.Href())
	case "tooltip":
		return it.Tooltip()
	case "description":
		return str(it.Description)
	case "aria":
		return tri(it.Aria)
	case "ariaRole":
		return str(it.AriaRole)
	case "ariaRoleDescription":
		return str(it.AriaRoleDescription)
	case "zindex":
		return num(it.Zindex)
	case "datum":
		return it.Datum
	case "clip":
		return tri(it.Clip)
	case "noBound":
		if it.NoBound {
			return jsval.True
		}
		return jsval.Undefined
	}
	if it.Extra != nil {
		return it.Extra.Lookup(key)
	}
	return jsval.Undefined
}

func gradientValue(g *Gradient) jsval.Value {
	o := jsval.NewObject(8)
	kind := "linear"
	if g.Radial {
		kind = "radial"
	}
	o.Set("gradient", jsval.Str(kind))
	if g.ID != "" {
		o.Set("id", jsval.Str(g.ID))
	}
	for _, c := range []struct {
		k string
		n Num
	}{{"x1", g.X1}, {"y1", g.Y1}, {"x2", g.X2}, {"y2", g.Y2}, {"r1", g.R1}, {"r2", g.R2}} {
		if c.n.Set() {
			o.Set(c.k, jsval.Num(c.n.Val()))
		}
	}
	stops := make([]jsval.Value, len(g.Stops))
	for i, s := range g.Stops {
		stops[i] = jsval.Obj(jsval.ObjectOf("offset", jsval.Num(s.Offset), "color", jsval.Str(s.Color)))
	}
	o.Set("stops", jsval.Arr(stops))
	return jsval.Obj(o)
}

// shapeFuncOf is the shape generator a function value carries (a geoShape
// or pathShape result), or nil.
func shapeFuncOf(v jsval.Value) ShapeFunc {
	impl, _ := jsval.FunctionImpl(v)
	f, _ := impl.(ShapeFunc)
	return f
}

// rawNumeric lists the numeric properties: every one keeps the value it was
// given when that is not a number (see Item.Raw), since upstream stores it as
// given and every reader (a template string, `+`, a comparison, `!= null`,
// an expression's item.x) sees that value.
func rawNumeric(k string) bool {
	switch k {
	case "x", "y", "x2", "y2", "width", "height", "opacity", "fillOpacity", "strokeOpacity",
		"strokeWidth", "strokeMiterLimit", "strokeDashOffset", "strokeOffset", "cornerRadius",
		"cornerRadiusTopLeft", "cornerRadiusTopRight", "cornerRadiusBottomLeft",
		"cornerRadiusBottomRight", "startAngle", "endAngle", "padAngle", "innerRadius",
		"outerRadius", "size", "scaleX", "scaleY", "tension", "fontSize", "dx", "dy", "angle",
		"radius", "theta", "limit", "lineHeight", "zindex":
		return true
	}
	return false
}

// rawString lists the string properties: every one keeps the value it was
// given when that is not a non-empty string (see Item.Raw). Upstream compares
// some strictly (`baseline === 'top'` fails for ['top']), writes any value but
// null and undefined to the SVG (an empty font-weight is an attribute), and
// expressions read them back as given.
func rawString(k string) bool {
	switch k {
	case "fill", "stroke", "align", "baseline", "strokeCap", "strokeJoin", "blend", "shape", "interpolate", "orient",
		"font", "fontWeight", "fontStyle", "fontVariant", "lineBreak", "ellipsis", "dir", "url",
		"cursor", "description", "ariaRole", "ariaRoleDescription":
		return true
	}
	return false
}

// setRawStr records the value of a string property when it is not a
// non-empty string, and forgets an earlier one when it is.
func (it *Item) setRawStr(k string, val jsval.Value) {
	if val.IsStr() && val.StrValue() != "" {
		if it.Raw != nil {
			delete(it.Raw, k)
		}
		return
	}
	if val.IsUndefined() {
		delete(it.Raw, k)
		return
	}
	if it.Raw == nil {
		it.Raw = make(map[string]jsval.Value, 1)
	}
	it.Raw[k] = val
}

// StrictStr is the value of a string property for a strict comparison
// (`item.baseline === 'top'`): the string, or "" when the property holds
// anything but a non-empty string, which equals none of the words upstream
// compares with.
func (it *Item) StrictStr(prop, s string) string {
	if it.Raw != nil {
		if _, ok := it.Raw[prop]; ok {
			return ""
		}
	}
	return s
}

// TruthyStr is a string property read for its truthiness (`item.ellipsis ||
// ...`, `item.fontStyle ? ... : ”`): its String when truthy, else "".
func (it *Item) TruthyStr(prop, s string) string {
	if it.Raw != nil {
		if v, ok := it.Raw[prop]; ok && !v.IsTruthy() {
			return ""
		}
	}
	return s
}

// StrTruthy is the truthiness of a string property as it was given: a value
// that prints empty ([]) is truthy where its text is not.
func (it *Item) StrTruthy(prop, s string) bool {
	if it.Raw != nil {
		if v, ok := it.Raw[prop]; ok {
			return v.IsTruthy()
		}
	}
	return s != ""
}

// StyleStr is a string property as the SVG renderer writes it (`if (value
// != null) s[name] = value`): its String, and false only for null and
// undefined, so an empty string is written.
func (it *Item) StyleStr(prop, s string) (string, bool) {
	if it.Raw != nil {
		if v, ok := it.Raw[prop]; ok {
			return s, !v.IsNullish()
		}
	}
	return s, s != ""
}

// StrictNum reports `item[prop] === x` for a numeric property: false when it
// holds anything but a number.
func (it *Item) StrictNum(prop string, n Num, x float64) bool {
	if it.Raw != nil {
		if _, ok := it.Raw[prop]; ok {
			return false
		}
	}
	return n.Set() && n.Val() == x
}

// truthyProp is the truthiness of a numeric property as it was given: a word
// is truthy where the number it converts to (NaN) is not.
func (it *Item) truthyProp(prop string, n Num) bool {
	if v, ok := it.Raw[prop]; ok {
		return v.IsTruthy()
	}
	return n.Truthy()
}

// orZeroProp is `value(item[prop], fallback) || 0` followed by the unary plus
// a path generator applies: given prop wins over the fallback unless unset.
func (it *Item) orZeroProp(prop string, n Num, fallbackProp string, fallback Num) float64 {
	if !n.Set() {
		prop, n = fallbackProp, fallback
	}
	if v, ok := it.Raw[prop]; ok {
		if !v.IsTruthy() {
			return 0
		}
		return jsval.ToNumber(v)
	}
	return n.Zero()
}

func (it *Item) setRaw(k string, val jsval.Value) {
	switch val.Kind() {
	case jsval.KindObj:
		if val.Get("$num").IsStr() {
			// a non-finite number as a recording carries it (see numOf)
			delete(it.Raw, k)
			return
		}
		fallthrough
	case jsval.KindStr, jsval.KindBool, jsval.KindArr, jsval.KindNull, jsval.KindTimestamp:
		// a Date keeps its object (+ concatenates its String); null too: it is unset like undefined for `!= null`, but
		// arithmetic and comparisons read it as 0 (`o.y2 - o.y`).
		if it.Raw == nil {
			it.Raw = make(map[string]jsval.Value, 1)
		}
		it.Raw[k] = val
	default:
		if it.Raw != nil {
			delete(it.Raw, k)
		}
	}
}

// setRawTri keeps the value given to a boolean property when it was not a
// boolean, so that reading the property back (an expression's item.defined)
// gives the value as upstream stores it, not the Tri it was reduced to: null
// included, which is Unset like undefined but reads back as null.
func (it *Item) setRawTri(k string, val jsval.Value) {
	if val.IsUndefined() || val.IsBool() {
		delete(it.Raw, k)
		return
	}
	if it.Raw == nil {
		it.Raw = make(map[string]jsval.Value, 1)
	}
	it.Raw[k] = val
}

// ForgetRaw drops the value a property was given, for a caller that assigns
// the typed field itself (the layout's item.x = v), which replaces it.
func (it *Item) ForgetRaw(prop string) {
	if it.Raw != nil {
		delete(it.Raw, prop)
	}
}

// RawValue returns the non-numeric value a numeric property was given, if any
// (see Item.Raw).
func (it *Item) RawValue(prop string) (jsval.Value, bool) {
	v, ok := it.Raw[prop]
	return v, ok
}

// OrZero is upstream's `item[prop] || 0` for a size property (width, height):
// a word the property was given is truthy and reads as the number it
// converts to, NaN for most, where a numeric NaN reads as 0.
func (it *Item) OrZero(prop string) float64 {
	if v, ok := it.Raw[prop]; ok {
		if !v.IsTruthy() {
			return 0
		}
		return jsval.ToNumber(v)
	}
	switch prop {
	case "x":
		return it.X.Zero()
	case "y":
		return it.Y.Zero()
	case "width":
		return it.Width.Zero()
	case "height":
		return it.Height.Zero()
	}
	return 0
}

// PosValue is `item[prop] || 0` (prop is "x" or "y") as a JavaScript value: the
// word the property was given, else the number. A template string prints it
// into a transform as it is, so the SVG writer escapes a word (it is spec or
// data text).
func (it *Item) PosValue(prop string) jsval.Value {
	if v, ok := it.Raw[prop]; ok {
		if !v.IsTruthy() {
			return jsval.Num(0)
		}
		return v
	}
	return jsval.Num(it.OrZero(prop))
}

// AngleTruthy is upstream's `if (item.angle)`: the truthiness of the value as
// given, so a word turns a mark (by NaN) where a zero does not.
func (it *Item) AngleTruthy() bool {
	if v, ok := it.Raw["angle"]; ok {
		return v.IsTruthy()
	}
	return it.Angle.Truthy()
}

// isWord reports a numeric property given a word (a truthy non-numeric
// string, object or array).
func (it *Item) isWord(prop string) bool {
	v, ok := it.Raw[prop]
	if !ok || !v.IsTruthy() {
		return false
	}
	f := jsval.ToNumber(v)
	return f != f
}
