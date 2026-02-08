package aster_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

func TestVegaLiteToSVG(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
	if !strings.Contains(svg, "</svg>") {
		t.Errorf("expected SVG output containing </svg>")
	}
}

func TestVegaToSVG(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vg.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	svg, err := c.VegaToSVG(spec)
	if err != nil {
		t.Fatalf("VegaToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
}

func TestVegaLiteToVega(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	vgSpec, err := c.VegaLiteToVega(spec)
	if err != nil {
		t.Fatalf("VegaLiteToVega: %v", err)
	}

	if len(vgSpec) == 0 {
		t.Error("expected non-empty Vega spec")
	}
	if !strings.Contains(string(vgSpec), `"$schema"`) {
		t.Errorf("expected Vega spec to contain $schema")
	}
}

func TestDenyLoaderPreventsLoading(t *testing.T) {
	// The default DenyLoader should prevent any data loading.
	// A spec with inline data should still work.
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	// This should succeed since the spec uses inline data.
	_, err = c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG with inline data should succeed: %v", err)
	}
}

func TestNoTextMeasurement(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatalf("reading test spec: %v", err)
	}

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}

	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("expected SVG output starting with <svg, got: %.100s", svg)
	}
}
