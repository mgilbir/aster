package vegalite

import (
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// TestLargeSpecsFinishPromptly guards against accidentally super-linear work on
// wide specifications (many layers, transforms, concat cells).
func TestLargeSpecsFinishPromptly(t *testing.T) {
	unit := func(i int) jsval.Value {
		return mkv("mark", "point", "encoding", mkv(
			"x", mkv("field", "a", "type", "quantitative"),
			"y", mkv("field", "b", "type", "quantitative"),
			"color", mkv("value", "red"),
		))
	}
	build := func(kind string, n int) jsval.Value {
		items := make([]jsval.Value, n)
		for i := range items {
			items[i] = unit(i)
		}
		return jsval.Obj(mk("data", mkv("values", arr(mkv("a", 1, "b", 2))), kind, jsval.Arr(items)))
	}
	// The limits are 2x the CI baseline (unscaled: the larger of the worst
	// without -race and the worst under -race / 5; Heavy gates timings run),
	// floor 1 s, rounded up; slower is a finding to investigate. Baselines:
	// layers 0.46 s, hconcat 0.73 s, vconcat 0.75 s, transforms 0.05 s (below).
	for _, c := range []struct {
		name  string
		spec  jsval.Value
		limit time.Duration
	}{
		{"layers", build("layer", 1500), 1 * time.Second},
		{"hconcat", build("hconcat", 1500), 1500 * time.Millisecond},
		{"vconcat", build("vconcat", 1500), 2 * time.Second},
	} {
		start := time.Now()
		if _, err := Compile(c.spec, Options{}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		d := time.Since(start)
		testutil.Timing(t, "large spec compile "+c.name, d, testutil.Scaled(c.limit))
		if d > testutil.Scaled(c.limit) {
			t.Errorf("%s took %v", c.name, d)
		}
	}
	tx := make([]jsval.Value, 5000)
	for i := range tx {
		tx[i] = mkv("calculate", "datum.a + 1", "as", "c")
	}
	spec := mkv("data", mkv("values", arr(mkv("a", 1))), "transform", jsval.Arr(tx), "mark", "point",
		"encoding", mkv("x", mkv("field", "c", "type", "quantitative")))
	start := time.Now()
	if _, err := Compile(spec, Options{}); err != nil {
		t.Fatal(err)
	}
	d := time.Since(start)
	testutil.Timing(t, "large spec compile transforms", d, testutil.Scaled(1*time.Second)) // CI baseline 0.05 s (0.17 s under -race): floor 1 s
	if d > testutil.Scaled(1*time.Second) {
		t.Errorf("transforms took %v", d)
	}
}
