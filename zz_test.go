package aster_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// TestZZFresh: is a signal-sweep difference a re-run bug? Render the case's
// spec with the write folded into the signal's initial value, no write.
func TestZZFresh(t *testing.T) {
	o := oracle.For(t, oracle.VL6)
	g, _ := o.Generate("signals")
	var out struct{ Cases []sweepCase }
	json.Unmarshal(g.Data, &out)
	c := oracleConverter(t)
	for _, sc := range out.Cases {
		if !strings.Contains(sc.Name, os.Getenv("CASE")) {
			continue
		}
		var spec map[string]any
		json.Unmarshal(sc.Spec, &spec)
		for _, w := range sc.Writes {
			for _, s := range spec["signals"].([]any) {
				m := s.(map[string]any)
				if m["name"] == w.Name {
					var v any
					json.Unmarshal(w.Value, &v)
					m["value"] = v
				}
			}
		}
		b, _ := json.Marshal(spec)
		want, _ := o.SVG(false, b)
		got, err := c.VegaToSVG(b)
		d, _ := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
		t.Logf("%s fresh: err=%v identical=%v equal=%v %.300s", sc.Name, err, d.Identical, d.Equal, d.Diff)
	}
}
