package aster_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

func TestUnknownVegaLiteVersionErrors(t *testing.T) {
	_, err := aster.New(aster.WithVegaLiteVersion("9.9"))
	if err == nil {
		t.Fatal("expected error for unknown Vega-Lite version")
	}
	if !strings.Contains(err.Error(), "unknown Vega-Lite version") {
		t.Fatalf("unexpected error: %v", err)
	}
	// The error should point the user at what is available.
	if !strings.Contains(err.Error(), "6.4.0") {
		t.Fatalf("error should list available versions, got: %v", err)
	}
}

func TestAvailableVersions(t *testing.T) {
	vs, err := aster.AvailableVersions()
	if err != nil {
		t.Fatalf("AvailableVersions: %v", err)
	}
	keys := make(map[string]aster.VersionInfo, len(vs))
	for _, v := range vs {
		keys[v.Key] = v
	}
	if _, ok := keys["vl6_4"]; !ok {
		t.Fatalf("expected vl6_4 in available versions, got %+v", vs)
	}
	if keys["vl6_4"].VegaLiteVersion != "6.4.0" {
		t.Errorf("vl6_4 VegaLiteVersion = %q, want 6.4.0", keys["vl6_4"].VegaLiteVersion)
	}
}

func TestInvalidPNGScaleRejected(t *testing.T) {
	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`
	for _, scale := range []float64{0, -1, -2.5} {
		if _, err := c.SVGToPNG(svg, aster.WithScale(scale)); err == nil {
			t.Errorf("expected error for scale %v", scale)
		}
	}
	// A valid scale still works.
	if _, err := c.SVGToPNG(svg, aster.WithScale(1.5)); err != nil {
		t.Errorf("valid scale rejected: %v", err)
	}
}
