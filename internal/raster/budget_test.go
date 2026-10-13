package raster

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/testutil"
)

// Tests for the context and work/memory budgets: a small SVG must not be able
// to run for minutes or allocate gigabytes.

func rectsSVG(size, n int) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="` + num(size) + `" height="` + num(size) + `">`)
	for i := 0; i < n; i++ {
		b.WriteString(`<rect width="` + num(size) + `" height="` + num(size) + `" fill="rgba(0,0,0,.1)"/>`)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func num(n int) string {
	var d [20]byte
	i := len(d)
	for {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			return string(d[i:])
		}
	}
}

func TestPixelOpsBudget(t *testing.T) {
	svg := rectsSVG(1000, 50) // 50 Mpx of fills
	if _, err := Render([]byte(svg), Options{Limits: Limits{MaxPixelOps: 10_000_000}}); !errors.Is(err, errLimit) {
		t.Fatalf("want errLimit, got %v", err)
	}
	if _, err := Render([]byte(svg), Options{Limits: Limits{MaxPixelOps: 200_000_000}}); err != nil {
		t.Fatalf("within budget: %v", err)
	}
}

// useBomb is 2^12 nested <use> copies of a full-canvas rect.
func useBomb() []byte {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="2000" height="2000"><defs><g id="g0"><rect width="2000" height="2000" fill="rgba(0,0,0,.1)"/></g>`)
	const levels = 12
	for i := 1; i <= levels; i++ {
		id, prev := "g"+num(i), "g"+num(i-1)
		b.WriteString(`<g id="` + id + `"><use xlink:href="#` + prev + `"/><use xlink:href="#` + prev + `"/></g>`)
	}
	b.WriteString(`</defs><use xlink:href="#g` + num(levels) + `"/></svg>`)
	return []byte(b.String())
}

func TestPixelOpsBudgetUseExpansion(t *testing.T) {
	// A small budget: the expansion must stop as soon as it is spent.
	if _, err := Render(useBomb(), Options{Limits: Limits{MaxPixelOps: 20_000_000}}); !errors.Is(err, errLimit) {
		t.Fatalf("want errLimit, got %v", err)
	}
}

func TestPixelOpsDefaultBudgetUseExpansion(t *testing.T) {
	if testing.Short() {
		t.Skip("spends the whole default pixel budget (slow under -race)")
	}
	start := time.Now()
	_, err := Render(useBomb(), Options{})
	if !errors.Is(err, errLimit) {
		t.Fatalf("want errLimit, got %v", err)
	}
	// CI baseline 15.33 s (Heavy gates timings run, oracle job, not under
	// -race). The owner's decision: the limit is 2x, 30.7 s, rounded up to
	// 32 s, scaled for -race: slower is a finding to investigate.
	testutil.Timing(t, "pixel-ops default budget, use expansion", time.Since(start), testutil.Scaled(32*time.Second))
	if d := time.Since(start); d > testutil.Scaled(32*time.Second) {
		t.Errorf("took %v", d)
	}
}

func TestCanvasBytesBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000">`)
	for i := 0; i < 10; i++ {
		b.WriteString(`<g opacity=".5">`)
	}
	b.WriteString(`<rect width="10" height="10"/>`)
	for i := 0; i < 10; i++ {
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	// 4 MB canvas; allow the canvas and only two layers.
	_, err := Render([]byte(b.String()), Options{Limits: Limits{MaxCanvasBytes: 12_000_000}})
	if !errors.Is(err, errLimit) || !strings.Contains(err.Error(), "pixel memory") {
		t.Fatalf("want pixel memory error, got %v", err)
	}
	if _, err := Render([]byte(b.String()), Options{}); err != nil {
		t.Fatalf("default limits: %v", err)
	}
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render([]byte(rectsSVG(100, 2)), Options{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: got %v", err)
	}

	// Cancelled while rendering: the deadline must cut a long render short.
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Render([]byte(rectsSVG(3000, 400)), Options{Context: ctx, Limits: Limits{MaxPixelOps: math.MaxInt}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: got %v", err)
	}
	// CI baseline 0.12 s (0.118 s under -race, 0.102 s without); limit 2x, floor 1 s: slower is a finding to investigate.
	testutil.Timing(t, "render honours a 100ms deadline", time.Since(start), testutil.Scaled(1*time.Second))
	if d := time.Since(start); d > testutil.Scaled(1*time.Second) {
		t.Errorf("deadline honoured after %v", d)
	}
}

func TestContextCancelledFilter(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="3000" height="3000"><filter id="f"><feGaussianBlur stdDeviation="20"/><feGaussianBlur stdDeviation="20"/><feGaussianBlur stdDeviation="20"/><feGaussianBlur stdDeviation="20"/></filter>` +
		strings.Repeat(`<rect width="3000" height="3000" filter="url(#f)"/>`, 50) + `</svg>`
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Render([]byte(svg), Options{Context: ctx})
	if err == nil {
		t.Fatal("expected an error")
	}
	// CI baseline 0.96 s without -race (0.20 s under -race, 0.04 s unscaled; Heavy gates timings run, worst of 4);
	// limit 2x = 1.92 s, rounded up to 2 s: slower is a finding to investigate.
	testutil.Timing(t, "filter render honours a 200ms deadline", time.Since(start), testutil.Scaled(2*time.Second))
	if d := time.Since(start); d > testutil.Scaled(2*time.Second) {
		t.Errorf("returned after %v (%v)", d, err)
	}
}

// The reproducers shipped with the security review (docs/history/security-review-2026.md).
func TestSecurityReviewSVGs(t *testing.T) {
	for _, name := range []string{"layers-8k.svg", "use-expansion-fullcanvas.svg", "rects-1000-fullcanvas.svg"} {
		data, err := os.ReadFile("../../testdata/security/" + name)
		if os.IsNotExist(err) {
			data, err = os.ReadFile("../../testdata/security/bounded/" + name)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) {
			if testing.Short() {
				t.Skip("heavy")
			}
			// The budget must stop the render; a timeout instead would mean
			// the work was not bounded by it. (Wall time is not asserted: it
			// depends on the machine's load, the budget does not.)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_, err := Render(data, Options{Context: ctx})
			if !errors.Is(err, errLimit) {
				t.Fatalf("want errLimit, got %v", err)
			}
		})
	}
}
