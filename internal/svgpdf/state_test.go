package svgpdf

import (
	"bytes"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
)

// contentAfter returns the portion of the content stream following the first
// occurrence of marker (exclusive), or fails if the marker is absent.
func contentAfter(t *testing.T, content, marker string) string {
	t.Helper()
	i := strings.Index(content, marker)
	if i < 0 {
		t.Fatalf("marker %q not found in content stream:\n%s", marker, content)
	}
	return content[i+len(marker):]
}

func convertContent(t *testing.T, svg string) string {
	t.Helper()
	pdf, err := Convert(svg, nil, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	verifyPDF(t, pdf)
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	return contentStreamOf(t, doc)
}

// TestStrokeStateDoesNotLeakToSibling is the reviewer's leak probe: two sibling
// <line>s share the same (unwrapped) graphics context because neither carries a
// transform or clip. The first sets a dash, half-opacity and a round cap; the
// second is bare and must render solid, butt-capped and opaque. Because PDF
// graphics state persists until reset, the second line must therefore re-emit
// the defaults: "0 J", "[] 0 d" and an alpha reset via /GSn gs.
func TestStrokeStateDoesNotLeakToSibling(t *testing.T) {
	svg := `<svg width="100" height="100">` +
		`<line x1="0" y1="10" x2="100" y2="10" stroke="#000" stroke-dasharray="6 3" stroke-opacity="0.5" stroke-linecap="round"/>` +
		`<line x1="0" y1="20" x2="100" y2="20" stroke="#000"/>` +
		`</svg>`
	content := convertContent(t, svg)

	// The first line's stroke ends at the first "S". Everything after it is the
	// second, bare line.
	second := contentAfter(t, content, "\nS\n")

	if !strings.Contains(second, "\n0 J\n") {
		t.Errorf("second line missing butt-cap reset (0 J):\n%s", second)
	}
	if !strings.Contains(second, "\n[] 0 d\n") {
		t.Errorf("second line missing dash reset ([] 0 d):\n%s", second)
	}
	if !strings.Contains(second, " gs\n") {
		t.Errorf("second line missing alpha reset (/GSn gs):\n%s", second)
	}
	// It must not re-establish the first line's dash.
	if strings.Contains(second, "[6 3]") {
		t.Errorf("second line leaked the first line's dash pattern:\n%s", second)
	}
}

// TestStateCacheSurvivesQQ pins the q/Q interaction: a transformed (hence
// q/Q-wrapped) translucent leaf followed by a bare leaf. The wrapper's Q
// restores PDF's graphics state to opaque, so the writer's cached view must be
// restored too — the following opaque leaf must NOT emit a redundant alpha
// reset. A single /GSn gs (inside the wrapper) is expected in the whole stream,
// and none after the Q.
func TestStateCacheSurvivesQQ(t *testing.T) {
	svg := `<svg width="100" height="100">` +
		`<rect transform="translate(5,5)" x="0" y="0" width="10" height="10" fill="#000" opacity="0.5"/>` +
		`<rect x="0" y="40" width="10" height="10" fill="#000"/>` +
		`</svg>`
	content := convertContent(t, svg)

	if got := strings.Count(content, " gs\n"); got != 1 {
		t.Errorf("expected exactly one alpha gs (inside the q/Q wrapper), got %d:\n%s", got, content)
	}
	afterQ := contentAfter(t, content, "\nQ\n")
	if strings.Contains(afterQ, " gs\n") {
		t.Errorf("bare leaf after Q emitted a redundant alpha op; cache not restored across Q:\n%s", afterQ)
	}
}

// TestDeeplyNestedSVGErrors verifies the nesting-depth cap turns a
// pathologically nested document into a clean error rather than a stack
// overflow.
func TestDeeplyNestedSVGErrors(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg width="10" height="10">`)
	const depth = maxNestingDepth + 50
	for i := 0; i < depth; i++ {
		b.WriteString("<g>")
	}
	for i := 0; i < depth; i++ {
		b.WriteString("</g>")
	}
	b.WriteString("</svg>")

	_, err := Convert(b.String(), nil, Options{})
	if err == nil {
		t.Fatal("expected an error for a deeply nested SVG, got none")
	}
	if !strings.Contains(err.Error(), "nesting") {
		t.Errorf("expected a nesting-depth error, got: %v", err)
	}
}

// TestModestNestingAccepted guards the depth cap against false positives: a
// document nested well within the cap must still convert.
func TestModestNestingAccepted(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg width="10" height="10">`)
	const depth = 20 // Vega stays well under this
	for i := 0; i < depth; i++ {
		b.WriteString("<g>")
	}
	b.WriteString(`<rect width="5" height="5" fill="#000"/>`)
	for i := 0; i < depth; i++ {
		b.WriteString("</g>")
	}
	b.WriteString("</svg>")

	if _, err := Convert(b.String(), nil, Options{}); err != nil {
		t.Fatalf("modestly nested SVG should convert: %v", err)
	}
}
