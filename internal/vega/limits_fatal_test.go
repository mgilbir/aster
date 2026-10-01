package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// A resource limit ends the render with an error that says so. Upstream has no
// such limits; were the engine's logged like an exception upstream throws, the
// render would carry on without the guide or transform that hit one and fail,
// if at all, somewhere unrelated (the SVG writer finding the hole it left).
func TestLimitsEndTheRender(t *testing.T) {
	for name, spec := range map[string]string{
		"axis tick count": `{"width":100,"height":50,
		  "scales":[{"name":"x","type":"linear","domain":[0,1],"range":"width"}],
		  "axes":[{"orient":"bottom","scale":"x","tickCount":5e6}]}`,
		"legend entries": `{"width":100,"height":50,
		  "data":[{"name":"t","transform":[{"type":"sequence","start":0,"stop":200000,"as":"k"}]}],
		  "scales":[{"name":"c","type":"ordinal","domain":{"data":"t","field":"k"},"range":"category"}],
		  "legends":[{"fill":"c"}]}`,
		"force iterations": `{"width":100,"height":50,
		  "data":[{"name":"n","values":[{"a":1}],"transform":[{"type":"force","iterations":1e9,"static":true}]}]}`,
		"format width": `{"width":100,"height":50,
		  "marks":[{"type":"text","encode":{"enter":{"text":{"signal":"format(1, '999999999d')"}}}}]}`,
		"sequence length": `{"width":100,"height":50,
		  "signals":[{"name":"s","update":"length(sequence(0, 1e7))"}]}`,
		"expression strings": `{"width":100,"height":50,
		  "signals":[{"name":"s","update":"length(pad('', 1e6) + pad('', 1e6))"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			// a string budget small enough for the last case to spend
			opts := Options{Limits: Limits{MaxStringBytes: 1 << 20}}
			_, err := Render(context.Background(), mustJSON(t, spec), opts)
			if !errors.Is(err, budget.ErrLimit) {
				t.Errorf("err = %v, want the limit error", err)
			}
		})
	}
}
