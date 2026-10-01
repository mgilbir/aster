package vega

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// writeSignal is View.signal(name, value) followed by a run.
func writeSignal(t testing.TB, v *runView, name string, val jsval.Value) {
	t.Helper()
	n := v.root.signals[name]
	if n == nil {
		t.Fatalf("no signal %q", name)
	}
	v.g.update(n, val, false, false)
	if err := v.run(); err != nil {
		t.Fatal(err)
	}
}

func legendSize(t testing.TB, v *runView) (w, h float64) {
	t.Helper()
	for _, m := range v.marks {
		if m != nil && m.Role == "legend" && len(m.Items) > 0 {
			return m.Items[0].Width.Val(), m.Items[0].Height.Val()
		}
	}
	t.Fatal("no legend")
	return
}

// A guide's bounds change when its content does, and the layout of the group
// holding it has to follow: Bound reflows the pulse of an axis, legend or
// title group (vega-view-transforms Bound), which is what touches the
// enclosing ViewLayout again. A legend whose title was written away must
// shrink to what a fresh render with that title gives.
func TestLegendSizeFollowsTitleWrite(t *testing.T) {
	spec := func(title string) jsval.Value {
		return mustJSON(t, `{"width":200,"height":120,"padding":5,
			"data":[{"name":"t","values":[{"k":"a"},{"k":"b"}]}],
			"signals":[{"name":"title","value":`+title+`}],
			"scales":[{"name":"c","type":"ordinal","domain":{"data":"t","field":"k"},"range":"category"}],
			"legends":[{"fill":"c","title":{"signal":"title"}}]}`)
	}
	for _, to := range []string{`null`, `""`, `["two","lines"]`} {
		v := debugView(t, spec(`"A rather long legend title"`))
		writeSignal(t, v, "title", mustJSON(t, to))
		gw, gh := legendSize(t, v)
		ww, wh := legendSize(t, debugView(t, spec(to)))
		if gw != ww || gh != wh {
			t.Errorf("title %s: legend %vx%v after the write, %vx%v drawn fresh", to, gw, gh, ww, wh)
		}
	}
}

// Collect keeps the tuples it holds in place and appends those that arrive
// (SortedList.data without a comparator), so a row a filter lets through again
// comes last in the scale's domain, not back where it was.
func TestFilterParameterWriteAppendsReadmittedRows(t *testing.T) {
	spec := mustJSON(t, `{"width":200,"height":120,
		"signals":[{"name":"cutoff","value":40}],
		"data":[{"name":"t","values":[{"k":"a","v":28},{"k":"b","v":55},{"k":"c","v":43},{"k":"d","v":91}],
			"transform":[{"type":"filter","expr":"datum.v > cutoff"}]}],
		"scales":[{"name":"x","type":"band","domain":{"data":"t","field":"k"},"range":"width"}]}`)
	v := debugView(t, spec)
	domain := func() string {
		s, _ := v.root.scaleNode("x").value.(scale.Scale)
		var out []string
		for _, d := range s.Domain() {
			out = append(out, d.AsString())
		}
		return strings.Join(out, ",")
	}
	if got := domain(); got != "b,c,d" {
		t.Fatalf("domain %s, want b,c,d", got)
	}
	writeSignal(t, v, "cutoff", jsval.Num(0))
	if got := domain(); got != "b,c,d,a" {
		t.Errorf("domain after the write %s, want b,c,d,a", got)
	}
	writeSignal(t, v, "cutoff", jsval.Num(50))
	if got := domain(); got != "b,d" {
		t.Errorf("domain after the second write %s, want b,d", got)
	}
}

func TestCollectIncremental(t *testing.T) {
	mk := func(names ...string) []jsval.Value {
		out := make([]jsval.Value, len(names))
		for i, n := range names {
			out[i] = jsval.Obj(jsval.ObjectOf("k", jsval.Str(n)))
		}
		return out
	}
	rows := mk("a", "b", "c", "d")
	names := func(l []jsval.Value) string {
		var s []string
		for _, t := range l {
			s = append(s, t.Get("k").AsString())
		}
		return strings.Join(s, "")
	}
	list := collectIncremental(nil, rows[1:])
	if got := names(list); got != "bcd" {
		t.Fatalf("first set %s", got)
	}
	list = collectIncremental(list, rows)
	if got := names(list); got != "bcda" {
		t.Errorf("re-admitted row: %s, want bcda", got)
	}
	list = collectIncremental(list, []jsval.Value{rows[3], rows[1]})
	if got := names(list); got != "bd" {
		t.Errorf("rows dropped: %s, want bd", got)
	}
	// A repeated tuple is not an identity this can follow: the set is taken as it comes.
	list = collectIncremental(list, []jsval.Value{rows[2], rows[2], rows[0]})
	if got := names(list); got != "cca" {
		t.Errorf("repeated tuple: %s, want cca", got)
	}
}

// A width written as a word is truthy, so ViewLayout reads it as NaN rather
// than as 0 (`group.width || 0`), and the view is NaN wide.
func TestViewWidthWrittenAsWord(t *testing.T) {
	spec := mustJSON(t, `{"signals":[{"name":"w","value":200}],"width":{"signal":"w"},"height":50,"padding":5,
		"scales":[{"name":"x","type":"band","domain":["a","b"],"range":"width"}],
		"axes":[{"orient":"bottom","scale":"x"}]}`)
	res, err := Render(context.Background(), spec, Options{
		SignalWrites: []SignalWrite{{Name: "w", Value: jsval.Str("wide")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(res.Width) {
		t.Errorf("view is %v wide, want NaN", res.Width)
	}
}
