package geo

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Accessor reads a value from a tuple (a compiled field reference).
type Accessor func(jsval.Value) jsval.Value

// FieldAccessor returns an Accessor for a Vega field path such as "a.b" or
// "a['b']". Like vega-util's field accessors, a missing property reads as
// undefined (which JavaScript arithmetic turns into NaN, unlike null's 0); a
// missing intermediate object, where upstream would throw, reads as undefined
// too.
func FieldAccessor(path string) Accessor {
	segs := jsval.ParseFieldPath(path)
	return func(t jsval.Value) jsval.Value {
		cur := t
		for _, seg := range segs {
			switch {
			case cur.IsObj():
				cur = cur.ObjValue().Lookup(seg)
			case cur.IsArr():
				i, err := strconv.Atoi(seg)
				if err != nil {
					return jsval.Undefined
				}
				cur = cur.Index(i)
			default:
				return jsval.Undefined
			}
		}
		return cur
	}
}

// Identity is the accessor returning the tuple itself.
func Identity(t jsval.Value) jsval.Value { return t }

// GeoJSONParams are the parameters of vega-geo's GeoJSON transform.
type GeoJSONParams struct {
	// Lon and Lat, when both set, collect [lon, lat] points into a MultiPoint
	// feature appended after the GeoJSON features.
	Lon, Lat Accessor
	// GeoJSON extracts a GeoJSON object from each tuple. When nil and no
	// longitude/latitude fields are given, each tuple is itself GeoJSON.
	GeoJSON Accessor
}

// GeoJSON is vega-geo's GeoJSON transform: it consolidates points and GeoJSON
// features from the tuples into one FeatureCollection, suited to a
// projection's fit argument. Points with a missing or non-numeric coordinate are
// dropped.
func GeoJSON(ctx context.Context, tuples []jsval.Value, p GeoJSONParams) (jsval.Value, error) {
	geojson := p.GeoJSON
	hasFields := p.Lon != nil || p.Lat != nil
	if geojson == nil && !hasFields {
		geojson = Identity
	}
	var features []jsval.Value
	if geojson != nil {
		features = make([]jsval.Value, 0, len(tuples))
		for i, t := range tuples {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return jsval.Undefined, err
				}
			}
			features = append(features, geojson(t))
		}
	}
	if p.Lon != nil && p.Lat != nil {
		points := make([]jsval.Value, 0, len(tuples))
		for i, t := range tuples {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return jsval.Undefined, err
				}
			}
			x, y := p.Lon(t), p.Lat(t)
			if x.IsNullish() || y.IsNullish() {
				continue
			}
			fx, fy := jsval.ToNumber(x), jsval.ToNumber(y)
			if fx != fx || fy != fy { // NaN
				continue
			}
			points = append(points, jsval.ArrOf(jsval.Num(fx), jsval.Num(fy)))
		}
		features = append(features, jsval.Obj(jsval.ObjectOf(
			"type", jsval.Str("Feature"),
			"geometry", jsval.Obj(jsval.ObjectOf(
				"type", jsval.Str("MultiPoint"),
				"coordinates", jsval.Arr(points),
			)),
		)))
	}
	return jsval.Obj(jsval.ObjectOf(
		"type", jsval.Str("FeatureCollection"),
		"features", jsval.Arr(features),
	)), nil
}

// PointRadius is the pointRadius parameter of geopath/geoshape: a constant or an
// expression of the object being drawn. The zero value means "not set" (the
// path keeps its own radius).
type PointRadius struct {
	Const float64
	Fn    func(object jsval.Value) float64
	Set   bool
}

// ConstRadius is a constant point radius.
func ConstRadius(r float64) PointRadius { return PointRadius{Const: r, Set: true} }

func (r PointRadius) apply(p *Path) {
	if !r.Set {
		return
	}
	if r.Fn != nil {
		p.SetPointRadiusFunc(r.Fn)
	} else {
		p.SetPointRadius(r.Const)
	}
}

// defaultPath is vega-projection's getProjectionPath for a missing projection:
// an unprojected path.
func projectionPath(p Projection) *Path {
	if p == nil {
		return NewPath(nil)
	}
	return p.Path()
}

// GeoPathParams are the parameters of vega-geo's GeoPath transform.
type GeoPathParams struct {
	Projection  Projection // nil draws unprojected coordinates
	Field       Accessor   // nil: the tuple is the GeoJSON object
	PointRadius PointRadius
	As          string // output field, default "path"
}

// GeoPath is vega-geo's GeoPath transform: it stores in each tuple's As field
// the SVG path data of its GeoJSON (null when nothing is drawn). Tuples are
// modified in place, as upstream does.
func GeoPath(ctx context.Context, tuples []jsval.Value, p GeoPathParams) (err error) {
	path := projectionPath(p.Projection)
	path.Bind(ctx)
	defer func() {
		if r := recover(); r != nil {
			le, ok := r.(*budget.Stop)
			if !ok {
				panic(r)
			}
			err = le.Err
		}
	}()
	field := p.Field
	if field == nil {
		field = Identity
	}
	as := p.As
	if as == "" {
		as = "path"
	}
	path.SetContext(nil) // upstream leaves the path in string mode
	prev := path.saveRadius()
	p.PointRadius.apply(path)
	defer path.restoreRadius(prev)

	for i, t := range tuples {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		if s, ok := path.String(field(t)); ok {
			o.Set(as, jsval.Str(s))
		} else {
			o.Set(as, jsval.Null)
		}
	}
	return nil
}

// GeoPointParams are the parameters of vega-geo's GeoPoint transform.
type GeoPointParams struct {
	Projection Projection // required
	Lon, Lat   Accessor   // required
	As         [2]string  // output fields, default {"x", "y"}
}

// GeoPoint is vega-geo's GeoPoint transform: it geo-codes each tuple's
// longitude/latitude to x/y through the projection. Where the projection has no
// location (albersUsa outside its regions) the outputs are undefined.
func GeoPoint(ctx context.Context, tuples []jsval.Value, p GeoPointParams) error {
	// A missing projection or field is only an error for a tuple to geo-code:
	// upstream calls them inside the per-tuple function, arguments first.
	if len(tuples) > 0 {
		switch {
		case p.Lon == nil:
			return errors.New("TypeError: lon is not a function")
		case p.Lat == nil:
			return errors.New("TypeError: lat is not a function")
		case p.Projection == nil:
			return errors.New("TypeError: proj is not a function")
		}
	}
	xf, yf := p.As[0], p.As[1]
	if xf == "" {
		xf = "x"
	}
	if yf == "" {
		yf = "y"
	}
	for i, t := range tuples {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		x, y, ok := p.Projection.Forward(jsval.ToNumber(p.Lon(t)), jsval.ToNumber(p.Lat(t)))
		if ok {
			o.Set(xf, jsval.Num(x))
			o.Set(yf, jsval.Num(y))
		} else {
			o.Set(xf, jsval.Undefined)
			o.Set(yf, jsval.Undefined)
		}
	}
	return nil
}

// Shape is vega-geo's GeoShape generator: the value a geoshape mark item keeps in
// its shape field. Given an item it produces the path of the item's GeoJSON,
// either as SVG path data or drawn onto a PathContext (for bounds).
type Shape struct {
	path  *Path
	field Accessor
	pr    PointRadius
}

// Bind charges the shape's points to the budget carried by ctx; see Path.Bind.
func (s *Shape) Bind(ctx context.Context) { s.path.Bind(ctx) }

// NewShape is the GeoShape transform's generator. A nil field reads the
// item's `datum`, the transform's default.
func NewShape(proj Projection, field Accessor, pr PointRadius) *Shape {
	if field == nil {
		field = FieldAccessor("datum")
	}
	return &Shape{path: projectionPath(proj), field: field, pr: pr}
}

func (s *Shape) withRadius(fn func()) {
	if !s.pr.Set {
		fn()
		return
	}
	prev := s.path.saveRadius()
	s.pr.apply(s.path)
	fn()
	s.path.restoreRadius(prev)
}

// Render is the scenegraph's shape-generator convention: with a nil ctx it
// returns the path data of the item (empty if nothing is drawn), otherwise it
// draws onto ctx and returns "".
func (s *Shape) Render(ctx PathContext, item jsval.Value) string {
	if ctx == nil {
		d, _ := s.Path(item)
		return d
	}
	s.Draw(ctx, item)
	return ""
}

// PathOf and DrawOf are Path and Draw for an object that is already the
// GeoJSON (the field accessor has been applied by the caller).
func (s *Shape) PathOf(object jsval.Value) (d string, ok bool) {
	s.path.SetContext(nil)
	s.withRadius(func() { d, ok = s.path.String(object) })
	return d, ok
}

// DrawOf draws an object that is already the GeoJSON onto ctx.
func (s *Shape) DrawOf(ctx PathContext, object jsval.Value) {
	s.path.SetContext(ctx)
	s.withRadius(func() { s.path.Draw(object) })
	s.path.SetContext(nil)
}

// Path returns the SVG path data of the item; ok is false when nothing is
// drawn (upstream's null).
func (s *Shape) Path(item jsval.Value) (d string, ok bool) {
	s.path.SetContext(nil)
	s.withRadius(func() { d, ok = s.path.String(s.field(item)) })
	return d, ok
}

// Draw draws the item onto ctx.
func (s *Shape) Draw(ctx PathContext, item jsval.Value) {
	s.path.SetContext(ctx)
	s.withRadius(func() { s.path.Draw(s.field(item)) })
	s.path.SetContext(nil)
}

// GraticuleParams are the parameters of vega-geo's Graticule transform. Only
// set (non-nil) parameters are applied, in this order, as upstream applies the
// parameters that are present.
type GraticuleParams struct {
	Extent, ExtentMajor, ExtentMinor *[2][2]float64
	Step, StepMajor, StepMinor       *[2]float64
	Precision                        *float64
}

// GraticuleFeature is vega-geo's Graticule transform: the GeoJSON MultiLineString
// of the configured graticule.
func GraticuleFeature(p GraticuleParams) (jsval.Value, error) {
	g := NewGraticule()
	if p.Extent != nil {
		g.SetExtent(*p.Extent)
	}
	if p.ExtentMajor != nil {
		g.SetExtentMajor(*p.ExtentMajor)
	}
	if p.ExtentMinor != nil {
		g.SetExtentMinor(*p.ExtentMinor)
	}
	if p.Step != nil {
		g.SetStep(*p.Step)
	}
	if p.StepMajor != nil {
		g.SetStepMajor(*p.StepMajor)
	}
	if p.StepMinor != nil {
		g.SetStepMinor(*p.StepMinor)
	}
	if p.Precision != nil {
		g.SetPrecision(*p.Precision)
	}
	return g.Lines()
}

// CollectGeoJSON is vega-geo's collectGeoJSON: the fit parameter of a
// projection (one GeoJSON object, or an array of features, geometries and
// feature collections) as a single object to stream.
func CollectGeoJSON(data jsval.Value) (jsval.Value, error) {
	var items []jsval.Value
	switch {
	case data.IsNullish():
	case data.IsArr():
		items = data.Items()
	default:
		items = []jsval.Value{data}
	}
	if len(items) == 1 {
		return items[0], nil
	}
	var features []jsval.Value
	for _, f := range items {
		if f.IsNullish() {
			return jsval.Undefined, errors.New("geo: cannot read properties of null (reading 'type') in projection fit")
		}
		if f.IsObj() && typeOf(f) == "FeatureCollection" {
			features = append(features, f.Get("features").Items()...)
			continue
		}
		var list []jsval.Value
		if f.IsArr() {
			list = f.Items()
		} else {
			list = []jsval.Value{f}
		}
		for _, d := range list {
			if d.IsNullish() {
				continue
			}
			if d.IsObj() && typeOf(d) == "Feature" {
				features = append(features, d)
			} else {
				features = append(features, jsval.Obj(jsval.ObjectOf("type", jsval.Str("Feature"), "geometry", d)))
			}
		}
	}
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("FeatureCollection"), "features", jsval.Arr(features))), nil
}

// ConfigureProjection is vega-geo's Projection operator. params holds the
// resolved operator parameters ("type", the ProjectionProperties, "pointRadius",
// "fit", "extent", "size"). current is the projection created by an earlier
// call (nil the first time). modified reports whether a parameter changed since
// then; it may be nil when current is nil.
//
// The projection is created on the first call or when "type" changed, with
// every non-null property applied in ProjectionProperties order; otherwise only
// the modified properties are applied. A truthy "fit" then fits the projection
// to the collected GeoJSON using "extent" or "size".
func ConfigureProjection(current Projection, params *jsval.Object, modified func(name string) bool) (Projection, error) {
	get := func(name string) jsval.Value { return params.Lookup(name) }
	proj := current
	if proj == nil || (modified != nil && modified("type")) {
		typ := get("type")
		name := ""
		if typ.IsTruthy() {
			name = typ.AsString()
		}
		var err error
		if proj, err = NewProjection(name); err != nil {
			return nil, err
		}
		for _, prop := range ProjectionProperties {
			if v := get(prop); !v.IsNullish() {
				if err := proj.Set(prop, v); err != nil {
					return nil, err
				}
			}
		}
	} else {
		for _, prop := range ProjectionProperties {
			if modified != nil && modified(prop) {
				if err := proj.Set(prop, get(prop)); err != nil {
					return nil, err
				}
			}
		}
	}
	if v := get("pointRadius"); !v.IsNullish() {
		proj.Path().SetPointRadius(jsval.ToNumber(v))
	}
	if fitv := get("fit"); fitv.IsTruthy() {
		if err := fitProjection(proj, fitv, get("extent"), get("size")); err != nil {
			return nil, err
		}
	}
	return proj, nil
}

func fitProjection(proj Projection, fitValue, extent, size jsval.Value) error {
	data, err := CollectGeoJSON(fitValue)
	if err != nil {
		return err
	}
	switch {
	case extent.IsTruthy():
		p0, ok0 := elem(extent, 0)
		p1, ok1 := elem(extent, 1)
		if !ok0 || !ok1 {
			return fmt.Errorf("geo: bad projection extent %s", extent.String())
		}
		a, oka := elem(p0, 0)
		b, okb := elem(p0, 1)
		c, okc := elem(p1, 0)
		d, okd := elem(p1, 1)
		if !oka || !okb || !okc || !okd {
			return fmt.Errorf("geo: bad projection extent %s", extent.String())
		}
		proj.FitExtent(jsval.ToNumber(a), jsval.ToNumber(b), jsval.ToNumber(c), jsval.ToNumber(d), data)
	case size.IsTruthy():
		w, okw := elem(size, 0)
		h, okh := elem(size, 1)
		if !okw || !okh {
			return fmt.Errorf("geo: bad projection size %s", size.String())
		}
		proj.FitSize(jsval.ToNumber(w), jsval.ToNumber(h), data)
	}
	return nil
}
