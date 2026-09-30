package vegalite

import (
	"testing"
	"time"

	"github.com/mgilbir/aster/purego/internal/jsval"
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
	for _, c := range []struct {
		name string
		spec jsval.Value
	}{
		{"layers", build("layer", 1500)},
		{"hconcat", build("hconcat", 1500)},
		{"vconcat", build("vconcat", 1500)},
	} {
		start := time.Now()
		if _, err := Compile(c.spec, Options{}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d := time.Since(start); d > 20*time.Second {
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
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("transforms took %v", d)
	}
}
