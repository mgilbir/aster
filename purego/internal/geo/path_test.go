package geo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

const dataDir = "../../../testdata/vega-datasets/data"

// loadTopology reads a vega-datasets topojson file, skipping the test when the
// datasets are not checked out.
func loadTopology(t testing.TB, name string) jsval.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, name))
	if err != nil {
		t.Skipf("dataset %s not available: %v", name, err)
	}
	v, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func topoFeatureOf(t testing.TB, topo jsval.Value, object string) jsval.Value {
	t.Helper()
	f, err := TopoFeature(topo, topo.Get("objects").Get(object))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func featureByID(t testing.TB, fc jsval.Value, id int) jsval.Value {
	t.Helper()
	for _, f := range fc.Get("features").Items() {
		if v, ok := f.Get("id").NumberOrNull(); ok && int(v) == id {
			return f
		}
	}
	t.Fatalf("feature %d not found", id)
	return jsval.Undefined
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

type goldenPaths struct {
	Cases []struct {
		ID    string                     `json:"id"`
		Small map[string]json.RawMessage `json:"small"`
		Big   map[string]json.RawMessage `json:"big"`
	} `json:"cases"`
	Configs []struct {
		ID    string          `json:"id"`
		Type  string          `json:"type"`
		Props json.RawMessage `json:"props"`
	} `json:"configs"`
}

// checkPath compares a generated path with a recorded one: null, the full
// string, or {len, sha}.
func checkPath(t *testing.T, label string, got string, ok bool, raw json.RawMessage) {
	t.Helper()
	if string(raw) == `"ERR"` || string(raw) == `"undefined"` {
		return // upstream threw
	}
	if string(raw) == "null" {
		if ok {
			t.Errorf("%s: got %q want null", label, trunc(got))
		}
		return
	}
	if !ok {
		t.Errorf("%s: got null want %s", label, trunc(string(raw)))
		return
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if got != s {
			t.Errorf("%s:\n got  %s\n want %s", label, trunc(got), trunc(s))
		}
		return
	}
	var h struct {
		Len int    `json:"len"`
		Sha string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("%s: bad golden %s", label, raw)
	}
	if len(got) != h.Len || shaOf(got) != h.Sha {
		t.Errorf("%s: len %d sha %s, want len %d sha %s\n got %s", label, len(got), shaOf(got), h.Len, h.Sha, trunc(got))
	}
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// corpus loads the synthetic geometries recorded by the generator and the real
// ones decoded from the vega-datasets topologies, keyed like the generator does.
func corpus(t testing.TB) map[string]jsval.Value {
	t.Helper()
	var synth map[string]json.RawMessage
	loadGolden(t, "geoms.json.gz", &synth)
	world := loadTopology(t, "world-110m.json")
	us := loadTopology(t, "us-10m.json")
	countries := topoFeatureOf(t, world, "countries")
	g := map[string]jsval.Value{
		"land":       topoFeatureOf(t, world, "land"),
		"countries":  countries,
		"states":     topoFeatureOf(t, us, "states"),
		"nation":     topoFeatureOf(t, us, "land"),
		"fiji":       featureByID(t, countries, 242),
		"russia":     featureByID(t, countries, 643),
		"antarctica": featureByID(t, countries, 10),
		"greenland":  featureByID(t, countries, 304),
		"usa":        featureByID(t, countries, 840),
		"chile":      featureByID(t, countries, 152),
		"indonesia":  featureByID(t, countries, 360),
		"canada":     featureByID(t, countries, 124),
		"norway":     featureByID(t, countries, 578),
	}
	for k, raw := range synth {
		g[k] = parseJSONValue(t, raw)
	}
	return g
}

func TestPathStrings(t *testing.T) {
	var g goldenPaths
	loadGolden(t, "paths.json.gz", &g)
	geoms := corpus(t)
	for i, c := range g.Cases {
		cfg := g.Configs[i]
		proj := projFromGolden(t, cfg.Type, cfg.Props)
		path := proj.Path()
		for _, group := range []map[string]json.RawMessage{c.Small, c.Big} {
			for k, raw := range group {
				s, ok := path.String(geoms[k])
				checkPath(t, c.ID+"/"+k, s, ok, raw)
			}
		}
	}
}

func TestRandomConfigurations(t *testing.T) {
	var g []struct {
		Type  string                     `json:"type"`
		Props json.RawMessage            `json:"props"`
		Out   map[string]json.RawMessage `json:"out"`
		Probe []any                      `json:"probe"`
	}
	loadGolden(t, "random.json.gz", &g)
	geoms := corpus(t)
	pts := make([][2]float64, 0, 40)
	var pg goldenPoints
	loadGolden(t, "points.json.gz", &pg)
	pts = append(pts, pg.Points[:40]...)
	for i, c := range g {
		p := projFromGolden(t, c.Type, c.Props)
		label := c.Type + " #" + jsvalInt(i) + " " + string(c.Props)
		for k, raw := range c.Out {
			s, ok := p.Path().String(geoms[k])
			checkPath(t, label+"/"+k, s, ok, raw)
		}
		for j, pt := range pts {
			x, y, ok := p.Forward(pt[0], pt[1])
			wx, wy, wok := pairOfGolden(c.Probe[j])
			if ok != wok || (ok && (!same(x, wx) || !same(y, wy))) {
				t.Errorf("%s: forward %v = [%v %v] ok=%v want [%v %v] ok=%v", label, pt, x, y, ok, wx, wy, wok)
				break
			}
		}
	}
}

func jsvalInt(i int) string { return jsval.JSNumberString(float64(i)) }
