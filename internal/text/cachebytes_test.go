package text

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// The Measurer outlives renders, and its caches are keyed by what the
// specification and its data choose: a long CJK text under many family names
// fills a rune memo per family list, and long family names fill the CSS cache.
// What stays alive after such renders is bounded (C32).
func TestMeasurerCachesRetainBoundedBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("measures 5 MB of text")
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for r := rune(0x4e00); r < 0x4e00+16000; r++ {
		b.WriteRune(r)
	}
	cjk := b.String()
	long := strings.Repeat("f", 100_000)
	m.MeasureText("warm up", "12px sans-serif")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 100 {
		m.MeasureText(cjk, fmt.Sprintf("12px family%d, sans-serif", i))
		m.MeasureText("x", fmt.Sprintf("12px %s%d", long, i))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	kept := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const bound = 2*(widthCacheBytes+cssCacheBytes+listCacheBytes) + 4<<20
	t.Logf("kept %d MB, bound %d MB", kept>>20, bound>>20)
	if kept > bound {
		t.Errorf("the Measurer keeps %d MB after the renders; its caches are bounded at %d MB", kept>>20, bound>>20)
	}
	runtime.KeepAlive(m)
}
