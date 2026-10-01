package vega

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/scene"
)

func hasWarning(res *Result, sub string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// A legend whose `columns` is not a valid array length makes upstream's
// gridLayout throw at `Array(ncols)`; the exception ends the pulse, so the
// view layout never runs and the chart keeps its size: no legend room is
// reserved, and Render still returns a result.
func TestLegendColumnsNotAnArrayLength(t *testing.T) {
	const tmpl = `{"width":100,"height":80,"padding":5,
	  "data":[{"name":"t","values":[{"g":"a"},{"g":"b"},{"g":"c"}]}],
	  "scales":[{"name":"c","type":"ordinal","domain":{"data":"t","field":"g"},"range":"category"}],
	  "legends":[{"fill":"c","columns":%s}]}`
	ok := renderSpec(t, strings.Replace(tmpl, "%s", "1", 1))
	if ok.Width <= 110 {
		t.Fatalf("a one-column legend should widen the view, width %v", ok.Width)
	}
	for _, cols := range []string{"0.5", "-4", "1e12"} {
		res := renderSpec(t, strings.Replace(tmpl, "%s", cols, 1))
		if res.Width != 110 {
			t.Errorf("columns %s: width %v, want the unadjusted 110", cols, res.Width)
		}
		if !hasWarning(res, "Invalid array length") {
			t.Errorf("columns %s: warnings %q lack the RangeError", cols, res.Warnings)
		}
	}
}

// d3's piecewise interpolator indexes its segments with Math.floor(t * n): a
// position that is NaN (a sequential scale over an infinite extent) calls
// undefined, a TypeError that ends the pulse like any exception.
func TestSequentialScaleOverInfiniteExtent(t *testing.T) {
	res := renderSpec(t, `{"width":100,"height":60,
	  "data":[{"name":"t","values":[{"v":"Infinity"},{"v":"-Infinity"},{"v":1}]}],
	  "scales":[{"name":"c","type":"linear","domain":{"data":"t","field":"v"},"range":{"scheme":"blues"}}],
	  "legends":[{"fill":"c","type":"gradient"}],
	  "marks":[{"type":"rect","from":{"data":"t"},"encode":{"enter":{"width":{"value":5},"height":{"value":5},"fill":{"scale":"c","field":"v"}}}}]}`)
	if !hasWarning(res, "I[i] is not a function") {
		t.Errorf("warnings %q lack the TypeError", res.Warnings)
	}
	if res.Height != 60 { // the pulse stopped before the legend was laid out
		t.Errorf("height %v: the legend must not have been laid out", res.Height)
	}
}

// A scheme range on an ordinal scale takes `count` samples of an
// interpolating scheme, (+_.schemeCount || count || 5); without a count, one
// sample per domain value. quantizeInterpolator samples at i/(count+1).
func TestOrdinalSchemeCount(t *testing.T) {
	fills := func(rng string) []string {
		res := renderSpec(t, `{"width":100,"height":20,
		  "data":[{"name":"t","values":[{"k":"a"},{"k":"b"},{"k":"c"},{"k":"d"}]}],
		  "scales":[{"name":"c","type":"ordinal","domain":{"data":"t","field":"k"},"range":`+rng+`}],
		  "marks":[{"type":"rect","from":{"data":"t"},"encode":{"enter":{"width":{"value":5},"height":{"value":5},"fill":{"scale":"c","field":"k"}}}}]}`)
		var out []string
		for _, m := range res.Scenegraph.Root.Items[0].Items {
			if m.Type == scene.MarkRect {
				for _, it := range m.Items {
					out = append(out, it.Fill.Str())
				}
			}
		}
		return out
	}
	// greys is a continuous scheme: sampled at 1/4, 2/4, 3/4 for count 3, so
	// the fourth domain value wraps around the three colours
	three := fills(`{"scheme":"greys","count":3}`)
	if len(three) != 4 || three[0] != three[3] || three[0] == three[1] || three[1] == three[2] {
		t.Errorf("count 3: fills %v, want three distinct samples cycling", three)
	}
	// without a count the domain size decides: four samples at 1/5 .. 4/5
	four := fills(`{"scheme":"greys"}`)
	if len(four) != 4 || four[0] == four[3] {
		t.Errorf("no count: fills %v, want four distinct samples", four)
	}
	if three[0] == four[0] {
		t.Errorf("a count of 3 and of 4 sample at different positions: %v %v", three, four)
	}
}
