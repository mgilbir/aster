package text

import (
	"strings"
	"sync"
	"testing"
)

// widthCacheHeld is what the width cache holds, counted as maxWidthBytes does.
func widthCacheHeld(m *Measurer) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for k := range m.widthCache {
		n += len(k.text) + len(k.css) + widthEntryBytes
	}
	return n
}

// The width cache is bounded by the bytes it holds as well as by its entries:
// long texts, which the entry bound alone would let fill it with gigabytes,
// leave it within maxWidthBytes, and a width computed after an eviction is the
// one a fresh Measurer gives.
func TestWidthCacheBytesBounded(t *testing.T) {
	m, _ := New()
	ref, _ := New()
	const font = "12px sans-serif"
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 40 {
				s := strings.Repeat("a", 1<<15) + strings.Repeat("b", w*100+i)
				m.MeasureText(s, font)
				if held := widthCacheHeld(m); held > maxWidthBytes {
					t.Errorf("width cache holds %d bytes, over %d", held, maxWidthBytes)
					return
				}
			}
		}()
	}
	wg.Wait()
	if held := widthCacheHeld(m); m.widthBytes != held || held > maxWidthBytes {
		t.Errorf("widthBytes = %d, the cache holds %d (max %d)", m.widthBytes, held, maxWidthBytes)
	}
	s := strings.Repeat("a", 1<<15) + "b"
	if got, want := m.MeasureText(s, font), ref.MeasureText(s, font); got != want {
		t.Errorf("width after eviction = %v, want %v", got, want)
	}
	// A text larger than the whole cache is measured but not kept.
	big := strings.Repeat("a", maxWidthBytes+1)
	if m.MeasureText(big, font) <= 0 {
		t.Error("oversized text has no width")
	}
	if held := widthCacheHeld(m); held > maxWidthBytes {
		t.Errorf("width cache holds %d bytes after an oversized text", held)
	}
}
