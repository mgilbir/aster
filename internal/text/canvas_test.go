package text

import (
	"testing"

	"github.com/mgilbir/aster/internal/fonts/dejavu"
)

// TestValidCanvasFont checks the font strings a canvas context accepts or
// ignores against what node-canvas 3.2 did with them: setting ctx.font to a
// string it cannot parse leaves the previous font in place. The family list
// never decides (the engine resolves families itself), so only what precedes
// it is parsed.
func TestValidCanvasFont(t *testing.T) {
	for font, want := range map[string]bool{
		"bold 13px sans-serif":     true,
		"italic 10px sans-serif":   true,
		"10px sans-serif":          true,
		"normal normal 12px Arial": true,
		"italic bold 11px \"Helvetica Neue\", Arial, sans-serif": true,
		"bold italic small-caps 12px serif":                      true,
		"1.5em serif":                                            true,
		"12pt Arial":                                             true,
		"600 14px Georgia, serif":                                true,
		"10px/normal monospace":                                  true,
		"oblique 10px sans-serif":                                true,
		"100% sans-serif":                                        true,
		"9px -apple-system, BlinkMacSystemFont":                  true,
		"10px 'unterminated":                                     true,
		"12px":                                                   true,
		"10px ":                                                  true,
		"Franklin Gothic Medium', 'Arial Narrow', Arial, sans-serif 10px sans-serif": false, // a style that is none of the three
		"Ubuntu 10px sans-serif":      false, // Vega-Lite's subtitleFontStyle: "Ubuntu"
		"12 sans-serif":               false,
		"sans-serif":                  false,
		"":                            false,
		"2rem serif":                  false,
		"bold bold 12px Arial":        false,
		"1200 14px Georgia":           false,
		"italic 10.5px/1.2 monospace": false,
		"x-large sans-serif":          false,
	} {
		if got := ValidCanvasFont(font); got != want {
			t.Errorf("ValidCanvasFont(%q) = %v, want %v", font, got, want)
		}
	}
}

func testMeasurer(t *testing.T, opts ...Option) *Measurer {
	t.Helper()
	opts = append([]Option{
		WithFont("DejaVu Sans", dejavu.SansRegular), WithFont("DejaVu Sans", dejavu.SansBold),
		WithFont("DejaVu Sans", dejavu.SansOblique), WithFont("DejaVu Sans", dejavu.SansBoldOblique),
		WithFont("DejaVu Sans Mono", dejavu.MonoRegular), WithFont("DejaVu Sans Mono", dejavu.MonoBold),
		WithDefaultFontFamily("DejaVu Sans"), WithDefaultMonospaceFamily("DejaVu Sans Mono"), WithDefaultSerifFamily("DejaVu Sans"),
	}, opts...)
	m, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestCanvasContextKeepsFontOnInvalid is vega-scenegraph's measureWidth in
// node: a font the context rejects measures with the font it held before, a
// cache hit does not set the context's font, and the width cache is keyed on
// the font string and the text.
func TestCanvasContextKeepsFontOnInvalid(t *testing.T) {
	m := testMeasurer(t, WithExactAdvances())
	c := NewCanvasContext(m)
	const text = "IMF outstanding credit levels"
	bold13 := m.MeasureText(text, "bold 13px sans-serif")
	plain10 := m.MeasureText(text, "10px sans-serif")
	if bold13 == plain10 {
		t.Fatal("test needs two fonts that measure differently")
	}

	// A new context holds the canvas default, 10px sans-serif.
	if got := c.MeasureText(text, "Ubuntu 10px sans-serif"); got != plain10 {
		t.Errorf("first measurement with an invalid font = %v, want the default font's %v", got, plain10)
	}
	c.MeasureText("x", "bold 13px sans-serif") // a valid font sets the context's font
	if got := c.MeasureText(text+"!", "Ubuntu 10px sans-serif"); got != m.MeasureText(text+"!", "bold 13px sans-serif") {
		t.Errorf("invalid font after bold 13px: %v, want the bold measurement", got)
	}
	// The width is cached under the invalid font's own key, so it sticks even
	// though the context's font changes afterwards.
	c.MeasureText("y", "italic 20px sans-serif")
	if got := c.MeasureText(text+"!", "Ubuntu 10px sans-serif"); got != m.MeasureText(text+"!", "bold 13px sans-serif") {
		t.Errorf("cached width changed: %v", got)
	}
	// A hit does not touch the font: this invalid font is new, so it measures
	// with the last font a miss set (italic 20px), not with bold 13px.
	c.MeasureText("x", "bold 13px sans-serif") // hit
	if got := c.MeasureText("z", "Nope 1px x"); got != m.MeasureText("z", "italic 20px sans-serif") {
		t.Errorf("after a cache hit the context font should be unchanged: %v", got)
	}
}

// TestPangoAdvances checks the widths node-canvas reported for DejaVu Sans
// under the two Pango builds the oracle runs on (macOS: Pango 1.57, advances
// rounded to nearest 1/1024 px; Linux: Pango 1.48, floored), bit for bit.
func TestPangoAdvances(t *testing.T) {
	round := testMeasurer(t, WithPangoAdvances(false))
	floor := testMeasurer(t, WithPangoAdvances(true))
	for _, c := range []struct {
		font, text   string
		round, floor float64
	}{
		{"11px sans-serif", "Hello, world", 65.166015625, 65.158203125},
		{"13px sans-serif", "The quick brown fox jumps over the lazy dog", 292.404296875, 292.3798828125},
		{"10.5px sans-serif", "Revenue (USD)", 79.6953125, 79.6884765625},
		{"bold 13px sans-serif", "Yo", 17.15234375, 17.150390625},
		{"bold 13px sans-serif", "Women's football performance vs economic development", 420.6357421875, 0},
		{"bold 12px sans-serif", "AVATAR", 51.08203125, 51.08203125},
		{"italic 10px sans-serif", "Wednesday", 58.4814453125, 58.4814453125},
	} {
		if got := round.MeasureText(c.text, c.font); got != c.round {
			t.Errorf("rounded %s %q = %v, want %v", c.font, c.text, got, c.round)
		}
		if c.floor == 0 {
			continue
		}
		if got := floor.MeasureText(c.text, c.font); got != c.floor {
			t.Errorf("floored %s %q = %v, want %v", c.font, c.text, got, c.floor)
		}
	}
}
