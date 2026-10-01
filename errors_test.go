package aster_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

// Every resource limit, whichever layer enforces it, is reported with an
// error wrapping aster.ErrLimit; a timeout is not a limit of the
// specification and is reported with context.DeadlineExceeded.
func TestErrLimit(t *testing.T) {
	deepSVG := func(depth int) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` +
			strings.Repeat("<g>", depth) + `<rect width="5" height="5"/>` + strings.Repeat("</g>", depth) + `</svg>`
	}
	big := t.TempDir()
	if err := os.WriteFile(filepath.Join(big, "big.json"), []byte(`[{"a":1},{"a":2},{"a":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"a":1},{"a":2},{"a":3}]`))
	}))
	defer srv.Close()
	loadSpec := func(url string) []byte { return []byte(`{"data":[{"name":"t","url":"` + url + `"}]}`) }
	cases := []struct {
		name string
		opts []aster.Option
		run  func(c *aster.Converter) error
	}{
		{"data rows", []aster.Option{aster.WithMemoryLimit(1 << 20)}, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(`{"data":[{"name":"t","transform":[{"type":"sequence","start":0,"stop":1e6}]}]}`))
			return err
		}},
		{"loaded file size", []aster.Option{aster.WithLoader(&aster.FileLoader{BaseDir: big, MaxBytes: 8})}, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(`{"data":[{"name":"t","url":"big.json"}]}`))
			return err
		}},
		{"loaded file size behind a fallback", []aster.Option{aster.WithLoader(aster.NewFallbackLoader(
			&aster.FileLoader{BaseDir: big, MaxBytes: 8}, aster.NewHTTPLoader(nil)))}, func(c *aster.Converter) error {
			_, err := c.VegaToSVG(loadSpec("big.json"))
			return err
		}},
		{"loaded HTTP size", []aster.Option{aster.WithLoader(&aster.HTTPLoader{AllowPrivateNetworks: true, MaxResponseBytes: 8})}, func(c *aster.Converter) error {
			_, err := c.VegaToSVG(loadSpec(srv.URL + "/big.json"))
			return err
		}},
		{"tick count", nil, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(`{"width":100,"height":50,
			  "scales":[{"name":"x","type":"linear","domain":[0,1],"range":"width"}],
			  "axes":[{"orient":"bottom","scale":"x","tickCount":5e6}]}`))
			return err
		}},
		{"specification nesting", nil, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(`{"data":[{"name":"t","values":` + strings.Repeat("[", 300) + strings.Repeat("]", 300) + `}]}`))
			return err
		}},
		{"expression length", nil, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(`{"signals":[{"name":"s","update":"` + strings.Repeat("1+", 1<<17) + `1"}]}`))
			return err
		}},
		{"Vega-Lite layer nesting", nil, func(c *aster.Converter) error {
			spec := `{"mark":"point"}`
			for range 70 {
				spec = `{"layer":[` + spec + `]}`
			}
			_, err := c.VegaLiteToSVG([]byte(spec))
			return err
		}},
		{"PNG size", nil, func(c *aster.Converter) error {
			_, err := c.SVGToPNG(`<svg xmlns="http://www.w3.org/2000/svg" width="100000" height="100000"/>`)
			return err
		}},
		{"PNG input nesting", nil, func(c *aster.Converter) error {
			_, err := c.SVGToPNG(deepSVG(2000))
			return err
		}},
		{"PDF input nesting", nil, func(c *aster.Converter) error {
			_, err := c.SVGToPDF(deepSVG(2000))
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := aster.New(tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := tc.run(c); !errors.Is(err, aster.ErrLimit) {
				t.Errorf("err = %.300v, want one wrapping aster.ErrLimit", err)
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		c, err := aster.New(aster.WithTimeout(time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, err = c.VegaToSVG([]byte(`{"data":[{"name":"t","transform":[{"type":"sequence","start":0,"stop":4e5},{"type":"collect","sort":{"field":"data"}}]}]}`))
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, aster.ErrLimit) {
			t.Errorf("err = %.300v, want context.DeadlineExceeded and not a limit", err)
		}
	})
}
