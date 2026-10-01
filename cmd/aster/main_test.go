package main

import (
	"bytes"
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
// -allow-private-networks as well.
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
		if err := runSVG(append([]string{"-i", spec, "-o", out}, tc.args...)); err != nil {
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
