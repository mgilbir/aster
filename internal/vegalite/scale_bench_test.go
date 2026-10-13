package vegalite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
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
	el := time.Since(start)
	testutil.Timing(t, "compile stops after a 20ms deadline", el, testutil.Scaled(2*time.Second))
	if el > testutil.Scaled(2*time.Second) {
		t.Fatalf("compile ran %v past a 20ms deadline", el)
	}
}

// TestLargeSpecsScaleLinearly guards against quadratic behaviour in
// specifications with many params, layers, views or transforms. Allocated
// bytes are a deterministic measure of the work a compile does, and the
// quadratic behaviour this guards against (copy-on-append of signal lists)
// shows up in them directly: 4x the input may allocate at most 8x (clean,
// 3.8 to 5.1; with copy-on-append planted back, 14). Nothing is timed. The
// race detector's sync.Pool makes allocation vary, so the guard is skipped
// under it.
func TestLargeSpecsScaleLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	if testutil.RaceEnabled {
		t.Skip("allocation varies under the race detector")
	}
	for _, kind := range []string{"params", "layer", "concat", "transform"} {
		t.Run(kind, func(t *testing.T) {
			if small, large := compileAlloc(t, bigSpec(kind, 500)), compileAlloc(t, bigSpec(kind, 2000)); large > 8*small {
				t.Errorf("%s: 500 allocated %d bytes but 2000 allocated %d (super-linear)", kind, small, large)
			}
		})
	}
}
