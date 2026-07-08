package textmeasure

import (
	"testing"

	"github.com/go-text/typesetting/font"
)

func TestParseCSSFont(t *testing.T) {
	tests := []struct {
		input  string
		style  font.Style
		weight font.Weight
		size   float64
		family string // first family
	}{
		{
			input:  "11px sans-serif",
			style:  font.StyleNormal,
			weight: font.WeightNormal,
			size:   11,
			family: "sans-serif",
		},
		{
			input:  "bold 14px Arial",
			style:  font.StyleNormal,
			weight: font.WeightBold,
			size:   14,
			family: "Arial",
		},
		{
			input:  "italic bold 14px Arial, Helvetica, sans-serif",
			style:  font.StyleItalic,
			weight: font.WeightBold,
			size:   14,
			family: "Arial",
		},
		{
			input:  "italic 700 12px 'Times New Roman'",
			style:  font.StyleItalic,
			weight: font.Weight(700),
			size:   12,
			family: "Times New Roman",
		},
		{
			input:  "16px monospace",
			style:  font.StyleNormal,
			weight: font.WeightNormal,
			size:   16,
			family: "monospace",
		},
		{
			input:  "",
			style:  font.StyleNormal,
			weight: font.WeightNormal,
			size:   11,
			family: "sans-serif",
		},
		{
			// pt is converted to px (1pt = 4/3 px), not silently treated as px.
			input:  "12pt Arial",
			style:  font.StyleNormal,
			weight: font.WeightNormal,
			size:   16,
			family: "Arial",
		},
		{
			// em resolves against the CSS default root font size (16px).
			input:  "1.5em serif",
			style:  font.StyleNormal,
			weight: font.WeightNormal,
			size:   24,
			family: "serif",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseCSSFont(tt.input)
			if got.Style != tt.style {
				t.Errorf("style: got %v, want %v", got.Style, tt.style)
			}
			if got.Weight != tt.weight {
				t.Errorf("weight: got %v, want %v", got.Weight, tt.weight)
			}
			if got.Size != tt.size {
				t.Errorf("size: got %v, want %v", got.Size, tt.size)
			}
			if len(got.Family) == 0 || got.Family[0] != tt.family {
				t.Errorf("family: got %v, want %v", got.Family, tt.family)
			}
		})
	}
}

func TestMeasureText(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Basic sanity checks.
	w := m.MeasureText("Hello", "11px sans-serif")
	if w <= 0 {
		t.Errorf("expected positive width, got %v", w)
	}

	// Longer text should be wider.
	w2 := m.MeasureText("Hello, World!", "11px sans-serif")
	if w2 <= w {
		t.Errorf("longer text should be wider: %v <= %v", w2, w)
	}

	// Larger font should be wider.
	w3 := m.MeasureText("Hello", "24px sans-serif")
	if w3 <= w {
		t.Errorf("larger font should be wider: %v <= %v", w3, w)
	}

	// Empty text should be zero.
	w4 := m.MeasureText("", "11px sans-serif")
	if w4 != 0 {
		t.Errorf("empty text should be 0, got %v", w4)
	}
}

// The generic "monospace" family must resolve to the embedded monospace face,
// not silently fall back to the sans-serif default. Proportional text ("il")
// is far narrower than fixed-width text in a real monospace font, so the two
// advances must differ; before monospace resolution was wired in, "monospace"
// fell through to Liberation Sans and the advances matched.
func TestMonospaceResolvesToMonoFace(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	const probe = "iiill" // very narrow in a proportional font, uniform in mono
	sans := m.MeasureText(probe, "13px sans-serif")
	mono := m.MeasureText(probe, "13px monospace")
	if sans <= 0 || mono <= 0 {
		t.Fatalf("zero advance: sans=%.2f mono=%.2f", sans, mono)
	}
	if mono <= sans*1.2 {
		t.Fatalf("monospace advance %.2f not distinctly wider than sans %.2f — monospace likely still falling back to sans", mono, sans)
	}
}

// An explicit override family is honored for the monospace generic.
func TestWithDefaultMonospaceFamilyOverride(t *testing.T) {
	// Overriding to the sans family collapses the mono/sans distinction.
	m, err := New(WithDefaultMonospaceFamily("Liberation Sans"))
	if err != nil {
		t.Fatal(err)
	}
	const probe = "iiill"
	sans := m.MeasureText(probe, "13px sans-serif")
	mono := m.MeasureText(probe, "13px monospace")
	if mono != sans {
		t.Fatalf("with monospace overridden to the sans family, advances should match: sans=%.2f mono=%.2f", sans, mono)
	}
}
