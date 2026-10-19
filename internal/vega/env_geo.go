package vega

import (
	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms/hierarchy"
)

// This file implements the expression environment's projection, shape and tree
// functions on the context (vega-functions geo.js, shape.js, tree.js).

// projectionOf resolves a projection reference. Its path generator is bound
// to the view's context, so the walks of the geo functions are bounded as a
// mark's are, whether or not a mark used the projection (C18): they panic
// with a *budget.Stop, which ends the evaluation.
func (c *rtContext) projectionOf(ref jsval.Value) (geo.Projection, bool) {
	if !ref.IsStr() {
		return nil, false
	}
	n := c.scaleNode(ref.StrValue())
	if n == nil {
		return nil, false
	}
	p, ok := n.value.(geo.Projection)
	if ok {
		p.Path().Bind(c.view.ctx)
	}
	return p, ok
}

// GeoArea implements expr.GeoProvider.
func (c *rtContext) GeoArea(projection, geojson, group jsval.Value) jsval.Value {
	if projection.IsTruthy() {
		p, ok := c.projectionOf(projection)
		if !ok {
			return jsval.Undefined
		}
		return jsval.Num(p.Path().Area(geojson))
	}
	a, err := geo.GeoArea(c.view.ctx, geojson)
	if err != nil {
		panic(&budget.Stop{Err: err}) // a bound: Program.Eval returns it
	}
	return jsval.Num(a)
}

func boundsValue(b [2][2]float64) jsval.Value {
	return jsval.ArrOf(
		jsval.ArrOf(jsval.Num(b[0][0]), jsval.Num(b[0][1])),
		jsval.ArrOf(jsval.Num(b[1][0]), jsval.Num(b[1][1])),
	)
}

func (c *rtContext) GeoBounds(projection, geojson, group jsval.Value) jsval.Value {
	if projection.IsTruthy() {
		p, ok := c.projectionOf(projection)
		if !ok {
			return jsval.Undefined
		}
		return boundsValue(p.Path().Bounds(geojson))
	}
	b, err := geo.GeoBounds(c.view.ctx, geojson)
	if err != nil {
		panic(&budget.Stop{Err: err}) // a bound: Program.Eval returns it
	}
	return boundsValue(b)
}

func (c *rtContext) GeoCentroid(projection, geojson, group jsval.Value) jsval.Value {
	pt := func(p [2]float64) jsval.Value { return jsval.ArrOf(jsval.Num(p[0]), jsval.Num(p[1])) }
	if projection.IsTruthy() {
		p, ok := c.projectionOf(projection)
		if !ok {
			return jsval.Undefined
		}
		return pt(p.Path().Centroid(geojson))
	}
	cen, err := geo.GeoCentroid(c.view.ctx, geojson)
	if err != nil {
		panic(&budget.Stop{Err: err}) // a bound: Program.Eval returns it
	}
	return pt(cen)
}

func (c *rtContext) GeoScale(projection, group jsval.Value) jsval.Value {
	p, ok := c.projectionOf(projection)
	if !ok {
		return jsval.Undefined
	}
	return jsval.Num(p.Scale())
}

// GeoTranslate is geoTranslate(projection): the projection's translation [x, y].
func (c *rtContext) GeoTranslate(projection, group jsval.Value) jsval.Value {
	p, ok := c.projectionOf(projection)
	if !ok {
		return jsval.Undefined
	}
	x, y := p.Translate()
	return jsval.ArrOf(jsval.Num(x), jsval.Num(y))
}

// GeoShape returns a shape handle: a function of a path context that draws the
// GeoJSON through the projection.
func (c *rtContext) GeoShape(projection, geojson, group jsval.Value) jsval.Value {
	p, _ := c.projectionOf(projection)
	shp := geo.NewShape(p, func(jsval.Value) jsval.Value { return geojson }, geo.PointRadius{})
	shp.Bind(c.view.ctx)
	return c.newShape(geoShapeSource, func(ctx scene.PathContext, _ *scene.Item) string {
		if p == nil {
			return ""
		}
		if ctx == nil {
			d, _ := shp.Path(jsval.Undefined)
			return d
		}
		shp.Draw(ctx, jsval.Undefined)
		return ""
	})
}

// PathShape returns a shape handle for SVG path data. Upstream parses the path
// when the shape is first drawn, not when it is made, so a path that is not a
// string (a number, null) is only an error then: path.match fails, and it is
// the shape's bounds or its drawing that throws.
func (c *rtContext) PathShape(path jsval.Value) jsval.Value {
	if !path.IsStr() {
		return c.newShape(pathShapeSource, func(ctx scene.PathContext, _ *scene.Item) string {
			if ctx == nil {
				return c.g.walk.String(path)
			}
			if path.IsNullish() {
				of := "undefined"
				if path.IsNull() {
					of = "null"
				}
				panic(&jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + of + " (reading 'match')"})
			}
			panic(&jsval.Thrown{Name: "TypeError", Msg: "path.match is not a function"})
		})
	}
	fn, err := scene.PathFuncFromString(path.StrValue())
	if err != nil {
		panic(&budget.Stop{Err: err}) // a bound: Program.Eval returns it
	}
	return c.newShape(pathShapeSource, func(ctx scene.PathContext, _ *scene.Item) string { return fn(ctx) })
}

// geoShapeSource and pathShapeSource are the source text of the functions
// geoShape() and pathShape() return upstream (vega-functions shape.js), what
// String() of one gives.
const (
	geoShapeSource  = "function (context) {\n    return p ? p.path.context(context)(geojson) : '';\n  }"
	pathShapeSource = "function (context) {\n    return context ? pathRender(context, p = p || pathParse(path)) : path;\n  }"
)

// shapeHandleBytes is what a shape handle is charged: the function object,
// its generator's closure and, for geoShape, the geo.Shape it draws with.
// Measured as what the handles kept alive when the view held them all
// (TestEveryEnvMethodIsMetered on the code before C4): 247 bytes a geoShape
// handle, 101 a pathShape one; twice the larger, rounded up.
const shapeHandleBytes = 512

// newShape returns the function value that stands for a shape generator in
// expressions: the generator itself rides on the value (C4, C92), so it is
// garbage once nothing refers to it, and no data or specification can spell
// one. Each is charged to the call's ledger as it is made, as an expression
// charges what it builds: a formula can hold any number of them in its rows.
func (c *rtContext) newShape(src string, f scene.ShapeFunc) jsval.Value {
	c.view.chargeRun(shapeHandleBytes)
	return jsval.HostFunction(src, f)
}

// shapeOf is the generator a shape value stands for, nil for any other value.
func shapeOf(h jsval.Value) scene.ShapeFunc {
	impl, _ := jsval.FunctionImpl(h)
	f, _ := impl.(scene.ShapeFunc)
	return f
}

// TreePath implements expr.TreeProvider.
func (c *rtContext) TreePath(name string, source, target jsval.Value) jsval.Value {
	t := c.treeOf(name)
	if t == nil {
		return jsval.Undefined
	}
	// The tree's lookup object is keyed by String(key), dates in the view's
	// zone (hierarchy.Stratify).
	s, d := t.NodeByKey(c.view.jsString(source)), t.NodeByKey(c.view.jsString(target))
	if s == nil || d == nil {
		return jsval.Undefined
	}
	path := s.Path(d)
	c.view.chargeItems(len(path))
	out := make([]jsval.Value, len(path))
	for i, n := range path {
		out[i] = n.Data
	}
	return jsval.Arr(out)
}

func (c *rtContext) TreeAncestors(name string, node jsval.Value) jsval.Value {
	t := c.treeOf(name)
	if t == nil {
		return jsval.Undefined
	}
	n := t.NodeByKey(c.view.jsString(node))
	if n == nil {
		return jsval.Undefined
	}
	anc := n.Ancestors()
	c.view.chargeItems(len(anc))
	out := make([]jsval.Value, len(anc))
	for i, a := range anc {
		out[i] = a.Data
	}
	return jsval.Arr(out)
}

func (c *rtContext) treeOf(name string) *hierarchy.Tree {
	n := c.dataNode(name, "values")
	if n == nil {
		return nil
	}
	return n.tree
}
