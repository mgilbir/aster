package aster_test

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// bodyLoader serves body for every URI.
type bodyLoader string

func (b bodyLoader) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }
func (b bodyLoader) Load(context.Context, string) ([]byte, error)           { return []byte(b), nil }

// What upstream logs and carries on past fails a call by default, and an
// error handler sees it, typed, and decides.
func TestErrorsReachTheCaller(t *testing.T) {
	const (
		dataURL     = "https://example.com/cars.json"
		failedLoad  = `{"data":[{"name":"d","url":"` + dataURL + `"}],"marks":[{"type":"rect","from":{"data":"d"}}]}`
		badBody     = `{"data":[{"name":"d","url":"x.csv","format":{"type":"json"}}],"marks":[{"type":"rect","from":{"data":"d"}}]}`
		exception   = `{"signals":[{"name":"s","update":"datum"}]}`
		logWarning  = `{"scales":[{"name":"x","type":"log","domain":[0,10],"range":[0,100]}]}`
		vlNoLoader  = `{"data":{"url":"` + dataURL + `"},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`
		deniedImage = `{"width":20,"height":20,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"https://example.com/a.png"},"width":{"value":10},"height":{"value":10}}}}]}`
	)
	newConverter := func(opts ...aster.Option) *aster.Converter {
		t.Helper()
		c, err := aster.New(opts...)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	c := newConverter()
	collect := func(got *[]error) aster.ErrorHandlerOption {
		return aster.WithErrorHandler(func(err error) error { *got = append(*got, err); return nil })
	}

	t.Run("a denied data load fails the render with a *LoadError", func(t *testing.T) {
		for name, render := range map[string]func() error{
			"VegaToSVG":     func() error { _, err := c.VegaToSVG([]byte(failedLoad)); return err },
			"VegaLiteToSVG": func() error { _, err := c.VegaLiteToSVG([]byte(vlNoLoader)); return err },
			"VegaToPNG":     func() error { _, err := c.VegaToPNG([]byte(failedLoad)); return err },
			"VegaToPDF":     func() error { _, err := c.VegaToPDF([]byte(failedLoad)); return err },
		} {
			err := render()
			var le *aster.LoadError
			if !errors.As(err, &le) || le.Resource != "data" || le.URL != dataURL || le.Err == nil {
				t.Errorf("%s: err = %v, want a *LoadError for the data", name, err)
			}
		}
	})

	t.Run("told to go on, the render is upstream's", func(t *testing.T) {
		var got []error
		svg, err := c.VegaToSVG([]byte(failedLoad), collect(&got))
		if err != nil {
			t.Fatal(err)
		}
		var le *aster.LoadError
		if len(got) != 1 || !errors.As(got[0], &le) || le.URL != dataURL {
			t.Fatalf("handler saw %v, want one *LoadError", got)
		}
		if !strings.HasPrefix(svg, "<svg") {
			t.Errorf("no SVG: %.80s", svg)
		}
	})

	t.Run("data that does not parse is a *LoadError", func(t *testing.T) {
		_, err := newConverter(aster.WithLoader(bodyLoader("{not json"))).VegaToSVG([]byte(badBody))
		var le *aster.LoadError
		if !errors.As(err, &le) || le.URL != "x.csv" {
			t.Errorf("err = %v, want a *LoadError for x.csv", err)
		}
	})

	t.Run("an exception during evaluation fails the render", func(t *testing.T) {
		if _, err := c.VegaToSVG([]byte(exception)); err == nil || !strings.Contains(err.Error(), "datum is not defined") {
			t.Errorf("err = %v, want the ReferenceError", err)
		}
		var got []error
		if _, err := c.VegaToSVG([]byte(exception), collect(&got)); err != nil || len(got) != 1 {
			t.Errorf("going on: err %v, handler saw %v", err, got)
		}
	})

	t.Run("a warning never fails the render, and reaches the handler", func(t *testing.T) {
		if _, err := c.VegaToSVG([]byte(logWarning)); err != nil {
			t.Fatalf("a warning failed the render: %v", err)
		}
		var got []error
		if _, err := c.VegaToSVG([]byte(logWarning), collect(&got)); err != nil {
			t.Fatal(err)
		}
		var w aster.Warning
		if len(got) != 1 || !errors.As(got[0], &w) || !strings.Contains(string(w), "Log scale domain includes zero") {
			t.Errorf("handler saw %v, want the log-scale Warning", got)
		}
	})

	t.Run("the handler's error ends the render", func(t *testing.T) {
		stop := errors.New("stop here")
		calls := 0
		_, err := c.VegaToPNG([]byte(failedLoad), aster.WithErrorHandler(func(error) error { calls++; return stop }))
		if !errors.Is(err, stop) || calls != 1 {
			t.Errorf("err = %v after %d calls, want the handler's error after one", err, calls)
		}
	})

	t.Run("a refused image fails SVG output", func(t *testing.T) {
		_, err := c.VegaToSVG([]byte(deniedImage))
		var le *aster.LoadError
		if !errors.As(err, &le) || le.Resource != "image" {
			t.Errorf("err = %v, want a *LoadError for the image", err)
		}
	})

	t.Run("a refused image fails PNG output, and is undrawn when let go", func(t *testing.T) {
		_, err := c.VegaToPNG([]byte(deniedImage))
		var le *aster.LoadError
		if !errors.As(err, &le) || le.Resource != "image" {
			t.Errorf("err = %v, want a *LoadError for the image", err)
		}
		var got []error
		if _, err := c.VegaToPNG([]byte(deniedImage), collect(&got)); err != nil || len(got) != 1 {
			t.Errorf("going on: err %v, handler saw %v (want the image once)", err, got)
		}
	})

	t.Run("an image that does not decode is a *LoadError", func(t *testing.T) {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="40">` +
			`<image xlink:href="data:image/png;base64,QUJDREVGRw==" width="40" height="40"/></svg>`
		for name, render := range map[string]func() error{
			"SVGToPNG": func() error { _, err := c.SVGToPNG(svg); return err },
			"SVGToPDF": func() error { _, err := c.SVGToPDF(svg); return err },
		} {
			var le *aster.LoadError
			if err := render(); !errors.As(err, &le) || le.Resource != "image" {
				t.Errorf("%s: err = %v, want a *LoadError for the image", name, err)
			}
		}
	})

	t.Run("a refused link is a Warning", func(t *testing.T) {
		spec := `{"width":20,"height":20,"marks":[{"type":"rect","encode":{"enter":{"href":{"value":"https://example.com/"},"width":{"value":10},"height":{"value":10}}}}]}`
		if _, err := c.VegaToSVG([]byte(spec)); err != nil {
			t.Fatalf("a refused link failed the render: %v", err)
		}
		var got []error
		if _, err := c.VegaToSVG([]byte(spec), collect(&got)); err != nil {
			t.Fatal(err)
		}
		var w aster.Warning
		if len(got) != 1 || !errors.As(got[0], &w) {
			t.Errorf("handler saw %v, want one Warning", got)
		}
	})
}

// A data: image is part of the specification: it is drawn with the default
// loader, which refuses every resource.
func TestDataImageNeedsNoLoader(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := `{"width":40,"height":40,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"` + validPNGDataURL(t) + `"},"width":{"value":40},"height":{"value":40}}}}]}`
	out, err := c.VegaToPNG([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if n := countReddish(img); n < 1000 {
		t.Errorf("the embedded image drew %d red pixels", n)
	}
}

// Only the image formats the engine draws bypass the Loader: any other data:
// URL is still the Loader's to allow, so a spec cannot put one in the output
// past a Loader that refuses it.
func TestOtherDataURLsGoThroughTheLoader(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	const url = "data:image/svg+xml;base64,PHN2Zy8+"
	spec := `{"width":20,"height":20,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"` + url + `"},"width":{"value":10},"height":{"value":10}}}}]}`
	var le *aster.LoadError
	if _, err := c.VegaToSVG([]byte(spec)); !errors.As(err, &le) || le.URL != url {
		t.Errorf("err = %v, want a *LoadError for the data: URL", err)
	}
	svg, err := c.VegaToSVG([]byte(spec), goOn)
	if err != nil || strings.Contains(svg, "svg+xml") {
		t.Errorf("err %v; refused data: URL in the output: %v", err, strings.Contains(svg, "svg+xml"))
	}
}

func TestErrClosed(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if _, err := c.VegaToSVG([]byte(`{}`)); !errors.Is(err, aster.ErrClosed) {
		t.Errorf("err = %v, want ErrClosed", err)
	}
}
