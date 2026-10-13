package svgpdf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"

	"github.com/mgilbir/aster/internal/text"
)

// TestGlyphCacheBounded: the per-render outline cache keeps at most
// maxGlyphCacheBytes, however many sizes a chart's text comes in (X4); past
// it outlines are still drawn.
func TestGlyphCacheBounded(t *testing.T) {
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := m.ShapeText("ABCDEFGHIJKLMNOPQRSTUVWXYZ@&%$#", "12px sans-serif")
	face := runs[0].Face
	r := &renderer{}
	for size := 1.0; size < 3000; size++ {
		for _, g := range runs[0].Glyphs {
			if _, ok := r.glyphOutline(face, g.GID, size); !ok {
				t.Fatal("no outline")
			}
		}
	}
	var held int64
	for _, o := range r.glyphs {
		held += int64(len(o)) * segmentBytes
	}
	if held > maxGlyphCacheBytes {
		t.Errorf("the cache holds %d MB of outlines", held>>20)
	}
}

// TestRenderChargesLedger: what a render keeps that the input sizes (an
// ExtGState per distinct opacity, the content stream's chunks) is charged
// to the call's ledger, so a memory limit stops it with ErrLimit (X4).
func TestRenderChargesLedger(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg width="10" height="10">`)
	for i := range 20000 {
		fmt.Fprintf(&b, `<rect width="1" height="1" fill-opacity="%g"/>`, float64(i+1)/20001)
	}
	b.WriteString(`</svg>`)
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
	_, err := Convert(b.String(), nil, Options{Context: budget.WithLedger(context.Background(), led)})
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
	led = budget.NewLedger(context.Background(), budget.LedgerLimits{})
	if _, err := Convert(b.String(), nil, Options{Context: budget.WithLedger(context.Background(), led)}); err != nil {
		t.Fatal(err)
	}
	if led.Bytes() < 20000*gsEntryBytes {
		t.Errorf("charged %d bytes for 20000 ExtGStates", led.Bytes())
	}
}
