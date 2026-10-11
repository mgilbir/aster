package expr

import (
	"runtime"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// The size estimate of a compiled pattern is at least what the compile
// really holds on the heap, and not wildly more: the caches keeping patterns
// are bounded by it. It also fails when goecma262 stops exposing the length
// of its program the way programBytes reads it.
func TestPatternSize(t *testing.T) {
	for _, src := range []string{"(a{1000}){190}", "[a-z]{3,50}x(\\d+)", "(?:foo|bar|baz){20}", "\\p{L}{50}", "[\\u0100-\\uffff]{100}", "(x{100}){100}"} {
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		p, err := jsval.NewPattern(src, "")
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&b)
		held := int64(b.HeapAlloc) - int64(a.HeapAlloc)
		runtime.KeepAlive(p)
		if patternBytes(p) < held || patternBytes(p) > 4*held+64<<10 {
			t.Errorf("/%s/: estimated %d bytes, the heap holds %d", src, patternBytes(p), held)
		}
	}
	if s := patternBytes(&jsval.Pattern{}); s < 200_000*instructionBytes {
		t.Errorf("a pattern whose program cannot be read must count as the largest: %d", s)
	}
}
