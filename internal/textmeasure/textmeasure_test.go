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
