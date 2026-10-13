package aster_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/raster"
)

// pixelWork renders svg to PNG at scale s with c, reporting the work the
// render spent and its budget.
func pixelWork(t *testing.T, c *aster.Converter, svg string, s float64) (raster.Work, error) {
	t.Helper()
	var w raster.Work
	ctx := raster.WithWorkReport(context.Background(), func(got raster.Work) { w = got })
	_, err := c.SVGToPNG(svg, aster.WithScale(s), aster.WithContext(ctx))
	return w, err
}

func newConverter(t *testing.T, opts ...aster.Option) *aster.Converter {
	t.Helper()
	c, err := aster.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// WithPixelWorkLimit sets the work budget at scale 1; the scale rule and the
// per-pixel floor apply to it as to the default.
func TestWithPixelWorkLimitFollowsTheScale(t *testing.T) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="red"/></svg>`
	for _, c := range []struct {
		opts []aster.Option
		base int
	}{
		{nil, 128 << 20},
		{[]aster.Option{aster.WithPixelWorkLimit(200 << 20)}, 200 << 20},
		{[]aster.Option{aster.WithPixelWorkLimit(1 << 20)}, 1 << 20},
	} {
		conv := newConverter(t, c.opts...)
		for _, s := range []float64{1, 3} {
			w, err := pixelWork(t, conv, svg, s)
			if err != nil {
				t.Fatal(err)
			}
			want := max(int(float64(c.base)*max(1, s*s)), 16*w.Width*w.Height)
			if w.Budget != want {
				t.Errorf("base %d, scale %v: budget %d, want %d", c.base, s, w.Budget, want)
			}
		}
	}
	// The floor of 16 a pixel still holds under a small base: a 3000x3000
	// canvas has 144 M.
	big := `<svg xmlns="http://www.w3.org/2000/svg" width="3000" height="3000"><rect width="10" height="10"/></svg>`
	w, err := pixelWork(t, newConverter(t, aster.WithPixelWorkLimit(1<<20)), big, 1)
	if err != nil || w.Budget != 16*3000*3000 {
		t.Errorf("3000x3000 under a 1 Mi base: budget %d (%v), want %d", w.Budget, err, 16*3000*3000)
	}
}

func TestWithPixelWorkLimitZeroIsRefused(t *testing.T) {
	_, err := aster.New(aster.WithPixelWorkLimit(0))
	if err == nil || !strings.Contains(err.Error(), "WithPixelWorkLimit(0)") {
		t.Fatalf("New(WithPixelWorkLimit(0)) = %v, want an error naming the option", err)
	}
}

// filterSVG is a 1024x1024 canvas under three full-canvas blurs: about 150 M
// pixel operations, past the default budget at scale 1.
const filterSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024">` +
	`<filter id="f" filterUnits="userSpaceOnUse" x="0" y="0" width="1024" height="1024">` +
	`<feGaussianBlur stdDeviation="5"/><feGaussianBlur stdDeviation="5"/><feGaussianBlur stdDeviation="5"/></filter>` +
	`<rect width="1024" height="1024" fill="teal" filter="url(#f)"/></svg>`

// SVG input whose filters pass the default budget fails with ErrLimit, the
// message naming the option, and renders under a raised one.
func TestWithPixelWorkLimitAdmitsHeavyFilters(t *testing.T) {
	w, err := pixelWork(t, newConverter(t), filterSVG, 1)
	if !errors.Is(err, aster.ErrLimit) || !strings.Contains(err.Error(), "WithPixelWorkLimit") {
		t.Fatalf("default budget: err = %v, want ErrLimit naming WithPixelWorkLimit", err)
	}
	t.Logf("default: stopped at %d of %d", w.Ops, w.Budget)
	w, err = pixelWork(t, newConverter(t, aster.WithPixelWorkLimit(256<<20)), filterSVG, 1)
	if err != nil {
		t.Fatalf("WithPixelWorkLimit(256 Mi): %v", err)
	}
	if w.Ops <= 128<<20 || w.Budget != 256<<20 {
		t.Errorf("WithPixelWorkLimit(256 Mi): %d of %d pixel operations, want past 128 Mi of 256 Mi", w.Ops, w.Budget)
	}
}

// The <use> bomb of the raster tests spends whatever budget it has: under a
// raised one it spends more before it fails.
func TestWithPixelWorkLimitRaisesTheBombsBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("spends 192 Mi pixel operations")
	}
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="2000" height="2000"><defs><g id="g0"><rect width="2000" height="2000" fill="rgba(0,0,0,.1)"/></g>`)
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, `<g id="g%d"><use xlink:href="#g%d"/><use xlink:href="#g%d"/></g>`, i, i-1, i-1)
	}
	b.WriteString(`</defs><use xlink:href="#g12"/></svg>`)
	w, err := pixelWork(t, newConverter(t, aster.WithPixelWorkLimit(192<<20)), b.String(), 1)
	if !errors.Is(err, aster.ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
	if w.Budget != 192<<20 || w.Ops <= 128<<20 || w.Ops > w.Budget {
		t.Errorf("stopped at %d of %d pixel operations, want past 128 Mi, within 192 Mi", w.Ops, w.Budget)
	}
}
