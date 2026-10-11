package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunPDF exercises the pdf subcommand end to end against a bundled
// testdata spec, across every -text mode, and checks that each output is a
// PDF with the expected font treatment.
func TestRunPDF(t *testing.T) {
	sizes := map[string]int{}
	for _, tc := range []struct {
		name string
		args []string
		// substring the PDF must (or must not) contain, pinning the mode
		wants   string
		rejects string
	}{
		{name: "default", args: nil, wants: "/FontFile2"},
		{name: "embed", args: []string{"-text", "embed"}, wants: "/FontFile2"},
		{name: "named", args: []string{"-text", "named"}, wants: "/Type0", rejects: "/FontFile2"},
		{name: "outlines", args: []string{"-text", "outlines"}, rejects: "/Font"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "bar-chart.pdf")
			args := append([]string{"-i", "../../testdata/bar-chart.vl.json", "-o", out}, tc.args...)
			if err := runPDF(args); err != nil {
				t.Fatalf("runPDF: %v", err)
			}

			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading output: %v", err)
			}
			if !bytes.HasPrefix(data, []byte("%PDF-")) {
				t.Fatalf("output does not start with %%PDF-")
			}
			if tc.wants != "" && !bytes.Contains(data, []byte(tc.wants)) {
				t.Errorf("PDF missing %s", tc.wants)
			}
			if tc.rejects != "" && bytes.Contains(data, []byte(tc.rejects)) {
				t.Errorf("PDF unexpectedly contains %s", tc.rejects)
			}
			sizes[tc.name] = len(data)
		})
	}
	// The size relationship that motivates the modes.
	if sizes["named"] >= sizes["embed"] || sizes["embed"] >= sizes["outlines"] {
		t.Errorf("size ordering violated: named=%d embed=%d outlines=%d",
			sizes["named"], sizes["embed"], sizes["outlines"])
	}
}

// An unknown -text value must fail fast with a clear error, not silently
// fall back to a default (matching the CLI's -timeout validation posture).
func TestRunPDFInvalidTextMode(t *testing.T) {
	err := runPDF([]string{"-i", "../../testdata/bar-chart.vl.json", "-o", filepath.Join(t.TempDir(), "x.pdf"), "-text", "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid -text value")
	}
	if !strings.Contains(err.Error(), "invalid -text") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// -allow-http reaches the public internet only; a loopback data service needs
// -allow-private-networks as well, and without it the load fails the command.
func TestRunSVGPrivateNetworks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"a":"HELLOWORLD"}]`))
	}))
	defer ts.Close()
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.vl.json")
	if err := os.WriteFile(spec, []byte(`{"$schema":"https://vega.github.io/schema/vega-lite/v5.json","data":{"url":"`+ts.URL+`/d.json"},"mark":"text","encoding":{"text":{"field":"a"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"allow-http", []string{"-allow-http"}, false},
		{"allow-domain", []string{"-allow-domain", "127.0.0.1"}, false},
		{"allow-http and private", []string{"-allow-http", "-allow-private-networks"}, true},
		{"allow-domain and private", []string{"-allow-domain", "127.0.0.1", "-allow-private-networks"}, true},
	} {
		out := filepath.Join(dir, "out.svg")
		err := runSVG(append([]string{"-i", spec, "-o", out}, tc.args...))
		if !tc.want {
			// A blocked load fails the command, naming the block.
			if err == nil || !strings.Contains(err.Error(), "private/loopback") {
				t.Errorf("%s: err = %v, want the blocked load", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		svg, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(svg, []byte("HELLOWORLD")); got != tc.want {
			t.Errorf("%s: data loaded = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// -signal sets a signal before the render: a JSON value, or else a string.
func TestRunSVGSignal(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(spec, []byte(`{"width":100,"height":20,"signals":[{"name":"w","value":10},{"name":"label","value":"a"}],
	  "marks":[{"type":"rect","encode":{"update":{"width":{"signal":"w"},"height":{"value":20}}}},
	           {"type":"text","encode":{"update":{"text":{"signal":"label"}}}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.svg")
	if err := runSVG([]string{"-i", spec, "-o", out, "-signal", "w=50", "-signal", "label=hello world"}); err != nil {
		t.Fatal(err)
	}
	svg, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(svg, []byte(`h50v20h-50Z`)) || !bytes.Contains(svg, []byte(`hello world`)) {
		t.Errorf("the signals were not set:\n%s", svg)
	}
	if err := runSVG([]string{"-i", spec, "-o", out, "-signal", "nope=1"}); err == nil {
		t.Error("an unknown signal: no error")
	}
}

// detectSpec tells every spec of the repository's corpora apart by its
// top-level properties alone, with its $schema removed, and by its $schema.
func TestDetectSpecOnTheCorpora(t *testing.T) {
	sets := []struct {
		glob string
		want specKind
	}{
		{"../../testdata/corpus/vega/*.vg.json", specVega},
		{"../../testdata/corpus/vg-gallery/*.vg.json", specVega},
		{"../../testdata/corpus/regress/*.vg.json", specVega},
		{"../../testdata/corpus/regress/*.vl.json", specVegaLite},
		{"../../testdata/corpus/vegalite/*.vl.json", specVegaLite},
		{"../../testdata/vega-lite/v6.4.3/specs/*.vl.json", specVegaLite},
		{"../../testdata/vl-convert/*.vl.json", specVegaLite},
	}
	total := 0
	for _, set := range sets {
		files, err := filepath.Glob(set.glob)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: %d files (%v)", set.glob, len(files), err)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(b, &top); err != nil {
				continue // a regression spec that is not an object: nothing to detect
			}
			total++
			if k, err := detectSpec(b); err != nil || k != set.want {
				t.Errorf("%s: %v, %v; want %v", f, k, err, set.want)
			}
			delete(top, "$schema")
			bare, _ := json.Marshal(top)
			if k, err := detectSpec(bare); err != nil || k != set.want {
				t.Errorf("%s without $schema: %v, %v; want %v", f, k, err, set.want)
			}
		}
	}
	if total < 1400 {
		t.Errorf("only %d specs checked", total)
	}
}

func TestDetectSpecRefusesToGuess(t *testing.T) {
	for _, spec := range []string{`{}`, `{"width":10,"height":10}`, `{"mark":"bar","marks":[]}`, `[1,2]`, `{`} {
		if k, err := detectSpec([]byte(spec)); err == nil {
			t.Errorf("%s: detected %v, want an error", spec, k)
		}
	}
}

// Without a $schema a Vega-Lite spec is rendered as Vega-Lite: it used to be
// rendered as Vega, an empty 0x0 chart and exit status 0.
func TestRunSVGWithoutSchema(t *testing.T) {
	in := filepath.Join(t.TempDir(), "noschema.json")
	spec := `{"data":{"values":[{"a":"A","b":28},{"a":"B","b":55}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`
	if err := os.WriteFile(in, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.svg")
	if err := runSVG([]string{"-i", in, "-o", out}); err != nil {
		t.Fatal(err)
	}
	svg, _ := os.ReadFile(out)
	if bytes.Contains(svg, []byte(`width="0" height="0"`)) || !bytes.Contains(svg, []byte("mark-rect")) {
		t.Errorf("not a bar chart:\n%.300s", svg)
	}
	if err := runSVG([]string{"-i", in, "-o", out, "-from", "vega"}); err != nil {
		t.Fatalf("-from vega: %v", err)
	}
}

// A load the default loader refuses fails the command, with what to do; with
// -lenient the chart renders without it.
func TestRunSVGFailedLoad(t *testing.T) {
	in := filepath.Join(t.TempDir(), "remote.vl.json")
	spec := `{"$schema":"https://vega.github.io/schema/vega-lite/v6.json","data":{"url":"https://example.com/cars.json"},"mark":"point"}`
	if err := os.WriteFile(in, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.svg")
	err := runSVG([]string{"-i", in, "-o", out})
	if err == nil || !strings.Contains(err.Error(), "-allow-http") {
		t.Fatalf("err = %v, want the refused load with a hint", err)
	}
	if err := runSVG([]string{"-i", in, "-o", out, "-lenient"}); err != nil {
		t.Fatalf("-lenient: %v", err)
	}
}
