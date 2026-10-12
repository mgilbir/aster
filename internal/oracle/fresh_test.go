package oracle

import (
	"bytes"
	"testing"
)

// Every answer is a fresh process's, even after an input that makes upstream
// change a built-in. Vega-Lite's aggregate node, given the field "__proto__",
// assigns its measures to Object.prototype, and a process that compiled it
// went on to give every later compilation a config holding them (wild
// corpus case vl_1541 was answered so, after the type-confusion differential
// had run such a mutant in the same process). The test asks one process, so
// the answers cannot come from different ones, and bypasses the cache.
func TestAnswerAfterBuiltinChangeIsFresh(t *testing.T) {
	const (
		// An error band over a field named __proto__ (the mutant).
		polluting = `{"data":{"values":[{"v":1,"w":2}]},"mark":"errorband","encoding":{"y":{"field":"__proto__","type":"quantitative"},"x":{"field":"v","type":"quantitative"}}}`
		// vl_1541's first layers, an error band and a line, over inline data.
		later = `{"data":{"values":[{"datetime":"2020-09-01","lower":1,"upper":3,"mean":2}]},"vconcat":[{"layer":[` +
			`{"mark":{"type":"errorband","clip":true,"color":"goldenrod"},"encoding":{"y":{"field":"lower","type":"quantitative","title":""},"y2":{"field":"upper"},"x":{"field":"datetime","type":"temporal"}}},` +
			`{"mark":{"type":"line","clip":true,"color":"goldenrod"},"encoding":{"y":{"field":"mean","type":"quantitative"},"x":{"field":"datetime","type":"temporal"}}}]}]}`
	)
	for _, set := range []string{VL6, VL5} {
		shared := For(t, set)
		single := func() *Oracle {
			o := &Oracle{root: shared.root, set: shared.set, zone: shared.zone, version: shared.version, free: make(chan *proc, 1)}
			o.free <- nil
			t.Cleanup(func() {
				if p := <-o.free; p != nil {
					p.stop()
				}
			})
			return o
		}
		compile := func(o *Oracle, spec string) Result {
			t.Helper()
			res, err := o.node(map[string]any{"op": "compile", "lite": true, "spec": spec})
			if err != nil {
				t.Fatalf("%s: %v", set, err)
			}
			if res.Err != "" {
				t.Fatalf("%s: upstream refuses %.80s: %s", set, spec, res.Err)
			}
			return res
		}
		want := compile(single(), later)
		o := single()
		compile(o, polluting)
		if got := compile(o, later); !bytes.Equal(got.Vega, want.Vega) {
			t.Errorf("%s: after the polluting spec, the answer is not a fresh process's:\n got %.300s\nwant %.300s", set, got.Vega, want.Vega)
		}
	}
}
