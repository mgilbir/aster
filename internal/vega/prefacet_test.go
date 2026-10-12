package vega

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/svg"
)

// PreFacet throws where upstream's does: when its field parameter is
// modified (a signal's, on the first run too), when a tuple it faceted comes
// back with its field rewritten, and when a field is not an array
// (field(t).forEach). Each spec of testdata/prefacet is compared with what
// node logs and draws for it after the signal writes writes.json names
// (testdata/prefacet.node.json).
func TestPreFacetThrowsAsUpstream(t *testing.T) {
	var want map[string]struct {
		Writes map[string]json.RawMessage
		Errors []string
		Texts  []string
	}
	if err := json.Unmarshal([]byte(readFile(t, "testdata/prefacet.node.json")), &want); err != nil {
		t.Fatal(err)
	}
	if len(want) < 9 {
		t.Fatalf("%d cases recorded", len(want))
	}
	text := regexp.MustCompile(`<text[^>]*>([^<]*)</text>`)
	jsName := regexp.MustCompile(`^[A-Za-z]*Error: `)
	for name, w := range want {
		t.Run(name, func(t *testing.T) {
			var writes []SignalWrite
			for k, raw := range w.Writes {
				v, err := jsval.ParseJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				writes = append(writes, SignalWrite{Name: k, Value: v})
			}
			var errs []string
			res, err := Render(context.Background(), mustJSON(t, readFile(t, "testdata/prefacet/"+name)), Options{
				SignalWrites: writes,
				Report: func(err error) error {
					if _, warning := err.(Warning); !warning {
						errs = append(errs, jsName.ReplaceAllString(err.Error(), ""))
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(errs, "|") != strings.Join(w.Errors, "|") {
				t.Errorf("errors %q, want %q", errs, w.Errors)
			}
			doc, err := svg.Render(context.Background(), res.Scenegraph, svg.Options{Width: res.Width, Height: res.Height})
			if err != nil {
				t.Fatal(err)
			}
			var texts []string
			for _, m := range text.FindAllStringSubmatch(doc, -1) {
				texts = append(texts, m[1])
			}
			if strings.Join(texts, "|") != strings.Join(w.Texts, "|") {
				t.Errorf("texts %q, want %q", texts, w.Texts)
			}
		})
	}
}

// What a prefacet keeps to tell a rewrite of its field (one record per cell,
// made the first time the cell is filled) is charged to the call's ledger as
// it grows, and given back when the run is over.
func TestPreFacetRecordIsCharged(t *testing.T) {
	spec := mustJSON(t, `{"signals":[{"name":"n","value":50}],
	  "data":[{"name":"d","transform":[{"type":"sequence","start":0,"stop":{"signal":"n"}},{"type":"formula","as":"kids","expr":"[datum.data]"}]}],
	  "marks":[{"type":"group","from":{"facet":{"name":"k","data":"d","field":"kids"}},
	    "marks":[{"type":"rect","from":{"data":"k"}}]}]}`)
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	ctx := budget.WithLedger(context.Background(), led)
	config := mergeConfig(defaultConfig(), jsval.Undefined, spec.Get("config"))
	scope := newScope(config, &parseOptions{})
	parseView(spec, scope)
	v := newView(ctx, Options{}, scope.locale)
	v.build(scope.toRuntime())
	if err := v.run(); err != nil {
		t.Fatal(err)
	}
	if want := int64(50 * prefacetLastBytes); v.runBytes != want || led.Bytes() < want {
		t.Fatalf("charged %d (ledger %d), want %d for 50 cells", v.runBytes, led.Bytes(), want)
	}
	// a rerun over 60 new tuples makes 60 more cells, each charged
	v.g.update(v.root.signals["n"], jsval.Num(60), false, false)
	if err := v.run(); err != nil {
		t.Fatal(err)
	}
	if want := int64(110 * prefacetLastBytes); v.runBytes != want {
		t.Errorf("charged %d after the rerun, want %d", v.runBytes, want)
	}
	v.releaseLists()
	if led.Bytes() != 0 {
		t.Errorf("%d bytes left on the ledger after the run", led.Bytes())
	}
}
