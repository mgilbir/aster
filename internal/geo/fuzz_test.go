package geo

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/fuzzutil"
	"github.com/mgilbir/aster/internal/jsval"
)

// FuzzTopoJSON decodes arbitrary documents as topologies: every object of the
// document is extracted as features and as a mesh with each filter, and the
// result is GeoJSON that serializes to valid JSON. Failures are errors, never
// panics, and the extraction finishes.
func FuzzTopoJSON(f *testing.F) {
	for _, s := range []string{
		`{"type":"Topology","transform":{"scale":[0.5,0.5],"translate":[-10,5]},"objects":{"a":{"type":"GeometryCollection","geometries":[` +
			`{"type":"Polygon","arcs":[[0,1]],"properties":{"x":1},"id":"p"},{"type":"LineString","arcs":[2]},{"type":"Point","coordinates":[1,2]},` +
			`{"type":"MultiPolygon","arcs":[[[0,-2]],[[1]]]},{"type":"MultiPoint","coordinates":[[1,2],[3,4]]},{"type":"MultiLineString","arcs":[[0],[-3]]},{}]},` +
			`"b":{"type":"Polygon","arcs":[[0,1]]}},` +
			`"arcs":[[[0,0],[10,0],[0,10]],[[10,10],[-10,0],[0,-10]],[[0,0],[1,1]]]}`,
		`{"type":"Topology","objects":{"a":{"type":"LineString","arcs":[0,-1]}},"arcs":[[[0,0,5],[1,1,6]]]}`,
		`{"type":"Topology","objects":{"a":{"type":"MultiPolygon","arcs":[[[0]],[[0,0,0]]]}},"arcs":[[[0,0],[1,0],[1,1],[0,0]]]}`,
		`{"arcs":[],"objects":{"a":{"type":"GeometryCollection","geometries":[{"type":"GeometryCollection","geometries":[]}]}}}`,
		`{"transform":{"scale":[0,"x"],"translate":[]},"arcs":[[[1e308,1e308],[1e308,1e308]]],"objects":{"a":{"type":"LineString","arcs":[0]}}}`,
		`{"arcs":[[[0,0]]],"objects":{"a":{"type":"LineString","arcs":[2147483648,-0.5,null]}}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		topo, err := jsval.ParseJSON(data)
		if err != nil {
			return
		}
		fuzzutil.Within(t, 10*time.Second, string(data), func() {
			objects := topo.Get("objects")
			if !objects.IsObj() {
				return
			}
			check := func(what string, v jsval.Value, err error) {
				if err == nil && !json.Valid(jsval.AppendJSON(nil, v)) {
					t.Errorf("%s of %q is not valid JSON", what, data)
				}
			}
			o := objects.ObjValue()
			for i := 0; i < o.Len(); i++ {
				v, err := TopoFeature(topo, o.ValueAt(i))
				check("feature", v, err)
				for _, filter := range []MeshFilter{MeshAll, MeshInterior, MeshExterior} {
					v, err := TopoMesh(topo, o.ValueAt(i), filter)
					check("mesh", v, err)
				}
				_, _ = TopologyFeatures(topo, o.KeyAt(i), "", "")
				_, _ = TopologyFeatures(topo, "", o.KeyAt(i), "interior")
			}
		})
	})
}
