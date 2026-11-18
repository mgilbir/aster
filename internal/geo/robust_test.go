package geo

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// weird returns a coordinate that is finite, huge, non-finite or not a number.
func weird(r *rand.Rand) jsval.Value {
	switch r.Intn(14) {
	case 0:
		return jsval.Num(math.NaN())
	case 1:
		return jsval.Num(math.Inf(1))
	case 2:
		return jsval.Num(math.Inf(-1))
	case 3:
		return jsval.Num((r.Float64() - 0.5) * 1e300)
	case 4:
		return jsval.Str("12.5")
	case 5:
		return jsval.Null
	case 6:
		return jsval.Undefined
	case 7:
		return jsval.Bool(true)
	case 8:
		return jsval.Num(float64(r.Intn(7)-3) * 90)
	case 9:
		return jsval.Num(float64(r.Intn(9)-4) * 180)
	}
	return jsval.Num((r.Float64() - 0.5) * 800)
}

func randPos(r *rand.Rand) jsval.Value {
	switch r.Intn(12) {
	case 0:
		return jsval.Arr(nil)
	case 1:
		return jsval.ArrOf(weird(r))
	case 2:
		return jsval.Num(3)
	case 3:
		return jsval.Null
	case 4:
		return jsval.ArrOf(weird(r), weird(r), weird(r), weird(r))
	}
	return jsval.ArrOf(weird(r), weird(r))
}

func randLine(r *rand.Rand, n int) jsval.Value {
	pts := make([]jsval.Value, n)
	for i := range pts {
		pts[i] = randPos(r)
	}
	return jsval.Arr(pts)
}

func randRing(r *rand.Rand) jsval.Value {
	n := r.Intn(9)
	ring := randLine(r, n)
	if n > 0 && r.Intn(2) == 0 { // close it
		items := append(ring.Items()[:n:n], ring.Items()[0])
		return jsval.Arr(items)
	}
	return ring
}

func randPolygon(r *rand.Rand) jsval.Value {
	rings := make([]jsval.Value, r.Intn(4))
	for i := range rings {
		rings[i] = randRing(r)
	}
	return jsval.Arr(rings)
}

func randGeometry(r *rand.Rand, depth int) jsval.Value {
	mk := func(typ string, coords jsval.Value) jsval.Value {
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str(typ), "coordinates", coords))
	}
	switch r.Intn(13) {
	case 0:
		return mk("Point", randPos(r))
	case 1:
		return mk("MultiPoint", randLine(r, r.Intn(5)))
	case 2:
		return mk("LineString", randLine(r, r.Intn(7)))
	case 3:
		lines := make([]jsval.Value, r.Intn(3))
		for i := range lines {
			lines[i] = randLine(r, r.Intn(6))
		}
		return mk("MultiLineString", jsval.Arr(lines))
	case 4, 5, 6:
		return mk("Polygon", randPolygon(r))
	case 7:
		polys := make([]jsval.Value, r.Intn(3))
		for i := range polys {
			polys[i] = randPolygon(r)
		}
		return mk("MultiPolygon", jsval.Arr(polys))
	case 8:
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("Sphere")))
	case 9:
		if depth > 3 {
			return jsval.Null
		}
		gs := make([]jsval.Value, r.Intn(4))
		for i := range gs {
			gs[i] = randGeometry(r, depth+1)
		}
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("GeometryCollection"), "geometries", jsval.Arr(gs)))
	case 10:
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("Feature"), "geometry", randGeometry(r, depth+1)))
	case 11:
		fs := make([]jsval.Value, r.Intn(4))
		for i := range fs {
			fs[i] = jsval.Obj(jsval.ObjectOf("type", jsval.Str("Feature"), "geometry", randGeometry(r, depth+1)))
		}
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("FeatureCollection"), "features", jsval.Arr(fs)))
	}
	return jsval.Obj(jsval.ObjectOf("type", weird(r), "coordinates", weird(r)))
}

// TestNoPanicOnGarbage streams random malformed geometry through every
// projection, path operation and spherical measure. Nothing may panic.
func TestNoPanicOnGarbage(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	var projs []Projection
	for _, name := range ProjectionTypes() {
		p, err := NewProjection(name)
		if err != nil {
			t.Fatal(err)
		}
		projs = append(projs, p)
		// and a clipped, rotated variant
		q, _ := NewProjection(name)
		q.Set("rotate", jsval.ArrOf(jsval.Num(20), jsval.Num(-40), jsval.Num(15)))
		q.Set("clipAngle", jsval.Num(75))
		q.Set("clipExtent", jsval.ArrOf(jsval.ArrOf(jsval.Num(100), jsval.Num(80)), jsval.ArrOf(jsval.Num(700), jsval.Num(420))))
		q.Set("precision", jsval.Num(0.2))
		projs = append(projs, q)
	}
	projs = append(projs, nil)
	n := 300
	if testing.Short() {
		n = 60
	}
	start := time.Now()
	js := jsThrows
	for i := 0; i < n; i++ {
		g := randGeometry(r, 0)
		for _, p := range projs {
			path := NewPath(p)
			js(func() { path.String(g) })
			js(func() { path.Area(g) })
			js(func() { path.Measure(g) })
			js(func() { path.Bounds(g) })
			js(func() { path.Centroid(g) })
			if p != nil {
				p.Forward(coord(weird(r)), coord(weird(r)))
				p.Invert(coord(weird(r)), coord(weird(r)))
				c := p.Copy()
				js(func() { c.Path().String(g) })
				fit, _ := NewProjection(p.Type())
				js(func() { fit.FitSize(bg, 300, 200, g) })
				js(func() { fit.FitWidth(bg, 300, g) })
				js(func() { fit.FitHeight(bg, 200, g) })
				js(func() { fit.FitExtent(bg, 1, 2, 300, 200, g) })
			}
		}
		js(func() { areaOf(g) })
		js(func() { boundsOf(g) })
		js(func() { centroidOf(g) })
		js(func() { GeoLength(g) })
		js(func() { GeoContains(g, [2]float64{coord(weird(r)), coord(weird(r))}) })
		js(func() { CollectGeoJSON(jsval.ArrOf(g, g)) })
	}
	// Calibrated, although the site is named a hang guard: it asserts after the
	// fact that the fixed-seed loop took no longer than expected, which is a
	// claim about speed, not a kill or a wait. CI baseline 6.14 s (Heavy gates
	// timings run, without -race; 3.26 s under -race, 0.65 s unscaled); limit 2x
	// = 12.3 s, rounded up to 15 s, scaled for -race: slower is a finding to
	// investigate.
	d := time.Since(start)
	testutil.Timing(t, "geo garbage (hang guard)", d, testutil.Scaled(15*time.Second))
	if d > testutil.Scaled(15*time.Second) {
		t.Errorf("garbage took %v", d)
	}
}

// jsThrows runs f, recovering the JavaScript exception upstream throws (a
// member read of null), which is a *jsval.Thrown panic the dataflow
// recovers: not a crash. Any other panic is.
func jsThrows(f func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*jsval.Thrown); !ok {
				panic(r)
			}
		}
	}()
	f()
}

func TestNoPanicOnGarbageParams(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	props := append([]string{"type", "pointRadius", "fit", "extent", "size"}, ProjectionProperties...)
	for i := 0; i < 400; i++ {
		o := jsval.NewObject(8)
		o.Set("type", jsval.Str(ProjectionTypes()[r.Intn(len(ProjectionTypes()))]))
		for _, k := range props[1:] {
			if r.Intn(3) == 0 {
				var v jsval.Value
				switch r.Intn(5) {
				case 0:
					v = weird(r)
				case 1:
					v = jsval.ArrOf(weird(r), weird(r))
				case 2:
					v = jsval.ArrOf(jsval.ArrOf(weird(r), weird(r)), jsval.ArrOf(weird(r), weird(r)))
				case 3:
					v = jsval.ArrOf(weird(r), weird(r), weird(r))
				default:
					v = randGeometry(r, 2)
				}
				o.Set(k, v)
			}
		}
		var p Projection
		var err error
		jsThrows(func() { p, err = ConfigureProjection(bg, nil, nil, o, nil) })
		if err != nil || p == nil {
			continue
		}
		jsThrows(func() { p.Path().String(randGeometry(r, 0)) })
		p.Forward(1, 2)
	}
}

func TestGeoPathContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tuples := make([]jsval.Value, 5000)
	for i := range tuples {
		tuples[i] = jsval.Obj(jsval.NewObject(1))
	}
	if err := GeoPath(ctx, tuples, GeoPathParams{}); err == nil {
		t.Error("expected the cancelled context to stop GeoPath")
	}
	if err := GeoPoint(ctx, tuples, GeoPointParams{Projection: mustProj(t, "mercator"), Lon: Identity, Lat: Identity}); err == nil {
		t.Error("expected the cancelled context to stop GeoPoint")
	}
}

func mustProj(t testing.TB, name string) Projection {
	t.Helper()
	p, err := NewProjection(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeepGeometryCollection(t *testing.T) {
	g := jsval.Obj(jsval.ObjectOf("type", jsval.Str("Point"), "coordinates", jsval.ArrOf(jsval.Num(1), jsval.Num(2))))
	for i := 0; i < 100000; i++ {
		g = jsval.Obj(jsval.ObjectOf("type", jsval.Str("GeometryCollection"), "geometries", jsval.ArrOf(g)))
	}
	p := mustProj(t, "mercator")
	s, _ := p.Path().String(g)
	if strings.Contains(s, "NaN") {
		t.Error(s)
	}
	areaOf(g)
	GeoContains(g, [2]float64{1, 2})
}

// FuzzPath feeds arbitrary JSON as geometry through paths and measures.
func FuzzPath(f *testing.F) {
	f.Add(`{"type":"Polygon","coordinates":[[[170,10],[-170,10],[-170,30],[170,30],[170,10]]]}`)
	f.Add(`{"type":"LineString","coordinates":[[0,0],[180,90],[-180,-90]]}`)
	f.Add(`{"type":"MultiPoint","coordinates":[[0,0],[1e308,1e308],[null,"x"]]}`)
	f.Add(`{"type":"Sphere"}`)
	f.Add(`{"type":"Feature","geometry":{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[1,2]}]}}`)
	projs := make([]Projection, 0, 4)
	for _, n := range []string{"mercator", "orthographic", "albersUsa", "identity"} {
		p, err := NewProjection(n)
		if err != nil {
			f.Fatal(err)
		}
		projs = append(projs, p)
	}
	f.Fuzz(func(t *testing.T, src string) {
		g, err := jsval.ParseJSONString(src)
		if err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for _, p := range projs {
				path := p.Path()
				jsThrows(func() { path.String(g) })
				jsThrows(func() { path.Area(g) })
				jsThrows(func() { path.Bounds(g) })
				jsThrows(func() { path.Centroid(g) })
			}
			jsThrows(func() { areaOf(g) })
			jsThrows(func() { boundsOf(g) })
			jsThrows(func() { centroidOf(g) })
		}()
		waitStart := time.Now()
		select {
		case <-done:
			testutil.Timing(t, "geo input terminates (hang guard)", time.Since(waitStart), testutil.Scaled(5*time.Second))
		case <-time.After(testutil.Scaled(5 * time.Second)): // hang guard, not a calibrated bound (CI baseline 0.012 s)
			t.Fatalf("input takes over %v: %q", testutil.Scaled(5*time.Second), src)
		}
	})
}

// TestStringListsStreamOnce: a list a geometry streams may be a string, read
// as JavaScript reads one, a code unit per index. Each read of s[i] on its
// own walks s from its start, so a loop over a long string was quadratic:
// the nightly FuzzPath found a 20 KB polygon ring taking 0.3 s (over 5 s
// under the fuzzer). Every list a stream loops over is read once instead.
// At this length a quadratic loop takes minutes, a linear one milliseconds.
func TestStringListsStreamOnce(t *testing.T) {
	long := jsval.Str(strings.Repeat("aé", 1<<18)) // 2^19 code units, not ASCII
	geoms := map[string]jsval.Value{
		"FeatureCollection":  jsval.Obj(jsval.ObjectOf("type", jsval.Str("FeatureCollection"), "features", long)),
		"MultiPoint":         jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiPoint"), "coordinates", long)),
		"LineString":         jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "coordinates", long)),
		"MultiLineString":    jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiLineString"), "coordinates", long)),
		"Polygon":            jsval.Obj(jsval.ObjectOf("type", jsval.Str("Polygon"), "coordinates", long)),
		"PolygonRing":        jsval.Obj(jsval.ObjectOf("type", jsval.Str("Polygon"), "coordinates", jsval.ArrOf(long))),
		"MultiPolygon":       jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiPolygon"), "coordinates", long)),
		"GeometryCollection": jsval.Obj(jsval.ObjectOf("type", jsval.Str("GeometryCollection"), "geometries", long)),
	}
	for name, g := range geoms {
		done := make(chan struct{})
		go func() {
			defer close(done)
			jsThrows(func() { boundsOf(g) })
		}()
		select {
		case <-done:
		case <-time.After(testutil.Scaled(60 * time.Second)): // hang guard, not a calibrated bound
			t.Fatalf("%s: a list given as a long string takes over %v", name, testutil.Scaled(60*time.Second))
		}
	}
}

// TestTopologyStringsReadOnce: topojson-client indexes whatever a topology
// holds, and a string is read a code unit per index. The arcs given as a
// long string, looked up once per reference, and an arc given as one,
// looped over, were each quadratic; every string a call reads by index is
// decoded once. At these lengths a quadratic read takes minutes, a linear
// one well under a second.
func TestTopologyStringsReadOnce(t *testing.T) {
	const n = 1 << 19
	long := jsval.Str(strings.Repeat("aé", n/2)) // n code units, not ASCII
	refs := make([]jsval.Value, n)
	for i := range refs {
		refs[i] = jsval.Int(i)
	}
	line := func(arcs jsval.Value) jsval.Value {
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "arcs", arcs))
	}
	topo := func(arcs, object jsval.Value) jsval.Value {
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("Topology"), "arcs", arcs,
			"objects", jsval.Obj(jsval.ObjectOf("o", object))))
	}
	cases := []struct {
		name string
		topo jsval.Value
		mesh bool
	}{
		{"arcs-string-feature", topo(long, line(jsval.ArrOf(refs...))), false},
		{"arc-string-feature", topo(jsval.ArrOf(long), line(jsval.ArrOf(jsval.Int(0)))), false},
		{"arc-string-mesh", topo(jsval.ArrOf(long), line(jsval.ArrOf(jsval.Int(0)))), true},
	}
	for _, c := range cases {
		o := c.topo.Get("objects").Get("o")
		done := make(chan struct{})
		go func() {
			defer close(done)
			if c.mesh {
				_, _ = TopoMesh(bg, c.topo, o, MeshAll)
			} else {
				_, _ = TopoFeature(bg, c.topo, o)
			}
		}()
		select {
		case <-done:
		case <-time.After(testutil.Scaled(60 * time.Second)): // hang guard, not a calibrated bound
			t.Fatalf("%s: a topology string read by index takes over %v", c.name, testutil.Scaled(60*time.Second))
		}
	}
}
