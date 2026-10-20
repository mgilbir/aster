package aster_test

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The oracle setup CONTRIBUTING.md and docs/testing.md give before
// `ASTER_ORACLE=require go test ./...` is every step scripts/check.sh takes
// before its own full oracle suite, so the command passes on a fresh clone
// (C60, audit 2026-10-18: the upstream vectors and the external corpora were
// missing, and their tests failed).
func TestOracleSetupDocsMatchCheck(t *testing.T) {
	check := readDoc(t, "scripts/check.sh")
	full := strings.Index(check, `gate "full suite (oracle required)"`)
	if full < 0 {
		t.Fatal("scripts/check.sh has no full oracle suite gate; reword the test with the script")
	}
	var steps []string
	for _, m := range regexp.MustCompile(`\(cd (testdata/[\w-]+) && npm ci`).FindAllStringSubmatch(check[:full], -1) {
		steps = append(steps, "cd "+m[1]+" && npm ci")
	}
	for _, m := range regexp.MustCompile(`(?m)^\s*gate "[^"]*" (scripts/[\w-]+\.sh)\s*$`).FindAllStringSubmatch(check[:full], -1) {
		switch m[1] {
		case "scripts/fmacheck.sh":
			continue // a gate, not a setup step
		}
		steps = append(steps, m[1])
	}
	if len(steps) < 5 {
		t.Fatalf("found %d setup steps in scripts/check.sh, expected the 3 npm installs and 2 scripts: %q", len(steps), steps)
	}
	for _, doc := range []string{"CONTRIBUTING.md", "docs/testing.md"} {
		_, fences := splitMarkdown(readDoc(t, doc))
		found := false
		for _, f := range fences {
			i := strings.Index(f.body, "ASTER_ORACLE=require go test ./...")
			if i < 0 {
				continue
			}
			found = true
			for _, s := range steps {
				if !strings.Contains(f.body[:i], s) {
					t.Errorf("%s: the setup before ASTER_ORACLE=require go test ./... lacks %q, which scripts/check.sh runs first", doc, s)
				}
			}
		}
		if !found {
			t.Errorf("%s gives no ASTER_ORACLE=require go test ./... command", doc)
		}
	}
}

// Every base URL the docs give for the Vega-Lite examples' data resolves
// their `data/cars.json` to a file vega-datasets serves (C62, audit
// 2026-10-18: loading-data.md's ".../data/" base made ".../data/data/cars.json",
// a 404, where cli.md and troubleshooting.md used the package root).
func TestDocsBaseURLsServeTheExamplesData(t *testing.T) {
	base := regexp.MustCompile("(?:BaseURL:\\s*\"|-base-url\\s+)(https?://[^\"\\s`)]+)")
	patterns := datasetURLs()
	n := 0
	for _, doc := range trackedFiles(t, "*.md") {
		if strings.HasPrefix(doc, "docs/audits/") || strings.HasPrefix(doc, "docs/history/") {
			continue
		}
		for _, m := range base.FindAllStringSubmatch(readDoc(t, doc), -1) {
			if !strings.Contains(m[1], "vega-datasets") {
				continue
			}
			n++
			b, err := url.Parse(m[1])
			if err != nil {
				t.Errorf("%s: base URL %s: %v", doc, m[1], err)
				continue
			}
			resolved := b.ResolveReference(&url.URL{Path: "data/cars.json"}).String()
			ok := false
			for _, re := range patterns {
				if s := re.FindStringSubmatch(resolved); s != nil {
					_, err := os.Stat(filepath.Join("testdata/vega-datasets", filepath.FromSlash(s[1])))
					ok = err == nil
					break
				}
			}
			if !ok {
				t.Errorf("%s: with the base URL %s, the examples' data/cars.json is %s, which vega-datasets does not serve", doc, m[1], resolved)
			}
		}
	}
	if n < 3 {
		t.Errorf("found %d vega-datasets base URLs in the docs, expected at least 3", n)
	}
}
