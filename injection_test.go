package aster_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// Spec text is data, not code: strings containing JS template-literal
// metacharacters (${...}, backticks, quotes, backslashes) must render as
// literal text and never be interpreted or break the render.
func TestSpecTemplateMetacharsAreInert(t *testing.T) {
	titles := []string{
		"Price ${revenue} report",
		"back`tick and ${nested}",
		`quote " and backslash \ end`,
		"${(()=>{throw new Error('INJECTED-EXEC')})()}",
	}

	c, err := aster.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	for _, title := range titles {
		t.Run(title, func(t *testing.T) {
			spec := []byte(`{
				"$schema": "https://vega.github.io/schema/vega-lite/v5.json",
				"title": ` + jsonString(title) + `,
				"data": {"values": [{"a": "A", "b": 28}]},
				"mark": "bar",
				"encoding": {"x": {"field": "a", "type": "nominal"}, "y": {"field": "b", "type": "quantitative"}}
			}`)

			svg, err := c.VegaLiteToSVG(spec)
			if err != nil {
				t.Fatalf("render failed on spec with special chars in title: %v", err)
			}
			if !strings.HasPrefix(svg, "<svg") {
				t.Fatalf("expected SVG output, got: %.80s", svg)
			}
		})
	}
}

// Theme JSON is also passed as inert data; special characters must not break
// the render (a backtick previously terminated the template literal).
func TestThemeSpecialCharsAreInert(t *testing.T) {
	spec := []byte(`{
		"$schema": "https://vega.github.io/schema/vega-lite/v5.json",
		"data": {"values": [{"a": "A", "b": 28}]},
		"mark": "bar",
		"encoding": {"x": {"field": "a", "type": "nominal"}, "y": {"field": "b", "type": "quantitative"}}
	}`)

	// A theme whose title text contains backticks and ${...}.
	theme := "{\"title\": {\"subtitle\": \"back`tick ${x}\"}}"

	c, err := aster.New(aster.WithTheme(theme))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		t.Fatalf("render failed with special chars in theme: %v", err)
	}
	if !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("expected SVG output, got: %.80s", svg)
	}
}

// jsonString returns a minimal JSON-encoded string literal for embedding a
// title into a spec fixture. Kept trivial to avoid importing encoding/json
// noise into every test.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
