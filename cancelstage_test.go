package aster_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/mgilbir/aster"
)

// A call whose context ends returns an error, whichever stage the end falls
// in; it never panics. The end is made to fall after each stage a call
// reports, exactly, by canceling from the stage hook (ContextWithStageHookForTest),
// so the test counts stages and times nothing: a deadline that lands in the
// stage after "compile" of VegaLiteToVega panicked out of the call once
// (the JSON write was outside every recover), a failure that showed only on a
// slow machine, in one run in some.
func TestCanceledAtEveryStage(t *testing.T) {
	read := func(parts ...string) []byte {
		b, err := os.ReadFile(filepath.Join(parts...))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// 4,000 params write well over the 1,024 values a write polls the
	// context at.
	big := read("testdata", "security", "bounded", "params-4000.vl.json")
	vl := read("testdata", "bar-chart.vl.json")
	vg := read("testdata", "bar-chart.vg.json")
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	svg, err := c.VegaToSVG(vg)
	if err != nil {
		t.Fatal(err)
	}
	ctxOpt := aster.WithContext
	calls := []struct {
		name string
		run  func(ctx context.Context) error
	}{
		{"VegaLiteToVega/params-4000", func(ctx context.Context) error { _, err := c.VegaLiteToVega(big, ctxOpt(ctx)); return err }},
		{"VegaLiteToVega", func(ctx context.Context) error { _, err := c.VegaLiteToVega(vl, ctxOpt(ctx)); return err }},
		{"VegaToSVG", func(ctx context.Context) error { _, err := c.VegaToSVG(vg, ctxOpt(ctx)); return err }},
		{"VegaLiteToSVG", func(ctx context.Context) error { _, err := c.VegaLiteToSVG(vl, ctxOpt(ctx)); return err }},
		{"VegaToPNG", func(ctx context.Context) error { _, err := c.VegaToPNG(vg, ctxOpt(ctx)); return err }},
		{"VegaLiteToPNG", func(ctx context.Context) error { _, err := c.VegaLiteToPNG(vl, ctxOpt(ctx)); return err }},
		{"SVGToPNG", func(ctx context.Context) error { _, err := c.SVGToPNG(svg, ctxOpt(ctx)); return err }},
		{"VegaToPDF", func(ctx context.Context) error { _, err := c.VegaToPDF(vg, ctxOpt(ctx)); return err }},
		{"VegaLiteToPDF", func(ctx context.Context) error { _, err := c.VegaLiteToPDF(vl, ctxOpt(ctx)); return err }},
		{"SVGToPDF", func(ctx context.Context) error { _, err := c.SVGToPDF(svg, ctxOpt(ctx)); return err }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			// A first, uncanceled call lists the stages it reports.
			var stages []string
			ctx := aster.ContextWithStageHookForTest(context.Background(), func(s string) { stages = append(stages, s) })
			if err := tc.run(ctx); err != nil {
				t.Fatal(err)
			}
			if len(stages) == 0 {
				t.Fatal("the call reports no stage: the test cuts nothing")
			}
			for i, stage := range stages {
				t.Run(fmt.Sprintf("after-%s-%d", stage, i), func(t *testing.T) {
					cctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					n := 0
					hooked := aster.ContextWithStageHookForTest(cctx, func(string) {
						if n++; n == i+1 {
							cancel()
						}
					})
					var err error
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Errorf("panic escaped the public API: %v\n%s", r, debug.Stack())
							}
						}()
						err = tc.run(hooked)
					}()
					if err == nil || !errors.Is(err, context.Canceled) {
						t.Errorf("canceled after stage %q (%d): err = %v, want one wrapping context.Canceled", stage, i, err)
					}
				})
			}
		})
	}
}
