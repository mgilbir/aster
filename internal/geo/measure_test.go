package geo

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

type goldenPathOptions struct {
	Digits []struct {
		Digits *float64                   `json:"digits"`
		Out    map[string]json.RawMessage `json:"out"`
	} `json:"digits"`
	Radii []struct {
		R          any             `json:"r"`
		Point      json.RawMessage `json:"point"`
		Multipoint json.RawMessage `json:"multipoint"`
	} `json:"radii"`
}

func TestPathDigitsAndRadius(t *testing.T) {
	var g goldenPathOptions
	data, err := readFile("testdata/digits.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	geoms := corpus(t)
	for _, d := range g.Digits {
		for k, raw := range d.Out {
			p, _ := NewProjection("mercator")
			path := p.Path()
			if d.Digits == nil {
				path.SetDigits(-1)
			} else {
				path.SetDigits(int(*d.Digits))
			}
			s, ok := path.String(geoms[k])
			label := "digits/" + k
			if d.Digits != nil {
				label += "/" + jsval.JSNumberString(*d.Digits)
			}
			checkPath(t, label, s, ok, raw)
		}
	}
	for _, r := range g.Radii {
		p, _ := NewProjection("mercator")
		path := p.Path()
		switch v := r.R.(type) {
		case float64:
			path.SetPointRadius(v)
		default: // "fn": the radius depends on the object's first coordinate
			path.SetPointRadiusFunc(func(o jsval.Value) float64 {
				c := o.Get("coordinates")
				if c.IsArr() {
					return coord(c.Index(0)) / 20
				}
				return 3
			})
		}
		s, ok := path.String(geoms["point"])
		checkPath(t, "radius point", s, ok, r.Point)
		s, ok = path.String(geoms["multipoint"])
		checkPath(t, "radius multipoint", s, ok, r.Multipoint)
	}
}

type goldenFit struct {
	Cfg struct {
		Type  string          `json:"type"`
		Props json.RawMessage `json:"props"`
	} `json:"cfg"`
	Target    string          `json:"target"`
	Mode      string          `json:"mode"`
	OK        bool            `json:"ok"`
	Scale     any             `json:"scale"`
	Translate []any           `json:"translate"`
	Probe     []any           `json:"probe"`
	Path      json.RawMessage `json:"path"`
}

func TestFit(t *testing.T) {
	var g []goldenFit
	loadGolden(t, "fit.json.gz", &g)
	geoms := corpus(t)
	geoms["usStates"] = geoms["states"]
	for _, c := range g {
		if !c.OK {
			continue
		}
		label := c.Cfg.Type + "/" + c.Target + "/" + c.Mode + " " + string(c.Cfg.Props)
		p := projFromGolden(t, c.Cfg.Type, c.Cfg.Props)
		obj := geoms[c.Target]
		switch c.Mode {
		case "extent":
			p.FitExtent(12, 8, 512, 308, obj)
		case "size":
			p.FitSize(640, 360, obj)
		case "width":
			p.FitWidth(500, obj)
		case "height":
			p.FitHeight(300, obj)
		}
		tx, ty := p.Translate()
		if !same(p.Scale(), gnum(c.Scale)) || !same(tx, gnum(c.Translate[0])) || !same(ty, gnum(c.Translate[1])) {
			t.Errorf("%s: scale %v translate [%v %v], want %v %v", label, p.Scale(), tx, ty, c.Scale, c.Translate)
			continue
		}
		for i, pt := range [][2]float64{{0, 0}, {-100, 40}, {10, 50}, {170, -20}} {
			x, y, ok := p.Forward(pt[0], pt[1])
			wx, wy, wok := pairOfGolden(c.Probe[i])
			if ok != wok && !(!ok && !wok) {
				if wok {
					t.Errorf("%s: probe %v unavailable", label, pt)
				}
				continue
			}
			if wok && (!same(x, wx) || !same(y, wy)) {
				t.Errorf("%s: probe %v = [%v %v] want [%v %v]", label, pt, x, y, wx, wy)
			}
		}
		s, ok := p.Path().String(obj)
		checkPath(t, label, s, ok, c.Path)
	}
}

type goldenMeasures struct {
	Spherical map[string]struct {
		Area     any `json:"area"`
		Bounds   any `json:"bounds"`
		Centroid any `json:"centroid"`
		Length   any `json:"length"`
		Contains any `json:"contains"`
	} `json:"spherical"`
	Dist []struct {
		A     [2]float64 `json:"a"`
		B     [2]float64 `json:"b"`
		D     any        `json:"d"`
		I     [][]any    `json:"i"`
		IDist any        `json:"idist"`
	} `json:"dist"`
	Rot []struct {
		R []float64 `json:"r"`
		F [][]any   `json:"f"`
		I [][]any   `json:"i"`
	} `json:"rot"`
	Circles []struct {
		C   [2]float64      `json:"c"`
		R   float64         `json:"r"`
		Pr  float64         `json:"pr"`
		Out json.RawMessage `json:"out"`
	} `json:"circles"`
	Planar []struct {
		Cfg struct {
			Type  string          `json:"type"`
			Props json.RawMessage `json:"props"`
		} `json:"cfg"`
		K        string `json:"k"`
		Area     any    `json:"area"`
		Measure  any    `json:"measure"`
		Bounds   any    `json:"bounds"`
		Centroid any    `json:"centroid"`
	} `json:"planar"`
}

func TestSphericalMeasures(t *testing.T) {
	var g goldenMeasures
	data, err := readFile("testdata/measures.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	geoms := corpus(t)
	probes := [][2]float64{{0, 0}, {-100, 40}, {10, 55}, {170, -18}, {-179.9, -15}, {0, 90}, {100, 62}, {-3, 60}}
	for k, want := range g.Spherical {
		obj := geoms[k]
		if s, ok := want.Area.(string); !ok || s != "ERR" {
			if got := GeoArea(obj); !same(got, gnum(want.Area)) {
				t.Errorf("%s: area %v want %v", k, got, want.Area)
			}
		}
		if _, ok := want.Bounds.(string); !ok {
			b := GeoBounds(obj)
			w := want.Bounds.([]any)
			w0, w1 := w[0].([]any), w[1].([]any)
			exp := [2][2]float64{{gnum(w0[0]), gnum(w0[1])}, {gnum(w1[0]), gnum(w1[1])}}
			if !sameBox(b, exp) {
				t.Errorf("%s: bounds %v want %v", k, b, exp)
			}
		}
		if _, ok := want.Centroid.(string); !ok {
			c := GeoCentroid(obj)
			w := want.Centroid.([]any)
			if !same(c[0], gnum(w[0])) || !same(c[1], gnum(w[1])) {
				t.Errorf("%s: centroid %v want %v", k, c, w)
			}
		}
		if s, ok := want.Length.(string); !ok || s != "ERR" {
			if got := GeoLength(obj); !same(got, gnum(want.Length)) {
				t.Errorf("%s: length %v want %v", k, got, want.Length)
			}
		}
		if w, ok := want.Contains.([]any); ok {
			for i, pt := range probes {
				if got := GeoContains(obj, pt); got != w[i].(bool) {
					t.Errorf("%s: contains %v = %v want %v", k, pt, got, w[i])
				}
			}
		}
	}
	for _, d := range g.Dist {
		if got := GeoDistance(d.A, d.B); !same(got, gnum(d.D)) {
			t.Errorf("distance %v %v: %v want %v", d.A, d.B, got, d.D)
		}
		it, dist := GeoInterpolate(d.A, d.B)
		if d.IDist != nil && !same(dist, gnum(d.IDist)) {
			t.Errorf("interpolate distance %v", dist)
		}
		ts := []float64{0, 0.25, 0.5, 1}
		for i, w := range d.I {
			p := it(ts[i])
			if !same(p[0], gnum(w[0])) || !same(p[1], gnum(w[1])) {
				t.Errorf("interpolate %v %v t=%v: %v want %v", d.A, d.B, ts[i], p, w)
			}
		}
	}
	for _, r := range g.Rot {
		fwd, inv := GeoRotation(r.R)
		for i, pt := range [][2]float64{{0, 0}, {10, 20}, {-170, 80}, {179, -45}}[:len(r.F)] {
			x, y := fwd(pt[0], pt[1])
			if !same(x, gnum(r.F[i][0])) || !same(y, gnum(r.F[i][1])) {
				t.Errorf("rotation %v forward %v = [%v %v] want %v", r.R, pt, x, y, r.F[i])
			}
		}
		for i, pt := range [][2]float64{{0, 0}, {10, 20}, {-170, 80}}[:len(r.I)] {
			x, y := inv(pt[0], pt[1])
			if !same(x, gnum(r.I[i][0])) || !same(y, gnum(r.I[i][1])) {
				t.Errorf("rotation %v invert %v = [%v %v] want %v", r.R, pt, x, y, r.I[i])
			}
		}
	}
	for _, c := range g.Circles {
		got, ok := GeoCircle(c.C, c.R, c.Pr)
		if !ok {
			t.Fatalf("circle %v refused", c)
		}
		want := parseJSONValue(t, c.Out)
		if !jsval.Equal(got, want) {
			t.Errorf("circle %v r=%v precision=%v:\n got  %s\n want %s", c.C, c.R, c.Pr, trunc(got.String()), trunc(want.String()))
		}
	}
	for _, e := range g.Planar {
		p := projFromGolden(t, e.Cfg.Type, e.Cfg.Props)
		path := p.Path()
		obj := geoms[e.K]
		label := e.Cfg.Type + "/" + e.K
		if s, ok := e.Area.(string); !ok || s != "ERR" {
			if got := path.Area(obj); !same(got, gnum(e.Area)) {
				t.Errorf("%s: area %v want %v", label, got, e.Area)
			}
		}
		if s, ok := e.Measure.(string); !ok || s != "ERR" {
			if got := path.Measure(obj); !same(got, gnum(e.Measure)) {
				t.Errorf("%s: measure %v want %v", label, got, e.Measure)
			}
		}
		if _, ok := e.Bounds.(string); !ok {
			b := path.Bounds(obj)
			w := e.Bounds.([]any)
			w0, w1 := w[0].([]any), w[1].([]any)
			exp := [2][2]float64{{gnum(w0[0]), gnum(w0[1])}, {gnum(w1[0]), gnum(w1[1])}}
			if !sameBox(b, exp) {
				t.Errorf("%s: bounds %v want %v", label, b, exp)
			}
		}
		if _, ok := e.Centroid.(string); !ok {
			c := path.Centroid(obj)
			w := e.Centroid.([]any)
			if !same(c[0], gnum(w[0])) || !same(c[1], gnum(w[1])) {
				t.Errorf("%s: centroid %v want %v", label, c, w)
			}
		}
	}
}

func sameBox(a, b [2][2]float64) bool {
	return same(a[0][0], b[0][0]) && same(a[0][1], b[0][1]) && same(a[1][0], b[1][0]) && same(a[1][1], b[1][1])
}

var _ = math.NaN
