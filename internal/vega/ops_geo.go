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
			// for (prop in _) gen[prop](_[prop]): each setter reads +_[i][j]
			// or +_[i] of the value given (null is a TypeError)
			gp := geo.GraticuleParams{
				Extent: p.givenPairs("extent"), ExtentMajor: p.givenPairs("extentMajor"),
				ExtentMinor: p.givenPairs("extentMinor"), Step: p.givenPair("step"),
				StepMajor: p.givenPair("stepMajor"), StepMinor: p.givenPair("stepMinor"),
			}
			if _, ok := p.given("precision"); ok {
				f := p.givenNum("precision", 2.5)
				gp.Precision = &f
			}
			g, err := geo.GraticuleFeature(ctxOf(n), gp)
			if err != nil {
				failErr(err)
			}
			out := []jsval.Value{g}
			n.g.view.holdCreated(n, 0, out)
			return changedPulse(pulse, out)
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
		err := geo.GeoPoint(ctxOf(n), in, geo.GeoPointParams{Projection: proj, Lon: lon, Lat: lat, As: p.asPair("x", "y"), AsExact: true})
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
			// An item's bounds draw its shape, and the SVG writer then asks
			// for its path data: both come from one walk of the geometry
			// (Shape.DrawString), the text kept for the writer as long as
			// the item holds the datum it was drawn for.
			texts := map[*scene.Item]shapeText{}
			fn := func(ctx scene.PathContext, it *scene.Item) string {
				obj := jsval.Obj(jsval.ObjectOf("datum", it.Datum))
				if ctx == nil {
					if t, ok := texts[it]; ok {
						delete(texts, it)
						if jsval.StrictEqual(t.datum, it.Datum) {
							return t.d
						}
					}
					d, _ := shape.Path(obj)
					return d
				}
				d, _ := shape.DrawString(ctx, obj)
				texts[it] = shapeText{datum: it.Datum, d: d}
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
				d, ok := shape.Path(t)
				o := jsval.FieldsFor(t, as)
				if ok {
					o.Set(as, jsval.Str(d))
				} else {
					o.Set(as, jsval.Null)
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

// shapeText is the path data a geoshape made for an item as its bounds drew
// it, and the datum it was made from.
type shapeText struct {
	datum jsval.Value
	d     string
}
