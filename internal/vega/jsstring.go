package vega

import (
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// jsString is JavaScript's String(x). A date prints as Date.prototype.toString
// does, down to the second and in the view's local zone, which is how two dates
// within one second can share a key.
func (v *runView) jsString(x jsval.Value) string {
	if x.IsTimestamp() {
		return format.DateToString(x.NumValue(), v.zone)
	}
	return x.AsString()
}
