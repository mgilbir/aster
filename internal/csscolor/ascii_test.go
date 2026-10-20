package csscolor

import "testing"

// A colour name or function is read ASCII case-insensitively: "WHITE" is
// white, but "wh\u0130te" and "blac\u212A" (the Kelvin sign) are not names,
// as they are not to resvg or a browser; Go's strings.ToLower would fold
// them to "white" and "black".
func TestColourNamesFoldASCIIOnly(t *testing.T) {
	for _, s := range []string{"WHITE", "White", "RGB(1,2,3)", "Hsl(0,0%,0%)", "TRANSPARENT"} {
		if _, ok := Parse(s); !ok {
			t.Errorf("%q is a colour", s)
		}
	}
	for _, s := range []string{"wh\u0130te", "blac\u212A", "l\u0130me", "\u212Ahaki", "WH\u0130TE", "\u017Filver"} {
		if c, ok := Parse(s); ok {
			t.Errorf("%q parsed as %v; CSS folds ASCII only", s, c)
		}
	}
	if ASCIILower("\u00C0BC \u0130 \u212A") != "\u00C0bc \u0130 \u212A" || !ASCIIEqualFold("NoNe", "none") || ASCIIEqualFold("\u017F", "s") || ASCIIEqualFold("\u212A", "k") {
		t.Error("ASCII fold")
	}
}
