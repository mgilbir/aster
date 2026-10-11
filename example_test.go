package aster_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mgilbir/aster"
)

const barChart = `{
  "data": {"values": [{"a": "A", "b": 28}, {"a": "B", "b": 55}]},
  "mark": "bar",
  "encoding": {
    "x": {"field": "a", "type": "nominal"},
    "y": {"field": "b", "type": "quantitative"}
  }
}`

func Example() {
	c, err := aster.New()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	svg, err := c.VegaLiteToSVG([]byte(barChart))
	if err != nil {
		log.Fatal(err)
	}
	png, err := c.VegaLiteToPNG([]byte(barChart), aster.WithScale(2))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(strings.HasPrefix(svg, "<svg"), string(png[1:4]))
	// Output: true PNG
}

// A spec that names its data by URL needs a Loader that allows it; with the
// default one the render fails with a *LoadError.
func ExampleLoadError() {
	c, err := aster.New() // DenyLoader: nothing may be loaded
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	spec := `{"data": {"url": "https://example.com/cars.json"}, "mark": "point"}`
	_, err = c.VegaLiteToSVG([]byte(spec))
	var le *aster.LoadError
	if errors.As(err, &le) {
		fmt.Println(le.Resource, le.URL)
	}
	// Output: data https://example.com/cars.json
}

// WithErrorHandler renders past what upstream Vega logs and carries on from,
// as a browser does, and sees each problem.
func ExampleWithErrorHandler() {
	c, err := aster.New()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	spec := `{"data": {"url": "https://example.com/cars.json"}, "mark": "point"}`
	svg, err := c.VegaLiteToSVG([]byte(spec), aster.WithErrorHandler(func(err error) error {
		var le *aster.LoadError
		if errors.As(err, &le) {
			fmt.Println("rendering without", le.URL)
		}
		return nil // go on; return err to fail the call instead
	}))
	fmt.Println(err == nil, strings.HasPrefix(svg, "<svg"))
	// Output:
	// rendering without https://example.com/cars.json
	// true true
}

// Converters for untrusted specifications: bounded, and loading only from
// known hosts.
func ExampleNew_untrusted() {
	c, err := aster.New(
		aster.WithTimeout(10*time.Second),
		aster.WithMemoryLimit(256<<20),
		aster.WithLoader(&aster.HTTPLoader{
			Client:         http.DefaultClient,
			AllowedDomains: []string{"cdn.jsdelivr.net"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	_, err = c.VegaToSVG([]byte(`{"data": [{"name": "t", "transform": [{"type": "sequence", "start": 0, "stop": 1e7}]}]}`))
	switch {
	case errors.Is(err, aster.ErrLimit):
		fmt.Println("refused: the spec asks for too much")
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Println("refused: too slow")
	}
	// Output: refused: the spec asks for too much
}

// WithSignal renders a chart as it looks after a parameter changes.
func ExampleWithSignal() {
	c, err := aster.New()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	spec := `{
	  "params": [{"name": "label", "value": "before"}],
	  "data": {"values": [{}]},
	  "mark": {"type": "text", "text": {"expr": "label"}}
	}`
	svg, err := c.VegaLiteToSVG([]byte(spec), aster.WithSignal("label", "after"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(strings.Contains(svg, ">after<"))
	// Output: true
}

// PDFTextNamed output, assembled into one document, embeds each font once:
// collect the glyphs every chart uses, then subset each font once.
func ExampleSubsetFont() {
	c, err := aster.New()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	glyphs := map[string][]uint16{} // by PostScript name
	sources := map[string][]byte{}
	for range 3 { // three charts
		_, usage, err := c.VegaLiteToPDFUsage([]byte(barChart), aster.WithPDFText(aster.PDFTextNamed))
		if err != nil {
			log.Fatal(err)
		}
		for _, u := range usage {
			glyphs[u.PostScriptName] = append(glyphs[u.PostScriptName], u.GIDs...)
			sources[u.PostScriptName] = u.Source
		}
	}
	ok := true
	for name, gids := range glyphs {
		subset, ps, err := aster.SubsetFont(sources[name], gids)
		if err != nil {
			log.Fatal(err)
		}
		// Embed subset once, as the font the charts call ps.
		ok = ok && ps == name && len(subset) < len(sources[name])
	}
	fmt.Println(len(glyphs) > 0, ok)
	// Output: true true
}
