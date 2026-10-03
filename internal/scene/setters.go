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
			*f(it) = strOf(val)
			return nil
		}
	}
	if f, ok := triFields[name]; ok {
		return func(it *Item, val jsval.Value) error {
			*f(it) = triOf(val)
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
			it.Fill = (*jsonLoader)(nil).paint(val)
			return nil
		}
	case "stroke":
		return func(it *Item, val jsval.Value) error {
			it.Stroke = (*jsonLoader)(nil).paint(val)
			return nil
		}
	case "interpolate":
		return func(it *Item, val jsval.Value) error {
			it.Interpolate = strOfTruthy(val)
			return nil
		}
	case "orient":
		return func(it *Item, val jsval.Value) error {
			it.Orient = strOfTruthy(val)
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
	"strokeMiterLimit":        func(it *Item) *Num { return &it.StrokeMiterLimit },
	"strokeDashOffset":        func(it *Item) *Num { return &it.StrokeDashOffset },
	"strokeOffset":            func(it *Item) *Num { return &it.StrokeOffset },
	"cornerRadius":            func(it *Item) *Num { return &it.CornerRadius },
	"cornerRadiusTopLeft":     func(it *Item) *Num { return &it.CornerRadiusTopLeft },
	"cornerRadiusTopRight":    func(it *Item) *Num { return &it.CornerRadiusTopRight },
	"cornerRadiusBottomLeft":  func(it *Item) *Num { return &it.CornerRadiusBottomLeft },
	"cornerRadiusBottomRight": func(it *Item) *Num { return &it.CornerRadiusBottomRight },
	"startAngle":              func(it *Item) *Num { return &it.StartAngle },
	"endAngle":                func(it *Item) *Num { return &it.EndAngle },
	"padAngle":                func(it *Item) *Num { return &it.PadAngle },
	"innerRadius":             func(it *Item) *Num { return &it.InnerRadius },
	"outerRadius":             func(it *Item) *Num { return &it.OuterRadius },
	"size":                    func(it *Item) *Num { return &it.Size },
	"scaleX":                  func(it *Item) *Num { return &it.ScaleX },
	"scaleY":                  func(it *Item) *Num { return &it.ScaleY },
	"tension":                 func(it *Item) *Num { return &it.Tension },
	"fontSize":                func(it *Item) *Num { return &it.FontSize },
	"dx":                      func(it *Item) *Num { return &it.Dx },
	"dy":                      func(it *Item) *Num { return &it.Dy },
	"angle":                   func(it *Item) *Num { return &it.Angle },
	"radius":                  func(it *Item) *Num { return &it.Radius },
	"theta":                   func(it *Item) *Num { return &it.Theta },
	"limit":                   func(it *Item) *Num { return &it.Limit },
	"lineHeight":              func(it *Item) *Num { return &it.LineHeight },
	"zindex":                  func(it *Item) *Num { return &it.Zindex },
}

var strFields = map[string]func(*Item) *string{
	"align":               func(it *Item) *string { return &it.Align },
	"baseline":            func(it *Item) *string { return &it.Baseline },
	"strokeCap":           func(it *Item) *string { return &it.StrokeCap },
	"strokeJoin":          func(it *Item) *string { return &it.StrokeJoin },
	"blend":               func(it *Item) *string { return &it.Blend },
	"fontWeight":          func(it *Item) *string { return &it.FontWeight },
	"fontStyle":           func(it *Item) *string { return &it.FontStyle },
	"fontVariant":         func(it *Item) *string { return &it.FontVariant },
	"lineBreak":           func(it *Item) *string { return &it.LineBreak },
	"ellipsis":            func(it *Item) *string { return &it.Ellipsis },
	"dir":                 func(it *Item) *string { return &it.Dir },
	"url":                 func(it *Item) *string { return &it.URL },
	"cursor":              func(it *Item) *string { return &it.Cursor },
	"description":         func(it *Item) *string { return &it.Description },
	"ariaRole":            func(it *Item) *string { return &it.AriaRole },
	"ariaRoleDescription": func(it *Item) *string { return &it.AriaRoleDescription },
}

var triFields = map[string]func(*Item) *Tri{
	"strokeForeground": func(it *Item) *Tri { return &it.StrokeForeground },
	"defined":          func(it *Item) *Tri { return &it.Defined },
	"aspect":           func(it *Item) *Tri { return &it.Aspect },
	"smooth":           func(it *Item) *Tri { return &it.Smooth },
	"aria":             func(it *Item) *Tri { return &it.Aria },
}

var valFields = map[string]func(*Item) *jsval.Value{
	"strokeDash": func(it *Item) *jsval.Value { return &it.StrokeDash },
	"text":       func(it *Item) *jsval.Value { return &it.Text },
	"tooltip":    func(it *Item) *jsval.Value { return &it.Tooltip },
	"datum":      func(it *Item) *jsval.Value { return &it.Datum },
}
