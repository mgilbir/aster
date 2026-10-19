package geo

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// MeshFilter selects which arcs TopoMesh keeps, by how many distinct geometries
// share them (vega-loader's `filter` option of the topojson format).
type MeshFilter uint8

const (
	// MeshAll keeps every arc of the object.
	MeshAll MeshFilter = iota
	// MeshInterior keeps arcs shared by two different geometries.
	MeshInterior
	// MeshExterior keeps arcs used by a single geometry (borders).
	MeshExterior
)

// ParseMeshFilter maps vega-loader's filter names; an unknown or empty name
// means no filter, as upstream (`filters[format.filter]` is then undefined).
func ParseMeshFilter(name string) MeshFilter {
	switch name {
	case "interior":
		return MeshInterior
	case "exterior":
		return MeshExterior
	}
	return MeshAll
}

// maxTopoDepth bounds GeometryCollection nesting in TopoJSON input.
const maxTopoDepth = 256

// topology is a decoded TopoJSON topology: its arcs and quantization
// transform, and the call's ledger and work meter, which pay for every
// position materialised from it.
type topology struct {
	arcs      []topoArc
	transform *topoTransform

	led   *budget.Ledger
	m     budget.Meter
	bytes int64 // charged to led for the decoded arcs
	// walk is the call's Walker: a position's coordinates are read as
	// text where topojson-client's arithmetic concatenates them.
	walk jsval.Walker
}

// topoArc is one arc: its positions, the input's own arrays, which
// topojson-client hands on as they are when the topology has no transform.
// A quantized arc's positions are deltas.
type topoArc struct {
	pts []jsval.Value
	// outVals is the number of values the arc's positions hold once decoded
	// with the topology's transform (each padded to two).
	outVals int
}

// topoTransform is the quantization transform, read as topojson-client
// uses it: `(x0 += input[0]) * kx + dx`, so the scales are numbers and the
// translations stay the JSON values they are (a string one concatenates).
type topoTransform struct {
	kx, ky float64
	dx, dy jsval.Value
}

var errTopology = errors.New("geo: invalid TopoJSON topology")

// Bytes the decoding charges to the ledger: an arc, an output position's
// slot in its line, counted twice for the line's growth (32), a transformed
// position's array header (24), and 16 per element it holds.
const (
	topoArcBytes  = 32 // a topoArc
	topoSlotBytes = 32
	topoArrBytes  = 24
	topoValBytes  = 16
)

// charge pays for an allocation of n bytes about to be made, and for work
// steps of work.
func (t *topology) charge(n, work int64) error {
	if err := t.led.Alloc(n); err != nil {
		return err
	}
	return t.m.Work(work)
}

// release gives back what decoding the arcs charged, once they are dropped.
func (t *topology) release() { t.led.Free(t.bytes) }

func decodeTopology(ctx context.Context, t jsval.Value) (*topology, error) {
	if !t.IsObj() {
		return nil, errTopology
	}
	top := &topology{led: budget.LedgerFrom(ctx), m: budget.NewMeter(ctx), walk: jsval.WalkerFor(ctx)}
	if tr := t.Get("transform"); !tr.IsNullish() {
		s, tl := tr.Get("scale"), tr.Get("translate")
		if !s.IsArr() || !tl.IsArr() {
			return nil, fmt.Errorf("%w: malformed transform", errTopology)
		}
		top.transform = &topoTransform{
			kx: coord(s.Index(0)), ky: coord(s.Index(1)),
			dx: tl.Index(0), dy: tl.Index(1),
		}
	}
	arcs := t.Get("arcs")
	if !arcs.IsArr() {
		return nil, fmt.Errorf("%w: missing arcs", errTopology)
	}
	items := arcs.Items()
	if err := top.charge(budget.Mul(int64(len(items)), topoArcBytes), int64(len(items))); err != nil {
		return nil, err
	}
	top.bytes = budget.Mul(int64(len(items)), topoArcBytes)
	top.arcs = make([]topoArc, len(items))
	for i, a := range items {
		if !a.IsArr() {
			top.release()
			return nil, fmt.Errorf("%w: arc %d is not an array", errTopology, i)
		}
		pts := a.Items()
		if err := top.m.Work(int64(len(pts))); err != nil {
			top.release()
			return nil, err
		}
		outVals := 0
		for _, p := range pts {
			if !p.IsArr() {
				top.release()
				return nil, fmt.Errorf("%w: arc %d has a malformed position", errTopology, i)
			}
			outVals += max(p.Len(), 2)
		}
		top.arcs[i] = topoArc{pts: pts, outVals: outVals}
	}
	return top, nil
}

func (t *topology) arc(i int) (*topoArc, error) {
	j := i
	if i < 0 {
		j = ^i
	}
	if j < 0 || j >= len(t.arcs) {
		return nil, fmt.Errorf("%w: arc index %d out of range", errTopology, i)
	}
	return &t.arcs[j], nil
}

// arcIndex converts a JSON arc reference to an int. A reference must be an
// integer of magnitude below 2^31, which every int holds; no topology has
// that many arcs.
func arcIndex(v jsval.Value) (int, error) {
	f, ok := v.NumberOrNull()
	if !ok || f != math.Trunc(f) || !(math.Abs(f) < 1<<31) {
		return 0, fmt.Errorf("%w: bad arc reference %s", errTopology, v.String())
	}
	return int(f), nil
}

// decoder builds GeoJSON geometry from topology geometry objects.
type decoder struct{ t *topology }

// transformed is topojson-client's transform of one position, `output =
// new Array(n)` with elements 0 and 1 set to `(x0 += input[0]) * kx + dx`
// and `(y0 += input[1]) * ky + dy` and the others copied: the running sums
// and the translations are JavaScript values, so a string coordinate
// concatenates as it does upstream.
func (d decoder) transformed(p jsval.Value, x0, y0 *jsSum) (jsval.Value, error) {
	tr := d.t.transform
	items := p.Items()
	arr, out := jsval.MakeArr(max(len(items), 2))
	if err := x0.add(p.Index(0), d.t); err != nil {
		return arr, err
	}
	if err := y0.add(p.Index(1), d.t); err != nil {
		return arr, err
	}
	var err error
	if out[0], err = jsScaleAdd(x0, tr.kx, tr.dx, d.t); err != nil {
		return arr, err
	}
	if out[1], err = jsScaleAdd(y0, tr.ky, tr.dy, d.t); err != nil {
		return arr, err
	}
	if len(items) > 2 {
		copy(out[2:], items[2:])
	}
	return arr, nil
}

// arcInto appends the points of arc i (reversed for negative i) to points,
// dropping the joint point shared with the previous arc. Every reference
// materialises the arc anew, as upstream does, so a topology can reference
// one long arc many times over: what it makes is charged first (C5).
// Without a transform the positions are the input's arrays, as upstream's.
func (d decoder) arcInto(i int, points []jsval.Value) ([]jsval.Value, error) {
	a, err := d.t.arc(i)
	if err != nil {
		return nil, err
	}
	n := len(a.pts)
	size, work := budget.Mul(int64(n), topoSlotBytes), int64(n)
	if d.t.transform != nil {
		size += budget.Mul(int64(n), topoArrBytes) + budget.Mul(int64(a.outVals), topoValBytes)
		work += int64(a.outVals)
	}
	if err := d.t.charge(size, work); err != nil {
		return nil, err
	}
	if len(points) > 0 {
		points = points[:len(points)-1]
	}
	start := len(points)
	if d.t.transform == nil {
		points = append(points, a.pts...)
	} else {
		var x0, y0 jsSum
		defer x0.release(d.t)
		defer y0.release(d.t)
		for _, pk := range a.pts {
			p, err := d.transformed(pk, &x0, &y0)
			if err != nil {
				return nil, err
			}
			points = append(points, p)
		}
	}
	if i < 0 {
		for lo, hi := start, len(points)-1; lo < hi; lo, hi = lo+1, hi-1 {
			points[lo], points[hi] = points[hi], points[lo]
		}
	}
	return points, nil
}

func (d decoder) line(arcs jsval.Value) (jsval.Value, error) {
	var points []jsval.Value
	for _, r := range arcs.Items() {
		i, err := arcIndex(r)
		if err != nil {
			return jsval.Undefined, err
		}
		if points, err = d.arcInto(i, points); err != nil {
			return jsval.Undefined, err
		}
	}
	return jsval.Arr(d.padTo(points, 2)), nil
}

// padTo repeats the first point until there are n of them, as topojson-client
// does for degenerate lines and rings ("should never happen per the
// specification"); an empty line repeats undefined.
func (d decoder) padTo(points []jsval.Value, n int) []jsval.Value {
	var first jsval.Value
	if len(points) > 0 {
		first = points[0]
	}
	for len(points) < n {
		points = append(points, first)
	}
	return points
}

func (d decoder) ring(arcs jsval.Value) (jsval.Value, error) {
	l, err := d.line(arcs)
	if err != nil {
		return l, err
	}
	return jsval.Arr(d.padTo(l.Items(), 4)), nil
}

func (d decoder) polygon(arcs jsval.Value) (jsval.Value, error) {
	items := arcs.Items()
	out := make([]jsval.Value, len(items))
	for i, a := range items {
		r, err := d.ring(a)
		if err != nil {
			return r, err
		}
		out[i] = r
	}
	return jsval.Arr(out), nil
}

// point decodes a lone position: topojson-client returns it as it is, or
// transformed as an arc's first position.
func (d decoder) point(p jsval.Value) (jsval.Value, error) {
	if d.t.transform == nil {
		return p, d.t.charge(topoSlotBytes, 1)
	}
	n := max(p.Len(), 2)
	if err := d.t.charge(topoSlotBytes+topoArrBytes+budget.Mul(int64(n), topoValBytes), int64(n)+1); err != nil {
		return jsval.Undefined, err
	}
	var x0, y0 jsSum
	defer x0.release(d.t)
	defer y0.release(d.t)
	return d.transformed(p, &x0, &y0)
}

// geometry decodes a TopoJSON geometry object into a GeoJSON geometry; unknown
// types become null.
func (d decoder) geometry(o jsval.Value, depth int) (jsval.Value, error) {
	if depth > maxTopoDepth {
		return jsval.Undefined, fmt.Errorf("%w: geometry nesting too deep: %w", errTopology, budget.ErrLimit)
	}
	typ := o.Get("type")
	name := ""
	if typ.IsStr() {
		name = typ.StrValue()
	}
	var coords jsval.Value
	switch name {
	case "GeometryCollection":
		gs := o.Get("geometries").Items()
		out := make([]jsval.Value, len(gs))
		for i, g := range gs {
			v, err := d.geometry(g, depth+1)
			if err != nil {
				return v, err
			}
			out[i] = v
		}
		return jsval.Obj(jsval.ObjectOf("type", typ, "geometries", jsval.Arr(out))), nil
	case "Point":
		p, err := d.point(o.Get("coordinates"))
		if err != nil {
			return p, err
		}
		coords = p
	case "MultiPoint":
		ps := o.Get("coordinates").Items()
		out := make([]jsval.Value, len(ps))
		for i, p := range ps {
			v, err := d.point(p)
			if err != nil {
				return v, err
			}
			out[i] = v
		}
		coords = jsval.Arr(out)
	case "LineString":
		l, err := d.line(o.Get("arcs"))
		if err != nil {
			return l, err
		}
		coords = l
	case "MultiLineString":
		items := o.Get("arcs").Items()
		out := make([]jsval.Value, len(items))
		for i, a := range items {
			l, err := d.line(a)
			if err != nil {
				return l, err
			}
			out[i] = l
		}
		coords = jsval.Arr(out)
	case "Polygon":
		p, err := d.polygon(o.Get("arcs"))
		if err != nil {
			return p, err
		}
		coords = p
	case "MultiPolygon":
		items := o.Get("arcs").Items()
		out := make([]jsval.Value, len(items))
		for i, a := range items {
			p, err := d.polygon(a)
			if err != nil {
				return p, err
			}
			out[i] = p
		}
		coords = jsval.Arr(out)
	default:
		return jsval.Null, nil
	}
	return jsval.Obj(jsval.ObjectOf("type", typ, "coordinates", coords)), nil
}

func (d decoder) feature(o jsval.Value, depth int) (jsval.Value, error) {
	id, bbox := o.Get("id"), o.Get("bbox")
	props := o.Get("properties")
	if props.IsNullish() {
		props = jsval.Obj(jsval.NewObject(0))
	}
	geom, err := d.geometry(o, depth)
	if err != nil {
		return geom, err
	}
	f := jsval.NewObject(5)
	f.Set("type", jsval.Str("Feature"))
	if !id.IsNullish() || !bbox.IsNullish() {
		f.Set("id", id)
		if !bbox.IsNullish() {
			f.Set("bbox", bbox)
		}
	}
	f.Set("properties", props)
	f.Set("geometry", geom)
	return jsval.Obj(f), nil
}

// TopoFeature is topojson.feature: a GeoJSON Feature for a topology object, or a
// FeatureCollection when the object is a GeometryCollection. The topology's
// quantization transform is applied; arcs are delta-decoded. What it
// decodes and makes is charged to the ledger in ctx first, and its work
// metered, stopping with ctx's error.
func TopoFeature(ctx context.Context, topo, object jsval.Value) (jsval.Value, error) {
	t, err := decodeTopology(ctx, topo)
	if err != nil {
		return jsval.Undefined, err
	}
	defer t.release()
	return topoFeature(t, object)
}

func topoFeature(t *topology, o jsval.Value) (jsval.Value, error) {
	d := decoder{t}
	if !o.IsObj() {
		return jsval.Undefined, fmt.Errorf("%w: object is not an object", errTopology)
	}
	if typ := o.Get("type"); typ.IsStr() && typ.StrValue() == "GeometryCollection" {
		gs := o.Get("geometries").Items()
		feats := make([]jsval.Value, len(gs))
		for i, g := range gs {
			if !g.IsObj() {
				return jsval.Undefined, fmt.Errorf("%w: geometry %d is not an object", errTopology, i)
			}
			f, err := d.feature(g, 0)
			if err != nil {
				return f, err
			}
			feats[i] = f
		}
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("FeatureCollection"), "features", jsval.Arr(feats))), nil
	}
	return d.feature(o, 0)
}

// TopoMesh is topojson.mesh: a single MultiLineString of the (stitched) arcs of
// a topology object, optionally filtered to interior or exterior arcs. It is
// bounded by ctx as TopoFeature is.
func TopoMesh(ctx context.Context, topo, object jsval.Value, filter MeshFilter) (jsval.Value, error) {
	t, err := decodeTopology(ctx, topo)
	if err != nil {
		return jsval.Undefined, err
	}
	defer t.release()
	return topoMesh(t, object, filter)
}

func topoMesh(t *topology, o jsval.Value, filter MeshFilter) (jsval.Value, error) {
	if !o.IsObj() {
		return jsval.Undefined, fmt.Errorf("%w: object is not an object", errTopology)
	}
	arcs, err := extractArcs(t, o, filter)
	if err != nil {
		return jsval.Undefined, err
	}
	stitched, err := stitch(t, arcs)
	if err != nil {
		return jsval.Undefined, err
	}
	d := decoder{t}
	lines := make([]jsval.Value, len(stitched))
	for i, f := range stitched {
		var points []jsval.Value
		for _, a := range f {
			if points, err = d.arcInto(a, points); err != nil {
				return jsval.Undefined, err
			}
		}
		lines[i] = jsval.Arr(d.padTo(points, 2))
	}
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiLineString"), "coordinates", jsval.Arr(lines))), nil
}

type geomRef struct {
	i int
	g *jsval.Object
}

// extractArcs collects, in ascending arc order, one signed arc index per arc
// used by the object, keeping arcs the filter accepts. The filter compares the
// first and last geometry that use an arc by identity.
func extractArcs(t *topology, object jsval.Value, filter MeshFilter) ([]int, error) {
	geomsByArc := map[int][]geomRef{}
	var order []int
	var err error

	extract0 := func(v jsval.Value, geom *jsval.Object) {
		if err != nil {
			return
		}
		var i int
		if i, err = arcIndex(v); err != nil {
			return
		}
		if err = t.m.Work(1); err != nil {
			return
		}
		j := i
		if i < 0 {
			j = ^i
		}
		if _, seen := geomsByArc[j]; !seen {
			order = append(order, j)
		}
		geomsByArc[j] = append(geomsByArc[j], geomRef{i, geom})
	}
	extractN := func(depth int, v jsval.Value, geom *jsval.Object) {
		var walk func(v jsval.Value, depth int)
		walk = func(v jsval.Value, depth int) {
			if depth == 0 {
				extract0(v, geom)
				return
			}
			for _, x := range v.Items() {
				walk(x, depth-1)
			}
		}
		walk(v, depth)
	}

	var geometry func(o jsval.Value, depth int)
	geometry = func(o jsval.Value, depth int) {
		if depth > maxTopoDepth {
			err = fmt.Errorf("%w: geometry nesting too deep: %w", errTopology, budget.ErrLimit)
			return
		}
		geom := o.ObjValue()
		switch typeName(o) {
		case "GeometryCollection":
			for _, g := range o.Get("geometries").Items() {
				geometry(g, depth+1)
			}
		case "LineString":
			extractN(1, o.Get("arcs"), geom)
		case "MultiLineString", "Polygon":
			extractN(2, o.Get("arcs"), geom)
		case "MultiPolygon":
			extractN(3, o.Get("arcs"), geom)
		}
	}
	geometry(object, 0)
	if err != nil {
		return nil, err
	}

	// geomsByArc.forEach visits array indexes ascending.
	sortInts(order)
	var arcs []int
	for _, j := range order {
		geoms := geomsByArc[j]
		switch filter {
		case MeshInterior:
			if geoms[0].g == geoms[len(geoms)-1].g {
				continue
			}
		case MeshExterior:
			if geoms[0].g != geoms[len(geoms)-1].g {
				continue
			}
		}
		if _, e := t.arc(geoms[0].i); e != nil {
			return nil, e
		}
		arcs = append(arcs, geoms[0].i)
	}
	return arcs, nil
}

func typeName(o jsval.Value) string {
	if t := o.Get("type"); t.IsStr() {
		return t.StrValue()
	}
	return ""
}

func sortInts(a []int) {
	// insertion sort is fine for the nearly sorted common case; fall back to a
	// simple heap-free merge for large unsorted input.
	if len(a) < 64 {
		for i := 1; i < len(a); i++ {
			for j := i; j > 0 && a[j-1] > a[j]; j-- {
				a[j-1], a[j] = a[j], a[j-1]
			}
		}
		return
	}
	tmp := make([]int, len(a))
	mergeSortInts(a, tmp)
}

func mergeSortInts(a, tmp []int) {
	n := len(a)
	if n < 32 {
		sortInts(a)
		return
	}
	mid := n / 2
	mergeSortInts(a[:mid], tmp[:mid])
	mergeSortInts(a[mid:], tmp[mid:])
	copy(tmp, a)
	i, j, k := 0, mid, 0
	for i < mid && j < n {
		if tmp[j] < tmp[i] {
			a[k] = tmp[j]
			j++
		} else {
			a[k] = tmp[i]
			i++
		}
		k++
	}
	for i < mid {
		a[k] = tmp[i]
		i++
		k++
	}
	for j < n {
		a[k] = tmp[j]
		j++
		k++
	}
}

// fragment is a chain of arcs being stitched, keyed by its start and end
// positions.
type fragment struct {
	arcs       []int
	start, end string
}

// fragMap is a JavaScript object used as a string-keyed map: iteration follows
// first-insertion order, and deleting then re-adding a key moves it to the end.
type fragMap struct {
	m     map[string]*fragEntry
	order []*fragEntry
}

type fragEntry struct {
	key  string
	f    *fragment
	live bool
}

func newFragMap() *fragMap { return &fragMap{m: map[string]*fragEntry{}} }

func (m *fragMap) get(k string) *fragment {
	if e := m.m[k]; e != nil {
		return e.f
	}
	return nil
}

func (m *fragMap) set(k string, f *fragment) {
	if e := m.m[k]; e != nil {
		e.f = f
		return
	}
	e := &fragEntry{key: k, f: f, live: true}
	m.m[k] = e
	m.order = append(m.order, e)
}

func (m *fragMap) del(k string) {
	if e := m.m[k]; e != nil {
		e.live = false
		delete(m.m, k)
	}
}

func (m *fragMap) each(fn func(k string, f *fragment)) {
	snapshot := make([]*fragEntry, 0, len(m.m))
	for _, e := range m.order {
		if e.live {
			snapshot = append(snapshot, e)
		}
	}
	for _, e := range snapshot {
		if e.live {
			fn(e.key, e.f)
		}
	}
}

// stitch joins arcs end to end into fragments (topojson-client's stitch), so a
// mesh draws each border as long polylines instead of many small arcs.
func stitch(t *topology, arcs []int) ([][]int, error) {
	stitched := map[int]bool{}
	byStart, byEnd := newFragMap(), newFragMap()
	var fragments [][]int
	emptyIndex := -1

	// Stitch empty arcs first, since they may be subsumed by other arcs.
	for j := 0; j < len(arcs); j++ {
		i := arcs[j]
		a, err := t.arc(i)
		if err != nil {
			return nil, err
		}
		if len(a.pts) < 2 {
			return nil, fmt.Errorf("%w: arc %d has fewer than two positions", errTopology, i)
		}
		if p := a.pts[1]; len(a.pts) < 3 && !p.Index(0).IsTruthy() && !p.Index(1).IsTruthy() {
			emptyIndex++
			tmp := arcs[emptyIndex]
			arcs[emptyIndex] = i
			arcs[j] = tmp
		}
	}

	// ends is topojson-client's: the first position and the last (with a
	// transform, the sums of the deltas, `p1[0] += dp[0]`), as the keys of
	// JavaScript objects, so String(array).
	ends := func(i int) (string, string, error) {
		a, _ := t.arc(i)
		p0 := t.walk.String(a.pts[0])
		var p1 string
		if t.transform != nil {
			var x, y jsSum
			defer x.release(t)
			defer y.release(t)
			for _, p := range a.pts {
				if err := x.add(p.Index(0), t); err != nil {
					return "", "", err
				}
				if err := y.add(p.Index(1), t); err != nil {
					return "", "", err
				}
			}
			p1 = x.String() + "," + y.String()
		} else {
			p1 = t.walk.String(a.pts[len(a.pts)-1])
		}
		if i < 0 {
			return p1, p0, nil
		}
		return p0, p1, nil
	}

	for _, i := range arcs {
		start, end, err := ends(i)
		if err != nil {
			return nil, err
		}
		if f := byEnd.get(start); f != nil {
			byEnd.del(f.end)
			f.arcs = append(f.arcs, i)
			f.end = end
			if g := byStart.get(end); g != nil {
				byStart.del(g.start)
				fg := f
				if g != f {
					fg = &fragment{arcs: append(append([]int(nil), f.arcs...), g.arcs...)}
				}
				fg.start = f.start
				fg.end = g.end
				byStart.set(fg.start, fg)
				byEnd.set(fg.end, fg)
			} else {
				byStart.set(f.start, f)
				byEnd.set(f.end, f)
			}
		} else if f := byStart.get(end); f != nil {
			byStart.del(f.start)
			f.arcs = append([]int{i}, f.arcs...)
			f.start = start
			if g := byEnd.get(start); g != nil {
				byEnd.del(g.end)
				gf := f
				if g != f {
					gf = &fragment{arcs: append(append([]int(nil), g.arcs...), f.arcs...)}
				}
				gf.start = g.start
				gf.end = f.end
				byStart.set(gf.start, gf)
				byEnd.set(gf.end, gf)
			} else {
				byStart.set(f.start, f)
				byEnd.set(f.end, f)
			}
		} else {
			f := &fragment{arcs: []int{i}, start: start, end: end}
			byStart.set(start, f)
			byEnd.set(end, f)
		}
	}

	flush := func(byEnd, byStart *fragMap) {
		byEnd.each(func(_ string, f *fragment) {
			byStart.del(f.start)
			for _, i := range f.arcs {
				if i < 0 {
					stitched[^i] = true
				} else {
					stitched[i] = true
				}
			}
			fragments = append(fragments, f.arcs)
		})
	}
	flush(byEnd, byStart)
	flush(byStart, byEnd)
	for _, i := range arcs {
		j := i
		if i < 0 {
			j = ^i
		}
		if !stitched[j] {
			fragments = append(fragments, []int{i})
		}
	}
	return fragments, nil
}

// TopologyFeatures is vega-loader's topojson format: it extracts the named
// object of a parsed TopoJSON topology as either `feature` (one tuple per
// feature) or `mesh` (a single MultiLineString, optionally filtered).
// Exactly the semantics of vega-loader: with a feature name the result is the
// features of the object (or the single Feature/geometry); with a mesh name the
// single mesh geometry.
//
// Decoding is bounded by ctx (see TopoFeature): an error wrapping
// budget.ErrLimit past the ledger's bytes, or ctx's error.
func TopologyFeatures(ctx context.Context, topo jsval.Value, feature, mesh, filter string) ([]jsval.Value, error) {
	useFeature := feature != ""
	if !useFeature && mesh == "" {
		return nil, errors.New("Missing TopoJSON feature or mesh parameter.")
	}
	property := mesh
	if useFeature {
		property = feature
	}
	objects := topo.Get("objects")
	if !topo.IsObj() || !objects.IsObj() {
		return nil, fmt.Errorf("Invalid TopoJSON object: %s", property)
	}
	object, ok := objects.ObjValue().Get(property)
	if !ok || !object.IsTruthy() {
		return nil, fmt.Errorf("Invalid TopoJSON object: %s", property)
	}
	t, err := decodeTopology(ctx, topo)
	if err != nil {
		return nil, err
	}
	defer t.release()
	var out jsval.Value
	if useFeature {
		out, err = topoFeature(t, object)
	} else {
		out, err = topoMesh(t, object, ParseMeshFilter(filter))
	}
	if err != nil {
		return nil, err
	}
	if feats := out.Get("features"); feats.IsArr() {
		return feats.Items(), nil
	}
	return []jsval.Value{out}, nil
}
