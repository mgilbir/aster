package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRunPDF exercises the pdf subcommand end to end against a bundled
// testdata spec and checks that the output is a PDF.
func TestRunPDF(t *testing.T) {
	out := filepath.Join(t.TempDir(), "bar-chart.pdf")
	if err := runPDF([]string{"-i", "../../testdata/bar-chart.vl.json", "-o", out}); err != nil {
		t.Fatalf("runPDF: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("output does not start with %%PDF-")
	}
}
