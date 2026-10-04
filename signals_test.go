package aster_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// A bar as wide as the signal w says.
const signalSpec = `{"width":100,"height":20,"signals":[{"name":"w","value":10}],
  "marks":[{"type":"rect","encode":{"update":{"x":{"value":0},"y":{"value":0},"width":{"signal":"w"},"height":{"value":20},"fill":{"value":"red"}}}}]}`

func TestWithSignal(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	def, err := c.VegaToSVG([]byte(signalSpec))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, `d="M0,0h10v20h-10Z"`) {
		t.Fatalf("the default bar is not 10 wide:\n%s", def)
	}
	set, err := c.VegaToSVG([]byte(signalSpec), aster.WithSignal("w", 50))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(set, `d="M0,0h50v20h-50Z"`) {
		t.Fatalf("the bar is not 50 wide after w=50:\n%s", set)
	}

	// Writes apply in order, and a json.RawMessage is the value it encodes.
	last, err := c.VegaToSVG([]byte(signalSpec), aster.WithSignal("w", 50), aster.WithSignal("w", json.RawMessage("30")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last, `d="M0,0h30v20h-30Z"`) {
		t.Fatalf("the last write did not win:\n%s", last)
	}

	// PNG and PDF render the chart as the signals leave it.
	pngDef, err := c.VegaToPNG([]byte(signalSpec))
	if err != nil {
		t.Fatal(err)
	}
	pngSet, err := c.VegaToPNG([]byte(signalSpec), aster.WithSignal("w", 50), aster.WithScale(2))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(pngDef, pngSet) {
		t.Error("the PNG ignored the signal")
	}
	pdfDef, err := c.VegaToPDF([]byte(signalSpec))
	if err != nil {
		t.Fatal(err)
	}
	pdfSet, err := c.VegaToPDF([]byte(signalSpec), aster.WithSignal("w", 50))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(pdfDef, pdfSet) {
		t.Error("the PDF ignored the signal")
	}

	// An SVG input has no signals; the option changes nothing.
	plain, err := c.SVGToPNG(def)
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := c.SVGToPNG(def, aster.WithSignal("w", 50))
	if err != nil || !bytes.Equal(plain, ignored) {
		t.Errorf("SVGToPNG with a signal: %v, changed %v", err, !bytes.Equal(plain, ignored))
	}
}

func TestWithSignalVegaLiteParameter(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := `{"params":[{"name":"cutoff","value":2}],
	  "data":{"values":[{"a":1},{"a":2},{"a":3},{"a":4}]},
	  "transform":[{"filter":"datum.a > cutoff"}],
	  "mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`
	count := func(svg string) int { return strings.Count(svg, `aria-roledescription="point"`) }
	def, err := c.VegaLiteToSVG([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	set, err := c.VegaLiteToSVG([]byte(spec), aster.WithSignal("cutoff", 0))
	if err != nil {
		t.Fatal(err)
	}
	if count(def) != 2 || count(set) != 4 {
		t.Errorf("points: %d by default and %d with cutoff 0, want 2 and 4", count(def), count(set))
	}
}

func TestWithSignalErrors(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.VegaToSVG([]byte(signalSpec), aster.WithSignal("nope", 1)); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("an unknown signal: err = %v", err)
	}
	if _, err := c.VegaToSVG([]byte(signalSpec), aster.WithSignal("w", func() {})); err == nil || !strings.Contains(err.Error(), `signal "w"`) {
		t.Errorf("a value JSON cannot encode: err = %v", err)
	}
	if _, err := c.VegaLiteToPDF([]byte(`{"mark":"point"}`), aster.WithSignal("nope", 1)); err == nil {
		t.Error("an unknown signal through PDF: no error")
	}
}
