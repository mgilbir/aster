package svgpdf

import (
	"bytes"
	"context"
	"math"
	"os"
	"runtime"
	"testing"

	"github.com/mgilbir/aster/internal/fonts/dejavu"
	"github.com/mgilbir/aster/internal/text"
)

// TestNamedCFFIsCIDFontType0: a CFF font's CIDFont is a CIDFontType0, which
// has no CIDToGIDMap; CIDFontType2 is a TrueType font's (C102).
func TestNamedCFFIsCIDFontType0(t *testing.T) {
	otf, err := os.ReadFile("../text/testdata/CFFTest.otf")
	if err != nil {
		t.Fatal(err)
	}
	m, err := text.New(text.WithFont("CFFTest", otf))
	if err != nil {
		t.Fatal(err)
	}
	svg := `<svg width="100" height="40"><text transform="translate(5,30)" font-family="CFFTest" font-size="20">01Q</text></svg>`
	pdf, err := Convert(svg, m, Options{Text: TextNamed})
	if err != nil {
		t.Fatal(err)
	}
	verifyPDF(t, pdf)
	if !bytes.Contains(pdf, []byte("/CIDFontType0")) || bytes.Contains(pdf, []byte("/CIDFontType2")) || bytes.Contains(pdf, []byte("/CIDToGIDMap")) {
		t.Errorf("a CFF font is not written as a CIDFontType0:\n%s", pdf)
	}
}

// TestConvertDoesNotCopyFontPrograms: Convert reports no font usage, so it
// copies no font program for one (C102); ConvertWithUsage copies each.
func TestConvertDoesNotCopyFontPrograms(t *testing.T) {
	m, err := text.New(text.WithFont("DejaVu Sans", dejavu.SansRegular))
	if err != nil {
		t.Fatal(err)
	}
	svg := `<svg width="100" height="40"><text transform="translate(5,30)" font-family="DejaVu Sans" font-size="20">AB</text></svg>`
	// The least of several runs: TotalAlloc counts the whole process, and
	// whatever else runs only adds to it.
	alloc := func(f func()) uint64 {
		f() // warm
		least := uint64(math.MaxUint64)
		for range 7 {
			var a, b runtime.MemStats
			runtime.ReadMemStats(&a)
			f()
			runtime.ReadMemStats(&b)
			least = min(least, b.TotalAlloc-a.TotalAlloc)
		}
		return least
	}
	plain := alloc(func() { _, _ = Convert(svg, m, Options{Text: TextNamed}) })
	with := alloc(func() { _, _, _ = ConvertWithUsage(svg, m, Options{Text: TextNamed, Context: context.Background()}) })
	if with < plain+uint64(len(dejavu.SansRegular)) {
		t.Errorf("ConvertWithUsage allocates %d bytes, Convert %d: the usage should hold a %d byte copy", with, plain, len(dejavu.SansRegular))
	}
	if plain > uint64(len(dejavu.SansRegular)) {
		t.Errorf("Convert allocates %d bytes, more than the %d byte font it does not report", plain, len(dejavu.SansRegular))
	}
}
