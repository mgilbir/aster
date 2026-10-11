package aster_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var mermaidBlock = regexp.MustCompile("(?ms)^```mermaid\n(.*?)^```")

// Every Mermaid diagram in the documentation renders: GitHub draws them in
// the reader's browser, and one that does not parse shows an error there
// instead. testdata/mermaid renders each with Mermaid's own renderer on jsdom
// (pinned there; install with npm ci). Without it the test skips, or fails
// with ASTER_ORACLE=require, as the oracle's tests do.
func TestDocsMermaid(t *testing.T) {
	type diagram struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	var diagrams []diagram
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == ".git" || strings.HasSuffix(name, "-cache") || name == "upstream-clones" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range mermaidBlock.FindAllSubmatchIndex(b, -1) {
			line := 1 + bytes.Count(b[:m[0]], []byte("\n"))
			diagrams = append(diagrams, diagram{Name: path + ":" + strconv.Itoa(line), Source: string(b[m[2]:m[3]])})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A floor, so a change to the extraction that finds nothing fails.
	if len(diagrams) < 4 {
		t.Fatalf("found %d Mermaid diagrams in the docs, expected at least 4", len(diagrams))
	}

	node, err := exec.LookPath("node")
	if err == nil {
		_, err = os.Stat("testdata/mermaid/node_modules/mermaid")
	}
	if err != nil {
		msg := "the Mermaid renderer is not installed (cd testdata/mermaid && npm ci): " + err.Error()
		if os.Getenv("ASTER_ORACLE") == "require" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	in, err := json.Marshal(diagrams)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "testdata/mermaid/render.mjs")
	cmd.Stdin = bytes.NewReader(in)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("render.mjs: %v", err)
	}
	var results []struct{ Name, Error string }
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("render.mjs output: %v", err)
	}
	if len(results) != len(diagrams) {
		t.Fatalf("rendered %d of %d diagrams", len(results), len(diagrams))
	}
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("%s: the diagram does not render: %s", r.Name, r.Error)
		}
	}
}
