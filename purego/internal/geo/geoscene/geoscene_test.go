package geoscene

import (
	"testing"

	"github.com/mgilbir/aster/purego/internal/geo"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
)

func TestShapeFunc(t *testing.T) {
	p, err := geo.NewProjection("identity")
	if err != nil {
		t.Fatal(err)
	}
	line, _ := jsval.ParseJSONString(`{"type":"LineString","coordinates":[[0,0],[5,5]]}`)
	item := &scene.Item{Datum: line}
	shape := geo.NewShape(p, nil, geo.PointRadius{})
	fn := ShapeFunc(shape, nil)
	if got := fn(nil, item); got != "M0,0L5,5" {
		t.Errorf("path data %q", got)
	}
	var sp scene.StringPath
	if got := fn(&sp, item); got != "" {
		t.Errorf("draw returned %q", got)
	}
	if got := sp.String(); got != "M0,0L5,5" {
		t.Errorf("drawn path %q", got)
	}
	pf := PathFunc(p.Path(), line)
	if got := pf(nil); got != "M0,0L5,5" {
		t.Errorf("PathFunc %q", got)
	}
}
