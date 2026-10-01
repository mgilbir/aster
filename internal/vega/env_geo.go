package vega

import (
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms/hierarchy"
)

// This file implements the expression environment's projection, shape and tree
// functions on the context (vega-functions geo.js, shape.js, tree.js).

func (c *rtContext) projectionOf(ref jsval.Value) (geo.Projection, bool) {
	if !ref.IsStr() {
		return nil, false
	}
	n := c.scaleNode(ref.StrValue())
	if n == nil {
		return nil, false
	}
	p, ok := n.value.(geo.Projection)
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
	return jsval.Num(geo.GeoArea(geojson))
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
	return boundsValue(geo.GeoBounds(geojson))
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
	return pt(geo.GeoCentroid(geojson))
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
	return c.view.newShape(func(ctx scene.PathContext, _ *scene.Item) string {
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

// PathShape returns a shape handle for SVG path data.
func (c *rtContext) PathShape(path jsval.Value) jsval.Value {
	fn, err := scene.PathFuncFromString(path.AsString())
	if err != nil {
		failErr(err)
	}
	return c.view.newShape(func(ctx scene.PathContext, _ *scene.Item) string { return fn(ctx) })
}

// newShape registers a shape generator and returns the opaque value that
// stands for it in expressions.
func (v *runView) newShape(f scene.ShapeFunc) jsval.Value {
	v.shapes = append(v.shapes, f)
	return obj("$shape", jsval.Int(len(v.shapes)-1))
}

// shapeOf resolves a shape handle.
func (v *runView) shapeOf(h jsval.Value) scene.ShapeFunc {
	if !h.IsObj() {
		return nil
	}
	id := h.Get("$shape")
	if !id.IsNum() {
		return nil
	}
	i := int(id.NumValue())
	if i < 0 || i >= len(v.shapes) {
		return nil
	}
	return v.shapes[i]
}

// TreePath implements expr.TreeProvider.
func (c *rtContext) TreePath(name string, source, target jsval.Value) jsval.Value {
	t := c.treeOf(name)
	if t == nil {
		return jsval.Undefined
	}
	s, d := t.NodeByKey(source.AsString()), t.NodeByKey(target.AsString())
	if s == nil || d == nil {
		return jsval.Undefined
	}
	path := s.Path(d)
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
	n := t.NodeByKey(node.AsString())
	if n == nil {
		return jsval.Undefined
	}
	anc := n.Ancestors()
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
