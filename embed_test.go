package aster_test

import (
	"os"
	"sort"
	"testing"

	"github.com/mgilbir/aster"
)

func sortedGIDs(g []uint16) []uint16 {
	out := append([]uint16(nil), g...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestSVGToPDFUsageAndSubset covers the file-level embedding exports: named-mode
// rendering reports per-face glyph usage, and SubsetFont turns a face plus a GID
// set into a smaller program that still parses and preserves those glyphs.
func TestSVGToPDFUsageAndSubset(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	pdf, uses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatalf("VegaLiteToPDFUsage: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatal("empty PDF")
	}
	if len(uses) == 0 {
		t.Fatal("no font usage reported; expected at least one text face")
	}

	for _, u := range uses {
		if u.PostScriptName == "" {
			t.Errorf("usage with empty PostScriptName")
		}
		if len(u.Source) == 0 {
			t.Errorf("%s: empty Source bytes", u.PostScriptName)
		}
		if len(u.GIDs) == 0 {
			t.Errorf("%s: no GIDs reported", u.PostScriptName)
		}

		subset, ps, err := aster.SubsetFont(u.Source, u.GIDs)
		if err != nil {
			t.Fatalf("SubsetFont(%s): %v", u.PostScriptName, err)
		}
		if ps != u.PostScriptName {
			t.Errorf("SubsetFont PostScriptName = %q, usage = %q", ps, u.PostScriptName)
		}
		if len(subset) == 0 {
			t.Errorf("%s: empty subset", u.PostScriptName)
		}
		if len(subset) >= len(u.Source) {
			t.Errorf("%s: subset (%d B) not smaller than source (%d B)", u.PostScriptName, len(subset), len(u.Source))
		}

		// The subset must itself be a valid font that still preserves the used
		// glyphs: subsetting it again to the same GIDs must succeed.
		if _, _, err := aster.SubsetFont(subset, u.GIDs); err != nil {
			t.Errorf("%s: subset is not a valid re-subsettable font: %v", u.PostScriptName, err)
		}
	}
}

// TestPDFUsageOutlineModeEmpty checks that requesting usage in outline mode is
// safe and reports nothing: outline output references no fonts.
func TestPDFUsageOutlineModeEmpty(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	pdf, uses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextOutlines))
	if err != nil {
		t.Fatalf("VegaLiteToPDFUsage(outlines): %v", err)
	}
	if len(pdf) == 0 {
		t.Fatal("empty PDF")
	}
	if len(uses) != 0 {
		t.Errorf("outline mode reported %d font usages, want 0", len(uses))
	}
}

// TestPDFUsageModeInvariance checks that reported glyph usage is the same
// whether text is embedded or named: the glyphs a chart draws do not depend on
// how the font is stored, so a named render's usage is a faithful basis for the
// subset an embed render would have produced.
func TestPDFUsageModeInvariance(t *testing.T) {
	spec, err := os.ReadFile("testdata/bar-chart.vl.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	_, embedUses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextEmbed))
	if err != nil {
		t.Fatal(err)
	}
	_, namedUses, err := c.VegaLiteToPDFUsage(spec, aster.WithPDFText(aster.PDFTextNamed))
	if err != nil {
		t.Fatal(err)
	}

	byName := func(us []aster.FontUsage) map[string][]uint16 {
		m := map[string][]uint16{}
		for _, u := range us {
			m[u.PostScriptName] = sortedGIDs(u.GIDs)
		}
		return m
	}
	em, nm := byName(embedUses), byName(namedUses)
	if len(em) != len(nm) {
		t.Fatalf("face count differs: embed %d, named %d", len(em), len(nm))
	}
	for ps, eg := range em {
		ng, ok := nm[ps]
		if !ok {
			t.Errorf("face %s missing from named usage", ps)
			continue
		}
		if len(eg) != len(ng) {
			t.Errorf("%s: glyph count differs embed %d vs named %d", ps, len(eg), len(ng))
			continue
		}
		for i := range eg {
			if eg[i] != ng[i] {
				t.Errorf("%s: glyph set differs at %d: embed %d, named %d", ps, i, eg[i], ng[i])
				break
			}
		}
	}
}
