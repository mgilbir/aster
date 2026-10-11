package aster_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	mdLink     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	mdFence    = regexp.MustCompile("(?ms)^```.*?^```")
	mdHeading  = regexp.MustCompile(`(?m)^#+\s+(.*)$`)
	slugStrip  = regexp.MustCompile("[`*_]")
	slugDelete = regexp.MustCompile(`[^\p{L}\p{N}_\- ]`)
)

// Every relative link in the repository's Markdown resolves, to a file that
// exists and, for a #fragment into a Markdown file, to one of its headings
// (as GitHub makes their anchors).
func TestDocsLinks(t *testing.T) {
	anchors := map[string]map[string]bool{}
	headings := func(path string) map[string]bool {
		if a, ok := anchors[path]; ok {
			return a
		}
		a := map[string]bool{}
		if b, err := os.ReadFile(path); err == nil {
			for _, m := range mdHeading.FindAllStringSubmatch(mdFence.ReplaceAllString(string(b), ""), -1) {
				a[slug(m[1])] = true
			}
		}
		anchors[path] = a
		return a
	}
	files := 0
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
		files++
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range mdLink.FindAllStringSubmatch(mdFence.ReplaceAllString(string(b), ""), -1) {
			link := m[1]
			if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
				continue
			}
			target, frag, _ := strings.Cut(link, "#")
			if target == "" {
				target = path
			} else {
				target = filepath.Join(filepath.Dir(path), target)
			}
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s: link %s: %v", path, link, err)
				continue
			}
			if frag != "" && strings.HasSuffix(target, ".md") && !headings(target)[frag] {
				t.Errorf("%s: link %s: no heading %q in %s", path, link, frag, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 30 {
		t.Errorf("checked %d Markdown files, expected at least 30", files)
	}
}

// slug is GitHub's anchor for a heading: lower case, markup and punctuation
// dropped, spaces as hyphens.
func slug(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = slugStrip.ReplaceAllString(h, "")
	h = slugDelete.ReplaceAllString(h, "")
	return strings.ReplaceAll(h, " ", "-")
}
