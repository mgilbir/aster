package geo

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
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

// topology is a decoded TopoJSON topology: its arcs and quantization transform.
type topology struct {
	arcs      []topoArc
	transform *topoTransform
}

// topoArc is one arc: n positions of dim numbers each, flat. A quantized
// arc's positions are deltas.
type topoArc struct {
	n, dim int
	v      []float64
}

type topoTransform struct{ kx, ky, dx, dy float64 }

var errTopology = errors.New("geo: invalid TopoJSON topology")

func decodeTopology(t jsval.Value) (*topology, error) {
	if !t.IsObj() {
		return nil, errTopology
	}
	top := &topology{}
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
	top.arcs = make([]topoArc, len(items))
	for i, a := range items {
		if !a.IsArr() {
			return nil, fmt.Errorf("%w: arc %d is not an array", errTopology, i)
		}
		pts := a.Items()
		arc := topoArc{n: len(pts), dim: 2}
		if len(pts) > 0 && pts[0].IsArr() && pts[0].Len() > 2 {
			arc.dim = pts[0].Len()
		}
		arc.v = make([]float64, len(pts)*arc.dim)
		for k, p := range pts {
			if !p.IsArr() {
				return nil, fmt.Errorf("%w: arc %d has a malformed position", errTopology, i)
			}
			for d := 0; d < arc.dim; d++ {
				arc.v[k*arc.dim+d] = coord(p.Index(d))
			}
		}
		top.arcs[i] = arc
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

// arcIndex converts a JSON arc reference to an int.
func arcIndex(v jsval.Value) (int, error) {
	f, ok := v.NumberOrNull()
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1<<31 {
		return 0, fmt.Errorf("%w: bad arc reference %s", errTopology, v.String())
	}
	return int(f), nil
}

// decoder builds GeoJSON geometry from topology geometry objects.
type decoder struct{ t *topology }

// emit decodes one arc position, accumulating the deltas of a quantized
// topology; it returns the position and the running sums for the next one.
func (d decoder) emit(a *topoArc, k int, slab *jsval.ArrSlab, x0, y0 float64) (jsval.Value, float64, float64) {
	base := k * a.dim
	arr, out := slab.Next()
	if tr := d.t.transform; tr != nil {
		x0 += a.v[base]
		y0 += a.v[base+1]
		out[0] = jsval.Num(float64(x0*tr.kx) + tr.dx)
		out[1] = jsval.Num(float64(y0*tr.ky) + tr.dy)
	} else {
		out[0] = jsval.Num(a.v[base])
		out[1] = jsval.Num(a.v[base+1])
	}
	for j := 2; j < a.dim; j++ {
		out[j] = jsval.Num(a.v[base+j])
	}
	return arr, x0, y0
}

// arcInto appends the points of arc i (reversed for negative i) to points,
// dropping the joint point shared with the previous arc.
func (d decoder) arcInto(i int, points []jsval.Value) ([]jsval.Value, error) {
	a, err := d.t.arc(i)
	if err != nil {
		return nil, err
	}
	if len(points) > 0 {
		points = points[:len(points)-1]
	}
	start := len(points)
	var x0, y0 float64
	slab := jsval.NewArrSlab(a.n, a.dim)
	for k := 0; k < a.n; k++ {
		var p jsval.Value
		p, x0, y0 = d.emit(a, k, slab, x0, y0)
		points = append(points, p)
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

func (d decoder) point(p jsval.Value) jsval.Value {
	// A lone position has no arc to accumulate over: only the transform applies.
	items := p.Items()
	out := make([]jsval.Value, len(items))
	for i, c := range items {
		out[i] = jsval.Num(coord(c))
	}
	if tr := d.t.transform; tr != nil && len(out) >= 2 {
		out[0] = jsval.Num(float64(coord(items[0])*tr.kx) + tr.dx)
		out[1] = jsval.Num(float64(coord(items[1])*tr.ky) + tr.dy)
	}
	return jsval.Arr(out)
}

// geometry decodes a TopoJSON geometry object into a GeoJSON geometry; unknown
// types become null.
func (d decoder) geometry(o jsval.Value, depth int) (jsval.Value, error) {
	if depth > maxTopoDepth {
		return jsval.Undefined, fmt.Errorf("%w: geometry nesting too deep", errTopology)
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
		coords = d.point(o.Get("coordinates"))
	case "MultiPoint":
		ps := o.Get("coordinates").Items()
		out := make([]jsval.Value, len(ps))
		for i, p := range ps {
			out[i] = d.point(p)
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
// quantization transform is applied; arcs are delta-decoded.
func TopoFeature(topo, object jsval.Value) (jsval.Value, error) {
	t, err := decodeTopology(topo)
	if err != nil {
		return jsval.Undefined, err
	}
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
// a topology object, optionally filtered to interior or exterior arcs.
func TopoMesh(topo, object jsval.Value, filter MeshFilter) (jsval.Value, error) {
	t, err := decodeTopology(topo)
	if err != nil {
		return jsval.Undefined, err
	}
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
			err = fmt.Errorf("%w: geometry nesting too deep", errTopology)
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
		if a.n < 3 && a.v[a.dim] == 0 && a.v[a.dim+1] == 0 {
			emptyIndex++
			tmp := arcs[emptyIndex]
			arcs[emptyIndex] = i
			arcs[j] = tmp
		}
	}

	ends := func(i int) (string, string) {
		a, _ := t.arc(i)
		p0 := a.v[:a.dim]
		var p1 []float64
		if t.transform != nil {
			p1 = []float64{0, 0}
			for k := 0; k < a.n; k++ {
				p1[0] += a.v[k*a.dim]
				p1[1] += a.v[k*a.dim+1]
			}
		} else {
			p1 = a.v[(a.n-1)*a.dim : a.n*a.dim]
		}
		if i < 0 {
			return posKey(p1), posKey(p0)
		}
		return posKey(p0), posKey(p1)
	}

	for _, i := range arcs {
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
func TopologyFeatures(topo jsval.Value, feature, mesh, filter string) ([]jsval.Value, error) {
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
	t, err := decodeTopology(topo)
	if err != nil {
		return nil, err
	}
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
