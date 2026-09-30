package vegalite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Large-specification benchmarks: compile time must grow about linearly with
// the number of params, layers, concat views and transforms.

func bigEncoding() Value {
	return mkv("x", mkv("field", "a", "type", "quantitative"), "y", mkv("field", "b", "type", "quantitative"))
}

func bigSpec(kind string, n int) Value {
	data := mkv("values", arr(mkv("a", 1, "b", 2), mkv("a", 2, "b", 3)))
	switch kind {
	case "params":
		var ps []Value
		for i := 0; i < n; i++ {
			ps = append(ps, mkv("name", fmt.Sprintf("p%d", i), "select", "point"))
		}
		return mkv("data", data, "params", jsval.Arr(ps), "mark", "point", "encoding", bigEncoding())
	case "intervals":
		var ps []Value
		for i := 0; i < n; i++ {
			ps = append(ps, mkv("name", fmt.Sprintf("p%d", i), "select", "interval"))
		}
		return mkv("data", data, "params", jsval.Arr(ps), "mark", "point", "encoding", bigEncoding())
	case "vars":
		var ps []Value
		for i := 0; i < n; i++ {
			ps = append(ps, mkv("name", fmt.Sprintf("v%d", i), "value", i))
		}
		return mkv("data", data, "params", jsval.Arr(ps), "mark", "point", "encoding", bigEncoding())
	case "layer":
		var ls []Value
		for i := 0; i < n; i++ {
			ls = append(ls, mkv("mark", "point", "encoding", bigEncoding()))
		}
		return mkv("data", data, "layer", jsval.Arr(ls))
	case "concat":
		var ls []Value
		for i := 0; i < n; i++ {
			ls = append(ls, mkv("mark", "point", "encoding", bigEncoding()))
		}
		return mkv("data", data, "hconcat", jsval.Arr(ls))
	case "transform":
		var ts []Value
		for i := 0; i < n; i++ {
			ts = append(ts, mkv("calculate", fmt.Sprintf("datum.a + %d", i), "as", fmt.Sprintf("c%d", i)))
		}
		return mkv("data", data, "transform", jsval.Arr(ts), "mark", "point", "encoding", bigEncoding())
	}
	panic(kind)
}

func BenchmarkCompileLarge(b *testing.B) {
	for _, kind := range []string{"params", "intervals", "vars", "layer", "concat", "transform"} {
		for _, n := range []int{250, 1000, 4000} {
			spec := bigSpec(kind, n)
			b.Run(fmt.Sprintf("%s-%d", kind, n), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := Compile(spec, Options{}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestContextBoundsCompile(t *testing.T) {
	spec := bigSpec("intervals", 6000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compile(spec, Options{Context: ctx}); err != context.Canceled {
		t.Fatalf("cancelled context: got %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Compile(spec, Options{Context: ctx})
	if err != context.DeadlineExceeded {
		t.Fatalf("timeout: got %v", err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("compile ran %v past a 20ms deadline", el)
	}
}

// TestLargeSpecsScaleLinearly guards against quadratic behaviour: 4x the
// params may cost far less than 16x the time.
func TestLargeSpecsScaleLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for _, kind := range []string{"params", "layer", "concat", "transform"} {
		run := func(n int) time.Duration {
			spec := bigSpec(kind, n)
			best := time.Hour
			for i := 0; i < 2; i++ {
				start := time.Now()
				if _, err := Compile(spec, Options{}); err != nil {
					t.Fatal(err)
				}
				if el := time.Since(start); el < best {
					best = el
				}
			}
			return best
		}
		small, large := run(500), run(2000)
		if large > 10*small+50*time.Millisecond {
			t.Errorf("%s: 500 took %v but 2000 took %v (super-linear)", kind, small, large)
		}
	}
}
