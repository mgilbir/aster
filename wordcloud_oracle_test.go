package aster_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// TestWordcloudMatchesNodeOnABoxCanvas compares the wordcloud transform with
// upstream's on every platform. The corpus comparison of vg-gallery/word-cloud
// depends on how node-canvas rasterizes glyphs, which differs between
// platforms, so its status is keyed to one (testdata/oracle-expect.txt) and
// it cannot catch a change in the transform anywhere else (C14, audit
// 2026-10-18). Here both sides lay the words out on the same deterministic
// canvas (the oracle's wordcloud-box.mjs, the engine's
// wordcloud.BoxRenderer), so what differs is the transform itself: how each
// parameter reaches the layout and the tuples it writes (x, y, font, size,
// weight, style, angle). Each variant changes one parameter of the gallery
// spec's transform.
func TestWordcloudMatchesNodeOnABoxCanvas(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs the node oracle")
	}
	o := oracle.For(t, oracle.VL6)
	c := oracleConverter(t, o, aster.WithWordcloudBoxForTest())
	src, err := os.ReadFile("testdata/corpus/vg-gallery/word-cloud.vg.json")
	if err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name  string
		param string
		value string // JSON; "" removes the parameter
	}{
		{"gallery", "", ""},
		{"padding 0", "padding", "0"},
		{"padding 5", "padding", "5"},
		{"padding field", "padding", `{"field":"datum.weight_padding"}`},
		{"spiral rectangular", "spiral", `"rectangular"`},
		{"size", "size", "[500,300]"},
		{"fontSizeRange", "fontSizeRange", "[8,90]"},
		{"constant fontSize", "fontSize", "20"},
		{"constant rotate", "rotate", "90"},
		{"no rotate", "rotate", ""},
		{"font", "font", `"monospace"`},
		{"fontStyle", "fontStyle", `"italic"`},
		{"constant fontWeight", "fontWeight", `"bold"`},
		{"as", "as", `["a","b","c","d","e","f","g"]`},
	}
	placed := regexp.MustCompile(`<text [^>]*transform="translate\(`)
	for _, v := range variants {
		spec := wordcloudVariant(t, src, v.param, v.value)
		want, err := o.WordcloudBoxSVG(spec)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		got, gerr := c.VegaToSVG(spec, asUpstream)
		switch out := errorOutcome(gerr, want.Err); out {
		case "both-error":
			t.Errorf("%s: both refuse it (engine: %v; node: %s): the variant tests nothing", v.name, gerr, want.Err)
			continue
		case "":
		default:
			t.Errorf("%s: %s: engine %v; node %s", v.name, out, gerr, firstLine(want.Err))
			continue
		}
		if n := len(placed.FindAllString(want.SVG, -1)); n < 20 && v.param != "as" {
			t.Errorf("%s: upstream places %d words; the variant must place enough to test the layout", v.name, n)
		}
		d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
		switch {
		case err != nil:
			t.Errorf("%s: %v", v.name, err)
		case !d.Equal:
			t.Errorf("%s: %d diffs; first: %s", v.name, d.DiffCount, firstLine(d.Diff))
		}
	}
}

// wordcloudVariant is the spec with the wordcloud transform's param set to the
// JSON value (removed when value is ""); a field padding reads a per-word
// field the variant adds.
func wordcloudVariant(t *testing.T, src []byte, param, value string) []byte {
	t.Helper()
	spec, err := jsval.ParseJSON(src)
	if err != nil {
		t.Fatal(err)
	}
	if param == "" {
		return src
	}
	var tr jsval.Value
	for _, m := range spec.Get("marks").Items() {
		for _, x := range m.Get("transform").Items() {
			if x.Get("type").AsString() == "wordcloud" {
				tr = x
			}
		}
	}
	if !tr.IsObj() {
		t.Fatal("no wordcloud transform in the spec")
	}
	if value == "" {
		tr.ObjValue().Delete(param)
	} else {
		v, err := jsval.ParseJSON([]byte(value))
		if err != nil {
			t.Fatal(err)
		}
		tr.ObjValue().Set(param, v)
	}
	if strings.Contains(value, "weight_padding") {
		// A per-word padding: 1 for light words, 4 for heavy ones.
		f, err := jsval.ParseJSON([]byte(`{"type":"formula","as":"weight_padding","expr":"datum.weight === 600 ? 4 : 1"}`))
		if err != nil {
			t.Fatal(err)
		}
		data := spec.Get("data").Items()[0]
		data.ObjValue().Set("transform", jsval.Arr(append(data.Get("transform").Items(), f)))
	}
	out := string(jsval.AppendJSON(nil, spec))
	return []byte(out)
}
