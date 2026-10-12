package aster_test

import (
	"go/constant"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/vega"
)

// docs/limits.md's table of fixed bounds says what the code does: each row's
// numbers, in order, are the values of the constants and defaults that
// enforce them, read from the code (unexported constants from the packages'
// source), so a bound changed in the code and not in the doc, or the other
// way round, fails. A row the test does not know fails, as does a bound it
// knows that the table lacks.
func TestFixedLimitsDocMatchesCode(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the engine's packages from source")
	}
	b, err := os.ReadFile("docs/limits.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i, j := strings.Index(doc, "<!-- fixed:begin"), strings.Index(doc, "<!-- fixed:end")
	if i < 0 || j < i {
		t.Fatal("docs/limits.md has no fixed:begin/end block")
	}

	imp := importer.ForCompiler(token.NewFileSet(), "source", nil)
	pkgs := map[string]*types.Package{}
	// c is the value of the constant pkg.name, exported or not.
	c := func(path, name string) int64 {
		t.Helper()
		full := path
		if !strings.Contains(path, ".") {
			full = "github.com/mgilbir/aster/internal/" + path
		}
		p, ok := pkgs[full]
		if !ok {
			if p, err = imp.Import(full); err != nil {
				t.Fatalf("type-checking %s: %v", full, err)
			}
			pkgs[full] = p
		}
		k, ok := p.Scope().Lookup(name).(*types.Const)
		if !ok {
			t.Fatalf("%s.%s is not a constant", path, name)
		}
		v, exact := constant.Int64Val(constant.ToInt(k.Val()))
		if !exact {
			t.Fatalf("%s.%s = %s is not an integer", path, name, k.Val())
		}
		return v
	}
	vl := vega.Limits{}.WithDefaults()
	rl := raster.Limits{}.WithDefaults()
	root := aster.FixedLimitsForTest
	// Each row's numbers, in order; a number that several bounds share lists
	// them all, and each must equal it.
	type slot []int64
	rows := map[string][]slot{
		"geographic path points":         {{int64(vl.MaxPoints)}},
		"projection `clipAngle`":         {{c("geo", "maxClipRadians")}},
		"dataflow evaluations":           {{int64(vl.MaxVisits)}},
		"re-evaluations":                 {{int64(vl.MaxReruns)}, {int64(vl.MaxReruns)}, {c("vega", "maxRunDepth")}},
		"delimited data (CSV, TSV, DSV)": {{c("vega", "maxDSVColumns")}, {c("vega", "dsvCellsPerRow")}},
		"warnings":                       {{c("vega", "maxWarningText")}, {c("vega", "maxWarnings")}},
		"one image": {
			{root["maxImageBytes"], int64(rl.MaxImageBytes), c("svgpdf", "defaultMaxImgBytes")},
			{root["maxImagePixels"], int64(rl.MaxImagePixels), c("svgpdf", "defaultMaxImgPixels")},
		},
		"HTTP response body":              {{c("loader", "defaultMaxResponseBytes")}},
		"file":                            {{c("loader", "defaultMaxResponseBytes")}},
		"JSON nesting":                    {{c("jsval", "MaxJSONDepth")}, {c("jsval", "MaxValueDepth")}},
		"expression nesting":              {{c("expr", "MaxDepth"), c("vegalite", "MaxExprDepth")}},
		"one string an expression builds": {{c("expr", "MaxStringLength")}, {c("expr", "v8MaxStringLength")}},
		"a regular expression compiled from a string": {{c("expr", "MaxPatternLength")}, {c("github.com/mgilbir/goecma262/compiler", "MaxProgramSize")}},
		"SVG input to `SVGToPNG`":                     {{int64(rl.MaxInputBytes)}, {int64(rl.MaxElements)}, {int64(rl.MaxDepth)}},
		"style sheets of SVG input to `SVGToPNG`":     {{int64(rl.MaxStyleBytes)}, {int64(rl.MaxCSSRules)}, {int64(rl.MaxCSSWork)}},
		"one filter region (PNG)":                     {{int64(rl.MaxFilterPixels)}},
		"SVG input to `SVGToPDF`": {
			{c("svgpdf", "defaultMaxInputBytes")}, {c("svgpdf", "defaultMaxElements")}, {c("svgpdf", "defaultMaxSegments")},
			{c("svgpdf", "defaultMaxTextBytes")}, {c("svgpdf", "maxGlyphCacheBytes")}, {c("svgpdf", "maxPaintedBytes")}, {c("svgpdf", "maxDashes")},
		},
		"fonts":        {{c("text", "maxFontBytes")}, {c("text", "maxSystemFontBytes")}, {c("text", "maxSystemFaces")}, {c("text", "programCacheBytes")}, {c("text", "programCacheMaxEntry")}},
		"text shaping": nil, // "about sixty times": DefaultShapingWork's comment derives it
		"caches shared between calls": {
			{c("expr", "programCacheBytes")}, {c("expr", "patternCacheBytes")},
			{c("expr", "programCacheMaxEntry"), c("expr", "patternCacheMaxEntry")},
			{c("scene", "customSymbolCacheBytes")}, {c("scene", "customSymbolCacheMaxEntry")},
			{c("text", "widthCacheBytes")}, {c("text", "widthCacheMaxEntry")}, {c("text", "cssCacheBytes")}, {c("text", "cssCacheMaxEntry")},
			{c("text", "listCacheBytes")}, {c("text", "listCacheMaxEntry")}, {c("text", "outlineCacheBytes")}, {c("text", "outlineCacheMaxEntry")},
		},
		"transforms that multiply rows": {
			{c("transforms", "MaxSequence")}, {c("expr", "MaxSequenceLength")}, {c("transforms", "MaxBins")},
			{c("transforms", "MaxSteps")}, {c("transforms", "MaxPolyOrder")}, {c("transforms/force", "MaxIterations")},
			{c("transforms/force", "MaxForceIterations")}, {c("transforms", "MaxGroupCells")}, {c("transforms/contour", "MaxGridCells")},
		},
		"ticks, labels and legend entries of one guide": {{c("scale", "MaxTicks")}},
		"values a scale's `ticks` lists":                {{c("scale", "maxTickValues")}, {c("scale", "maxLogIter")}, {c("format", "MaxRangeLen")}},
		"colours sampled from a scheme (`schemeCount`)": {{c("scale", "MaxSchemeSamples")}},
		"`config.locale.number`":                        {{c("format", "MaxLocaleSymbol")}, {c("format", "MaxLocaleText")}},
		"`config.locale.time`": {
			{c("format", "MaxLocalePattern")}, {c("format", "MaxLocaleExpansion")}, {c("format", "MaxLocaleName")}, {c("format", "MaxLocaleNames")},
		},
		"format memos of one call (`format`, `timeFormat`, `timeParse` and labels)": {{c("format", "formatMemoBytes")}, {c("format", "formatMemoMaxEntry")}},
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(doc[i:j], "\n") {
		cells := strings.Split(strings.ReplaceAll(line, `\|`, "¦"), "|") // an escaped pipe is text
		if len(cells) != 4 {
			continue
		}
		name := strings.TrimSpace(cells[1])
		if name == "Bound" || strings.HasPrefix(name, "---") {
			continue
		}
		want, ok := rows[name]
		if !ok {
			t.Errorf("row %q is not a bound the test knows", name)
			continue
		}
		seen[name] = true
		got := docQuantities(t, cells[2])
		if len(got) != len(want) {
			t.Errorf("%s: the row has %d numbers %v, the code %d bounds", name, len(got), got, len(want))
			continue
		}
		for k, s := range want {
			for _, v := range s {
				if got[k] != v {
					t.Errorf("%s: number %d is %d in the doc, %d in the code", name, k+1, got[k], v)
				}
			}
		}
	}
	for name := range rows {
		if !seen[name] {
			t.Errorf("bound %q is missing from docs/limits.md's fixed-bounds table", name)
		}
	}
}

var (
	mdLinkText = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdCodeSpan = regexp.MustCompile("`[^`]*`")
	// docQuantity is "2^50", or an integer ("1,000,000") with an optional
	// binary unit, standing alone: not part of a decimal ("6.4e16"), a word
	// or a name ("UTF-16", "ADR 0011" is removed with its link).
	docQuantity = regexp.MustCompile(`(?:^|[^\w.,^\-])(2\^\d+|\d{1,3}(?:,\d{3})+|\d+)(?: (KiB|MiB|GiB|Ki|Mi|Gi))?(?:[^\w.^\-]|[.,](?:\s|$)|$)`)
)

// docQuantities lists the numbers of a fixed-bounds cell, in order.
func docQuantities(t *testing.T, cell string) []int64 {
	t.Helper()
	cell = mdLinkText.ReplaceAllString(cell, "")
	cell = mdCodeSpan.ReplaceAllString(cell, "") // code: an example, not a bound
	var out []int64
	for _, m := range docQuantity.FindAllStringSubmatch(cell, -1) {
		var v int64
		if e, ok := strings.CutPrefix(m[1], "2^"); ok {
			n, _ := strconv.Atoi(e)
			v = 1 << n
		} else {
			v = quantity(t, strings.ReplaceAll(m[1], ",", ""))
		}
		switch m[2] {
		case "KiB", "Ki":
			v <<= 10
		case "MiB", "Mi":
			v <<= 20
		case "GiB", "Gi":
			v <<= 30
		}
		out = append(out, v)
	}
	return out
}
