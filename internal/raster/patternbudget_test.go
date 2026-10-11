package raster

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
)

// Pattern tiles kept for reuse are charged to the call's ledger, so
// WithMemoryLimit bounds them, not a fixed 256 MiB (C63): forty 4 MB tiles
// under a 16 MiB ledger leave at most 16 MiB of them alive, and the image is
// the one an unbounded render draws.
func TestPatternTilesChargedToLedger(t *testing.T) {
	if testing.Short() {
		t.Skip("renders 160 MB of pattern tiles")
	}
	var b bytes.Buffer
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000"><defs>`)
	for i := range 40 {
		fmt.Fprintf(&b, `<pattern id="p%d" width="1000" height="1000" patternUnits="userSpaceOnUse"><rect width="%d" height="10" fill="#%02x0000"/></pattern>`, i, 10+i, i)
	}
	b.WriteString(`</defs>`)
	for i := range 40 {
		fmt.Fprintf(&b, `<rect x="%d" width="10" height="1000" fill="url(#p%d)"/>`, 20*i, i)
	}
	b.WriteString(`</svg>`)
	svg := b.Bytes()

	render := func(ctx context.Context) ([]byte, uint64) {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		base := ms.HeapAlloc
		var peak atomic.Uint64
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			var m runtime.MemStats
			for {
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
				select {
				case <-stop:
					return
				case <-time.After(time.Millisecond):
				}
			}
		}()
		out, err := RenderPNG(svg, Options{Context: ctx})
		close(stop)
		<-done
		if err != nil {
			t.Fatal(err)
		}
		return out, (max(peak.Load(), base) - base) >> 20
	}
	// Frequent collections keep dropped tiles from lingering as garbage in
	// the sampled heap, so the peaks below measure what the renderer keeps.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	free, freeMB := render(context.Background())
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 16 << 20})
	got, peakMB := render(budget.WithLedger(context.Background(), l))
	if !bytes.Equal(got, free) {
		t.Fatal("the ledger changed the image")
	}
	if l.PeakBytes() > 16<<20 || l.Bytes() != 0 {
		t.Fatalf("ledger peak %d, left %d", l.PeakBytes(), l.Bytes())
	}
	// The unbounded render keeps every tile (160 MB); the bounded one keeps
	// 16 MiB of them besides the 4 MB canvas and a tile in flight. The two
	// peaks are compared rather than one checked against a fixed figure:
	// the heap the sampler sees depends on the collector's pacing, which
	// differs from machine to machine, and a ratio of two renders in the
	// same process does not.
	t.Logf("peak heap %d MB, %d MB without the ledger", peakMB, freeMB)
	if 2*peakMB > freeMB {
		t.Errorf("peak heap %d MB with the tiles bounded at 16 MiB, against %d MB unbounded", peakMB, freeMB)
	}
}
