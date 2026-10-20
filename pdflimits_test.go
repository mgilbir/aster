package aster_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// TestPDFBoundsFollowTheSVGBudget is C44 (audit 2026-10-18): the PDF writer's
// bounds on a chart's SVG are what the SVG writer may make, so VegaToPDF
// and VegaLiteToPDF draw every chart SVG output and PNG draw, and
// WithMemoryLimit moves them as it moves the SVG budget. SVG input keeps
// its own bounds, which a small limit lowers. Either fails naming what it
// bounded.
func TestPDFBoundsFollowTheSVGBudget(t *testing.T) {
	for _, n := range []uint64{0, 16 << 20, 1 << 30, 16 << 30} {
		chart, input, svgBytes := aster.PDFLimitsForTest(n)
		if chart.MaxInputBytes < svgBytes {
			t.Errorf("WithMemoryLimit(%d): a chart's SVG may be %d bytes, the PDF writer takes %d", n, svgBytes, chart.MaxInputBytes)
		}
		// Every bound of the SVG a chart makes scales with what it may be.
		if chart.MaxElements < svgBytes/64 || chart.MaxPathSegments < svgBytes/8 || chart.MaxTextBytes < svgBytes/16 {
			t.Errorf("WithMemoryLimit(%d): bounds %+v for %d bytes of SVG", n, chart, svgBytes)
		}
		if input.MaxInputBytes > 64<<20 || (n > 0 && input.MaxInputBytes > max(int(n/4), 1<<20)) {
			t.Errorf("WithMemoryLimit(%d): SVG input of %d bytes taken", n, input.MaxInputBytes)
		}
	}
	c, err := aster.New(aster.WithMemoryLimit(4 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	big := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + strings.Repeat(`<rect width="1" height="1"/>`, 50000) + `</svg>`
	_, err = c.SVGToPDF(big)
	if !errors.Is(err, aster.ErrLimit) || !strings.Contains(err.Error(), "SVG input") {
		t.Errorf("SVG input past its bound: %v", err)
	}
}
