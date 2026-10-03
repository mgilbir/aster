package svgpdf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/fuzzutil"
	"github.com/mgilbir/aster/internal/text"
)

var fuzzLimits = Limits{MaxInputBytes: 1 << 20, MaxElements: 5000, MaxPathSegments: 100_000, MaxTextBytes: 100_000}

// FuzzConvert translates arbitrary SVG to PDF in each text mode. Convert
// recovers a panic into an "internal error", so that counts as a failure here:
// the input is refused with one of the package's errors (all start "svgpdf:",
// and a resource limit wraps ErrLimit), or it yields a PDF that pdf0 parses
// back with a well-formed page tree and a balanced, decodable content stream
// (verifyPDF), in bounded time.
func FuzzConvert(f *testing.F) {
	m, err := text.New()
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range fuzzutil.Files(f, "../../testdata/vl-convert/expected/v5_8/*.svg", "../../testdata/security/*.svg") {
		f.Add(s, uint8(0))
	}
	for _, s := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><g transform="translate(5,5)" clip-path="url(#c)"><rect width="10" height="5" fill="#f00" fill-opacity="0.5" stroke="rgb(0,0,255)" stroke-width="2" stroke-dasharray="3,1"/>` +
			`<path d="M0,0L10,10A5,5,0,1,1,20,0Q1,2,3,4Z" fill="none" stroke="black"/><line x1="0" y1="0" x2="5" y2="5" stroke="green"/>` +
			`<text x="2" y="12" font-family="sans-serif" font-size="9" text-anchor="middle" transform="rotate(-90)">Ünïcode ✓</text></g>` +
			`<defs><clipPath id="c"><rect width="30" height="15"/></clipPath></defs></svg>`,
		`<svg width="1e9" height="-1"><rect width="nan" height="5"/><path d="M1e308,1e308L-1e308,0"/></svg>`,
		`<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY a "b">]><svg width="9" height="9"><text>&a;&#x41;</text></svg>`,
	} {
		f.Add(s, uint8(0))
	}
	f.Fuzz(func(t *testing.T, svg string, mode uint8) {
		opts := Options{Text: TextMode(mode % 3), Limits: fuzzLimits}
		fuzzutil.Within(t, 20*time.Second, "Convert", func() {
			pdf, err := Convert(svg, m, opts)
			if err != nil {
				if !strings.HasPrefix(err.Error(), "svgpdf:") || strings.Contains(err.Error(), "internal error") ||
					errors.Is(err, context.Canceled) {
					t.Fatalf("%q: error %q", svg, err)
				}
				return
			}
			verifyPDF(t, pdf)
		})
	})
}
