package raster

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// The render's caches keyed by what the document controls are bounded by
// their bytes and charged to the ledger (X4): a gradient drawn at 5,000
// opacities makes 5,000 colour tables, 20 MB, of which the cache keeps at
// most maxGradCacheBytes, charged and given back.
func TestRenderCachesBoundedAndCharged(t *testing.T) {
	doc := func(fill string) string {
		var b strings.Builder
		b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="50" height="50"><defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>`)
		for i := range 5000 {
			fmt.Fprintf(&b, `<rect width="50" height="50" fill="%s" fill-opacity="%g"/>`, fill, float64(i+1)/5001)
		}
		b.WriteString(`</svg>`)
		return b.String()
	}
	peak := func(svg string) int64 {
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
		if _, err := Render([]byte(svg), Options{Context: budget.WithLedger(context.Background(), l)}); err != nil {
			t.Fatal(err)
		}
		if l.Bytes() != 0 {
			t.Errorf("%d bytes left charged after the render", l.Bytes())
		}
		return l.PeakBytes()
	}
	// The document itself is charged too (its tree): the same document
	// painted in a colour of the same length is the base.
	base := peak(doc("#000000"))
	if p := peak(doc("url(#g)")) - base; p < maxGradCacheBytes/2 || p > maxGradCacheBytes+1<<20 {
		t.Errorf("ledger peak %d bytes over the document's: the gradient cache is charged up to %d", p, maxGradCacheBytes)
	}
}

func TestRenderCacheBounds(t *testing.T) {
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1000})
	c := renderCache[int, int]{maxBytes: 500, maxEntries: 3}
	for i := range 100 {
		c.put(l, i, i, 100)
		if c.bytes > 500 || len(c.m) > 3 || l.Bytes() != c.bytes {
			t.Fatalf("after %d puts: %d bytes, %d entries, ledger %d", i+1, c.bytes, len(c.m), l.Bytes())
		}
	}
	c.put(l, -1, -1, 600) // larger than the cache: not kept
	if _, ok := c.get(-1); ok {
		t.Error("an entry larger than the cache was kept")
	}
	c.drop(l)
	if l.Bytes() != 0 {
		t.Errorf("ledger %d after drop", l.Bytes())
	}
}
