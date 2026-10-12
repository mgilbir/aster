package aster_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mgilbir/aster"
)

// TestVegaLiteWarningsReachHandler: the Vega-Lite compiler's warnings reach
// WithErrorHandler as Warnings, with upstream's text, from VegaLiteToVega and
// from the render methods; without a handler they are dropped and the call
// succeeds; a handler that returns an error for one fails the call (C38).
func TestVegaLiteWarningsReachHandler(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, tc := range []struct{ name, spec, want string }{
		{"typo'd transform", `{"data":{"values":[{"a":1}]},"mark":"point","transform":[{"calcualte":"1","as":"b"}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
			`Ignoring an invalid transform: {"as":"b","calcualte":"1"}.`},
		{"incompatible channel", `{"data":{"values":[{"a":1}]},"mark":"area","encoding":{"x":{"field":"a","type":"quantitative"},"size":{"field":"a","type":"quantitative"}}}`,
			`size dropped as it is incompatible with "area".`},
		{"invalid aggregate", `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","aggregate":"avg"}}}`,
			`Invalid aggregation operator "avg".`},
	} {
		got, err := compileWarnings(c, []byte(tc.spec))
		if err != nil || !slices.Equal(got, []string{tc.want}) {
			t.Errorf("%s: VegaLiteToVega warnings %q (err %v), want [%q]", tc.name, got, err, tc.want)
		}
		var seen []string
		if _, err := c.VegaLiteToSVG([]byte(tc.spec), aster.WithErrorHandler(func(err error) error {
			var w aster.Warning
			if errors.As(err, &w) {
				seen = append(seen, string(w))
				return nil
			}
			return err
		})); err != nil || !slices.Contains(seen, tc.want) {
			t.Errorf("%s: VegaLiteToSVG warnings %q (err %v), want %q", tc.name, seen, err, tc.want)
		}
		if _, err := c.VegaLiteToVega([]byte(tc.spec)); err != nil {
			t.Errorf("%s: without a handler: %v", tc.name, err)
		}
		stop := errors.New("stop")
		if _, err := c.VegaLiteToVega([]byte(tc.spec), aster.WithErrorHandler(func(error) error { return stop })); !errors.Is(err, stop) {
			t.Errorf("%s: a handler's error: got %v", tc.name, err)
		}
	}
}

// TestVegaLiteWarningTextIsBounded: a Vega-Lite warning carries spec text (an
// invalid transform, with a field name of 100 KB), and reaches the handler cut to 1 KiB, as a
// Vega warning does (docs/limits.md, "warnings").
func TestVegaLiteWarningTextIsBounded(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	field := strings.Repeat("é", 50_000) // 100 KB, two bytes a character
	spec := `{"data":{"values":[{"a":1}]},"mark":"point","transform":[{"calcualte":"1","as":"` + field + `"}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`
	got, err := compileWarnings(c, []byte(spec))
	if err != nil || len(got) == 0 {
		t.Fatalf("warnings %d (err %v), want some", len(got), err)
	}
	for _, w := range got {
		if len(w) > 1024+len("…") || !utf8.ValidString(w) {
			t.Errorf("warning of %d bytes (valid UTF-8 %v), want at most 1 KiB and an ellipsis", len(w), utf8.ValidString(w))
		}
	}
	long := false
	for _, w := range got {
		long = long || strings.HasSuffix(w, "…") && strings.Contains(w, "éé")
	}
	if !long {
		t.Errorf("no warning carries the cut field name: %.200q", got)
	}
}
