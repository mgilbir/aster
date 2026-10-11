package aster_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/fonts/dejavu"
	"github.com/mgilbir/aster/internal/fonts/liberation"
)

// pdfFaces renders txt in font (at weight) to PDF and returns the PostScript
// names of the faces that drew it, sorted.
func pdfFaces(t *testing.T, font, weight, txt string, opts ...aster.Option) []string {
	t.Helper()
	c, err := aster.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := `{"mark":{"type":"text","font":"` + font + `","fontWeight":"` + weight + `"},"data":{"values":[{"a":"` + txt + `"}]},"encoding":{"text":{"field":"a"}}}`
	_, usage, err := c.VegaLiteToPDFUsage([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range usage {
		names = append(names, u.PostScriptName)
	}
	slices.Sort(names)
	return names
}

// Which face draws a character is the caller's to decide: a registered family
// is made of the faces registered for it, the last of equal fit wins, and
// WithFontFallback sets the chain for characters the family lacks.
func TestCallerControlsFonts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		font, weight, text string
		opts               []aster.Option
		want               []string
	}{
		{"a registered family replaces an embedded one of that name",
			"Liberation Sans", "normal", "a", []aster.Option{aster.WithFont("Liberation Sans", dejavu.SansRegular)}, []string{"DejaVuSans"}},
		{"...for every weight: the embedded bold is not that family any more",
			"Liberation Sans", "bold", "a", []aster.Option{aster.WithFont("Liberation Sans", dejavu.SansRegular)}, []string{"DejaVuSans"}},
		{"registering Arial replaces its metric alias, Liberation Sans",
			"Arial", "normal", "a", []aster.Option{aster.WithFont("Arial", dejavu.SansRegular)}, []string{"DejaVuSans"}},
		{"of faces of equal fit the last registered wins",
			"X", "normal", "a", []aster.Option{aster.WithFont("X", dejavu.SansRegular), aster.WithFont("X", liberation.SerifRegular)}, []string{"LiberationSerif"}},
		{"...in either order",
			"X", "normal", "a", []aster.Option{aster.WithFont("X", liberation.SerifRegular), aster.WithFont("X", dejavu.SansRegular)}, []string{"DejaVuSans"}},
		{"a better fit beats a later registration",
			"X", "bold", "a", []aster.Option{aster.WithFont("X", liberation.SerifBold), aster.WithFont("X", dejavu.SansRegular)}, []string{"LiberationSerif-Bold"}},
		{"by default registered fonts are the first fallback",
			"Liberation Serif", "normal", "☃", []aster.Option{aster.WithFont("D", dejavu.SansRegular)}, []string{"DejaVuSans"}},
		{"WithFontFallback chooses the fallback",
			"Liberation Serif", "normal", "☃", []aster.Option{aster.WithFont("D", dejavu.SansRegular), aster.WithFontFallback("Noto Emoji", "D")}, []string{"NotoEmoji-Regular"}},
		{"by default emoji fall back to Noto Emoji",
			"Liberation Serif", "normal", "a😀", nil, []string{"LiberationSerif", "NotoEmoji-Regular"}},
		{"no fallback families: no fallback",
			"Liberation Serif", "normal", "a😀", []aster.Option{aster.WithFontFallback()}, []string{"LiberationSans", "LiberationSerif"}},
	} {
		if got := pdfFaces(t, tc.font, tc.weight, tc.text, tc.opts...); !slices.Equal(got, tc.want) {
			t.Errorf("%s: drawn with %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFontFallbackWithoutAFontFails(t *testing.T) {
	if _, err := aster.New(aster.WithFontFallback("No Such Family")); err == nil || !strings.Contains(err.Error(), "No Such Family") {
		t.Errorf("err = %v, want New to name the missing fallback family", err)
	}
}

// New loads the fonts it is given, so one that does not load is refused
// there, with or without text measurement, and not on every render.
func TestNewRefusesABrokenFont(t *testing.T) {
	for _, opts := range [][]aster.Option{
		{aster.WithFont("Bad", []byte("not a font"))},
		{aster.WithFont("Bad", []byte("not a font")), aster.WithTextMeasurement(false)},
		{aster.WithFontFace("Bad", []byte("not a font"), 1)},
	} {
		if _, err := aster.New(opts...); err == nil || !strings.Contains(err.Error(), `"Bad"`) {
			t.Errorf("err = %v, want New to refuse the font", err)
		}
	}
	// A good one is ready for use: the first render loads nothing more.
	c, err := aster.New(aster.WithFont("Good", dejavu.SansRegular))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.VegaLiteToSVG([]byte(`{"mark":{"type":"text","font":"Good"},"data":{"values":[{"a":"x"}]},"encoding":{"text":{"field":"a"}}}`)); err != nil {
		t.Fatal(err)
	}
}
