package aster_test

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/fuzzutil"
)

// flat is the equirectangular projection as a caller would register it: the
// raw projection the built-in one has, its default scale, and a "ratio"
// parameter stretching latitude (1 by default).
var flat = aster.Projection{
	Raw: func(params map[string]float64) aster.RawProjection {
		r := params["ratio"]
		return aster.RawProjection{
			Forward: func(lambda, phi float64) (float64, float64) { return lambda, r * phi },
			Invert:  func(x, y float64) (float64, float64) { return x, y / r },
		}
	},
	Params: map[string]float64{"ratio": 1},
	Scale:  152.63,
}

// worldSpec draws the world's countries, a graticule and a few points with
// projection settings props (a JSON object's members, without braces).
func worldSpec(typ, props string) string {
	if typ != "" {
		typ = `"type": "` + typ + `",`
	}
	return `{
  "width": 500, "height": 300,
  "signals": [{"name": "r", "value": 1}, {"name": "lon", "value": -20}],
  "data": [
    {"name": "world", "url": "data/world-110m.json", "format": {"type": "topojson", "feature": "countries"}},
    {"name": "graticule", "transform": [{"type": "graticule", "step": [30, 30]}]},
    {"name": "points", "values": [{"lon": -74, "lat": 40.7}, {"lon": 139.7, "lat": 35.7}, {"lon": 2.35, "lat": 48.85}],
     "transform": [{"type": "geopoint", "projection": "p", "fields": ["lon", "lat"]}]}
  ],
  "projections": [{"name": "p", ` + typ + props + `}],
  "marks": [
    {"type": "shape", "from": {"data": "graticule"}, "encode": {"update": {"stroke": {"value": "#ccc"}, "fill": {"value": null}}}, "transform": [{"type": "geoshape", "projection": "p"}]},
    {"type": "shape", "from": {"data": "world"}, "encode": {"update": {"fill": {"value": "#9b9"}, "stroke": {"value": "#fff"}}}, "transform": [{"type": "geoshape", "projection": "p"}]},
    {"type": "symbol", "from": {"data": "points"}, "encode": {"update": {"x": {"field": "x"}, "y": {"field": "y"}, "size": {"value": 30}}}},
    {"type": "text", "encode": {"update": {"text": {"signal": "format(invert('p', [250, 150])[0], '.4f')"}, "x": {"value": 5}, "y": {"value": 15}}}}
  ]
}`
}

// TestCustomProjectionRendersAsBuiltIn checks that a registered projection
// whose raw projection and defaults are a built-in one's renders exactly as
// the built-in one does, with every projection property, fitting, a
// parameter set by a signal, and from Vega-Lite.
func TestCustomProjectionRendersAsBuiltIn(t *testing.T) {
	c, err := aster.New(aster.WithLoader(&aster.FileLoader{BaseDir: "testdata/vega-datasets"}), aster.WithProjection("Flat", flat))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, props := range []string{
		`"scale": 80`,
		`"fit": {"signal": "data('world')"}, "size": [500, 300]`,
		`"rotate": [{"signal": "lon"}, -30, 10], "center": [10, 20], "translate": [240, 160], "scale": 70`,
		`"clipAngle": 70, "rotate": [100, -40], "precision": 0.2, "scale": 90`,
		`"clipExtent": [[40, 30], [400, 250]], "reflectX": true, "reflectY": true, "scale": 60`,
		`"scale": 75, "ratio": 1`,
		`"scale": 75, "ratio": {"signal": "r"}`,
	} {
		want, err := c.VegaToSVG([]byte(worldSpec("equirectangular", props)))
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range []string{"flat", "FLAT"} {
			got, err := c.VegaToSVG([]byte(worldSpec(typ, props)))
			if err != nil {
				t.Fatalf("%s {%s}: %v", typ, props, err)
			}
			if got != want {
				t.Errorf("%s {%s}: differs from equirectangular", typ, props)
			}
		}
	}

	// A parameter changes the raw projection: through a signal (the
	// projection operator re-running with the parameter modified) and set
	// directly, alike.
	base := `"scale": 75, "ratio": {"signal": "r"}`
	one, _ := c.VegaToSVG([]byte(worldSpec("flat", base)))
	two, err := c.VegaToSVG([]byte(worldSpec("flat", base)), aster.WithSignal("r", 2))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := c.VegaToSVG([]byte(worldSpec("flat", `"scale": 75, "ratio": 2`)))
	if err != nil {
		t.Fatal(err)
	}
	// A signal written after the first run does not grow the autosized view
	// upstream either, so the geometry and the inverted point (the label)
	// are compared, not the whole document.
	if drawing(two) == drawing(one) || drawing(two) != drawing(direct) {
		t.Errorf("ratio 2: changed %v, as set directly %v", drawing(two) != drawing(one), drawing(two) == drawing(direct))
	}

	// Vega-Lite names it too.
	vl := func(typ string) string {
		return `{"width": 400, "height": 250, "data": {"url": "data/world-110m.json", "format": {"type": "topojson", "feature": "countries"}},
			"projection": {"type": "` + typ + `"}, "mark": "geoshape"}`
	}
	want, err := c.VegaLiteToSVG([]byte(vl("equirectangular")))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.VegaLiteToSVG([]byte(vl("flat"))); err != nil || got != want {
		t.Errorf("Vega-Lite: %v, same as equirectangular %v", err, got == want)
	}
}

// drawing is an SVG's path data and text, in order.
func drawing(svg string) string {
	return strings.Join(drawingParts.FindAllString(svg, -1), "\n")
}

var drawingParts = regexp.MustCompile(` d="[^"]*"|<text[^>]*>[^<]*`)

// TestCustomProjectionScope checks that the types are the Converter's own,
// and that a registered type replaces a built-in one of its name, also the
// default (a projection with no type is Mercator).
func TestCustomProjectionScope(t *testing.T) {
	loader := aster.WithLoader(&aster.FileLoader{BaseDir: "testdata/vega-datasets"})
	plain, err := aster.New(loader)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := plain.VegaToSVG([]byte(worldSpec("flat", `"scale": 80`))); err == nil || !strings.Contains(err.Error(), "unrecognized projection type: flat") {
		t.Errorf("unregistered type: %v", err)
	}
	equi, err := plain.VegaToSVG([]byte(worldSpec("equirectangular", `"scale": 80`)))
	if err != nil {
		t.Fatal(err)
	}

	over, err := aster.New(loader, aster.WithProjection("mercator", flat))
	if err != nil {
		t.Fatal(err)
	}
	defer over.Close()
	for _, typ := range []string{"mercator", "Mercator", ""} {
		got, err := over.VegaToSVG([]byte(worldSpec(typ, `"scale": 80`)))
		if err != nil {
			t.Fatal(err)
		}
		if got != equi {
			t.Errorf("type %q is not the registered projection", typ)
		}
	}
	// The other converter's Mercator is still the built-in one.
	merc, err := plain.VegaToSVG([]byte(worldSpec("mercator", `"scale": 80`)))
	if err != nil {
		t.Fatal(err)
	}
	if merc == equi {
		t.Error("registering mercator on one converter changed another's")
	}
}

// TestWithProjectionRefuses checks the definitions New refuses.
func TestWithProjectionRefuses(t *testing.T) {
	ok := func(map[string]float64) aster.RawProjection {
		return aster.RawProjection{Forward: func(x, y float64) (float64, float64) { return x, y }}
	}
	for _, tc := range []struct {
		name string
		p    aster.Projection
		want string
	}{
		{"", aster.Projection{Raw: ok}, "empty projection name"},
		{"a", aster.Projection{}, "no Raw function"},
		{"a", aster.Projection{Raw: func(map[string]float64) aster.RawProjection { return aster.RawProjection{} }}, "no Forward function"},
		{"a", aster.Projection{Raw: func(map[string]float64) aster.RawProjection { panic("boom") }}, "Raw panicked"},
		{"a", aster.Projection{Raw: ok, Params: map[string]float64{"radius": 1, "size": 2}}, `parameter "size" is not one Vega forwards`},
		{"a", aster.Projection{Raw: ok, Params: map[string]float64{"radius": math.NaN()}}, "not finite"},
		{"a", aster.Projection{Raw: ok, Scale: math.Inf(1)}, "scale +Inf is not finite"},
		{"a", aster.Projection{Raw: ok, ClipAngle: 1e300}, "clipAngle"},
	} {
		c, err := aster.New(aster.WithProjection(tc.name, tc.p))
		if err == nil {
			c.Close()
			t.Errorf("%q %+v: New succeeded", tc.name, tc.p)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %q, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

// TestCustomProjectionFailures checks that a projection's code failing during
// a render fails the render with an error, and that a hostile raw projection
// (non-finite, huge or erratic coordinates) renders or fails in bounded time.
func TestCustomProjectionFailures(t *testing.T) {
	loader := aster.WithLoader(&aster.FileLoader{BaseDir: "testdata/vega-datasets"})
	panicky := aster.Projection{Raw: func(params map[string]float64) aster.RawProjection {
		bad := params["radius"] != 0
		return aster.RawProjection{Forward: func(x, y float64) (float64, float64) {
			if bad && x > 1 {
				panic("projection bug")
			}
			return x, y
		}, Invert: func(x, y float64) (float64, float64) { return x, y }}
	}, Params: map[string]float64{"radius": 0}}
	noForward := aster.Projection{Raw: func(params map[string]float64) aster.RawProjection {
		if params["radius"] != 0 {
			return aster.RawProjection{}
		}
		return aster.RawProjection{Forward: func(x, y float64) (float64, float64) { return x, y }}
	}, Params: map[string]float64{"radius": 0}}
	c, err := aster.New(loader, aster.WithProjection("panicky", panicky), aster.WithProjection("noforward", noForward))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.VegaToSVG([]byte(worldSpec("panicky", `"radius": 1`))); err == nil || !strings.Contains(err.Error(), "projection bug") {
		t.Errorf("a panicking Forward: %v", err)
	}
	if _, err := c.VegaToSVG([]byte(worldSpec("noforward", `"radius": 1`))); err == nil || !strings.Contains(err.Error(), "no Forward function") {
		t.Errorf("a Raw returning no Forward for a parameter: %v", err)
	}
	// The converter still works after both.
	if _, err := c.VegaToSVG([]byte(worldSpec("panicky", `"scale": 80`))); err != nil {
		t.Errorf("after the failures: %v", err)
	}

	hostile := []func(x, y float64) (float64, float64){
		func(x, y float64) (float64, float64) { return math.NaN(), y },
		func(x, y float64) (float64, float64) { return math.Inf(1), math.Inf(-1) },
		func(x, y float64) (float64, float64) { return x * 1e300, y * 1e300 },
		func(x, y float64) (float64, float64) { return math.Tan(x * 50), math.Tan(y * 50) },
		func(x, y float64) (float64, float64) { return math.Sin(x * 1e6), math.Cos(y * 1e6) },
	}
	for i, f := range hostile {
		f := f
		hc, err := aster.New(loader, aster.WithProjection("h", aster.Projection{Raw: func(map[string]float64) aster.RawProjection {
			return aster.RawProjection{Forward: f, Invert: f}
		}}))
		if err != nil {
			t.Fatal(err)
		}
		for _, props := range []string{`"scale": 80`, `"fit": {"signal": "data('world')"}, "size": [500, 300]`, `"clipAngle": 60`} {
			fuzzutil.Within(t, 60*time.Second, fmt.Sprintf("hostile %d {%s}", i, props), func() {
				_, err := hc.VegaToSVG([]byte(worldSpec("h", props)))
				if isEngineBug(err) {
					t.Errorf("hostile %d {%s}: %v", i, props, err)
				}
			})
		}
		hc.Close()
	}
}

// TestCustomProjectionConcurrent renders with one registered type from many
// goroutines at once (under -race, the parameters handed to Raw must not be
// shared).
func TestCustomProjectionConcurrent(t *testing.T) {
	c, err := aster.New(aster.WithLoader(&aster.FileLoader{BaseDir: "testdata/vega-datasets"}), aster.WithProjection("flat", flat))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	want := map[float64]string{}
	for _, r := range []float64{1, 1.5, 2} {
		if want[r], err = c.VegaToSVG([]byte(worldSpec("flat", `"scale": 75, "ratio": {"signal": "r"}`)), aster.WithSignal("r", r)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := []float64{1, 1.5, 2}[i%3]
			got, err := c.VegaToSVG([]byte(worldSpec("flat", `"scale": 75, "ratio": {"signal": "r"}`)), aster.WithSignal("r", r))
			if err == nil && got != want[r] {
				err = errors.New("output differs from a lone render's")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}
