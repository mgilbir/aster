package geo

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// The bounds of the geographic walks (ADR 0010, findings C3, C5, C17, C18,
// C65 of the 2026-10-11 audit).

// pointList records the points a stream receives.
type pointList struct{ pts [][2]float64 }

func (s *pointList) Point(x, y float64) { s.pts = append(s.pts, [2]float64{x, y}) }
func (s *pointList) LineStart()         {}
func (s *pointList) LineEnd()           {}
func (s *pointList) PolygonStart()      {}
func (s *pointList) PolygonEnd()        {}
func (s *pointList) Sphere()            {}

// upstreamCircleStream is d3's circleStream loop verbatim, stopped after
// limit steps: ok is false when it had not ended by then.
func upstreamCircleStream(s Stream, radius, delta, direction float64, from, to *cpoint, limit int) (ok bool) {
	if !truthy(delta) {
		return true
	}
	cosRadius := jsmath.Cos(radius)
	step := direction * delta
	var t0, t1 float64
	if from == nil {
		t0 = radius + direction*tau
		t1 = radius - step/2
	} else {
		t0 = circleRadius(cosRadius, from.x, from.y)
		t1 = circleRadius(cosRadius, to.x, to.y)
		if direction > 0 && t0 < t1 || direction <= 0 && t0 > t1 {
			t0 += direction * tau
		}
	}
	for t := t0; direction > 0 && t > t1 || direction <= 0 && t < t1; t -= step {
		if limit == 0 {
			return false
		}
		limit--
		s.Point(t, 0) // the angle is what the bound is about
	}
	return true
}

// TestCircleStreamBoundKeepsUpstream holds circleStream to upstream's loop
// wherever that ends, at every magnitude of radius up to where it stops
// ending (C3), by comparing the number of steps each takes: the points are a
// function of the step's angle, the same in both.
func TestCircleStreamBoundKeepsUpstream(t *testing.T) {
	from, to := &cpoint{x: 0.3, y: 0.2}, &cpoint{x: -0.4, y: 0.1}
	for e := 0; e <= 60; e++ {
		for _, sign := range []float64{1, -1} {
			for _, delta := range []float64{6 * radians, 2 * radians, 1e-3} {
				radius := sign * math.Ldexp(1.37, e)
				for _, dir := range []float64{1, -1} {
					for _, ends := range [][2]*cpoint{{nil, nil}, {from, to}} {
						var want pointList
						done := upstreamCircleStream(&want, radius, delta, dir, ends[0], ends[1], 1<<20)
						var got pointList
						circleStream(&got, radius, delta, dir, ends[0], ends[1])
						if done && len(got.pts) != len(want.pts) {
							t.Fatalf("radius %g delta %g dir %g: %d points, upstream %d", radius, delta, dir, len(got.pts), len(want.pts))
						}
						if !done && len(got.pts) > maxCircleSteps {
							t.Fatalf("radius %g delta %g dir %g: %d points from a loop upstream never ends", radius, delta, dir, len(got.pts))
						}
					}
				}
			}
		}
	}
}

// TestCircleStreamHugeRadiusEnds is C3: a radius past 2^50 used to loop
// forever.
func TestCircleStreamHugeRadiusEnds(t *testing.T) {
	for _, r := range []float64{1e16, 1e18 * radians, math.MaxFloat64, math.Inf(1), math.NaN()} {
		var got pointList
		circleStream(&got, r, 6*radians, 1, nil, nil)
		circleStream(&got, r, 6*radians, -1, &cpoint{x: 0.1, y: 0.2}, &cpoint{x: 0.3, y: 0.1})
		if len(got.pts) > 2*maxCircleSteps {
			t.Fatalf("radius %g: %d points", r, len(got.pts))
		}
	}
}

func TestClipAngleRefusedPastUpstreamsEnd(t *testing.T) {
	for _, a := range []float64{1e18, -1e17, math.Inf(1), math.Inf(-1)} {
		p, _ := NewProjection("orthographic")
		err := p.Set("clipAngle", jsval.Num(a))
		if !errors.Is(err, budget.ErrLimit) {
			t.Errorf("clipAngle %g: %v, want ErrLimit", a, err)
		}
	}
	// Angles upstream draws, however odd, and the falsy ones that select
	// antimeridian cutting.
	for _, a := range []float64{90, 270, -90, 450, 1e15, 0, math.NaN()} {
		p, _ := NewProjection("orthographic")
		if err := p.Set("clipAngle", jsval.Num(a)); err != nil {
			t.Errorf("clipAngle %g: %v", a, err)
		}
	}
}

// comb is a polygon of n teeth reaching into the rectangle [0,0]-[w,10] from
// below it: every tooth is its own visible ring.
func comb(n int) jsval.Value {
	var ring []jsval.Value
	pt := func(x, y float64) { ring = append(ring, jsval.ArrOf(jsval.Num(x), jsval.Num(y))) }
	for i := 0; i < n; i++ {
		x := float64(2*i) + 0.5
		pt(x, 20)
		pt(x, 5)
		pt(x+1, 5)
		pt(x+1, 20)
	}
	pt(float64(2*n), 30)
	pt(0, 30)
	ring = append(ring, ring[0])
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("Polygon"), "coordinates", jsval.ArrOf(jsval.Arr(ring))))
}

// rejoinSteps clips the comb with a rectangle, counting the rings drawn and
// the search steps clipRejoin took.
type rejoinCount struct {
	pointList
	rings int
}

func (r *rejoinCount) LineStart() { r.rings++ }

// TestRejoinLinear: d3's rejoin searched for the next unvisited intersection
// from the first one for every ring it drew, quadratic in the rings.
func TestRejoinLinear(t *testing.T) {
	for _, n := range []int{100, 1000, 4000} {
		r := &rectClip{0, 0, float64(2 * n), 10}
		poly := comb(n)
		// Collect the visible segments as rectStream does, then rejoin them.
		var segs [][]cpoint
		var pts []cpoint
		for _, c := range poly.Get("coordinates").Index(0).Items() {
			pts = append(pts, cpoint{c.Index(0).NumValue(), c.Index(1).NumValue(), 0})
		}
		var cur []cpoint
		for i := 0; i+1 < len(pts); i++ {
			a, b := [2]float64{pts[i].x, pts[i].y}, [2]float64{pts[i+1].x, pts[i+1].y}
			if clipSegment(&a, &b, r.x0, r.y0, r.x1, r.y1) {
				if len(cur) == 0 {
					cur = append(cur, cpoint{a[0], a[1], 0})
				}
				cur = append(cur, cpoint{b[0], b[1], 0})
				if !r.visible(pts[i+1].x, pts[i+1].y) {
					segs = append(segs, cur)
					cur = nil
				}
			}
		}
		out := &rejoinCount{}
		steps := clipRejoin(segs, r.compareIntersection, false, r.interpolate, out)
		if out.rings != n {
			t.Fatalf("%d teeth: %d rings", n, out.rings)
		}
		if steps > 4*len(segs)+4 {
			t.Errorf("%d teeth: %d search steps for %d segments; the search must be linear", n, steps, len(segs))
		}
	}
}

// TestRectClipCombOutput holds the rejoined comb to d3's output, recorded
// with node (d3-geo 3.1.1): one closed ring per tooth.
func TestRectClipCombOutput(t *testing.T) {
	p, _ := NewProjection("identity")
	if err := p.Set("clipExtent", jsval.ArrOf(jsval.ArrOf(jsval.Num(0), jsval.Num(0)), jsval.ArrOf(jsval.Num(6), jsval.Num(10)))); err != nil {
		t.Fatal(err)
	}
	got, _ := p.Path().String(comb(3))
	const want = "M0.5,10L0.5,5L1.5,5L1.5,10L0.5,10ZM2.5,10L2.5,5L3.5,5L3.5,10L2.5,10ZM4.5,10L4.5,5L5.5,5L5.5,10L4.5,10Z"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// zigzag is a line of n long segments, each subdivided by the resampler.
func zigzag(n int) jsval.Value {
	items := make([]jsval.Value, n)
	for i := range items {
		y := -80.0
		if i%2 == 1 {
			y = 80
		}
		items[i] = jsval.ArrOf(jsval.Num(-179+358*float64(i)/float64(n)), jsval.Num(y))
	}
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "coordinates", jsval.Arr(items)))
}

// TestResampledPointsCharged is C17: points the resampler makes and a clip
// extent then drops are charged, where they are produced.
func TestResampledPointsCharged(t *testing.T) {
	p, _ := NewProjection("equirectangular")
	for prop, v := range map[string]jsval.Value{
		"scale":      jsval.Num(1e9),
		"precision":  jsval.Num(1e-9),
		"clipExtent": jsval.ArrOf(jsval.ArrOf(jsval.Num(-1e12), jsval.Num(-1e12)), jsval.ArrOf(jsval.Num(-1e12), jsval.Num(-1e12))),
	} {
		if err := p.Set(prop, v); err != nil {
			t.Fatal(err)
		}
	}
	ctx := budget.With(context.Background(), &budget.Budget{MaxPoints: 100_000})
	err := GeoPath(ctx, []jsval.Value{zigzag(50)}, GeoPathParams{Projection: p})
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("50 segments resampled to depth 16 against 100k points: %v", err)
	}
}

// TestFitBounded is C65: the fit's measuring walk is bounded and cancellable.
func TestFitBounded(t *testing.T) {
	p, _ := NewProjection("orthographic")
	if err := p.Set("precision", jsval.Num(1e-12)); err != nil {
		t.Fatal(err)
	}
	ctx := budget.With(context.Background(), &budget.Budget{MaxPoints: 100_000})
	if err := p.FitSize(ctx, 100, 100, zigzag(50)); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("fit through 50 resampled segments against 100k points: %v", err)
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.FitSize(done, 100, 100, zigzag(50)); !errors.Is(err, context.Canceled) {
		t.Fatalf("fit with a cancelled context: %v", err)
	}
	// A fit cut short leaves the projection usable, and fits again.
	if err := p.FitSize(bg, 100, 100, zigzag(4)); err != nil {
		t.Fatal(err)
	}
}

// TestPathMeasuresBound is C18: a bound path's measures charge their walk.
func TestPathMeasuresBound(t *testing.T) {
	p, _ := NewProjection("equirectangular")
	if err := p.Set("precision", jsval.Num(1e-9)); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("scale", jsval.Num(1e9)); err != nil {
		t.Fatal(err)
	}
	path := p.Path()
	path.Bind(budget.With(context.Background(), &budget.Budget{MaxPoints: 100_000}))
	for name, measure := range map[string]func(jsval.Value){
		"area":     func(o jsval.Value) { path.Area(o) },
		"bounds":   func(o jsval.Value) { path.Bounds(o) },
		"centroid": func(o jsval.Value) { path.Centroid(o) },
		"measure":  func(o jsval.Value) { path.Measure(o) },
	} {
		func() {
			defer func() {
				s, ok := recover().(*budget.Stop)
				if !ok || !errors.Is(s.Err, budget.ErrLimit) {
					t.Errorf("%s: want a *budget.Stop wrapping ErrLimit, got %v", name, s)
				}
			}()
			measure(zigzag(50))
		}()
	}
}

// TestSphericalMeasuresCancel: geoArea, geoBounds and geoCentroid without a
// projection meter their walk and stop with the context.
func TestSphericalMeasuresCancel(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	big := zigzag(5000)
	if _, err := GeoArea(done, big); !errors.Is(err, context.Canceled) {
		t.Errorf("GeoArea: %v", err)
	}
	if _, err := GeoBounds(done, big); !errors.Is(err, context.Canceled) {
		t.Errorf("GeoBounds: %v", err)
	}
	if _, err := GeoCentroid(done, big); !errors.Is(err, context.Canceled) {
		t.Errorf("GeoCentroid: %v", err)
	}
}

func topoDoc(t *testing.T, src string) jsval.Value {
	t.Helper()
	v, err := jsval.ParseJSON([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestTopoPositionsKeepTheirLength holds topojson decoding to
// topojson-client 3 (recorded with node): each position keeps its own
// length, and only a transform pads one to two numbers.
func TestTopoPositionsKeepTheirLength(t *testing.T) {
	const objs = `"objects":{"l":{"type":"LineString","arcs":[0,-2]},"p":{"type":"Point","coordinates":[5]},"mp":{"type":"MultiPoint","coordinates":[[1,2,3],[]]}}`
	const arcs = `"arcs":[[[1,2,3],[4,5],[6],[]],[[7,8],[9,10,11,12]]]`
	for _, c := range []struct {
		transform string
		want      map[string]string
	}{
		{"", map[string]string{
			"l":  `{"type":"LineString","coordinates":[[1,2,3],[4,5],[6],[9,10,11,12],[7,8]]}`,
			"p":  `{"type":"Point","coordinates":[5]}`,
			"mp": `{"type":"MultiPoint","coordinates":[[1,2,3],[]]}`,
		}},
		{`"transform":{"scale":[2,3],"translate":[10,20]},`, map[string]string{
			"l":  `{"type":"LineString","coordinates":[[12,26,3],[20,41],[32,null],[42,74,11,12],[24,44]]}`,
			"p":  `{"type":"Point","coordinates":[20,null]}`,
			"mp": `{"type":"MultiPoint","coordinates":[[12,26,3],[null,null]]}`,
		}},
	} {
		topo := topoDoc(t, `{"type":"Topology",`+c.transform+arcs+`,`+objs+`}`)
		for name, want := range c.want {
			f, err := TopoFeature(bg, topo, topo.Get("objects").Get(name))
			if err != nil {
				t.Fatal(err)
			}
			if got := string(jsval.AppendJSON(nil, f.Get("geometry"))); got != want {
				t.Errorf("%q %s:\n got %s\nwant %s", c.transform, name, got, want)
			}
		}
	}
}

// expandTopo is C5: one arc whose first position has c numbers, referenced r
// times by one line. With a transform every reference makes its positions
// anew; without one they are the input's arrays, as upstream's.
func expandTopo(t *testing.T, c, r int, transform bool) jsval.Value {
	t.Helper()
	pos := make([]jsval.Value, c)
	for i := range pos {
		pos[i] = jsval.Num(0)
	}
	refs := make([]jsval.Value, r)
	for i := range refs {
		refs[i] = jsval.Num(0)
	}
	arc := jsval.ArrOf(jsval.Arr(pos), jsval.ArrOf(jsval.Num(0), jsval.Num(0)))
	o := jsval.ObjectOf(
		"type", jsval.Str("Topology"),
		"arcs", jsval.ArrOf(arc),
		"objects", jsval.Obj(jsval.ObjectOf("o", jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "arcs", jsval.Arr(refs))))),
	)
	if transform {
		o.Set("transform", jsval.Obj(jsval.ObjectOf(
			"scale", jsval.ArrOf(jsval.Num(1), jsval.Num(1)),
			"translate", jsval.ArrOf(jsval.Num(0), jsval.Num(0)))))
	}
	return jsval.Obj(o)
}

func ledgerCtx(maxBytes int64) (context.Context, *budget.Ledger) {
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: maxBytes})
	return budget.WithLedger(context.Background(), l), l
}

// TestTopologyCharged: the positions references materialise are charged to
// the ledger before they are made, so an expansion fails with ErrLimit
// instead of allocating.
func TestTopologyCharged(t *testing.T) {
	// 2,000 references to an arc of a 2,000-number position: 4M values.
	ctx, l := ledgerCtx(16 << 20)
	if _, err := TopologyFeatures(ctx, expandTopo(t, 2000, 2000, true), "o", "", ""); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("4M-value expansion against a 16 MiB ledger: %v", err)
	}
	if l.Bytes() > 16<<20 {
		t.Fatalf("ledger holds %d bytes past its limit", l.Bytes())
	}
	// A small expansion succeeds, and what it makes is charged: at least 16
	// bytes per value it holds (the decoded arcs are given back).
	ctx, l = ledgerCtx(0)
	feats, err := TopologyFeatures(ctx, expandTopo(t, 100, 100, true), "o", "", "")
	if err != nil {
		t.Fatal(err)
	}
	values := 0
	for _, p := range feats[0].Get("geometry").Get("coordinates").Items() {
		values += p.Len()
	}
	if values < 100*100 {
		t.Fatalf("decoded %d values", values)
	}
	if got := l.Bytes(); got < int64(values)*16 {
		t.Errorf("ledger holds %d bytes for %d decoded values", got, values)
	}
	if l.WorkDone() < int64(values) {
		t.Errorf("work %d for %d decoded values", l.WorkDone(), values)
	}
	// Without a transform the positions are shared, but each reference
	// still takes a slot per position in its line.
	ctx, l = ledgerCtx(0)
	feats, err = TopologyFeatures(ctx, expandTopo(t, 100, 1000, false), "o", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if n := feats[0].Get("geometry").Get("coordinates").Len(); l.Bytes() < int64(n)*16 {
		t.Errorf("ledger holds %d bytes for %d positions", l.Bytes(), n)
	}
}

func TestTopologyCancelled(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TopologyFeatures(done, expandTopo(t, 2000, 100, true), "o", "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("decoding with a cancelled context: %v", err)
	}
}

// TestGraticuleRangeIsD3s: d3.range's count is `| 0`, ToInt32 (checked with
// node: range(0, 2**32+5, 1) has 5 elements, range(0, 2**31+5, 1) throws).
func TestGraticuleRangeIsD3s(t *testing.T) {
	g := NewGraticule()
	if r, ok := g.rangeOf(0, math.Ldexp(1, 32)+5, 1); !ok || len(r) != 5 {
		t.Errorf("range(0, 2^32+5, 1): %v %v", r, ok)
	}
	g = NewGraticule()
	if _, ok := g.rangeOf(0, math.Ldexp(1, 31)+5, 1); ok || !errors.Is(g.err, errInvalidArrayLength) {
		t.Errorf("range(0, 2^31+5, 1): %v", g.err)
	}
	for _, stop := range []float64{math.NaN(), math.Inf(1)} {
		g = NewGraticule()
		if r, ok := g.rangeOf(0, stop, 1); !ok || len(r) != 0 {
			t.Errorf("range(0, %g, 1): %v %v", stop, r, ok)
		}
	}
}

// TestGraticuleCharged: a graticule's points are charged to the ledger
// before they are made.
func TestGraticuleCharged(t *testing.T) {
	p := 0.01 // 36,000 points a parallel
	ctx, _ := ledgerCtx(4 << 20)
	if _, err := GraticuleFeature(ctx, GraticuleParams{Precision: &p}); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("a fine graticule against a 4 MiB ledger: %v", err)
	}
	ctx, l := ledgerCtx(0)
	g, err := GraticuleFeature(ctx, GraticuleParams{})
	if err != nil {
		t.Fatal(err)
	}
	points := 0
	for _, line := range g.Get("coordinates").Items() {
		points += line.Len()
	}
	if l.Bytes() < int64(points)*graticulePointBytes {
		t.Errorf("ledger holds %d bytes for %d points", l.Bytes(), points)
	}
}

// encTagged is JSON.stringify(v, (k, v) => Number.isNaN(v) ? "NaN!" : v):
// JSON with NaN told apart from null.
func encTagged(v jsval.Value) string {
	switch v.Kind() {
	case jsval.KindNum:
		if math.IsNaN(v.NumValue()) {
			return `"NaN!"`
		}
	case jsval.KindArr:
		var b strings.Builder
		b.WriteByte('[')
		for i, it := range v.Items() {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(encTagged(it))
		}
		b.WriteByte(']')
		return b.String()
	case jsval.KindObj:
		o := v.ObjValue()
		var b strings.Builder
		b.WriteByte('{')
		for i := 0; i < o.Len(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(jsval.AppendJSON(nil, jsval.Str(o.KeyAt(i))))
			b.WriteByte(':')
			b.WriteString(encTagged(o.ValueAt(i)))
		}
		b.WriteByte('}')
		return b.String()
	}
	return string(jsval.AppendJSON(nil, v))
}

// TestTopoRawCoordinates holds decoding to topojson-client 3.1 (recorded with
// node) for coordinates that are not numbers: without a transform the
// positions are the input's arrays, values as they are; with one, the sums
// and the translation are JavaScript `+`, so a string concatenates.
func TestTopoRawCoordinates(t *testing.T) {
	const objs = `"objects":{"l":{"type":"LineString","arcs":[0,1]},"p":{"type":"Point","coordinates":["3",null]},` +
		`"mp":{"type":"MultiPoint","coordinates":[["1","2"],[null,4],[true,"x"]]},"pg":{"type":"Polygon","arcs":[[2]]},` +
		`"c":{"type":"GeometryCollection","geometries":[{"type":"LineString","arcs":[0]},{"type":"LineString","arcs":[1]},{"type":"LineString","arcs":[3]}]}}`
	const arcs = `"arcs":[[["1",2],["3",4],[null,"5"]],[[null,"5"],[2,"0"],[7,8,"z"]],[[0,0],["10",null],[20,"5"],[0,0]],[["0",""],[null,false]]]`
	feat := func(g string) string { return `{"type":"Feature","properties":{},"geometry":` + g + `}` }
	for _, c := range []struct {
		transform string
		want      map[string]string
	}{
		{"", map[string]string{
			"l":        feat(`{"type":"LineString","coordinates":[["1",2],["3",4],[null,"5"],[2,"0"],[7,8,"z"]]}`),
			"p":        feat(`{"type":"Point","coordinates":["3",null]}`),
			"mp":       feat(`{"type":"MultiPoint","coordinates":[["1","2"],[null,4],[true,"x"]]}`),
			"pg":       feat(`{"type":"Polygon","coordinates":[[[0,0],["10",null],[20,"5"],[0,0]]]}`),
			"c":        `{"type":"FeatureCollection","features":[` + feat(`{"type":"LineString","coordinates":[["1",2],["3",4],[null,"5"]]}`) + `,` + feat(`{"type":"LineString","coordinates":[[null,"5"],[2,"0"],[7,8,"z"]]}`) + `,` + feat(`{"type":"LineString","coordinates":[["0",""],[null,false]]}`) + `]}`,
			"mesh":     `{"type":"MultiLineString","coordinates":[[["0",""],[null,false]],[["1",2],["3",4],[null,"5"],[2,"0"],[7,8,"z"]]]}`,
			"interior": `{"type":"MultiLineString","coordinates":[]}`,
			"exterior": `{"type":"MultiLineString","coordinates":[[["0",""],[null,false]],[["1",2],["3",4],[null,"5"],[2,"0"],[7,8,"z"]]]}`,
		}},
		{`"transform":{"scale":[2,3],"translate":[10,"20"]},`, map[string]string{
			"l":        feat(`{"type":"LineString","coordinates":[[12,"620"],[36,"1820"],[10,"1520"],[14,"15020"],[28,"152420","z"]]}`),
			"p":        feat(`{"type":"Point","coordinates":[16,"020"]}`),
			"mp":       feat(`{"type":"MultiPoint","coordinates":[[12,"620"],[10,"1220"],[12,"NaN20"]]}`),
			"pg":       feat(`{"type":"Polygon","coordinates":[[[10,"020"],[30,"020"],[2050,"1520"],[20410,"15020"]]]}`),
			"c":        `{"type":"FeatureCollection","features":[` + feat(`{"type":"LineString","coordinates":[[12,"620"],[36,"1820"],["NaN!","19520"]]}`) + `,` + feat(`{"type":"LineString","coordinates":[[10,"1520"],[14,"15020"],[28,"152420","z"]]}`) + `,` + feat(`{"type":"LineString","coordinates":[[10,"020"],["NaN!","NaN20"]]}`) + `]}`,
			"mesh":     `{"type":"MultiLineString","coordinates":[[[10,"020"],["NaN!","NaN20"]],[[10,"1520"],[14,"15020"],[28,"152420","z"]],[[12,"620"],[36,"1820"],["NaN!","19520"]]]}`,
			"interior": `{"type":"MultiLineString","coordinates":[]}`,
			"exterior": `{"type":"MultiLineString","coordinates":[[[10,"020"],["NaN!","NaN20"]],[[10,"1520"],[14,"15020"],[28,"152420","z"]],[[12,"620"],[36,"1820"],["NaN!","19520"]]]}`,
		}},
	} {
		topo := topoDoc(t, `{"type":"Topology",`+c.transform+arcs+`,`+objs+`}`)
		for name, want := range c.want {
			var got jsval.Value
			var err error
			switch name {
			case "mesh", "interior", "exterior":
				got, err = TopoMesh(bg, topo, topo.Get("objects").Get("c"), map[string]MeshFilter{"mesh": MeshAll, "interior": MeshInterior, "exterior": MeshExterior}[name])
			default:
				got, err = TopoFeature(bg, topo, topo.Get("objects").Get(name))
			}
			if err != nil {
				t.Fatalf("%q %s: %v", c.transform, name, err)
			}
			if g := encTagged(got); g != want {
				t.Errorf("%q %s:\n got %s\nwant %s", c.transform, name, g, want)
			}
		}
	}
}

// TestTopoStringSumBounded: a quantized arc of string deltas sums to a
// string as long as the arc; it is charged and its work metered.
func TestTopoStringSumBounded(t *testing.T) {
	pts := make([]jsval.Value, 8000)
	for i := range pts {
		pts[i] = jsval.ArrOf(jsval.Str("1"), jsval.Str("1"))
	}
	topo := jsval.Obj(jsval.ObjectOf(
		"type", jsval.Str("Topology"),
		"transform", jsval.Obj(jsval.ObjectOf("scale", jsval.ArrOf(jsval.Num(1), jsval.Num(1)), "translate", jsval.ArrOf(jsval.Num(0), jsval.Num(0)))),
		"arcs", jsval.ArrOf(jsval.Arr(pts)),
		"objects", jsval.Obj(jsval.ObjectOf("o", jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "arcs", jsval.ArrOf(jsval.Num(0)))))),
	))
	ctx, l := ledgerCtx(0)
	if _, err := TopologyFeatures(ctx, topo, "o", "", ""); err != nil {
		t.Fatal(err)
	}
	// Two sums of up to 8,000 digits, parsed at every position.
	if l.WorkDone() < 8000*8000/16 {
		t.Errorf("work %d", l.WorkDone())
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TopologyFeatures(done, topo, "o", "", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	// The string's bytes are charged as it grows, and given back.
	ctx, l = ledgerCtx(4 << 10)
	top := &topology{led: l, m: budget.NewMeter(ctx)}
	var sum jsSum
	var err error
	for i := 0; i < 4000 && err == nil; i++ {
		err = sum.add(jsval.Str("1"), top)
	}
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("a 4,000-byte sum against a 4 KiB ledger: %v", err)
	}
	sum.release(top)
	if l.Bytes() != 0 {
		t.Errorf("%d bytes still charged after release", l.Bytes())
	}
}
