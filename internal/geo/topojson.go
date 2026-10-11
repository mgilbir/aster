package geo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

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
// transform, and the call's ledger and work meter, which pay for decoding
// and for every position materialised from it.
type topology struct {
	arcs      []topoArc
	transform *topoTransform

	led   *budget.Ledger
	m     budget.Meter
	bytes int64 // charged to led for the decoded arcs
}

// topoArc is one arc: n positions, flat in v. Positions keep their own
// length, as topojson-client keeps each input position (only a transform
// pads one to two numbers): dim is the length of every position, or -1 when
// they differ and off holds the n+1 offsets of the positions in v. A
// quantized arc's positions are deltas.
type topoArc struct {
	n, dim int
	off    []int
	v      []float64
	// outVals is the number of values the arc's positions hold once decoded
	// with the topology's transform (each padded to two numbers).
	outVals int
}

// pos returns position k.
func (a *topoArc) pos(k int) []float64 {
	if a.off == nil {
		return a.v[k*a.dim : (k+1)*a.dim : (k+1)*a.dim]
	}
	return a.v[a.off[k]:a.off[k+1]:a.off[k+1]]
}

// at is element i of a position, NaN past its end: undefined in JavaScript
// arithmetic.
func at(p []float64, i int) float64 {
	if i < len(p) {
		return p[i]
	}
	return nan
}

type topoTransform struct{ kx, ky, dx, dy float64 }

var errTopology = errors.New("geo: invalid TopoJSON topology")

// Bytes the decoding charges to the ledger: a decoded number, and an output
// position, which is an array header in a slab (24), a slot in its line,
// counted twice for the line's growth (32), and 16 per element.
const (
	topoArcBytes = 72 // a topoArc
	topoNumBytes = 8
	topoPosBytes = 24 + 32
	topoValBytes = 16
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
	top := &topology{led: budget.LedgerFrom(ctx), m: budget.NewMeter(ctx)}
	if tr := t.Get("transform"); !tr.IsNullish() {
		s, tl := tr.Get("scale"), tr.Get("translate")
		if !s.IsArr() || !tl.IsArr() {
			return nil, fmt.Errorf("%w: malformed transform", errTopology)
		}
		top.transform = &topoTransform{
			kx: coord(s.Index(0)), ky: coord(s.Index(1)),
			dx: coord(tl.Index(0)), dy: coord(tl.Index(1)),
		}
	}
	arcs := t.Get("arcs")
	if !arcs.IsArr() {
		return nil, fmt.Errorf("%w: missing arcs", errTopology)
	}
	items := arcs.Items()
	if err := top.alloc(budget.Mul(int64(len(items)), topoArcBytes), int64(len(items))); err != nil {
		return nil, err
	}
	top.arcs = make([]topoArc, len(items))
	for i, a := range items {
		if !a.IsArr() {
			top.release()
			return nil, fmt.Errorf("%w: arc %d is not an array", errTopology, i)
		}
		arc, err := top.decodeArc(a.Items())
		if err != nil {
			top.release()
			if errors.Is(err, errTopology) {
				return nil, fmt.Errorf("%w: arc %d has a malformed position", errTopology, i)
			}
			return nil, err
		}
		top.arcs[i] = arc
	}
	return top, nil
}

// alloc is charge, for memory the topology keeps until release.
func (t *topology) alloc(n, work int64) error {
	if err := t.charge(n, work); err != nil {
		return err
	}
	t.bytes += n
	return nil
}

// decodeArc decodes the positions of one arc, charging them first.
func (t *topology) decodeArc(pts []jsval.Value) (topoArc, error) {
	arc := topoArc{n: len(pts)}
	total, outVals := 0, 0
	for k, p := range pts {
		if !p.IsArr() {
			return arc, errTopology
		}
		l := p.Len()
		switch {
		case k == 0:
			arc.dim = l
		case l != arc.dim:
			arc.dim = -1
		}
		total += l
		outVals += max(l, 2)
	}
	if t.transform != nil {
		arc.outVals = outVals
	} else {
		arc.outVals = total
	}
	size := budget.Mul(int64(total), topoNumBytes)
	if arc.dim < 0 {
		size += budget.Mul(int64(len(pts)+1), 8)
	}
	if err := t.alloc(size, int64(len(pts))+int64(total)); err != nil {
		return arc, err
	}
	arc.v = make([]float64, 0, total)
	if arc.dim < 0 {
		arc.off = make([]int, 0, len(pts)+1)
	}
	for _, p := range pts {
		if arc.off != nil {
			arc.off = append(arc.off, len(arc.v))
		}
		for _, c := range p.Items() {
			arc.v = append(arc.v, coord(c))
		}
	}
	if arc.off != nil {
		arc.off = append(arc.off, len(arc.v))
	}
	return arc, nil
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

// outLen is the length of a decoded position of n numbers: topojson-client
// returns an untransformed position as it is, and makes a transformed one
// `new Array(n)` with elements 0 and 1 set.
func (d decoder) outLen(n int) int {
	if d.t.transform != nil {
		return max(n, 2)
	}
	return n
}

// emit decodes one arc position into out, accumulating the deltas of a
// quantized topology; it returns the running sums for the next one.
func (d decoder) emit(p []float64, out []jsval.Value, x0, y0 float64) (float64, float64) {
	j := 0
	if tr := d.t.transform; tr != nil {
		x0 += at(p, 0)
		y0 += at(p, 1)
		out[0] = jsval.Num(float64(x0*tr.kx) + tr.dx)
		out[1] = jsval.Num(float64(y0*tr.ky) + tr.dy)
		j = 2
	}
	for ; j < len(p); j++ {
		out[j] = jsval.Num(p[j])
	}
	return x0, y0
}

// arcInto appends the points of arc i (reversed for negative i) to points,
// dropping the joint point shared with the previous arc. Every reference
// materialises the arc anew, as upstream does, so a topology can reference
// one long arc many times over: the positions are charged before they are
// made (C5).
func (d decoder) arcInto(i int, points []jsval.Value) ([]jsval.Value, error) {
	a, err := d.t.arc(i)
	if err != nil {
		return nil, err
	}
	size := budget.Mul(int64(a.n), topoPosBytes) + budget.Mul(int64(a.outVals), topoValBytes)
	if err := d.t.charge(size, int64(a.n)+int64(a.outVals)); err != nil {
		return nil, err
	}
	if len(points) > 0 {
		points = points[:len(points)-1]
	}
	start := len(points)
	var x0, y0 float64
	if a.dim >= 0 {
		slab := jsval.NewArrSlab(a.n, d.outLen(a.dim))
		for k := 0; k < a.n; k++ {
			p, out := slab.Next()
			x0, y0 = d.emit(a.pos(k), out, x0, y0)
			points = append(points, p)
		}
	} else {
		for k := 0; k < a.n; k++ {
			pk := a.pos(k)
			p, out := jsval.MakeArr(d.outLen(len(pk)))
			x0, y0 = d.emit(pk, out, x0, y0)
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

// point decodes a lone position, after charging it. It has no arc to
// accumulate over: only the transform applies, padding the position to two
// numbers as for an arc's.
func (d decoder) point(p jsval.Value) (jsval.Value, error) {
	items := p.Items()
	n := d.outLen(len(items))
	if err := d.t.charge(topoPosBytes+budget.Mul(int64(n), topoValBytes), int64(n)+1); err != nil {
		return jsval.Undefined, err
	}
	arr, out := jsval.MakeArr(n)
	for i, c := range items {
		out[i] = jsval.Num(coord(c))
	}
	if tr := d.t.transform; tr != nil {
		out[0] = jsval.Num(float64(coord(p.Index(0))*tr.kx) + tr.dx)
		out[1] = jsval.Num(float64(coord(p.Index(1))*tr.ky) + tr.dy)
	}
	return arr, nil
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

func posKey(v []float64) string {
	var b strings.Builder
	var buf []byte
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		buf = jsval.AppendJSNumber(buf[:0], x)
		b.Write(buf)
	}
	return b.String()
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
		if a.n < 2 {
			return nil, fmt.Errorf("%w: arc %d has fewer than two positions", errTopology, i)
		}
		// `!arc[1][0] && !arc[1][1]`: a missing or NaN number is falsy too.
		if p := a.pos(1); a.n < 3 && !truthy(at(p, 0)) && !truthy(at(p, 1)) {
			emptyIndex++
			tmp := arcs[emptyIndex]
			arcs[emptyIndex] = i
			arcs[j] = tmp
		}
	}

	ends := func(i int) (string, string) {
		a, _ := t.arc(i)
		p0 := a.pos(0)
		var p1 []float64
		if t.transform != nil {
			p1 = []float64{0, 0}
			for k := 0; k < a.n; k++ {
				p := a.pos(k)
				p1[0] += at(p, 0)
				p1[1] += at(p, 1)
			}
		} else {
			p1 = a.pos(a.n - 1)
		}
		if i < 0 {
			return posKey(p1), posKey(p0)
		}
		return posKey(p0), posKey(p1)
	}

	for _, i := range arcs {
		if a, _ := t.arc(i); a != nil {
			if err := t.m.Work(int64(a.n)); err != nil {
				return nil, err
			}
		}
		start, end := ends(i)
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
