package aster_test

import (
	"os"
	"path"
	"regexp"
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
	mdHTML     = regexp.MustCompile(`(?i)<[a-z]+\s[^>]*?\b(?:href|src)\s*=\s*["']([^"']+)["']`)
	mdHeading  = regexp.MustCompile(`(?m)^ {0,3}#+\s+(.*?)\s*#*\s*$`)
	slugStrip  = regexp.MustCompile("[`*_]")
	slugDelete = regexp.MustCompile(`[^\p{L}\p{N}_\- ]`)
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
	for _, f := range trackedFiles(t, "*") {
		tracked[f] = true
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			tracked[d] = true
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
			for _, m := range mdHeading.FindAllStringSubmatch(prose, -1) {
				a[slug(m[1])] = true
			}
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
	// Floors, so that a walk or an extraction that finds nothing fails.
	if len(files) < 30 || links < 150 {
		t.Errorf("checked %d links in %d Markdown files, expected at least 150 in 30", links, len(files))
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
