package aster_test

import (
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	// mdInline is the target of an inline link or image, `[t](url)`,
	// `[t](url "title")` or `[t](<url>)`.
	mdInline = regexp.MustCompile(`\]\(\s*(<[^>\n]*>|[^)\s]+)`)
	// mdRefDef is a reference definition, `[id]: url "title"`.
	mdRefDef = regexp.MustCompile(`(?m)^ {0,3}\[([^\]\n]+)\]:[ \t]*(<[^>\n]*>|\S+)`)
	// mdRefUse is a full reference link, `[text][id]`, or a collapsed one, `[id][]`.
	mdRefUse = regexp.MustCompile(`\[([^\]\n]*)\]\[([^\]\n]*)\]`)
	// mdHTML is the href or src of an HTML element.
	mdHTML = regexp.MustCompile(`(?i)<[a-z]+\s[^>]*?\b(?:href|src)\s*=\s*["']([^"']+)["']`)
	// mdPathSpan is a code span, which may name a path into the repository.
	mdPathSpan = regexp.MustCompile("`([^`\\s]+)`")
	// mdFileName is a bare file name, as `compare_test.go` or `Makefile`.
	mdFileName = regexp.MustCompile(`^(?:[\w.-]+\.(?:go|md|mod|sum|sh|yml|yaml|mjs|allow|txt)|Makefile)$`)
	mdHeading  = regexp.MustCompile(`(?m)^ {0,3}#+\s+(.*?)\s*#*\s*$`)
	slugStrip  = regexp.MustCompile("[`*]")
	slugDelete = regexp.MustCompile(`[^\p{L}\p{N}\p{M}_\- ]`)
)

// Every relative link in the repository's tracked Markdown resolves, to a
// tracked file or directory and, for a #fragment into a Markdown file, to one
// of its headings (as GitHub makes their anchors). Links are read as GitHub
// reads them: inline (with or without a title), reference definitions, and
// the href and src of HTML; not inside code, fenced (at any indentation) or
// inline. A reference link must have its definition.
func TestDocsLinks(t *testing.T) {
	files := trackedFiles(t, "*.md")
	tracked := map[string]bool{}
	topDirs := map[string]bool{}   // the repository's top-level directories
	baseNames := map[string]bool{} // the name of every tracked file
	for _, f := range trackedFiles(t, "*") {
		tracked[f] = true
		baseNames[path.Base(f)] = true
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			tracked[d] = true
			if path.Dir(d) == "." {
				topDirs[d] = true
			}
		}
	}
	anchors := map[string]map[string]bool{}
	headings := func(p string) map[string]bool {
		if a, ok := anchors[p]; ok {
			return a
		}
		a := map[string]bool{}
		if b, err := os.ReadFile(p); err == nil {
			prose, _ := splitMarkdown(string(b))
			a = headingAnchors(prose)
		}
		anchors[p] = a
		return a
	}
	links := 0
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		prose, _ := splitMarkdown(string(b))
		prose = stripInlineCode(prose)
		var targets []string
		for _, m := range mdInline.FindAllStringSubmatch(prose, -1) {
			targets = append(targets, m[1])
		}
		defs := map[string]bool{}
		for _, m := range mdRefDef.FindAllStringSubmatch(prose, -1) {
			defs[strings.ToLower(strings.TrimSpace(m[1]))] = true
			targets = append(targets, m[2])
		}
		for _, m := range mdRefUse.FindAllStringSubmatch(prose, -1) {
			id := m[2]
			if id == "" {
				id = m[1]
			}
			if !defs[strings.ToLower(strings.TrimSpace(id))] {
				t.Errorf("%s: reference link %s has no definition", p, m[0])
			}
		}
		for _, m := range mdHTML.FindAllStringSubmatch(prose, -1) {
			targets = append(targets, m[1])
		}
		for _, link := range targets {
			links++
			link = strings.TrimSuffix(strings.TrimPrefix(link, "<"), ">")
			if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
				continue
			}
			target, frag, _ := strings.Cut(link, "#")
			if target == "" {
				target = p
			} else {
				target = path.Clean(path.Join(path.Dir(p), target))
			}
			if !tracked[target] {
				t.Errorf("%s: link %s: %s is not a tracked file or directory", p, link, target)
				continue
			}
			if frag != "" && strings.HasSuffix(target, ".md") && !headings(target)[frag] {
				t.Errorf("%s: link %s: no heading %q in %s", p, link, frag, target)
			}
		}
	}
	// A repository path named in code (`testdata/security/x.vg.json`,
	// `.github/workflows/ci.yml`, `compare_test.go`, `Makefile`) is a
	// reference too: it must name a tracked file or directory, so that a file
	// moved or deleted does not leave the docs pointing at nothing. A span is
	// a path when it starts with one of the repository's top-level
	// directories (or a directory beside the document); a bare file name must
	// be the name of a tracked file.
	paths := 0
	for _, p := range files {
		// An audit or a history records the tree it read, as it was; only
		// its reproducers, which the tests still render, must still be there.
		record := strings.HasPrefix(p, "docs/audits/") || strings.HasPrefix(p, "docs/history/")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		prose, _ := splitMarkdown(string(b))
		for _, m := range mdPathSpan.FindAllStringSubmatch(prose, -1) {
			rp := strings.TrimSuffix(strings.TrimRight(m[1], ".,:;"), "/")
			if strings.ContainsAny(rp, "*<>{}$ ") || strings.Contains(rp, "...") || strings.Contains(rp, "…") {
				continue // a pattern or a placeholder, not a path
			}
			rp, _, _ = strings.Cut(rp, ":") // file.go:123
			if !strings.Contains(rp, "/") {
				if !mdFileName.MatchString(rp) || record {
					continue // not a file name
				}
				paths++
				if !baseNames[rp] {
					t.Errorf("%s: names %s, which is not the name of a tracked file", p, rp)
				}
				continue
			}
			first, _, _ := strings.Cut(rp, "/")
			if first == "" || first == "." || first == ".." || !topDirs[first] && !tracked[path.Join(path.Dir(p), first)] {
				continue // not a path into the repository (github.com/…, data/cars.json)
			}
			if i := strings.LastIndex(rp, "."); i > strings.LastIndex(rp, "/") && i+1 < len(rp) && rp[i+1] >= 'A' && rp[i+1] <= 'Z' {
				rp = rp[:i] // internal/budget.Ledger: the package's directory
			}
			if record && !strings.HasPrefix(rp, "testdata/security/") {
				continue
			}
			paths++
			if !tracked[rp] && !tracked[path.Clean(path.Join(path.Dir(p), rp))] && !gitIgnored(rp) {
				t.Errorf("%s: names %s, which is not a tracked file or directory", p, rp)
			}
		}
	}
	if paths < 50 {
		t.Errorf("checked %d repository paths named in the docs, expected at least 50", paths)
	}
	// Floors, so that a walk or an extraction that finds nothing fails.
	if len(files) < 30 || links < 150 {
		t.Errorf("checked %d links in %d Markdown files, expected at least 150 in 30", links, len(files))
	}
}

// slug is GitHub's anchor for a heading (github-slugger): lower case, the
// code and emphasis markup dropped, punctuation but '-' and '_' dropped,
// spaces as hyphens.
func slug(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = slugStrip.ReplaceAllString(h, "")
	h = slugDelete.ReplaceAllString(h, "")
	return strings.ReplaceAll(h, " ", "-")
}

// headingAnchors are the anchors GitHub gives the headings of a document's
// prose: a slug already taken gets the first free suffix -1, -2, ..., as
// github-slugger's occurrences count does.
func headingAnchors(prose string) map[string]bool {
	anchors := map[string]bool{}
	occurrences := map[string]int{}
	for _, m := range mdHeading.FindAllStringSubmatch(prose, -1) {
		s := slug(m[1])
		orig := s
		for anchors[s] {
			occurrences[orig]++
			s = orig + "-" + strconv.Itoa(occurrences[orig])
		}
		anchors[s] = true
	}
	return anchors
}

// fence is a fenced code block of a Markdown document.
type fence struct {
	info string // the info string: "mermaid", "go", ...
	body string // its lines, the fence's indentation removed
	line int    // the line of its opening fence, from 1
}

// splitMarkdown separates a Markdown document into its prose, with every
// fenced code block blanked (the line count kept), and the blocks. A fence is
// three or more backticks or tildes at any indentation (a block in a list
// item is indented), closed by at least as many of the same character.
func splitMarkdown(doc string) (prose string, fences []fence) {
	lines := strings.Split(doc, "\n")
	var out strings.Builder
	var cur *fence
	var marker string
	var indent int
	var body []string
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " \t")
		if cur == nil {
			if m := fenceMarker(trimmed); m != "" && !(m[0] == '`' && strings.Contains(trimmed[len(m):], "`")) {
				cur = &fence{info: strings.Fields(trimmed[len(m):] + " x")[0], line: i + 1}
				if strings.TrimSpace(trimmed[len(m):]) == "" {
					cur.info = ""
				}
				marker, indent, body = m, len(l)-len(trimmed), nil
				out.WriteString("\n")
				continue
			}
			out.WriteString(l)
			out.WriteString("\n")
			continue
		}
		if m := fenceMarker(trimmed); m != "" && m[0] == marker[0] && len(m) >= len(marker) && strings.TrimSpace(trimmed[len(m):]) == "" {
			cur.body = strings.Join(body, "\n")
			if len(body) > 0 {
				cur.body += "\n"
			}
			fences = append(fences, *cur)
			cur = nil
			out.WriteString("\n")
			continue
		}
		// The fence's indentation is removed from its lines, as CommonMark does.
		n := 0
		for n < indent && n < len(l) && l[n] == ' ' {
			n++
		}
		body = append(body, l[n:])
		out.WriteString("\n")
	}
	return out.String(), fences
}

// fenceMarker is the run of three or more backticks or tildes s starts with.
func fenceMarker(s string) string {
	if len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return ""
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return s[:n]
}

var inlineCode = regexp.MustCompile("(`+)[^`]*?(`+)")

// stripInlineCode blanks the code spans of prose.
func stripInlineCode(prose string) string {
	return inlineCode.ReplaceAllStringFunc(prose, func(s string) string { return strings.Repeat(" ", len(s)) })
}

// gitIgnored reports whether path is a place .gitignore keeps out of the
// repository (a cache the tests make), which the docs may name.
func gitIgnored(p string) bool {
	// .gitignore's own lines first: check-ignore refuses a path through a
	// symbolic link, which a cache shared between checkouts may be.
	if b, err := os.ReadFile(".gitignore"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimPrefix(strings.TrimSpace(l), "/"); l == p || l == p+"/" {
				return true
			}
		}
	}
	for _, q := range []string{p, p + "/x"} { // a directory pattern matches what is under it
		if exec.Command("git", "check-ignore", "-q", "--no-index", "--", q).Run() == nil {
			return true
		}
	}
	return false
}

// docs/adr/README.md indexes every decision record: an ADR added without its
// row is one nobody finds.
func TestADRIndexComplete(t *testing.T) {
	idx, err := os.ReadFile("docs/adr/README.md")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range trackedFiles(t, "docs/adr/[0-9]*.md") {
		n++
		if name := path.Base(f); !strings.Contains(string(idx), "]("+name+")") {
			t.Errorf("docs/adr/README.md does not list %s", name)
		}
	}
	if n < 12 {
		t.Errorf("found %d ADRs, expected at least 12", n)
	}
}
