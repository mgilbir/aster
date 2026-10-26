package scene

import "github.com/mgilbir/aster/internal/jsval"

// Setter assigns one property of an item from a value, with the effect of
// Item.Set for the name the Setter was made for. An encoder sets the same few
// properties on every item, so it resolves each name once here; Set matches
// the name against the whole property list on every call.
type Setter func(it *Item, val jsval.Value) error

// SetterFor returns the Setter of a property name. Properties it has no direct
// form for (the ones that are not a plain copy: path, shape, clip, font, href,
// and names the item does not model) go through Item.Set.
func SetterFor(name string) Setter {
	if f, ok := numFields[name]; ok {
		if rawNumeric(name) {
			return func(it *Item, val jsval.Value) error {
				it.setRaw(name, val)
				*f(it) = numOf(val)
				return nil
			}
		}
		return func(it *Item, val jsval.Value) error {
			*f(it) = numOf(val)
			return nil
		}
	}
	if f, ok := strFields[name]; ok {
		return func(it *Item, val jsval.Value) error {
			it.setRawStr(name, val)
			*f(it) = strOf(it.Walker(), val)
			return nil
		}
	}
	if f, ok := triFields[name]; ok {
		conv := FalseOf
		if name == "strokeForeground" {
			conv = triOf
		}
		return func(it *Item, val jsval.Value) error {
			*f(it) = conv(val)
			it.setRawTri(name, val)
			return nil
		}
	}
	if f, ok := valFields[name]; ok {
		return func(it *Item, val jsval.Value) error {
			*f(it) = val
			return nil
		}
	}
	switch name {
	case "fill":
		return func(it *Item, val jsval.Value) error {
			it.setRawStr("fill", val)
			it.Fill = paintOf(it.Walker(), val)
			return nil
		}
	case "stroke":
		return func(it *Item, val jsval.Value) error {
			it.setRawStr("stroke", val)
			it.Stroke = paintOf(it.Walker(), val)
			return nil
		}
	case "interpolate":
		return func(it *Item, val jsval.Value) error {
			it.setRawStr("interpolate", val)
			it.lineW().Interpolate = strOfTruthy(it.Walker(), val)
			return nil
		}
	case "orient":
		return func(it *Item, val jsval.Value) error {
			it.setRawStr("orient", val)
			it.lineW().Orient = strOfTruthy(it.Walker(), val)
			it.keepRawOrient(val)
			return nil
		}
	}
	return func(it *Item, val jsval.Value) error {
		_, err := it.Set(name, val)
		return err
	}
}

// The properties that are a plain copy of the value, by type: what setProp
// does for each is the assignment of numOf, strOf, triOf or the value itself.
var numFields = map[string]func(*Item) *Num{
	"x":                       func(it *Item) *Num { return &it.X },
	"y":                       func(it *Item) *Num { return &it.Y },
	"x2":                      func(it *Item) *Num { return &it.X2 },
	"y2":                      func(it *Item) *Num { return &it.Y2 },
	"width":                   func(it *Item) *Num { return &it.Width },
	"height":                  func(it *Item) *Num { return &it.Height },
	"opacity":                 func(it *Item) *Num { return &it.Opacity },
	"fillOpacity":             func(it *Item) *Num { return &it.FillOpacity },
	"strokeOpacity":           func(it *Item) *Num { return &it.StrokeOpacity },
	"strokeWidth":             func(it *Item) *Num { return &it.StrokeWidth },
	"strokeMiterLimit":        func(it *Item) *Num { return &it.strokeW().StrokeMiterLimit },
	"strokeDashOffset":        func(it *Item) *Num { return &it.strokeW().StrokeDashOffset },
	"strokeOffset":            func(it *Item) *Num { return &it.strokeW().StrokeOffset },
	"cornerRadius":            func(it *Item) *Num { return &it.geomW().CornerRadius },
	"cornerRadiusTopLeft":     func(it *Item) *Num { return &it.geomW().CornerRadiusTopLeft },
	"cornerRadiusTopRight":    func(it *Item) *Num { return &it.geomW().CornerRadiusTopRight },
	"cornerRadiusBottomLeft":  func(it *Item) *Num { return &it.geomW().CornerRadiusBottomLeft },
	"cornerRadiusBottomRight": func(it *Item) *Num { return &it.geomW().CornerRadiusBottomRight },
	"startAngle":              func(it *Item) *Num { return &it.geomW().StartAngle },
	"endAngle":                func(it *Item) *Num { return &it.geomW().EndAngle },
	"padAngle":                func(it *Item) *Num { return &it.geomW().PadAngle },
	"innerRadius":             func(it *Item) *Num { return &it.geomW().InnerRadius },
	"outerRadius":             func(it *Item) *Num { return &it.geomW().OuterRadius },
	"size":                    func(it *Item) *Num { return &it.Size },
	"scaleX":                  func(it *Item) *Num { return &it.pathW().ScaleX },
	"scaleY":                  func(it *Item) *Num { return &it.pathW().ScaleY },
	"tension":                 func(it *Item) *Num { return &it.lineW().Tension },
	"fontSize":                func(it *Item) *Num { return &it.textW().FontSize },
	"dx":                      func(it *Item) *Num { return &it.textW().Dx },
	"dy":                      func(it *Item) *Num { return &it.textW().Dy },
	"angle":                   func(it *Item) *Num { return &it.Angle },
	"radius":                  func(it *Item) *Num { return &it.textW().Radius },
	"theta":                   func(it *Item) *Num { return &it.textW().Theta },
	"limit":                   func(it *Item) *Num { return &it.textW().Limit },
	"lineHeight":              func(it *Item) *Num { return &it.textW().LineHeight },
	"zindex":                  func(it *Item) *Num { return &it.Zindex },
}

var strFields = map[string]func(*Item) *string{
	"align":               func(it *Item) *string { return &it.Align },
	"baseline":            func(it *Item) *string { return &it.Baseline },
	"strokeCap":           func(it *Item) *string { return &it.strokeW().StrokeCap },
	"strokeJoin":          func(it *Item) *string { return &it.strokeW().StrokeJoin },
	"blend":               func(it *Item) *string { return &it.strokeW().Blend },
	"fontWeight":          func(it *Item) *string { return &it.textW().FontWeight },
	"fontStyle":           func(it *Item) *string { return &it.textW().FontStyle },
	"fontVariant":         func(it *Item) *string { return &it.textW().FontVariant },
	"lineBreak":           func(it *Item) *string { return &it.textW().LineBreak },
	"ellipsis":            func(it *Item) *string { return &it.textW().Ellipsis },
	"dir":                 func(it *Item) *string { return &it.textW().Dir },
	"url":                 func(it *Item) *string { return &it.imageW().URL },
	"cursor":              func(it *Item) *string { return &it.linkW().Cursor },
	"description":         func(it *Item) *string { return &it.Description },
	"ariaRole":            func(it *Item) *string { return &it.AriaRole },
	"ariaRoleDescription": func(it *Item) *string { return &it.AriaRoleDescription },
}

var triFields = map[string]func(*Item) *Tri{
	"strokeForeground": func(it *Item) *Tri { return &it.strokeW().StrokeForeground },
	"defined":          func(it *Item) *Tri { return &it.Defined },
	"aspect":           func(it *Item) *Tri { return &it.imageW().Aspect },
	"smooth":           func(it *Item) *Tri { return &it.imageW().Smooth },
	"aria":             func(it *Item) *Tri { return &it.Aria },
}

// RefreshArrays reads again the properties the item holds an array for (in
// Raw), into the fields that hold what the array read when it was set.
// Upstream keeps the value an encoder gives and reads it where it draws, and
// a continuous scale whose range holds arrays answers every call with one
// array, updated in place (d3's genericArray): every item given it draws its
// last value. The SVG renderer calls this before it reads an item. An object
// reads the same whatever it holds ("[object Object]", NaN), and a boolean
// property is true for any array: those are left as they are.
func (it *Item) RefreshArrays() {
	for k, v := range it.Raw {
		if !v.IsArr() {
			continue
		}
		if f, ok := numFields[k]; ok {
			*f(it) = numOf(v)
			continue
		}
		w := it.Walker()
		if f, ok := strFields[k]; ok {
			*f(it) = strOf(w, v)
			continue
		}
		switch k {
		case "fill":
			it.Fill = paintOf(w, v)
		case "stroke":
			it.Stroke = paintOf(w, v)
		case "interpolate":
			it.lineW().Interpolate = strOfTruthy(w, v)
		case "orient":
			it.lineW().Orient = strOfTruthy(w, v)
		case "shape":
			// an array is never a shape generator (setProp)
			it.Shape.Name = strOf(w, v)
		}
	}
}

var valFields = map[string]func(*Item) *jsval.Value{
	"strokeDash": func(it *Item) *jsval.Value { return &it.strokeW().StrokeDash },
	"text":       func(it *Item) *jsval.Value { return &it.textW().Text },
	"tooltip":    func(it *Item) *jsval.Value { return &it.linkW().Tooltip },
	"datum":      func(it *Item) *jsval.Value { return &it.Datum },
}
