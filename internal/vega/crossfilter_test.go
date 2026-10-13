package vega

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/svg"
)

// A crossfilter hands on the indices of its tuples, not the tuples, which stay
// the pulse's source: what each operator after it reads (what the pulse adds,
// or its source), writes (a field into a number is a TypeError), keeps (a
// sieve's raw source, data()) and draws is upstream's. Each spec of
// testdata/crossfilter is compared with what node records for it
// (testdata/crossfilter.node.json: every data set's tuples, the first error
// logged, the texts drawn), the transforms that follow a crossfilter in its
// own data set and those of data sets derived from it through resolvefilter.
func TestCrossfilterPulsesAreUpstreams(t *testing.T) {
	b, err := os.ReadFile("testdata/crossfilter.node.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]struct {
		Data  map[string]string
		Error string
		Texts []string
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) < 35 {
		t.Fatalf("%d cases recorded", len(want))
	}
	text := regexp.MustCompile(`<text[^>]*>([^<]*)</text>`)
	inExpr := regexp.MustCompile(` \(in expression .*\)$`)
	for name, w := range want {
		t.Run(name, func(t *testing.T) {
			spec := mustJSON(t, readFile(t, "testdata/crossfilter/"+name))
			var msgs []string // the errors reported, warnings left out
			report := func(err error) error {
				if _, w := err.(Warning); !w {
					msgs = append(msgs, err.Error())
				}
				return nil
			}
			res, err := Render(context.Background(), spec, Options{Report: report})
			if err != nil {
				t.Fatal(err)
			}
			first := ""
			if len(msgs) > 0 {
				// node logs the message alone
				first = inExpr.ReplaceAllString(msgs[0], "")
				if i := strings.Index(first, ": "); i >= 0 && strings.HasSuffix(first[:i], "Error") {
					first = first[i+2:]
				}
			}
			if first != w.Error {
				t.Errorf("first error %q, want %q (reported %q)", first, w.Error, msgs)
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
			msgs = nil
			v := reportingView(t, spec, report)
			for d, wj := range w.Data {
				got, _ := v.root.Data(d)
				g := string(jsval.AppendJSON(nil, got))
				// a kde2d grid's values are a typed array upstream, which
				// JSON.stringify writes as an object
				if g != wj && !(strings.HasPrefix(name, "then-kde2d") && strings.ReplaceAll(g, `"values":[]`, `"values":{}`) == wj) {
					t.Errorf("data %s:\n got %.300s\nwant %.300s", d, g, wj)
				}
			}
		})
	}
}

// reportingView is debugView with a Report that goes on past every error.
func reportingView(t testing.TB, spec jsval.Value, report func(error) error) *runView {
	t.Helper()
	config := mergeConfig(defaultConfig(), jsval.Undefined, spec.Get("config"))
	scope := newScope(config, &parseOptions{})
	parseView(spec, scope)
	flow := scope.toRuntime()
	v := newView(context.Background(), Options{Report: report}, scope.locale)
	v.build(flow)
	if err := v.run(); err != nil {
		t.Fatal(err)
	}
	return v
}
