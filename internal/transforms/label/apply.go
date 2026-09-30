package label

import "github.com/mgilbir/aster/internal/jsval"

// DefaultOutput are the tuple fields written when no `as` is given.
var DefaultOutput = [5]string{"x", "y", "opacity", "align", "baseline"}

// Apply writes placements into each label's Tuple under the field names in
// as, leaving fields undefined where upstream does. Tuples that are not
// objects are skipped.
func Apply(ps []Placement, as [5]string) {
	for i := range ps {
		p := &ps[i]
		obj := p.Label.Tuple.ObjValue()
		if obj == nil {
			continue
		}
		x, y := jsval.Undefined, jsval.Undefined
		if p.HasPos {
			x, y = jsval.Num(p.X), jsval.Num(p.Y)
		}
		align, baseline := jsval.Undefined, jsval.Undefined
		if p.Align != "" {
			align = jsval.Str(p.Align)
		}
		if p.Baseline != "" {
			baseline = jsval.Str(p.Baseline)
		}
		obj.Set(as[0], x)
		obj.Set(as[1], y)
		obj.Set(as[2], jsval.Num(p.Opacity))
		obj.Set(as[3], align)
		obj.Set(as[4], baseline)
	}
}
