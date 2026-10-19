package geo

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestTopoFeatureEdgeShapes is C165 (audit 2026-10-18), against
// topojson-client 3.1 (node 24.21.0): line() pads a line of fewer than two
// points with its first point once (an empty one is [undefined]); a member
// of a GeometryCollection that is not an object is a Feature with a null
// geometry, and a null member throws, as reading its id does.
func TestTopoFeatureEdgeShapes(t *testing.T) {
	topo, err := jsval.ParseJSONString(`{"type":"Topology","arcs":[[[0,0],[1,1]],[[5,5]]],"objects":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := jsval.ParseJSONString(`{"type":"GeometryCollection","geometries":[5,"x",[1],true,{"type":"LineString","arcs":[]},{"type":"LineString","arcs":[1]},{"type":"Polygon","arcs":[[]]},{"type":"GeometryCollection","geometries":[7,{"type":"Point","coordinates":[1,2]}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := TopoFeature(context.Background(), topo, obj)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},"geometry":null},{"type":"Feature","properties":{},"geometry":null},{"type":"Feature","properties":{},"geometry":null},{"type":"Feature","properties":{},"geometry":null},{"type":"Feature","properties":{},"geometry":{"type":"LineString","coordinates":[null]}},{"type":"Feature","properties":{},"geometry":{"type":"LineString","coordinates":[[5,5],[5,5]]}},{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[null,null,null,null]]}},{"type":"Feature","properties":{},"geometry":{"type":"GeometryCollection","geometries":[null,{"type":"Point","coordinates":[1,2]}]}}]}`
	if s := jsonText(got); s != want {
		t.Errorf("feature:\n got %s\nwant %s", s, want)
	}
	for _, src := range []string{
		`{"type":"GeometryCollection","geometries":[null]}`,
		`{"type":"GeometryCollection","geometries":[{"type":"GeometryCollection","geometries":[null]}]}`,
	} {
		o, err := jsval.ParseJSONString(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := TopoFeature(context.Background(), topo, o); err == nil {
			t.Errorf("%s: no error; topojson-client throws", src)
		}
	}
}
