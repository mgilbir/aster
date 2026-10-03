package scene

import (
	"reflect"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A Setter must do what Item.Set does for its name, for every property and
// every kind of value an encoder can produce.
func TestSettersMatchSet(t *testing.T) {
	names := []string{
		"x", "y", "x2", "y2", "width", "height", "align", "baseline", "fill", "stroke",
		"opacity", "fillOpacity", "strokeOpacity", "strokeWidth", "strokeCap", "strokeJoin",
		"strokeMiterLimit", "strokeDash", "strokeDashOffset", "strokeForeground", "strokeOffset",
		"blend", "cornerRadius", "cornerRadiusTopLeft", "cornerRadiusTopRight",
		"cornerRadiusBottomLeft", "cornerRadiusBottomRight", "startAngle", "endAngle", "padAngle",
		"innerRadius", "outerRadius", "shape", "size", "path", "scaleX", "scaleY", "interpolate",
		"tension", "orient", "defined", "text", "font", "fontSize", "fontWeight", "fontStyle",
		"fontVariant", "dx", "dy", "angle", "radius", "theta", "limit", "lineBreak", "lineHeight",
		"ellipsis", "dir", "url", "aspect", "smooth", "cursor", "href", "tooltip", "description",
		"aria", "ariaRole", "ariaRoleDescription", "zindex", "datum", "clip", "noBound",
		"xc", "bounds", "mark", "nonsense",
	}
	values := []jsval.Value{
		jsval.Undefined, jsval.Null, jsval.Num(0), jsval.Num(1.5), jsval.Num(-3), jsval.True, jsval.False,
		jsval.Str(""), jsval.Str("wide"), jsval.Str("3"), jsval.Str("#4c78a8"),
		jsval.ArrOf(jsval.Num(1), jsval.Num(2)), jsval.Arr(nil),
		jsval.Obj(jsval.ObjectOf("$num", jsval.Str("Infinity"))),
		jsval.Obj(jsval.ObjectOf("$num", jsval.Str("NaN"))),
		jsval.Obj(jsval.NewObject(0)),
	}
	for _, name := range names {
		set := SetterFor(name)
		for _, val := range values {
			// Two items with some earlier state, so a clearing assignment shows.
			a, b := &Item{}, &Item{}
			a.Set("x", jsval.Str("start"))
			b.Set("x", jsval.Str("start"))
			a.Set(name, jsval.Str("before"))
			b.Set(name, jsval.Str("before"))
			_, errA := a.Set(name, val)
			errB := set(b, val)
			if (errA == nil) != (errB == nil) {
				t.Fatalf("%s %v: Set error %v, Setter error %v", name, val, errA, errB)
			}
			for _, n := range names {
				if x, y := a.Get(n), b.Get(n); !jsval.Equal(x, y) && !(x.IsNum() && y.IsNum() && x.NumValue() != x.NumValue() && y.NumValue() != y.NumValue()) {
					t.Fatalf("%s=%v: property %s reads %v after Set and %v after the Setter", name, val, n, x, y)
				}
			}
			if !reflect.DeepEqual(a.Raw, b.Raw) {
				t.Fatalf("%s=%v: Raw %v after Set, %v after the Setter", name, val, a.Raw, b.Raw)
			}
			if !reflect.DeepEqual(a.Extra, b.Extra) {
				t.Fatalf("%s=%v: Extra differs", name, val)
			}
			if a.Shape.Name != b.Shape.Name || a.Path().D != b.Path().D || a.Path().Set != b.Path().Set || a.Clip != b.Clip || a.Fill != b.Fill || a.Stroke != b.Stroke {
				t.Fatalf("%s=%v: structural fields differ", name, val)
			}
		}
	}
}
