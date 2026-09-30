package geo

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

type topoHash struct {
	Len int    `json:"len"`
	Sha string `json:"sha"`
}

func hashOf(v jsval.Value) topoHash {
	s := string(jsval.AppendJSON(nil, v))
	return topoHash{len(s), shaOf(s)}
}

func TestTopoJSON(t *testing.T) {
	var g map[string]json.RawMessage
	loadGolden(t, "topo.json.gz", &g)
	for _, name := range []string{"world", "us"} {
		file := map[string]string{"world": "world-110m.json", "us": "us-10m.json"}[name]
		topo := loadTopology(t, file)
		var want map[string]map[string]topoHash
		if err := json.Unmarshal(g[name], &want); err != nil {
			t.Fatal(err)
		}
		for key, w := range want {
			obj := topo.Get("objects").Get(key)
			f, err := TopoFeature(topo, obj)
			if err != nil {
				t.Fatalf("%s/%s: %v", name, key, err)
			}
			if h := hashOf(f); h != w["feature"] {
				t.Errorf("%s/%s feature: %v want %v", name, key, h, w["feature"])
			}
			for mode, filter := range map[string]MeshFilter{"mesh": MeshAll, "interior": MeshInterior, "exterior": MeshExterior} {
				m, err := TopoMesh(topo, obj, filter)
				if err != nil {
					t.Fatalf("%s/%s %s: %v", name, key, mode, err)
				}
				if h := hashOf(m); h != w[mode] {
					t.Errorf("%s/%s %s: %v want %v", name, key, mode, h, w[mode])
				}
			}
		}
	}
	// The whole decoded land feature, structurally.
	world := loadTopology(t, "world-110m.json")
	land, err := TopoFeature(world, world.Get("objects").Get("land"))
	if err != nil {
		t.Fatal(err)
	}
	want := parseJSONValue(t, g["small"])
	if !jsval.Equal(land, want) {
		t.Error("land feature differs from topojson-client's")
	}
}

func TestTopologyFeatures(t *testing.T) {
	world := loadTopology(t, "world-110m.json")
	feats, err := TopologyFeatures(world, "countries", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(feats) != 177 {
		t.Errorf("countries: %d features", len(feats))
	}
	one, err := TopologyFeatures(world, "land", "", "")
	if err != nil || len(one) != 1 {
		t.Errorf("land: %v %d", err, len(one))
	}
	mesh, err := TopologyFeatures(world, "", "countries", "interior")
	if err != nil || len(mesh) != 1 || mesh[0].Get("type").StrValue() != "MultiLineString" {
		t.Errorf("mesh: %v %v", err, mesh)
	}
	if _, err := TopologyFeatures(world, "", "", ""); err == nil {
		t.Error("missing feature/mesh should fail")
	}
	if _, err := TopologyFeatures(world, "nosuch", "", ""); err == nil {
		t.Error("missing object should fail")
	}
}

func TestTopoJSONMalformed(t *testing.T) {
	for _, src := range []string{
		`{}`,
		`{"arcs": 5, "objects": {}}`,
		`{"arcs": [[1,2]], "objects": {"a": {"type": "LineString", "arcs": [0]}}}`,
		`{"arcs": [[[0,0],[1,1]]], "objects": {"a": {"type": "LineString", "arcs": [7]}}}`,
		`{"arcs": [[[0,0],[1,1]]], "objects": {"a": {"type": "LineString", "arcs": ["x"]}}}`,
		`{"arcs": [[[0,0],[1,1]]], "transform": {"scale": 1}, "objects": {"a": {"type": "LineString", "arcs": [0]}}}`,
		`{"arcs": [[]], "objects": {"a": {"type": "MultiLineString", "arcs": [[0]]}}}`,
	} {
		topo := parseJSONValue(t, []byte(src))
		obj := topo.Get("objects").Get("a")
		// None of these may panic.
		_, _ = TopoFeature(topo, obj)
		_, _ = TopoMesh(topo, obj, MeshAll)
		_, _ = TopologyFeatures(topo, "a", "", "")
	}
}
