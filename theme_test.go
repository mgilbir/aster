package aster_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// continuousSpec uses quantitative encodings so the chart body is sized by
// view.continuousWidth/Height rather than band steps.
var continuousSpec = []byte(`{
	"$schema": "https://vega.github.io/schema/vega-lite/v5.json",
	"data": {"values": [{"a": 1, "b": 28}, {"a": 2, "b": 55}]},
	"mark": "point",
	"encoding": {
		"x": {"field": "a", "type": "quantitative"},
		"y": {"field": "b", "type": "quantitative"}
	}
}`)

func svgWidth(t *testing.T, svg string) int {
	t.Helper()
	m := regexp.MustCompile(`<svg[^>]*\bwidth="(\d+)"`).FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("no width attribute in SVG: %.120s", svg)
	}
	w, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parsing width %q: %v", m[1], err)
	}
	return w
}

// Theme keys the Vega-Lite compiler consumes (background, view sizing) must
// take effect on the Vega-Lite render path. They are baked into the compiled
// Vega spec, so applying the theme only at vega.parse silently dropped them.
func TestThemeBackgroundApplied(t *testing.T) {
	c, err := aster.New(aster.WithTheme(`{"background": "red"}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	svg, err := c.VegaLiteToSVG(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}
	if !strings.Contains(svg, `fill="red"`) {
		t.Errorf("themed background not applied; SVG head: %.200s", svg)
	}
}

func TestThemeViewSizeApplied(t *testing.T) {
	plain, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = plain.Close() }()
	themed, err := aster.New(aster.WithTheme(`{"view": {"continuousWidth": 400}}`))
	if err != nil {
		t.Fatalf("New themed: %v", err)
	}
	defer func() { _ = themed.Close() }()

	svg0, err := plain.VegaLiteToSVG(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG plain: %v", err)
	}
	svg1, err := themed.VegaLiteToSVG(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG themed: %v", err)
	}

	w0, w1 := svgWidth(t, svg0), svgWidth(t, svg1)
	// The default continuousWidth is well below 400; the themed chart body is
	// 400 plus axes/padding.
	if w1 < 400 {
		t.Errorf("continuousWidth 400 not applied: themed SVG width %d", w1)
	}
	if w1 <= w0 {
		t.Errorf("themed width %d not larger than default %d", w1, w0)
	}
}

// Runtime-level theme keys must keep working after the theme is also passed
// to the compiler (the two applications must not conflict).
func TestThemeAxisConfigApplied(t *testing.T) {
	c, err := aster.New(aster.WithTheme(`{"axis": {"labelFontSize": 23}}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	svg, err := c.VegaLiteToSVG(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}
	if !strings.Contains(svg, `font-size="23px"`) {
		t.Error("axis labelFontSize from theme not applied")
	}
}

// VegaLiteToVega honors the theme so that compiling first and rendering the
// compiled spec later produces the same output as rendering directly.
func TestVegaLiteToVegaHonorsTheme(t *testing.T) {
	c, err := aster.New(aster.WithTheme(`{"background": "red"}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	vg, err := c.VegaLiteToVega(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToVega: %v", err)
	}
	if !strings.Contains(string(vg), `"background":"red"`) &&
		!strings.Contains(string(vg), `"background": "red"`) {
		t.Errorf("compiled Vega spec does not carry themed background: %.200s", vg)
	}

	svgDirect, err := c.VegaLiteToSVG(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}
	svgCompiled, err := c.VegaToSVG(vg)
	if err != nil {
		t.Fatalf("VegaToSVG: %v", err)
	}
	if svgDirect != svgCompiled {
		t.Error("compile-then-render differs from direct render under a theme")
	}
}

// Rendering without a theme must be unaffected by the compile-time plumbing.
func TestNoThemeUnchanged(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	svg, err := c.VegaLiteToSVG(continuousSpec)
	if err != nil {
		t.Fatalf("VegaLiteToSVG: %v", err)
	}
	if !strings.Contains(svg, `fill="white"`) {
		t.Error("default white background missing without a theme")
	}
}
