package text

import "testing"

// Pango lays text out in lines: the width of text with line separators is that
// of its widest line, as node-canvas's measureText reports.
func TestMeasureTextWidestLine(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	const font = "12px sans-serif"
	wide, narrow := m.MeasureText("cdefgh", font), m.MeasureText("ab", font)
	if wide <= narrow {
		t.Fatalf("fixture widths %v %v", wide, narrow)
	}
	for _, sep := range []string{"\n", "\r", "\r\n", " ", " "} {
		if got := m.MeasureText("ab"+sep+"cdefgh", font); got != wide {
			t.Errorf("ab%qcdefgh: %v, want %v", sep, got, wide)
		}
		if got := m.MeasureText("cdefgh"+sep+"ab"+sep+sep, font); got != wide {
			t.Errorf("cdefgh%qab%q%q: %v, want %v", sep, sep, sep, got, wide)
		}
	}
	if got := m.MeasureText("\n", font); got != 0 {
		t.Errorf("a lone newline: %v", got)
	}
	// U+0085 and the vertical tab are not line separators to Pango.
	if got := m.MeasureText("ab\u0085cdefgh", font); got <= wide {
		t.Errorf("U+0085 split the text: %v", got)
	}
}
