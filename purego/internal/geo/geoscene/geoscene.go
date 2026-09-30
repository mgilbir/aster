// Package geoscene adapts the geo package's path generators to the scenegraph's
// PathFunc and ShapeFunc conventions (draw into a context, or with a nil context
// return the SVG path data). It lives apart from both packages so that neither
// has to import the other.
package geoscene

import (
	"github.com/mgilbir/aster/purego/internal/geo"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
)

// context converts a scenegraph path context to the geo one, keeping a nil
// interface nil (a nil scene.PathContext must select string output).
func context(ctx scene.PathContext) geo.PathContext {
	if ctx == nil {
		return nil
	}
	return ctx
}

// PathFunc is a scene.PathFunc drawing the GeoJSON object with the path
// generator, as `path(object)` with a context or `path.context(null)(object)`.
func PathFunc(p *geo.Path, object jsval.Value) scene.PathFunc {
	return func(ctx scene.PathContext) string { return p.Render(context(ctx), object) }
}

// ShapeFunc is the scene.ShapeFunc of a geoshape: it draws the item's GeoJSON
// through the shape. object extracts the GeoJSON from the item; nil means the
// item's datum, geoshape's default field.
func ShapeFunc(s *geo.Shape, object func(*scene.Item) jsval.Value) scene.ShapeFunc {
	if object == nil {
		object = func(it *scene.Item) jsval.Value { return it.Datum }
	}
	return func(ctx scene.PathContext, it *scene.Item) string {
		obj := object(it)
		if ctx == nil {
			d, _ := s.PathOf(obj)
			return d
		}
		s.DrawOf(context(ctx), obj)
		return ""
	}
}
