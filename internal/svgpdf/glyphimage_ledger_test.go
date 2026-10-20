package svgpdf

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// TestGlyphImagesCharged is C20's class in the PDF writer (audit 2026-10-18,
// routed to this PR): a bitmap colour glyph's PNG is decoded, converted and
// written as an image, each allocation charged to the call's ledger before
// it is made, as an <image>'s are. The sbix glyph is drawn from its largest
// strike, 80 by 80 pixels.
func TestGlyphImagesCharged(t *testing.T) {
	m := colourShaper(t)
	peak := func(s string) int64 {
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><text x="10" y="80" font-family="SbixTest" font-size="60">` + s + `</text></svg>`
		if _, err := Convert(svg, m, Options{Context: budget.WithLedger(context.Background(), l)}); err != nil {
			t.Fatal(err)
		}
		return l.PeakBytes()
	}
	const px = 80 * 80
	if d := peak("\U0001F600") - peak("A"); d < 2*4*px {
		t.Errorf("the glyph's image raised the ledger's peak by %d bytes; decoding it and its planes take at least %d", d, 2*4*px)
	}
}
