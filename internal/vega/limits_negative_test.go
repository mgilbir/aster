package vega

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A negative Limits field removes that bound, whichever field it is; zero
// selects the default.
func TestNegativeLimitsRemoveTheBound(t *testing.T) {
	spec := `{"autosize":{"type":"fit","resize":true},"width":200,"height":100,
	  "signals":[{"name":"m","value":1}],
	  "data":[{"name":"t","values":[{"a":1}],"on":[{"trigger":"true","insert":"{a:2}"}],
	    "transform":[{"type":"formula","as":"s","expr":"pad('', 100)"}]},
	    {"name":"u","transform":[{"type":"sequence","start":0,"stop":20}]}],
	  "scales":[{"name":"y","type":"linear","domain":[0,{"signal":"m"}],"range":"height"}],
	  "axes":[{"orient":"left","scale":"y"}],
	  "marks":[{"type":"group","from":{"facet":{"name":"f","data":"u","groupby":"data"}},
	    "marks":[{"type":"rect","from":{"data":"f"}}]}]}`
	lim := Limits{
		MaxRows: -1, MaxVisits: -1, MaxReruns: -1, MaxItems: -1, MaxSubflows: -1,
		MaxOperators: -1, MaxLoadBytes: -1, MaxPoints: -1, MaxCanvasBytes: -1,
		MaxStringBytes: -1, MaxParseBytes: -1, RowBytes: -1,
	}
	writes := []SignalWrite{{Name: "m", Value: jsval.Num(1e6)}}
	if _, err := Render(context.Background(), mustJSON(t, spec), Options{Limits: lim, SignalWrites: writes}); err != nil {
		t.Fatal(err)
	}
}
