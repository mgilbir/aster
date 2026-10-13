package raster

import (
	"errors"
	"strings"
	"testing"
)

// A clip's coverage map lives as long as the element it clips, and nested
// clips hold one each: they are charged to the canvas budget, and given back
// with their element (C103). Forty nested circular clips over a 200x200
// canvas hold forty 40,000-byte maps, more than a budget of four canvases
// leaves for them; one clip at a time, as siblings, fits.
func TestClipCoverageCharged(t *testing.T) {
	head := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="200"><defs><clipPath id="c"><circle cx="100" cy="100" r="99"/></clipPath></defs>`
	nested := head + strings.Repeat(`<g clip-path="url(#c)">`, 40) + `<rect width="200" height="200"/>` + strings.Repeat(`</g>`, 40) + `</svg>`
	siblings := head + strings.Repeat(`<g clip-path="url(#c)"><rect width="200" height="200"/></g>`, 40) + `</svg>`
	lim := Limits{MaxCanvasBytes: 4 * 200 * 200 * 4}
	if _, err := Render([]byte(nested), Options{Limits: lim}); !errors.Is(err, errLimit) {
		t.Errorf("40 nested clips: err = %v", err)
	}
	if _, err := Render([]byte(siblings), Options{Limits: lim}); err != nil {
		t.Errorf("40 sibling clips: %v", err)
	}
}
