package geo

import (
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// The fast decimal formatting of path coordinates must equal JavaScript's
// Math.round(v*k)/k printed with Number.prototype.toString.
func TestPathNumberFormat(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, digits := range []int{0, 1, 2, 3, 4, 5, 6, 7, 10, 15, -1, 16} {
		s := newPathString(digits)
		for i := 0; i < 20000; i++ {
			var v float64
			switch i % 5 {
			case 0:
				v = (r.Float64() - 0.5) * 2000
			case 1:
				v = float64(r.Intn(2000)-1000) / 8
			case 2:
				v = (r.Float64() - 0.5) * math.Pow(10, float64(r.Intn(30)-10))
			case 3:
				v = float64(r.Intn(100000)) * 1e-4
			default:
				v = math.Round((r.Float64()-0.5)*1e6) / 1000
			}
			s.buf = s.buf[:0]
			s.num(v)
			var want string
			if digits < 0 || digits > 15 {
				want = jsval.JSNumberString(v)
			} else {
				k := math.Pow10(digits)
				want = jsval.JSNumberString(jsRound(v*k) / k)
			}
			if string(s.buf) != want {
				t.Fatalf("digits %d v=%v: %s want %s", digits, v, s.buf, want)
			}
		}
	}
	s := newPathString(3)
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), math.Copysign(0, -1), -0.0004, 1e21, 1e-7, 123456789012345678} {
		s.buf = s.buf[:0]
		s.num(v)
		want := jsval.JSNumberString(jsRound(v*1000) / 1000)
		if string(s.buf) != want {
			t.Errorf("v=%v: %s want %s", v, s.buf, want)
		}
	}
}

func TestJSRound(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{{2.5, 3}, {-2.5, -2}, {0.49999999999999994, 0}, {-0.2, math.Copysign(0, -1)}, {1e300, 1e300}, {-1.5, -1}, {1.5, 2}} {
		if got := jsRound(c.in); got != c.want || math.Signbit(got) != math.Signbit(c.want) {
			t.Errorf("round(%v) = %v want %v", c.in, got, c.want)
		}
	}
	if !math.IsNaN(jsRound(math.NaN())) || !math.IsInf(jsRound(math.Inf(1)), 1) {
		t.Error("non-finite")
	}
}

func TestAdderExact(t *testing.T) {
	var a adder
	a.add(1)
	a.add(1e100)
	a.add(1)
	a.add(-1e100)
	if got := a.value(); got != 2 {
		t.Errorf("sum = %v, want 2 (exact summation)", got)
	}
	var b adder
	for i := 0; i < 1000; i++ {
		b.add(0.1)
	}
	if got := b.value(); got != 100.00000000000001 && got != 100 {
		t.Errorf("0.1*1000 = %v", got)
	}
}

type recordCtx struct{ ops []string }

func (c *recordCtx) MoveTo(x, y float64) {
	c.ops = append(c.ops, "M"+jsval.JSNumberString(x)+","+jsval.JSNumberString(y))
}
func (c *recordCtx) LineTo(x, y float64) {
	c.ops = append(c.ops, "L"+jsval.JSNumberString(x)+","+jsval.JSNumberString(y))
}
func (c *recordCtx) Arc(x, y, r, a0, a1 float64, ccw bool) {
	c.ops = append(c.ops, "A"+jsval.JSNumberString(x)+","+jsval.JSNumberString(y)+","+jsval.JSNumberString(r))
}
func (c *recordCtx) ClosePath() { c.ops = append(c.ops, "Z") }

func TestPathContext(t *testing.T) {
	p := mustProj(t, "identity")
	path := p.Path()
	poly := parseJSONValue(t, []byte(`{"type":"Polygon","coordinates":[[[0,0],[10,0],[10,10],[0,0]]]}`))
	pt := parseJSONValue(t, []byte(`{"type":"MultiPoint","coordinates":[[5,5]]}`))
	ctx := &recordCtx{}
	if got := path.Render(ctx, poly); got != "" {
		t.Errorf("Render with context returned %q", got)
	}
	path.SetPointRadius(2)
	path.Render(ctx, pt)
	want := "M0,0 L10,0 L10,10 Z M7,5 A5,5,2"
	if got := strings.Join(ctx.ops, " "); got != want {
		t.Errorf("ops %q want %q", got, want)
	}
	if s := path.Render(nil, poly); s != "M0,0L10,0L10,10Z" {
		t.Errorf("Render nil context: %q", s)
	}
	// The path is left in string mode.
	if s, ok := path.String(poly); !ok || s != "M0,0L10,0L10,10Z" {
		t.Errorf("String after Render: %q %v", s, ok)
	}
}

func TestShapeRender(t *testing.T) {
	p := mustProj(t, "identity")
	shape := NewShape(p, nil, PointRadius{})
	item := parseJSONValue(t, []byte(`{"datum":{"type":"LineString","coordinates":[[0,0],[1,1]]}}`))
	if got := shape.Render(nil, item); got != "M0,0L1,1" {
		t.Errorf("shape path %q", got)
	}
	ctx := &recordCtx{}
	shape.Render(ctx, item)
	if got := strings.Join(ctx.ops, " "); got != "M0,0 L1,1" {
		t.Errorf("shape ops %q", got)
	}
	if got := shape.Render(nil, jsval.Obj(jsval.NewObject(0))); got != "" {
		t.Errorf("empty shape %q", got)
	}
	r := NewShape(p, nil, ConstRadius(1))
	pt := parseJSONValue(t, []byte(`{"datum":{"type":"Point","coordinates":[2,3]}}`))
	if got := r.Render(nil, pt); got != "M2,3m0,1a1,1 0 1,1 0,-2a1,1 0 1,1 0,2z" {
		t.Errorf("point shape %q", got)
	}
	if p.Path().PointRadius() != 4.5 {
		t.Error("shape must restore the path's point radius")
	}
}

func TestProjectionCopy(t *testing.T) {
	for _, name := range ProjectionTypes() {
		p := mustProj(t, name)
		p.Set("scale", jsval.Num(123))
		p.Set("translate", jsval.ArrOf(jsval.Num(300), jsval.Num(200)))
		p.Set("precision", jsval.Num(0.3))
		p.Set("reflectY", jsval.True)
		p.Path().SetPointRadius(7)
		c := p.Copy()
		if c.Type() != p.Type() || c.Path().PointRadius() != 7 {
			t.Errorf("%s: copy lost type or point radius", name)
		}
		x0, y0, ok0 := p.Forward(-100, 40)
		x1, y1, ok1 := c.Forward(-100, 40)
		// vega-projection's copy() replays the getters, so degrees round-trip
		// through radians and the last bit can move, as upstream.
		if ok0 != ok1 || !near(x0, x1, 1e-12) || !near(y0, y1, 1e-12) {
			t.Errorf("%s: copy projects differently: %v,%v vs %v,%v", name, x0, y0, x1, y1)
		}
		g := parseJSONValue(t, []byte(`{"type":"LineString","coordinates":[[-100,40],[20,30],[100,-20]]}`))
		s0, _ := p.Path().String(g)
		s1, _ := c.Path().String(g)
		if s0 != s1 {
			t.Errorf("%s: copy draws differently:\n%s\n%s", name, s0, s1)
		}
		// Independent: changing the copy leaves the original alone.
		c.Set("scale", jsval.Num(5))
		if p.Scale() != 123 {
			t.Errorf("%s: copy is not independent", name)
		}
	}
}

func TestProjectionSetErrors(t *testing.T) {
	p := mustProj(t, "mercator")
	for _, prop := range []string{"translate", "center", "rotate", "clipExtent"} {
		if prop == "clipExtent" {
			if err := p.Set(prop, jsval.Null); err != nil {
				t.Errorf("clipExtent(null) should clear: %v", err)
			}
			continue
		}
		if err := p.Set(prop, jsval.Null); err == nil {
			t.Errorf("%s(null): expected an error, as upstream throws", prop)
		}
		if err := p.Set(prop, jsval.Undefined); err == nil {
			t.Errorf("%s(undefined): expected an error", prop)
		}
	}
	// Numbers, strings and short arrays coerce like JavaScript arithmetic.
	if err := p.Set("scale", jsval.Str("200")); err != nil || p.Scale() != 200 {
		t.Errorf("scale string: %v %v", err, p.Scale())
	}
	if err := p.Set("translate", jsval.ArrOf(jsval.Num(1))); err != nil {
		t.Error(err)
	}
	if _, y := p.Translate(); !math.IsNaN(y) {
		t.Errorf("translate([1]) y = %v, want NaN", y)
	}
	// Unknown properties and properties a projection lacks are ignored.
	usa := mustProj(t, "albersUsa")
	for _, prop := range []string{"rotate", "center", "clipAngle", "nosuch", "parallels", "radius"} {
		if err := usa.Set(prop, jsval.ArrOf(jsval.Num(1), jsval.Num(2))); err != nil {
			t.Errorf("albersUsa %s: %v", prop, err)
		}
	}
	if _, err := NewProjection("nosuch"); err == nil {
		t.Error("unknown projection type should fail")
	}
	for _, name := range []string{"", "MERCATOR", "AlbersUSA", "naturalEarth1", "EqualEarth"} {
		if _, err := NewProjection(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestConfigureProjectionUpdates(t *testing.T) {
	o := jsval.ObjectOf("type", jsval.Str("mercator"), "scale", jsval.Num(100))
	p, err := ConfigureProjection(nil, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	o.Set("scale", jsval.Num(250))
	o.Set("translate", jsval.ArrOf(jsval.Num(10), jsval.Num(20)))
	// Only modified properties are re-applied to an existing projection.
	p2, err := ConfigureProjection(p, o, func(name string) bool { return name == "scale" })
	if err != nil || p2 != p {
		t.Fatalf("update: %v", err)
	}
	if x, y := p.Translate(); p.Scale() != 250 || x != 480 || y != 250 {
		t.Errorf("scale %v translate %v %v", p.Scale(), x, y)
	}
	// A modified type builds a new projection.
	o.Set("type", jsval.Str("orthographic"))
	p3, err := ConfigureProjection(p, o, func(name string) bool { return name == "type" })
	if err != nil || p3 == p || p3.Type() != "orthographic" || p3.Scale() != 250 {
		t.Errorf("type change: %v %v", p3, err)
	}
	if _, err := ConfigureProjection(nil, jsval.ObjectOf("type", jsval.Str("nosuch")), nil); err == nil {
		t.Error("unknown type")
	}
}

func TestCollectGeoJSON(t *testing.T) {
	feat := parseJSONValue(t, []byte(`{"type":"Feature","geometry":{"type":"Point","coordinates":[1,2]}}`))
	geom := parseJSONValue(t, []byte(`{"type":"Point","coordinates":[3,4]}`))
	fc := parseJSONValue(t, []byte(`{"type":"FeatureCollection","features":[{"type":"Feature","geometry":null}]}`))
	single, _ := CollectGeoJSON(jsval.ArrOf(feat))
	if !jsval.SameRef(single, feat) {
		t.Error("a single object is returned as is")
	}
	got, err := CollectGeoJSON(jsval.ArrOf(feat, geom, fc, jsval.ArrOf(geom, jsval.Null)))
	if err != nil {
		t.Fatal(err)
	}
	fs := got.Get("features").Items()
	// feat, geom (wrapped), fc's feature, geom (wrapped); the null is dropped.
	if got.Get("type").StrValue() != "FeatureCollection" || len(fs) != 4 {
		t.Fatalf("got %s", got)
	}
	if fs[1].Get("type").StrValue() != "Feature" || !jsval.SameRef(fs[1].Get("geometry"), geom) {
		t.Errorf("bare geometry should be wrapped: %s", fs[1])
	}
	if _, err := CollectGeoJSON(jsval.ArrOf(feat, jsval.Null)); err == nil {
		t.Error("a null element makes upstream throw")
	}
	empty, err := CollectGeoJSON(jsval.Undefined)
	if err != nil || empty.Get("features").Len() != 0 {
		t.Errorf("empty: %v %v", empty, err)
	}
}

func TestAngle(t *testing.T) {
	var g struct {
		Subset [][2]float64 `json:"subset"`
		Xy     [][2]float64 `json:"xy"`
		Out    []struct {
			Name  string          `json:"name"`
			Angle float64         `json:"angle"`
			Fwd   [][]any         `json:"fwd"`
			Inv   [][]any         `json:"inv"`
			Path  json.RawMessage `json:"path"`
		} `json:"out"`
	}
	data, err := readFile("testdata/angle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	geoms := corpus(t)
	for _, c := range g.Out {
		var p Projection
		switch c.Name {
		case "identity":
			id := newIdentity("identity")
			id.SetAngle(c.Angle)
			p = id
		default:
			name := map[string]string{"albers": "albers"}[c.Name]
			if name == "" {
				name = c.Name
			}
			sp := mustProj(t, name).(*standard)
			// d3's own defaults, without Vega's wrappers' extras: same thing here
			sp.SetAngle(c.Angle)
			sp.SetRotate([]float64{10, -20})
			p = sp
		}
		label := c.Name + " angle " + jsval.JSNumberString(c.Angle)
		for i, pt := range g.Subset {
			x, y, _ := p.Forward(pt[0], pt[1])
			if !same(x, gnum(c.Fwd[i][0])) || !same(y, gnum(c.Fwd[i][1])) {
				t.Errorf("%s forward %v = [%v %v] want %v", label, pt, x, y, c.Fwd[i])
			}
		}
		for i, q := range g.Xy {
			lon, lat, ok := p.Invert(q[0], q[1])
			if c.Inv[i] == nil {
				continue
			}
			if !ok || !near(lon, gnum(c.Inv[i][0]), 1e-9) || !near(lat, gnum(c.Inv[i][1]), 1e-9) {
				t.Errorf("%s invert %v = [%v %v] want %v", label, q, lon, lat, c.Inv[i])
			}
		}
		key := "lineAM"
		if c.Name == "identity" {
			key = "polyHole"
		}
		s, ok := p.Path().String(geoms[key])
		checkPath(t, label+" path", s, ok, c.Path)
	}
}
