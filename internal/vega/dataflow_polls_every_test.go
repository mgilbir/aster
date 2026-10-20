package vega

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// pollRows is how many rows the cases of TestEveryOperatorPollsThroughout
// hand the operator they exercise: the transforms poll their context every
// 4,096 rows at least (transforms.ctxCheckMask), the dataflow's own operators
// every 1,024 steps of work, so an operator that polls throughout polls at
// least pollRows/4096 times.
const pollRows = 20_000

// pollSeq is a data set t of n rows built by the dataflow (no JSON to parse):
// i, a (i mod 97), b (a shuffle of i mod 1000), k (one of 50 keys), w (one of
// 300 words), p (i's parent in a binary tree) and arr (a pair).
func pollSeq(n int) string {
	return fmt.Sprintf(`{"name":"t","transform":[{"type":"sequence","start":0,"stop":%d,"as":"i"},
		{"type":"formula","as":"a","expr":"datum.i %% 97"},
		{"type":"formula","as":"b","expr":"(datum.i * 7919) %% 1000"},
		{"type":"formula","as":"k","expr":"'k' + (datum.i %% 50)"},
		{"type":"formula","as":"w","expr":"'w' + (datum.i %% 300) + ' x'"},
		{"type":"formula","as":"p","expr":"datum.i == 0 ? null : floor((datum.i - 1) / 2)"},
		{"type":"formula","as":"arr","expr":"[datum.a, datum.b]"},
		{"type":"formula","as":"lon","expr":"(datum.i %% 360) - 180"},
		{"type":"formula","as":"lat","expr":"(datum.b %% 160) - 80"}]}`, n)
}

// pollSpec is a specification of the rows of pollSeq(n) and then data and
// marks.
func pollSpec(n int, data, rest string) string {
	if data != "" {
		data = "," + data
	}
	if rest != "" {
		rest = "," + rest
	}
	return `{"width":200,"height":200,"projections":[{"name":"proj","type":"mercator"}],"data":[` + pollSeq(n) + data + `]` + rest + `}`
}

// derived is a data set d of t's rows through the transforms tx.
func derived(tx string) string { return `{"name":"d","source":"t","transform":[` + tx + `]}` }

// opPollCase exercises an operator over rows rows (or as many units of the work
// it does); ops are the operators it covers.
type opPollCase struct {
	ops  []string
	rows int
	spec string
	opts Options
}

func everyOperatorCases() []opPollCase {
	n := pollRows
	stratify := `{"type":"stratify","key":"i","parentKey":"p"}`
	return []opPollCase{
		{ops: []string{"sequence", "formula"}, rows: n, spec: pollSpec(n, "", "")},
		{ops: []string{"filter"}, rows: n, spec: pollSpec(n, derived(`{"type":"filter","expr":"datum.a > 10"}`), "")},
		{ops: []string{"aggregate"}, rows: n, spec: pollSpec(n, derived(`{"type":"aggregate","groupby":["k"],"ops":["count","sum"],"fields":[null,"b"]}`), "")},
		{ops: []string{"bin"}, rows: n, spec: pollSpec(n, derived(`{"type":"bin","field":"b","extent":[0,1000]}`), "")},
		{ops: []string{"countpattern"}, rows: n, spec: pollSpec(n, derived(`{"type":"countpattern","field":"w"}`), "")},
		{ops: []string{"cross"}, rows: 150 * 150, spec: pollSpec(150, derived(`{"type":"cross"}`), "")},
		{ops: []string{"density"}, rows: n, spec: pollSpec(n, derived(`{"type":"density","distribution":{"function":"kde","from":"t","field":"b"},"steps":100}`), "")},
		{ops: []string{"dotbin"}, rows: n, spec: pollSpec(n, derived(`{"type":"dotbin","field":"b"}`), "")},
		{ops: []string{"extent"}, rows: n, spec: pollSpec(n, derived(`{"type":"extent","field":"b"}`), "")},
		{ops: []string{"flatten"}, rows: 2 * n, spec: pollSpec(n, derived(`{"type":"flatten","fields":["arr"]}`), "")},
		{ops: []string{"fold"}, rows: 2 * n, spec: pollSpec(n, derived(`{"type":"fold","fields":["a","b"]}`), "")},
		{ops: []string{"identifier"}, rows: n, spec: pollSpec(n, derived(`{"type":"identifier","as":"id"}`), "")},
		{ops: []string{"impute"}, rows: n, spec: pollSpec(n, derived(`{"type":"impute","field":"b","key":"a","groupby":["k"]}`), "")},
		{ops: []string{"joinaggregate"}, rows: n, spec: pollSpec(n, derived(`{"type":"joinaggregate","groupby":["k"],"fields":["b"],"ops":["mean"]}`), "")},
		{ops: []string{"kde"}, rows: n, spec: pollSpec(n, derived(`{"type":"kde","field":"b","steps":100}`), "")},
		{ops: []string{"kde2d"}, rows: n, spec: pollSpec(n, derived(`{"type":"kde2d","x":{"expr":"datum.a"},"y":{"expr":"datum.b / 10"},"size":[100,100]}`), "")},
		{ops: []string{"loess"}, rows: 8000, spec: pollSpec(8000, derived(`{"type":"loess","x":"a","y":"b","bandwidth":0.05}`), "")},
		{ops: []string{"lookup"}, rows: n, spec: pollSpec(n, derived(`{"type":"lookup","from":"t","key":"i","fields":["p"],"as":["parent"]}`), "")},
		{ops: []string{"pivot"}, rows: n, spec: pollSpec(n, derived(`{"type":"pivot","field":"k","value":"b","groupby":["a"]}`), "")},
		{ops: []string{"project"}, rows: n, spec: pollSpec(n, derived(`{"type":"project","fields":["a"]}`), "")},
		{ops: []string{"quantile"}, rows: n, spec: pollSpec(n, derived(`{"type":"quantile","field":"b"}`), "")},
		{ops: []string{"regression"}, rows: n, spec: pollSpec(n, derived(`{"type":"regression","x":"a","y":"b"}`), "")},
		{ops: []string{"sample"}, rows: n, spec: pollSpec(n, derived(`{"type":"sample","size":100}`), "")},
		{ops: []string{"stack"}, rows: n, spec: pollSpec(n, derived(`{"type":"stack","groupby":["a"],"field":"b"}`), "")},
		{ops: []string{"timeunit"}, rows: n, spec: pollSpec(n, derived(`{"type":"formula","as":"d","expr":"datetime(2000, 0, 1 + datum.i)"},{"type":"timeunit","field":"d","units":["year","month"]}`), "")},
		{ops: []string{"window"}, rows: n, spec: pollSpec(n, derived(`{"type":"window","ops":["row_number"],"sort":{"field":"b"},"groupby":["k"]}`), "")},
		{ops: []string{"pie"}, rows: n, spec: pollSpec(n, derived(`{"type":"pie","field":"b"}`), "")},
		{ops: []string{"voronoi"}, rows: n, spec: pollSpec(n, derived(`{"type":"voronoi","x":{"expr":"datum.i % 200"},"y":{"expr":"floor(datum.i / 200)"},"size":[200,200]}`), "")},
		{ops: []string{"contour"}, rows: n, spec: pollSpec(n, derived(`{"type":"contour","x":{"expr":"datum.a"},"y":{"expr":"datum.b / 10"},"size":[100,100]}`), "")},
		{ops: []string{"isocontour"}, rows: 128 * 128, spec: pollSpec(n, derived(`{"type":"kde2d","x":{"expr":"datum.a"},"y":{"expr":"datum.b / 10"},"size":[128,128],"as":"grid"},{"type":"isocontour","field":"grid","levels":5}`), "")},
		{ops: []string{"heatmap"}, rows: 128 * 128, spec: pollSpec(n, derived(`{"type":"kde2d","x":{"expr":"datum.a"},"y":{"expr":"datum.b / 10"},"size":[128,128],"as":"grid"}`),
			`"marks":[{"type":"image","from":{"data":"d"},"encode":{"enter":{"width":{"value":128},"height":{"value":128}}},"transform":[{"type":"heatmap","field":"datum.grid","color":"#000"}]}]`)},
		{ops: []string{"geojson"}, rows: n, spec: pollSpec(n, derived(`{"type":"geojson","fields":["lon","lat"]}`), "")},
		{ops: []string{"geopoint"}, rows: n, spec: pollSpec(n, derived(`{"type":"geopoint","projection":"proj","fields":["lon","lat"]}`), "")},
		{ops: []string{"geopath"}, rows: n, spec: pollSpec(n, derived(`{"type":"formula","as":"f","expr":"{type: 'Feature', geometry: {type: 'Point', coordinates: [datum.lon, datum.lat]}}"},{"type":"geopath","projection":"proj","field":"f"}`), "")},
		{ops: []string{"geoshape"}, rows: n, spec: pollSpec(n, derived(`{"type":"formula","as":"f","expr":"{type: 'Feature', geometry: {type: 'Point', coordinates: [datum.lon, datum.lat]}}"},{"type":"geoshape","projection":"proj","field":"f"}`), "")},
		{ops: []string{"graticule"}, rows: 100_000, spec: pollSpec(1, `{"name":"g","transform":[{"type":"graticule","step":[0.5,0.5]}]}`, "")},
		{ops: []string{"force"}, rows: 1000 * 100, spec: pollSpec(1000, derived(`{"type":"force","iterations":100,"static":true,"forces":[{"force":"collide","radius":2},{"force":"x","x":"a"}]}`), "")},
		{ops: []string{"label"}, rows: 8000, spec: pollSpec(8000, "",
			`"marks":[{"type":"text","from":{"data":"t"},"encode":{"enter":{"x":{"field":"a"},"y":{"signal":"datum.b / 5"},"text":{"field":"k"}}},"transform":[{"type":"label","size":[200,200]}]}]`)},
		{ops: []string{"wordcloud"}, rows: 2000, spec: pollSpec(2000, "",
			`"marks":[{"type":"text","from":{"data":"t"},"encode":{"enter":{"text":{"field":"k"},"fontSize":{"value":8}}},"transform":[{"type":"wordcloud","size":[200,200]}]}]`)},
		{ops: []string{"linkpath"}, rows: n, spec: pollSpec(n, derived(`{"type":"linkpath","sourceX":"a","sourceY":"b","targetX":"b","targetY":"a"}`), "")},
		{ops: []string{"nest"}, rows: n, spec: pollSpec(n, derived(`{"type":"nest","keys":["k","a"],"generate":true}`), "")},
		{ops: []string{"stratify", "tree", "treelinks"}, rows: n, spec: pollSpec(n, derived(stratify+`,{"type":"tree","size":[200,200]}`)+`,{"name":"l","source":"d","transform":[{"type":"treelinks"}]}`, "")},
		{ops: []string{"pack"}, rows: n, spec: pollSpec(n, derived(stratify+`,{"type":"pack","size":[200,200]}`), "")},
		{ops: []string{"partition"}, rows: n, spec: pollSpec(n, derived(stratify+`,{"type":"partition","size":[200,200]}`), "")},
		{ops: []string{"treemap"}, rows: n, spec: pollSpec(n, derived(stratify+`,{"type":"treemap","size":[200,200]}`), "")},
		{ops: []string{"crossfilter", "resolvefilter"}, rows: n, spec: pollSpec(n,
			`{"name":"c","source":"t","transform":[{"type":"crossfilter","signal":"xf","fields":["a","b"],"query":[[0,50],[0,500]]},{"type":"resolvefilter","ignore":1,"filter":{"signal":"xf"}}]}`, "")},
		{ops: []string{"facet"}, rows: n, spec: pollSpec(n, "",
			`"marks":[{"type":"group","from":{"facet":{"name":"f","data":"t","groupby":"k"}}}]`)},
		{ops: []string{"prefacet"}, rows: 2000 * 10, spec: pollSpec(2000, derived(`{"type":"formula","as":"ten","expr":"sequence(10)"}`),
			`"marks":[{"type":"group","from":{"facet":{"name":"f","data":"d","field":"ten"}}}]`)},
		// A copy: a step per 16 values (appendRows).
		{ops: []string{"multivalues"}, rows: 2 * n / 16, spec: pollSpec(n, "",
			`"scales":[{"name":"o","type":"quantile","domain":{"fields":[{"data":"t","field":"i"},{"data":"t","field":"b"}]},"range":["a","b","c"]}]`)},
		{ops: []string{"axisticks", "overlap"}, rows: n, spec: pollSpec(n, "",
			`"scales":[{"name":"x","type":"band","domain":{"data":"t","field":"i"},"range":"width"}],"axes":[{"orient":"bottom","scale":"x","labelOverlap":true}]`)},
		// A legend's entries are facet cells, n/2 of them.
		{ops: []string{"legendentries"}, rows: n / 2, spec: pollSpec(n/2, "",
			`"scales":[{"name":"c","type":"ordinal","domain":{"data":"t","field":"i"},"range":"category"}],"legends":[{"fill":"c","symbolLimit":0}]`)},
		{ops: []string{"bound", "datajoin", "encode", "mark"}, rows: n, spec: pollSpec(n, "",
			`"marks":[{"type":"symbol","from":{"data":"t"},"encode":{"enter":{"x":{"field":"a"},"y":{"field":"b"}}}}]`)},
		{ops: []string{"load"}, rows: n, spec: `{"signals":[{"name":"u","value":"rows.csv"}],"data":[{"name":"t","url":{"signal":"u"},"format":{"type":"csv"}}]}`, opts: Options{Loader: csvLoader(n)}},
	}
}

// operatorsWithoutRows are the operators whose work no input sizes: value
// operators that compute one value from their parameters, and operators that
// hand a pulse on without visiting its tuples. The others are exercised by
// everyOperatorCases or by TestDataflowOperatorsPollThroughout's spec.
var operatorsWithoutRows = map[string]string{
	"proxy":       "hands its parameter on",
	"field":       "makes an accessor",
	"key":         "makes a key function",
	"compare":     "makes a comparator",
	"expression":  "makes an expression function",
	"params":      "collects its parameters",
	"projection":  "configures a projection; its walks of geometry are the geo operators', bound to the call (C18)",
	"sieve":       "hands its pulse on",
	"render":      "hands its pulse on",
	"multiextent": "combines one extent per field, each made by an extent operator over the rows",
	"viewlayout":  "lays out a group's guides and children, whose number the operator limit bounds",
}

// operatorsPolledElsewhere are covered by TestDataflowOperatorsPollThroughout.
var operatorsPolledElsewhere = []string{"collect", "relay", "values", "tupleindex", "itemstore", "sortitems", "scale"}

// csvLoader serves rows.csv: n rows of two columns.
type csvLoader int

func (n csvLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("a,b\n")
	for i := range int(n) {
		fmt.Fprintf(&b, "%d,%d\n", i, i%97)
	}
	return []byte(b.String()), nil
}

func (csvLoader) Sanitize(ctx context.Context, uri string) (string, error) { return uri, nil }

// Every operator that a specification can make visit any number of rows polls
// its context while it does (ADR 0010), whatever the operator. The operators
// are enumerated from the registry (transformFactories), so a new one fails
// here until it is given a case, or a reason it visits no rows: an operator
// left out cannot hide a loop without a poll, as crossfilter, resolvefilter,
// facet, prefacet, nest, stratify and treelinks did (C21, audit 2026-10-18).
// Counted, not timed: each case's operator polls at least once per 4,096 of
// the rows it handles.
func TestEveryOperatorPollsThroughout(t *testing.T) {
	if testing.Short() {
		t.Skip("renders every operator over 20,000 rows")
	}
	covered := map[string]bool{}
	for _, op := range operatorsPolledElsewhere {
		covered[op] = true
	}
	for op := range operatorsWithoutRows {
		covered[op] = true
	}
	cases := everyOperatorCases()
	for _, c := range cases {
		for _, op := range c.ops {
			covered[op] = true
		}
	}
	var missing []string
	for op := range transformFactories {
		if !covered[op] {
			missing = append(missing, op)
		}
	}
	for op := range covered {
		if _, ok := transformFactories[op]; !ok {
			t.Errorf("%s is listed but is no operator", op)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("operators with no case and no reason to visit no rows: %v", missing)
	}
	for _, c := range cases {
		t.Run(strings.Join(c.ops, "+"), func(t *testing.T) {
			ctx := newOpPollCtx()
			ctx.render(t, c.spec, c.opts)
			min := max(1, c.rows/4096)
			for _, op := range c.ops {
				if !ctx.ran[op] {
					t.Errorf("%s did not run", op)
				} else if ctx.in[op] < min {
					t.Errorf("%s polled %d times over %d rows; want at least %d", op, ctx.in[op], c.rows, min)
				}
			}
			if worst, at := ctx.worstGap(); worst > maxWorkBetweenPolls {
				t.Errorf("%d steps of work between two polls (the polls in %s); want at most %d", worst, at, maxWorkBetweenPolls)
			}
		})
	}
}
