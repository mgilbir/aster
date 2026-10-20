package aster_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster"
)

// A nil option is no option, for New as for every method, and the methods of
// a Converter New did not make (its zero value, a nil pointer) return an
// error: none of them panics (C55). The conditional option is the case:
// var o aster.Option; if cond { o = aster.WithTheme(t) }; aster.New(o).
func TestNilOptionsAndZeroConverter(t *testing.T) {
	spec := []byte(`{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`)
	noPanic := func(name string, f func() error) error {
		t.Helper()
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", name, r)
					err = errors.New("panicked")
				}
			}()
			err = f()
		}()
		return err
	}
	var nilOpt aster.Option
	if err := noPanic("New(nil)", func() error {
		c, err := aster.New(nilOpt, nil)
		if err == nil {
			_ = c.Close()
		}
		return err
	}); err != nil {
		t.Errorf("New(nil): %v, want a Converter", err)
	}
	c, err := aster.New(aster.WithLoader(&callCountLoader{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var ro aster.RenderOption
	var po aster.PNGOption
	var fo aster.PDFOption
	calls := methodCalls(spec, ro, po, fo)
	for name, f := range calls {
		if err := noPanic(name+" with a nil option", func() error { return f(c) }); err != nil {
			t.Errorf("%s with a nil option: %v", name, err)
		}
	}
	var zero aster.Converter
	var nilC *aster.Converter
	for which, conv := range map[string]*aster.Converter{"zero Converter": &zero, "nil *Converter": nilC} {
		for name, f := range calls {
			err := noPanic(which+"."+name, func() error { return f(conv) })
			if err == nil || !strings.Contains(err.Error(), "not made by New") {
				t.Errorf("%s.%s: %v, want an error saying it was not made by New", which, name, err)
			}
		}
		if err := noPanic(which+".Close", conv.Close); err != nil {
			t.Errorf("%s.Close: %v", which, err)
		}
	}
}

// methodCalls are the rendering methods of a Converter, each called with spec
// (or the SVG it renders to) and the given options.
func methodCalls(spec []byte, ro aster.RenderOption, po aster.PNGOption, fo aster.PDFOption) map[string]func(*aster.Converter) error {
	vega := []byte(`{"width":20,"height":20,"data":[{"name":"d","url":"d.json"}],"marks":[{"type":"rect","from":{"data":"d"},"encode":{"enter":{"width":{"value":5},"height":{"value":5}}}}]}`)
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><image href="i.png" width="10" height="10"/></svg>`
	e := func(_ any, err error) error { return err }
	e3 := func(_, _ any, err error) error { return err }
	return map[string]func(*aster.Converter) error{
		"VegaToSVG":          func(c *aster.Converter) error { return e(c.VegaToSVG(vega, ro)) },
		"VegaLiteToSVG":      func(c *aster.Converter) error { return e(c.VegaLiteToSVG(spec, ro)) },
		"VegaLiteToVega":     func(c *aster.Converter) error { return e(c.VegaLiteToVega(spec, ro)) },
		"VegaToPNG":          func(c *aster.Converter) error { return e(c.VegaToPNG(vega, po)) },
		"VegaLiteToPNG":      func(c *aster.Converter) error { return e(c.VegaLiteToPNG(spec, po)) },
		"SVGToPNG":           func(c *aster.Converter) error { return e(c.SVGToPNG(svg, po)) },
		"VegaToPDF":          func(c *aster.Converter) error { return e(c.VegaToPDF(vega, fo)) },
		"VegaLiteToPDF":      func(c *aster.Converter) error { return e(c.VegaLiteToPDF(spec, fo)) },
		"SVGToPDF":           func(c *aster.Converter) error { return e(c.SVGToPDF(svg, fo)) },
		"VegaToPDFUsage":     func(c *aster.Converter) error { return e3(c.VegaToPDFUsage(vega, fo)) },
		"VegaLiteToPDFUsage": func(c *aster.Converter) error { return e3(c.VegaLiteToPDFUsage(spec, fo)) },
		"SVGToPDFUsage":      func(c *aster.Converter) error { return e3(c.SVGToPDFUsage(svg, fo)) },
	}
}

// callCountLoader serves one data set and one image, counting its calls.
type callCountLoader struct{ n atomic.Int32 }

func (l *callCountLoader) Sanitize(_ context.Context, u string) (string, error) {
	l.n.Add(1)
	return u, nil
}

func (l *callCountLoader) Load(_ context.Context, u string) ([]byte, error) {
	l.n.Add(1)
	if strings.HasSuffix(u, ".png") {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	return []byte(`[{"a":1}]`), nil
}

// A per-call option that is not valid fails the call before it starts: no
// Loader call, no render (C56: an invalid WithScale was refused after the
// whole render and its Loader traffic).
func TestInvalidCallOptionsFailFirst(t *testing.T) {
	l := &callCountLoader{}
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := []byte(`{"data":{"url":"d.json"},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`)
	for _, scale := range []float64{-1, 0, math.NaN(), math.Inf(1)} {
		for name, f := range methodCalls(spec, nil, aster.WithScale(scale), nil) {
			if !strings.Contains(name, "PNG") {
				continue
			}
			l.n.Store(0)
			if err := f(c); err == nil || !strings.Contains(err.Error(), "scale") {
				t.Errorf("%s, WithScale(%v): %v", name, scale, err)
			}
			if n := l.n.Load(); n != 0 {
				t.Errorf("%s, WithScale(%v): %d Loader calls before the refusal", name, scale, n)
			}
		}
	}
	for name, f := range methodCalls(spec, nil, nil, aster.WithPDFText(99)) {
		if !strings.Contains(name, "PDF") {
			continue
		}
		l.n.Store(0)
		if err := f(c); err == nil || !strings.Contains(err.Error(), "text mode") {
			t.Errorf("%s, WithPDFText(99): %v", name, err)
		}
		if n := l.n.Load(); n != 0 {
			t.Errorf("%s, WithPDFText(99): %d Loader calls before the refusal", name, n)
		}
	}
}
