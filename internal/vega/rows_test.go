package vega

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// mapLoader serves fixed bodies by url.
type mapLoader map[string]string

func (m mapLoader) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }

func (m mapLoader) Load(_ context.Context, uri string) ([]byte, error) {
	if b, ok := m[uri]; ok {
		return []byte(b), nil
	}
	return nil, fmt.Errorf("no such url %q", uri)
}

// dataSets is a list of data set definitions as a Vega specification.
func dataSets(defs ...string) string {
	return `{"width":10,"height":10,"data":[` + strings.Join(defs, ",") + `]}`
}

func seqData(name string, stop int) string {
	return fmt.Sprintf(`{"name":%q,"transform":[{"type":"sequence","start":0,"stop":%d}]}`, name, stop)
}

// MaxRows bounds the rows alive in the render, whichever operator made them:
// each operator holds the tuples it created, replaced (not added to) when it
// runs again, and an operator that only passes tuples on (a filter) holds
// none, so it can neither add to the count nor take from it.
func TestMaxRowsBoundsTheRowsAlive(t *testing.T) {
	const maxRows = 1000
	over := map[string]string{
		// ten filters that drop everything used to credit 9,000 rows
		"filters do not credit rows": func() string {
			defs := []string{seqData("d", 900)}
			for i := range 10 {
				defs = append(defs, fmt.Sprintf(`{"name":"f%d","source":"d","transform":[{"type":"filter","expr":"false"}]}`, i))
			}
			for i := range 9 {
				defs = append(defs, seqData(fmt.Sprintf("s%d", i), 999))
			}
			return dataSets(defs...)
		}(),
		"aggregate outputs": dataSets(`{"name":"d","transform":[{"type":"sequence","start":0,"stop":600},
			{"type":"aggregate","groupby":["data"]}]}`),
		"values from a signal": `{"signals":[{"name":"v","update":"sequence(0, 5000)"}],
			"data":[{"name":"d","values":{"signal":"v"}}]}`,
		"rows a trigger inserts": `{"data":[{"name":"d","values":[{"a":1}],
			"on":[{"trigger":"true","insert":"sequence(0, 2000)"}]}]}`,
		"cells of a prefacet": `{"data":[{"name":"d","transform":[{"type":"sequence","start":0,"stop":10},
			{"type":"formula","as":"kids","expr":"sequence(0, 200)"}]}],
			"marks":[{"type":"group","from":{"facet":{"name":"k","data":"d","field":"kids"}},
			"marks":[{"type":"rect","from":{"data":"k"}}]}]}`,
		"nodes a nest generates and its links": dataSets(`{"name":"t","transform":[{"type":"sequence","start":0,"stop":400},
			{"type":"formula","as":"k","expr":"datum.data"},
			{"type":"nest","keys":["k"],"generate":true}]}`,
			`{"name":"l","source":"t","transform":[{"type":"treelinks"}]}`),
		"derived copies a relay hands on": dataSets(seqData("d", 600),
			`{"name":"e","source":"d","transform":[{"type":"formula","as":"x","expr":"1"}]}`),
	}
	for name, spec := range over {
		t.Run(name, func(t *testing.T) {
			_, err := Render(context.Background(), mustJSON(t, spec), Options{Limits: Limits{MaxRows: maxRows}})
			if !errors.Is(err, budget.ErrLimit) {
				t.Fatalf("err = %v, want the row limit", err)
			}
			if !strings.Contains(err.Error(), "data rows") || !strings.Contains(err.Error(), "exceeds the limit of 1000") {
				t.Errorf("err = %v, want the budget's own message", err)
			}
		})
	}
	under := map[string]struct {
		spec   string
		writes []SignalWrite
	}{
		// a sequence rerun from 600 to 601 rows holds 601, not 1,201
		"a rerun replaces what an operator holds": {
			spec: `{"signals":[{"name":"n","value":600}],
				"data":[{"name":"d","transform":[{"type":"sequence","start":0,"stop":{"signal":"n"}}]}]}`,
			writes: []SignalWrite{{Name: "n", Value: jsval.Num(601)}},
		},
		// the rows a data set gives up are free for another
		"rows given up are free again": {
			spec: `{"signals":[{"name":"n","value":900},{"name":"m","value":0}],
				"data":[{"name":"a","transform":[{"type":"sequence","start":0,"stop":{"signal":"n"}}]},
				{"name":"b","transform":[{"type":"sequence","start":0,"stop":{"signal":"m"}}]}]}`,
			writes: []SignalWrite{{Name: "n", Value: jsval.Num(100)}, {Name: "m", Value: jsval.Num(850)}},
		},
		"a chain of transforms over the same tuples": {
			spec: dataSets(`{"name":"d","transform":[{"type":"sequence","start":0,"stop":900},
				{"type":"formula","as":"x","expr":"datum.data*2"},
				{"type":"filter","expr":"datum.x >= 0"},
				{"type":"collect","sort":{"field":"x","order":"descending"}},
				{"type":"extent","field":"x","signal":"e"}]}`,
				`{"name":"e1","source":"d","transform":[{"type":"filter","expr":"true"}]}`,
				`{"name":"e2","source":"d","transform":[{"type":"filter","expr":"true"}]}`),
		},
	}
	for name, c := range under {
		t.Run(name, func(t *testing.T) {
			_, err := Render(context.Background(), mustJSON(t, c.spec), Options{Limits: Limits{MaxRows: maxRows}, SignalWrites: c.writes})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The bytes of rows outweighing what a row stands for count too, and a
// filter's output holds no weight of its own.
func TestRowWeightIsHeldByTheCreator(t *testing.T) {
	lim := Limits{MaxRows: 1000, RowBytes: 512}
	heavy := dataSets(`{"name":"d","transform":[{"type":"sequence","start":0,"stop":100},
		{"type":"formula","as":"s","expr":"pad('', 2000, 'x')"}]}`,
		`{"name":"f1","source":"d","transform":[{"type":"filter","expr":"true"}]}`,
		`{"name":"f2","source":"d","transform":[{"type":"filter","expr":"true"}]}`)
	if _, err := Render(context.Background(), mustJSON(t, heavy), Options{Limits: lim}); err != nil {
		t.Fatalf("100 rows of 2 KB: %v", err)
	}
	// 300 rows of 2 KB weigh 300 + 300*(2000+...-512)/512 > 1000 rows
	heavier := strings.Replace(heavy, `"stop":100`, `"stop":300`, 1)
	if _, err := Render(context.Background(), mustJSON(t, heavier), Options{Limits: lim}); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("300 rows of 2 KB: err = %v, want the row limit", err)
	}
}

// A data set loaded from a url holds its rows.
func TestLoadedRowsAreCounted(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := range 1500 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"a":%d}`, i)
	}
	b.WriteString("]")
	loader := mapLoader{"d.json": b.String()}
	for name, spec := range map[string]string{
		"at parse":         `{"data":[{"name":"d","url":"d.json"}]}`,
		"from a signal":    `{"signals":[{"name":"u","value":"d.json"}],"data":[{"name":"d","url":{"signal":"u"}}]}`,
		"with a transform": `{"data":[{"name":"d","url":"d.json","transform":[{"type":"filter","expr":"true"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Render(context.Background(), mustJSON(t, spec), Options{Loader: loader, Limits: Limits{MaxRows: 1000}})
			if !errors.Is(err, budget.ErrLimit) {
				t.Fatalf("err = %v, want the row limit", err)
			}
			// the budget's own message, which names what was counted
			if want := "data rows 1500 exceeds the limit of 1000"; !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
}

// Every test of the package checks that no transform passes on tuples no
// operator holds (rows.go); benchmarks measure the engine without the check.
func TestMain(m *testing.M) {
	flag.Parse()
	if b := flag.Lookup("test.bench"); b == nil || b.Value.String() == "" {
		checkHeld = true
	}
	os.Exit(m.Run())
}

// The tuple list an operator outputs is memory the rows do not count (a
// filter's list of the rows it lets through): it is charged to the call's
// ledger, replaced when the operator runs again, and given back when the
// render is over.
func TestTupleListsAreCharged(t *testing.T) {
	defs := []string{seqData("d", 20_000)}
	for i := range 200 {
		defs = append(defs, fmt.Sprintf(`{"name":"f%d","source":"d","transform":[{"type":"filter","expr":"true"}]}`, i))
	}
	spec := mustJSON(t, dataSets(defs...))
	// 200 lists of 20,000 tuples are 64 MB
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 16 << 20})
	ctx := budget.WithLedger(context.Background(), led)
	if _, err := Render(ctx, spec, Options{}); !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("err = %v, want the ledger's limit", err)
	}
	// ten fit, and are given back once the render is over
	led = budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 16 << 20})
	ctx = budget.WithLedger(context.Background(), led)
	small := mustJSON(t, dataSets(defs[:11]...))
	if _, err := Render(ctx, small, Options{}); err != nil {
		t.Fatal(err)
	}
	if led.PeakBytes() < 10*20_000*16 || led.Bytes() != 0 {
		t.Errorf("peak %d, after the render %d: want the lists charged, then given back", led.PeakBytes(), led.Bytes())
	}
}
