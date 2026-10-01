package vega

import (
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms"
)

func init() {
	tf := transformFactories
	ctxOf := func(n *opNode) contextT { return n.g.ctx }

	tf["geojson"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			gp := geo.GeoJSONParams{}
			fields := p.fields("fields")
			if len(fields) >= 2 {
				gp.Lon, gp.Lat = geoAccessor(fields[0]), geoAccessor(fields[1])
			}
			if f := p.field("geojson"); !f.IsNil() {
				gp.GeoJSON = geoAccessor(f)
			}
			v, err := geo.GeoJSON(ctxOf(n), pulse.tuples, gp)
			if err != nil {
				failErr(err)
			}
			n.value = v
			return nil
		}), nil
	}

	tf["graticule"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			gp := geo.GraticuleParams{}
			if v := p.extent2x2("extent"); v != nil {
				gp.Extent = v
			}
			if v := p.extent2x2("extentMajor"); v != nil {
				gp.ExtentMajor = v
			}
			if v := p.extent2x2("extentMinor"); v != nil {
				gp.ExtentMinor = v
			}
			if v := p.pair2("step"); v != nil {
				gp.Step = v
			}
			if v := p.pair2("stepMajor"); v != nil {
				gp.StepMajor = v
			}
			if v := p.pair2("stepMinor"); v != nil {
				gp.StepMinor = v
			}
			if p.has("precision") {
				f := p.num("precision", 2.5)
				gp.Precision = &f
			}
			g, err := geo.GraticuleFeature(gp)
			if err != nil {
				failErr(err)
			}
			return changedPulse(pulse, []jsval.Value{g})
		}), nil
	}

	tf["geopoint"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		proj, _ := p.Get("projection").(geo.Projection)
		if !p.has("fields") {
			fail("Cannot read properties of undefined (reading '0')")
		}
		// A field missing from the list (null, or past its end) is an accessor
		// that is not a function, which only matters to a tuple.
		fields := p.fields("fields")
		var lon, lat geo.Accessor
		if len(fields) > 0 {
			lon = geoAccessor(fields[0])
		}
		if len(fields) > 1 {
			lat = geoAccessor(fields[1])
		}
		as := pairAs(p.strs("as"))
		err := geo.GeoPoint(ctxOf(n), in, geo.GeoPointParams{Projection: proj, Lon: lon, Lat: lat, As: as})
		return in, err
	})

	tf["geopath"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		proj, _ := p.Get("projection").(geo.Projection)
		gp := geo.GeoPathParams{Projection: proj, As: p.str("as"), PointRadius: pointRadius(p)}
		if f := p.field("field"); !f.IsNil() {
			gp.Field = geoAccessor(f)
		}
		return in, geo.GeoPath(ctxOf(n), in, gp)
	})

	tf["geoshape"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		built := false // upstream's this.value: the generator, once made
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			// A new generator (the first run, or a changed parameter such as
			// the projection refit to a resized view) reflows the source:
			// every item counts as modified, so the mark is encoded and
			// bounded again with the shapes it now draws.
			reflow := !built || p.Modified()
			built = true
			proj, _ := p.Get("projection").(geo.Projection)
			var field geo.Accessor
			if f := p.field("field"); !f.IsNil() {
				field = geoAccessor(f)
			}
			shape := geo.NewShape(proj, field, pointRadius(p))
			shape.Bind(n.g.ctx)
			as := p.str("as")
			if as == "" {
				as = "shape"
			}
			fn := func(ctx scene.PathContext, it *scene.Item) string {
				obj := jsval.Obj(jsval.ObjectOf("datum", it.Datum))
				if ctx == nil {
					d, _ := shape.Path(obj)
					return d
				}
				shape.Draw(ctx, obj)
				return ""
			}
			if pulse.items != nil {
				for _, it := range pulse.items {
					it.Shape.Func = fn
				}
				if reflow {
					return reflowPulse(pulse)
				}
				return nil
			}
			// as a data transform each tuple keeps the generator's path text
			for _, t := range pulse.tuples {
				if o := t.ObjValue(); o != nil {
					if d, ok := shape.Path(t); ok {
						o.Set(as, jsval.Str(d))
					} else {
						o.Set(as, jsval.Null)
					}
				}
			}
			return nil
		}), nil
	}
}

func geoAccessor(f transforms.Field) geo.Accessor { return geo.Accessor(f.Get) }

func pointRadius(p *opParams) geo.PointRadius {
	x := p.vals.at("pointRadius")
	switch v := x.(type) {
	case jsval.Value:
		if v.IsNullish() {
			return geo.PointRadius{}
		}
		return geo.ConstRadius(jsval.ToNumber(v))
	case transforms.Field:
		return geo.PointRadius{Set: true, Fn: func(o jsval.Value) float64 { return jsval.ToNumber(v.Apply(o)) }}
	case *boundExpr:
		return geo.PointRadius{Set: true, Fn: func(o jsval.Value) float64 { return jsval.ToNumber(v.call(o)) }}
	}
	return geo.PointRadius{}
}

// extent2x2 reads [[x0, y0], [x1, y1]].
func (p *opParams) extent2x2(name string) *[2][2]float64 {
	l := p.list(name)
	if len(l) < 2 {
		return nil
	}
	var out [2][2]float64
	for i := 0; i < 2; i++ {
		v, _ := l[i].(jsval.Value)
		if !v.IsArr() {
			if a, ok := l[i].([]any); ok && len(a) >= 2 {
				out[i][0], out[i][1] = jsval.ToNumber(toValue(a[0])), jsval.ToNumber(toValue(a[1]))
				continue
			}
			return nil
		}
		out[i][0], out[i][1] = jsval.ToNumber(v.Index(0)), jsval.ToNumber(v.Index(1))
	}
	return &out
}
