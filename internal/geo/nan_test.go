package geo

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestNaNCoordinatesAsUpstream replays testdata/nan.json (gen_nan.mjs):
// paths, areas and containment of geometries whose coordinates are not
// numbers. d3-geo tests such numbers with JavaScript's truthiness (`!x`,
// `if (x)`, `x || y`), for which NaN is false; reading those as `x == 0`
// or `x != 0` treats NaN as a value (C122, audit 2026-10-18).
func TestNaNCoordinatesAsUpstream(t *testing.T) {
	raw, err := os.ReadFile("testdata/nan.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Get("paths").Items() {
		name := c.Get("name").StrValue()
		p, err := NewProjection(strings.ToLower(name))
		if err != nil {
			t.Fatal(err)
		}
		opts := c.Get("opts").ObjValue()
		for i := 0; i < opts.Len(); i++ {
			if err := p.Set(opts.KeyAt(i), opts.ValueAt(i)); err != nil {
				t.Fatal(err)
			}
		}
		got, ok := p.Path().String(c.Get("geom"))
		want := c.Get("path")
		switch {
		case want.IsNull():
			if ok {
				t.Errorf("%s %v %s: %q; d3 null", name, jsonText(c.Get("opts")), jsonText(c.Get("geom")), got)
			}
		case !ok || got != want.StrValue():
			t.Errorf("%s %v %s:\n got %q\nwant %q", name, jsonText(c.Get("opts")), jsonText(c.Get("geom")), got, want.StrValue())
		}
	}
	for _, m := range doc.Get("measures").Items() {
		geom := m.Get("geom")
		area, err := GeoArea(context.Background(), geom)
		if err != nil {
			t.Fatal(err)
		}
		want := math.NaN()
		if w := m.Get("area"); w.IsNum() {
			want = w.NumValue()
		}
		if area != want && !(math.IsNaN(area) && math.IsNaN(want)) {
			t.Errorf("geoArea(%s) = %v; d3 %v", jsonText(geom), area, want)
		}
		for i, pt := range [][2]float64{{0, 0}, {5, 5}, {130, 30}, {180, 0}} {
			if got, want := GeoContains(geom, pt), m.Get("contains").Index(i).BoolValue(); got != want {
				t.Errorf("geoContains(%s, %v) = %v; d3 %v", jsonText(geom), pt, got, want)
			}
		}
	}
}

// TestAdderDropsNaNPartials replays d3-array's Adder with infinities, whose
// NaN partials `if (lo)` drops (C122).
func TestAdderDropsNaNPartials(t *testing.T) {
	raw, err := os.ReadFile("testdata/nan.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Get("sums").Items() {
		var a adder
		for _, x := range c.Get("xs").Items() {
			a.add(jsval.StringToNumber(x.StrValue()))
		}
		if got, want := jsval.JSNumberString(a.value()), c.Get("sum").StrValue(); got != want {
			t.Errorf("Adder of %s = %s; d3 %s", jsonText(c.Get("xs")), got, want)
		}
	}
}

func jsonText(v jsval.Value) string { return string(jsval.Walker{}.AppendJSON(nil, v)) }
