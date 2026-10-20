package geo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// This file ports topojson-client's feature and mesh (feature.js, mesh.js,
// stitch.js, transform.js, reverse.js) with the JavaScript semantics they
// have for a topology of any shape: arcs are looked up by property key
// (`arcs[i < 0 ? ~i : i]`), positions are whatever values the arcs hold,
// a quantization transform's arithmetic is JavaScript's (`(x0 +=
// input[0]) * kx + dx`, which concatenates a string), and a member read of
// null, or a method of a value that has none, throws the TypeError it throws
// upstream (ADR 0013). Only what upstream reads is read: a malformed arc no
// geometry refers to is never looked at. What the decoding materialises is
// charged to the call's ledger before it is made (every reference to an arc
// makes its positions anew), and its work metered.

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

// MeshFilterJS is the filter mesh is called with: one of vega-loader's
// filters (Kind), none (MeshAll), or a value that throws when it is called
// (Throws, the TypeError): vega-loader's filters is a plain object, whose
// inherited Object.prototype functions throw called as a plain function.
type MeshFilterJS struct {
	Kind   MeshFilter
	Throws string
}

// maxTopoDepth bounds GeometryCollection nesting in TopoJSON input.
const maxTopoDepth = 256

// Bytes the decoding charges to the ledger: an output position's slot in its
// line, counted twice for the line's growth (32), a transformed position's
// array header (24), and 16 per element it holds.
const (
	topoSlotBytes = 32
	topoArrBytes  = 24
	topoValBytes  = 16
)

// topoThrow is an exception topojson-client throws (a TypeError, a
// RangeError), or a bound the decoding reached, raised as a panic inside the
// decoding and returned as its error.
type topoThrow struct{ err error }

func topoThrowf(format string, args ...any) {
	panic(topoThrow{fmt.Errorf(format, args...)})
}

// topoCatch recovers a topoThrow into *err.
func topoCatch(err *error) {
	if r := recover(); r != nil {
		t, ok := r.(topoThrow)
		if !ok {
			panic(r)
		}
		*err = t.err
	}
}

// topology is a TopoJSON topology being decoded, and the call's ledger and
// work meter, which pay for every position materialised from it.
type topology struct {
	val jsval.Value

	led *budget.Ledger
	m   budget.Meter
	// walk is the call's Walker: values are read as text where
	// topojson-client's arithmetic concatenates them or uses them as keys.
	walk jsval.Walker
	dec  *decoder
}

func newTopology(ctx context.Context, t jsval.Value) *topology {
	return &topology{val: t, led: budget.LedgerFrom(ctx), m: budget.NewMeter(ctx), walk: jsval.WalkerFor(ctx)}
}

// charge pays for an allocation of n bytes about to be made, and for work
// steps of work.
func (t *topology) charge(n, work int64) error {
	if err := t.led.Alloc(n); err != nil {
		return err
	}
	return t.m.Work(work)
}

// mustCharge is charge, throwing its error.
func (t *topology) mustCharge(n, work int64) {
	if err := t.charge(n, work); err != nil {
		panic(topoThrow{err})
	}
}

// member is `v[key]`: the TypeError for null or undefined, else the member
// as JavaScript reads it.
func member(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		topoThrowf("Cannot read properties of %s (reading '%s')", nullishText(v), key)
	}
	return ProtoRead(v, key)
}

// ProtoRead is jsval.Member plus the one read it leaves out: `v.__proto__`
// of a value without such an own member is its prototype, an empty array
// standing for Array.prototype (length 0, no elements) and an empty object
// for Object.prototype (no type, no arcs).
func ProtoRead(v jsval.Value, key string) jsval.Value {
	r := jsval.Member(v, key)
	if key == "__proto__" && (r.IsUndefined() || r.IsNull()) {
		switch {
		case v.IsArr():
			return jsval.Arr(nil)
		case v.IsObj():
			return jsval.Obj(jsval.NewObject(0))
		}
	}
	return r
}

func nullishText(v jsval.Value) string {
	if v.IsNull() {
		return "null"
	}
	return "undefined"
}

// elements is `v.map(...)` or `v.forEach(...)` (method) of what expr reads:
// an array's elements, else the TypeError V8 raises.
func elements(v jsval.Value, expr, method string) []jsval.Value {
	switch {
	case v.IsArr():
		return v.Items()
	case v.IsNullish():
		topoThrowf("Cannot read properties of %s (reading '%s')", nullishText(v), method)
	}
	topoThrowf("%s.%s is not a function", expr, method)
	return nil
}

// at is `v[k]` of an index k: an array's element directly, else member.
func at(v jsval.Value, k int) jsval.Value {
	if v.IsArr() {
		return v.Index(k)
	}
	return member(v, strconv.Itoa(k))
}

// arcRef is `arcs[i < 0 ? ~i : i]`: for an integer reference into an array of
// arcs, the element, without making its key.
func (t *topology) arcRef(arcs, i jsval.Value) jsval.Value {
	if arcs.IsArr() && i.IsNum() {
		if f := i.NumValue(); f == math.Trunc(f) && math.Abs(f) < 1<<31 {
			j := int(f)
			if j < 0 {
				j = ^j
			}
			return arcs.Index(j)
		}
	}
	return member(arcs, t.arcKey(i))
}

// key is the property key of a value: String(v).
func (t *topology) key(v jsval.Value) string {
	if v.IsStr() {
		return v.StrValue()
	}
	return t.walk.String(v)
}

// arcKey is the key `arcs[i < 0 ? ~i : i]` looks an arc up by.
func (t *topology) arcKey(i jsval.Value) string {
	if f := jsval.ToNumber(i); f < 0 {
		return strconv.Itoa(int(^toInt32(f)))
	}
	if i.IsNum() {
		if f := i.NumValue(); f >= 0 && f < 1<<31 && f == math.Trunc(f) {
			return strconv.Itoa(int(f))
		}
	}
	return t.key(i)
}

// toInt32 is ECMAScript's ToInt32 of a number.
func toInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	f = math.Trunc(f)
	m := math.Mod(f, 1<<32)
	if m < 0 {
		m += 1 << 32
	}
	return int32(uint32(m))
}

// less0 is `i < 0`.
func less0(i jsval.Value) bool { return jsval.ToNumber(i) < 0 }

// pointTransform is transform.js's transformPoint: the identity without a
// transform, else the running sums of a quantized arc's deltas, scaled and
// translated.
type pointTransform struct {
	t      *topology
	has    bool
	kx, ky float64
	dx, dy jsval.Value
	x0, y0 jsSum
}

// transformOf is `transform(topology.transform)`, which reads the scale and
// the translation at once.
func (t *topology) transformOf() *pointTransform {
	p := &pointTransform{t: t}
	tr := member(t.val, "transform")
	if tr.IsNullish() {
		return p
	}
	p.has = true
	scale := member(tr, "scale")
	p.kx = jsval.ToNumber(member(scale, "0"))
	p.ky = jsval.ToNumber(member(scale, "1"))
	translate := member(tr, "translate")
	p.dx = member(translate, "0")
	p.dy = member(translate, "1")
	return p
}

// release gives back what the running sums hold.
func (p *pointTransform) release() {
	p.x0.release(p.t)
	p.y0.release(p.t)
}

// apply is transformPoint(input, k); reset is `!k` (the first position of
// an arc, or a lone point).
func (p *pointTransform) apply(input jsval.Value, reset bool) jsval.Value {
	t := p.t
	if !p.has {
		t.mustCharge(topoSlotBytes, 1)
		return input
	}
	if reset {
		p.x0.release(t)
		p.y0.release(t)
		p.x0, p.y0 = jsSum{}, jsSum{}
	}
	// `n = input.length, output = new Array(n)`: an array of n holes for a
	// number n that is a valid length, a RangeError for any other number,
	// and [n] for a value that is no number.
	// (Elements 0 and 1 are assigned below, so [n]'s element is replaced.)
	size := 1
	if input.IsArr() {
		size = input.Len()
	} else if n := member(input, "length"); n.IsNum() {
		f := n.NumValue()
		if f < 0 || f != math.Trunc(f) || f >= 1<<32 {
			topoThrowf("Invalid array length")
		}
		size = int(f)
	}
	length := max(size, 2)
	t.mustCharge(topoSlotBytes+topoArrBytes+budget.Mul(int64(length), topoValBytes), int64(length)+1)
	arr, out := jsval.MakeArr(length)
	if err := p.x0.add(at(input, 0), t); err != nil {
		panic(topoThrow{err})
	}
	v, err := jsScaleAdd(&p.x0, p.kx, p.dx, t)
	if err != nil {
		panic(topoThrow{err})
	}
	out[0] = v
	if err := p.y0.add(at(input, 1), t); err != nil {
		panic(topoThrow{err})
	}
	if out[1], err = jsScaleAdd(&p.y0, p.ky, p.dy, t); err != nil {
		panic(topoThrow{err})
	}
	// `while (j < n) output[j] = input[j], ++j`, for a number n.
	for j := 2; j < size; j++ {
		out[j] = at(input, j)
	}
	return arr
}

// decoder is feature.js's object(topology, o): its transformPoint and the
// topology's arcs, read once.
type decoder struct {
	t    *topology
	tp   *pointTransform
	arcs jsval.Value
}

// decoder is object()'s state: transform(topology.transform) and
// topology.arcs, read on the first object() as upstream reads them on each
// (reading them again reads the same values, and what throws throws the
// first time). The running sums restart at every arc and lone point.
func (t *topology) decoder() *decoder {
	if t.dec == nil {
		tp := t.transformOf()
		t.dec = &decoder{t: t, tp: tp, arcs: member(t.val, "arcs")}
	}
	return t.dec
}

// arcInto is arc(i, points): the joint position shared with the previous arc
// dropped, the arc's positions pushed (transformed), and for a negative i the
// arc's own positions reversed.
func (d *decoder) arcInto(i jsval.Value, points []jsval.Value) []jsval.Value {
	if len(points) > 0 {
		points = points[:len(points)-1]
	}
	a := d.t.arcRef(d.arcs, i)
	n := member(a, "length")
	if a.IsArr() {
		// The line grows by the arc at once, as an append of it would.
		points = slices.Grow(points, len(a.Items()))
		for k, p := range a.Items() {
			points = append(points, d.tp.apply(p, k == 0))
		}
	} else {
		bound := jsval.ToNumber(n)
		for k := 0; float64(k) < bound; k++ {
			points = append(points, d.tp.apply(at(a, k), k == 0))
		}
	}
	if less0(i) {
		points = reversePoints(points, n)
	}
	return points
}

// reversePoints is reverse.js's reverse(array, n): `j = array.length, i = j -
// n; while (i < --j) swap(array[i++], array[j])`.
func reversePoints(points []jsval.Value, n jsval.Value) []jsval.Value {
	j := len(points)
	start := float64(j) - jsval.ToNumber(n)
	if math.IsNaN(start) {
		return points
	}
	if start != math.Trunc(start) {
		// array[i] of a fractional i reads undefined and writes a property
		// no index holds: each array[j] the loop visits becomes undefined.
		for i := start; i < float64(j-1); i++ {
			j--
			points[j] = jsval.Undefined
		}
		return points
	}
	i := int(math.Max(start, -1))
	for j--; i < j; i, j = i+1, j-1 {
		if i < 0 {
			// array[-1] is a property, not an element: read undefined.
			points[j] = jsval.Undefined
			continue
		}
		points[i], points[j] = points[j], points[i]
	}
	return points
}

// line is line(arcs): the arcs' positions, padded to two with the first
// ("This should never happen per the specification").
func (d *decoder) line(arcs jsval.Value) jsval.Value {
	if arcs.IsArr() {
		return d.lineOf(arcs.Items())
	}
	var points []jsval.Value
	n := jsval.ToNumber(member(arcs, "length"))
	for i := 0; float64(i) < n; i++ {
		d.t.mustCharge(0, 1)
		points = d.arcInto(at(arcs, i), points)
	}
	return jsval.Arr(padPoints(points, 2))
}

// lineOf is line() of an array of arc references.
func (d *decoder) lineOf(arcs []jsval.Value) jsval.Value {
	var points []jsval.Value
	for _, a := range arcs {
		d.t.mustCharge(0, 1)
		points = d.arcInto(a, points)
	}
	return jsval.Arr(padPoints(points, 2))
}

// padPoints pushes points[0] until there are n.
func padPoints(points []jsval.Value, n int) []jsval.Value {
	var first jsval.Value
	if len(points) > 0 {
		first = points[0]
	}
	for len(points) < n {
		points = append(points, first)
	}
	return points
}

// ring is ring(arcs): a line padded to four positions.
func (d *decoder) ring(arcs jsval.Value) jsval.Value {
	return jsval.Arr(padPoints(d.line(arcs).Items(), 4))
}

// polygon is `arcs.map(ring)`.
func (d *decoder) polygon(arcs jsval.Value) jsval.Value {
	items := elements(arcs, "arcs", "map")
	out := make([]jsval.Value, len(items))
	for i, a := range items {
		out[i] = d.ring(a)
	}
	return jsval.Arr(out)
}

// point is point(p): transformPoint(p), with the running sums reset.
func (d *decoder) point(p jsval.Value) jsval.Value { return d.tp.apply(p, true) }

// geometry is geometry(o): a GeoJSON geometry for a TopoJSON one, null for a
// type it does not know.
func (d *decoder) geometry(o jsval.Value, depth int) jsval.Value {
	if depth > maxTopoDepth {
		panic(topoThrow{fmt.Errorf("geo: TopoJSON geometry nesting too deep: %w", budget.ErrLimit)})
	}
	typ := member(o, "type")
	name, _ := typ.Strict()
	var coords jsval.Value
	switch name {
	case "GeometryCollection":
		gs := elements(member(o, "geometries"), "o.geometries", "map")
		out := make([]jsval.Value, len(gs))
		for i, g := range gs {
			out[i] = d.geometry(g, depth+1)
		}
		return jsval.Obj(jsval.ObjectOf("type", typ, "geometries", jsval.Arr(out)))
	case "Point":
		coords = d.point(member(o, "coordinates"))
	case "MultiPoint":
		ps := elements(member(o, "coordinates"), "o.coordinates", "map")
		out := make([]jsval.Value, len(ps))
		for i, p := range ps {
			out[i] = d.point(p)
		}
		coords = jsval.Arr(out)
	case "LineString":
		coords = d.line(member(o, "arcs"))
	case "MultiLineString":
		items := elements(member(o, "arcs"), "o.arcs", "map")
		out := make([]jsval.Value, len(items))
		for i, a := range items {
			out[i] = d.line(a)
		}
		coords = jsval.Arr(out)
	case "Polygon":
		coords = d.polygon(member(o, "arcs"))
	case "MultiPolygon":
		items := elements(member(o, "arcs"), "o.arcs", "map")
		out := make([]jsval.Value, len(items))
		for i, a := range items {
			out[i] = d.polygon(a)
		}
		coords = jsval.Arr(out)
	default:
		return jsval.Null
	}
	return jsval.Obj(jsval.ObjectOf("type", typ, "coordinates", coords))
}

// object is feature.js's object(topology, o).
func (t *topology) object(o jsval.Value) jsval.Value {
	d := t.decoder()
	defer d.tp.release()
	return d.geometry(o, 0)
}

// feature is feature.js's inner feature(topology, o).
func (t *topology) feature(o jsval.Value) jsval.Value {
	id, bbox := member(o, "id"), member(o, "bbox")
	props := member(o, "properties")
	if props.IsNullish() {
		props = jsval.Obj(jsval.NewObject(0))
	}
	geom := t.object(o)
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
	return jsval.Obj(f)
}

// TopoFeatureJS is topojson.feature(topology, o): a GeoJSON Feature for a
// topology object, or a FeatureCollection when the object is a
// GeometryCollection (a string o names one of topology.objects). What it
// makes is charged to the ledger in ctx first, and its work metered; an
// error is what topojson-client throws, a bound (wrapping budget.ErrLimit)
// or ctx's error.
func TopoFeatureJS(ctx context.Context, topo, o jsval.Value) (out jsval.Value, err error) {
	defer topoCatch(&err)
	t := newTopology(ctx, topo)
	if o.IsStr() {
		o = member(member(topo, "objects"), o.StrValue())
	}
	if typ, _ := member(o, "type").Strict(); typ == "GeometryCollection" {
		gs := elements(member(o, "geometries"), "o.geometries", "map")
		feats := make([]jsval.Value, len(gs))
		for i, g := range gs {
			feats[i] = t.feature(g)
		}
		return jsval.Obj(jsval.ObjectOf("type", jsval.Str("FeatureCollection"), "features", jsval.Arr(feats))), nil
	}
	return t.feature(o), nil
}

// TopoMeshJS is topojson.mesh(topology, object, filter): one MultiLineString
// of the object's arcs, stitched, those the filter keeps when it has one.
// It is bounded by ctx as TopoFeatureJS is.
func TopoMeshJS(ctx context.Context, topo, o jsval.Value, filter MeshFilterJS) (out jsval.Value, err error) {
	defer topoCatch(&err)
	t := newTopology(ctx, topo)
	arcs := t.extractArcs(o, filter)
	frags := t.stitch(arcs)
	// object(topology, {type: "MultiLineString", arcs: fragments}):
	// `o.arcs.map(line)` of the fragments, arrays the stitch made, decoded
	// as such without building that object.
	d := t.decoder()
	defer d.tp.release()
	lines := make([]jsval.Value, len(frags))
	for i, f := range frags {
		lines[i] = d.lineOf(f)
	}
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiLineString"), "coordinates", jsval.Arr(lines))), nil
}

// TopoFeature is TopoFeatureJS.
func TopoFeature(ctx context.Context, topo, object jsval.Value) (jsval.Value, error) {
	return TopoFeatureJS(ctx, topo, object)
}

// TopoMesh is TopoMeshJS with one of vega-loader's filters.
func TopoMesh(ctx context.Context, topo, object jsval.Value, filter MeshFilter) (jsval.Value, error) {
	return TopoMeshJS(ctx, topo, object, MeshFilterJS{Kind: filter})
}

// arcUse is what extractArcs keeps of geomsByArc[j]: the first reference
// to arc j as the geometry spells it, and the first and the last geometry
// that use it (all that the filter and the mesh read of the list).
type arcUse struct {
	i           jsval.Value
	first, last *jsval.Object // geometries are objects (a primitive has no type)
	j           uint32
}

// extractArcs is mesh.js's extractArcs: every arc reference of the object's
// geometries recorded under its arc's index (geomsByArc[j], which only an
// array index keeps where forEach visits it), then, in index order, the
// first reference of each arc the filter keeps (comparing the first and the
// last geometry that use it, by identity).
func (t *topology) extractArcs(object jsval.Value, filter MeshFilterJS) []jsval.Value {
	// The use of arc j is uses[dense[j]-1], or for an index far past the
	// others uses[sparse[j]].
	var dense []int32
	var sparse map[uint32]int
	var uses []arcUse
	var maxJ uint32
	var geom *jsval.Object
	extract0 := func(i jsval.Value) {
		t.mustCharge(0, 1)
		var j uint32
		ok := false
		if f := i.NumValue(); i.IsNum() && f == math.Trunc(f) && math.Abs(f) < 1<<31 {
			if k := int(f); k < 0 {
				j, ok = uint32(^k), ^k >= 0
			} else {
				j, ok = uint32(k), true
			}
		} else {
			j, ok = jsval.ArrayIndex(t.arcKey(i))
		}
		if !ok {
			// A property of the array forEach does not visit, unless the array
			// inherits one of that name: a method is found by `geomsByArc[j]`
			// and has no push; so does a length above 0 (before any arc is
			// recorded it is 0, and assigning [] to it changes nothing).
			key := t.arcKey(i)
			if key == "length" && len(uses) == 0 && maxJ == 0 {
				return // length 0 is falsy: `geomsByArc.length = []` changes nothing
			}
			if key == "length" {
				topoThrowf("TypeError: (geomsByArc[j] || (geomsByArc[j] = [])).push is not a function")
			}
			if m := jsval.Member(jsval.Arr(nil), key); !m.IsUndefined() && m.IsTruthy() {
				topoThrowf("TypeError: (geomsByArc[j] || (geomsByArc[j] = [])).push is not a function")
			}
			return
		}
		if int64(j) < int64(len(dense)) && dense[j] != 0 {
			uses[dense[j]-1].last = geom
			return
		}
		if k, seen := sparse[j]; seen {
			uses[k].last = geom
			return
		}
		if int64(j) <= 4*int64(len(uses))+1024 {
			if int(j) >= len(dense) {
				n := max(int(j)+1, 2*len(dense))
				t.mustCharge(int64(n-len(dense))*4, 1)
				dense = append(dense, make([]int32, n-len(dense))...)
			}
			dense[j] = int32(len(uses)) + 1
		} else {
			if sparse == nil {
				sparse = map[uint32]int{}
			}
			sparse[j] = len(uses)
		}
		uses = append(uses, arcUse{i: i, first: geom, last: geom, j: j})
		maxJ = max(maxJ, j)
	}
	var extract func(arcs jsval.Value, depth int, expr string)
	extract = func(arcs jsval.Value, depth int, expr string) {
		for _, a := range elements(arcs, expr, "forEach") {
			if depth == 1 {
				extract0(a)
			} else {
				extract(a, depth-1, "arcs")
			}
		}
	}
	var geometry func(o jsval.Value, depth int)
	geometry = func(o jsval.Value, depth int) {
		if depth > maxTopoDepth {
			panic(topoThrow{fmt.Errorf("geo: TopoJSON geometry nesting too deep: %w", budget.ErrLimit)})
		}
		geom = o.ObjValue()
		typ, _ := member(o, "type").Strict()
		switch typ {
		case "GeometryCollection":
			for _, g := range elements(member(o, "geometries"), "o.geometries", "forEach") {
				geometry(g, depth+1)
			}
		case "LineString":
			extract(member(o, "arcs"), 1, "arcs")
		case "MultiLineString", "Polygon":
			extract(member(o, "arcs"), 2, "arcs")
		case "MultiPolygon":
			extract(member(o, "arcs"), 3, "arcs")
		}
	}
	geometry(object, 0)

	// geomsByArc.forEach: the arcs in index order: through the dense table
	// when every use is in it, else sorted.
	t.mustCharge(0, int64(len(uses)))
	if sparse != nil {
		slices.SortFunc(uses, func(a, b arcUse) int { return cmp.Compare(a.j, b.j) })
		dense = dense[:0]
		for k := range uses {
			dense = append(dense, int32(k)+1)
		}
	}
	var arcs []jsval.Value
	for _, k := range dense {
		if k == 0 {
			continue
		}
		u := &uses[k-1]
		first, last := u.first, u.last
		switch {
		case filter.Throws != "":
			topoThrowf("%s", filter.Throws)
		case filter.Kind == MeshInterior:
			if first == last {
				continue
			}
		case filter.Kind == MeshExterior:
			if first != last {
				continue
			}
		}
		arcs = append(arcs, u.i)
	}
	return arcs
}

// fragment is a chain of arcs being stitched, and the keys of its start and
// end positions.
type fragment struct {
	arcs       []jsval.Value
	start, end string
	one        [1]jsval.Value // arcs' first backing, made with the fragment
}

// newFragment is a fragment of the one arc i.
func newFragment(i jsval.Value, start, end string) *fragment {
	f := &fragment{start: start, end: end}
	f.one[0] = i
	f.arcs = f.one[:]
	return f
}

// fragMap is a JavaScript object used as a string-keyed map: a key it does not
// hold may name an inherited member, and for-in visits the array-index keys
// first, ascending, then the others in the order they were first added
// (deleting a key and adding it again moves it to the end).
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

// get is `map[k]`: the fragment, or for a key the object inherits (an
// Object.prototype member, which is no fragment), the TypeError of the method
// stitch then calls on it (method).
func (m *fragMap) get(k, method string) *fragment {
	if e := m.m[k]; e != nil {
		return e.f
	}
	if _, ok := jsval.Inherited(k); ok || k == "__proto__" {
		topoThrowf("f.%s is not a function", method)
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

// each is `for (k in map)`: the keys present when it starts, array indexes
// first, each visited unless deleted before its turn.
func (m *fragMap) each(fn func(f *fragment)) {
	snapshot := make([]*fragEntry, 0, len(m.m))
	for _, e := range m.order {
		if e.live {
			snapshot = append(snapshot, e)
		}
	}
	sort.SliceStable(snapshot, func(a, b int) bool {
		ia, oka := jsval.ArrayIndex(snapshot[a].key)
		ib, okb := jsval.ArrayIndex(snapshot[b].key)
		if oka && okb {
			return ia < ib
		}
		return oka && !okb
	})
	for _, e := range snapshot {
		if e.live {
			fn(e.f)
		}
	}
}

// arcAt is `topology.arcs[i < 0 ? ~i : i]`.
func (t *topology) arcAt(i jsval.Value) jsval.Value {
	return t.arcRef(member(t.val, "arcs"), i)
}

// stitch is stitch.js: the arcs joined end to end into fragments, so a mesh
// draws each border as long polylines instead of many small arcs. The
// positions are keys of JavaScript objects: String(position).
func (t *topology) stitch(arcs []jsval.Value) [][]jsval.Value {
	stitched := map[string]bool{}
	byStart, byEnd := newFragMap(), newFragMap()
	var fragments [][]jsval.Value
	emptyIndex := -1

	// Stitch empty arcs first, since they may be subsumed by other arcs.
	for j := 0; j < len(arcs); j++ {
		t.mustCharge(0, 1)
		i := arcs[j]
		arc := t.arcAt(i)
		if jsval.ToNumber(member(arc, "length")) < 3 {
			p := at(arc, 1)
			if !at(p, 0).IsTruthy() && !at(p, 1).IsTruthy() {
				emptyIndex++
				arcs[emptyIndex], arcs[j] = i, arcs[emptyIndex]
			}
		}
	}

	ends := func(i jsval.Value) (string, string) {
		arc := t.arcAt(i)
		p0 := at(arc, 0)
		var p1 string
		if member(t.val, "transform").IsTruthy() {
			var x, y jsSum
			defer x.release(t)
			defer y.release(t)
			for _, dp := range elements(arc, "arc", "forEach") {
				if err := x.add(at(dp, 0), t); err != nil {
					panic(topoThrow{err})
				}
				if err := y.add(at(dp, 1), t); err != nil {
					panic(topoThrow{err})
				}
			}
			p1 = x.String() + "," + y.String()
		} else {
			last := jsval.ToNumber(member(arc, "length")) - 1
			p1 = t.key(member(arc, t.walk.String(jsval.Num(last))))
		}
		s0 := t.key(p0)
		if less0(i) {
			return p1, s0
		}
		return s0, p1
	}

	for _, i := range arcs {
		t.mustCharge(0, 1)
		start, end := ends(i)
		if f := byEnd.get(start, "push"); f != nil {
			byEnd.del(f.end)
			f.arcs = append(f.arcs, i)
			f.end = end
			if g := byStart.get(end, "push"); g != nil {
				byStart.del(g.start)
				fg := f
				if g != f {
					fg = &fragment{arcs: append(append([]jsval.Value(nil), f.arcs...), g.arcs...)}
				}
				fg.start = f.start
				fg.end = g.end
				byStart.set(fg.start, fg)
				byEnd.set(fg.end, fg)
			} else {
				byStart.set(f.start, f)
				byEnd.set(f.end, f)
			}
		} else if f := byStart.get(end, "unshift"); f != nil {
			byStart.del(f.start)
			f.arcs = append([]jsval.Value{i}, f.arcs...)
			f.start = start
			if g := byEnd.get(start, "unshift"); g != nil {
				byEnd.del(g.end)
				gf := f
				if g != f {
					gf = &fragment{arcs: append(append([]jsval.Value(nil), g.arcs...), f.arcs...)}
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
			f := newFragment(i, start, end)
			byStart.set(start, f)
			byEnd.set(end, f)
		}
	}

	// stitchedArcs[i < 0 ? ~i : i]: an integer reference by its index, any
	// other by its property key (which may name an inherited member, truthy).
	stitchedIdx := map[int]bool{}
	mark := func(i jsval.Value, set bool) bool {
		if f := i.NumValue(); i.IsNum() && f == math.Trunc(f) && math.Abs(f) < 1<<31 {
			j := int(f)
			if j < 0 {
				j = ^j
			}
			if set {
				stitchedIdx[j] = true
			}
			return stitchedIdx[j]
		}
		k := t.arcKey(i)
		if set {
			stitched[k] = true
		}
		_, inherited := jsval.Inherited(k)
		return stitched[k] || inherited || k == "__proto__"
	}
	flush := func(byEnd, byStart *fragMap) {
		byEnd.each(func(f *fragment) {
			byStart.del(f.start)
			for _, i := range f.arcs {
				mark(i, true)
			}
			fragments = append(fragments, f.arcs)
		})
	}
	flush(byEnd, byStart)
	flush(byStart, byEnd)
	for _, i := range arcs {
		if mark(i, false) {
			continue
		}
		fragments = append(fragments, []jsval.Value{i})
	}
	return fragments
}

// TopologyFeatures is vega-loader's topojson format over a parsed topology:
// the features of the object named feature (or the one Feature), else the
// mesh of the object named mesh, filtered by the filter named filter.
func TopologyFeatures(ctx context.Context, topo jsval.Value, feature, mesh, filter string) ([]jsval.Value, error) {
	if feature == "" && mesh == "" {
		return nil, errors.New("Missing TopoJSON feature or mesh parameter.")
	}
	property := mesh
	if feature != "" {
		property = feature
	}
	if topo.IsNullish() {
		return nil, fmt.Errorf("Cannot read properties of %s (reading 'objects')", nullishText(topo))
	}
	objects := jsval.Member(topo, "objects")
	if objects.IsNullish() {
		return nil, fmt.Errorf("Cannot read properties of %s (reading '%s')", nullishText(objects), property)
	}
	object := ProtoRead(objects, property)
	if !object.IsTruthy() {
		return nil, fmt.Errorf("Invalid TopoJSON object: %s", property)
	}
	var out jsval.Value
	var err error
	if feature != "" {
		out, err = TopoFeatureJS(ctx, topo, object)
	} else {
		out, err = TopoMeshJS(ctx, topo, object, MeshFilterJS{Kind: ParseMeshFilter(filter)})
	}
	if err != nil {
		return nil, err
	}
	if feats := jsval.Member(out, "features"); feats.IsTruthy() {
		if feats.IsArr() {
			return feats.Items(), nil
		}
		return []jsval.Value{feats}, nil
	}
	return []jsval.Value{out}, nil
}
