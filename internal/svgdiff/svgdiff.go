// Package svgdiff compares two SVG documents structurally: the element trees
// must match name for name and attribute for attribute, and numbers embedded
// in attribute values and text are compared within a tolerance so that
// last-digit floating point noise is not reported as a difference.
//
// It exists to measure how close the pure-Go engine's output is to upstream
// Vega's, so it reports the first difference with enough context to find it.
package svgdiff

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Options tunes the comparison.
type Options struct {
	// Abs and Rel bound how far two numbers may differ: |a-b| <= Abs or
	// |a-b| <= Rel*max(|a|,|b|).
	Abs, Rel float64
	// IgnoreAttrs lists attribute names skipped on every element.
	IgnoreAttrs []string
}

// DefaultOptions tolerates differences below a hundredth of a pixel.
var DefaultOptions = Options{Abs: 0.01, Rel: 1e-6}

// Result describes the outcome of a comparison.
type Result struct {
	Identical bool   // byte-for-byte equal
	Equal     bool   // structurally equal within tolerance
	Diff      string // first difference, empty when Equal
	// Elements and DiffCount count the compared elements and the number of
	// differing element/attribute sites (capped).
	Elements  int
	DiffCount int
}

type node struct {
	name     string
	attrs    map[string]string
	text     string
	children []*node
}

// Compare compares got against want.
func Compare(got, want []byte, opts Options) (Result, error) {
	if bytes.Equal(got, want) {
		return Result{Identical: true, Equal: true}, nil
	}
	g, err := parse(got)
	if err != nil {
		return Result{}, fmt.Errorf("svgdiff: parsing got: %w", err)
	}
	w, err := parse(want)
	if err != nil {
		return Result{}, fmt.Errorf("svgdiff: parsing want: %w", err)
	}
	c := comparer{opts: opts, ignore: map[string]bool{}}
	for _, a := range opts.IgnoreAttrs {
		c.ignore[a] = true
	}
	c.node(g, w, "")
	return Result{Equal: c.count == 0, Diff: c.first, Elements: c.elements, DiffCount: c.count}, nil
}

// maxDepth bounds element nesting in a compared document: the comparison
// recurses once per level, and the path it reports grows with the depth.
// Vega's SVG nests under 20 levels.
const maxDepth = 512

func parse(doc []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = true
	root := &node{name: "#document"}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: qname(t.Name), attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				n.attrs[qname(a.Name)] = a.Value
			}
			top := stack[len(stack)-1]
			top.children = append(top.children, n)
			stack = append(stack, n)
			if len(stack) > maxDepth+1 { // the document node is stack[0]
				return nil, fmt.Errorf("elements nest more than %d levels", maxDepth)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top := stack[len(stack)-1]
			top.text += string(t)
		}
	}
	return root, nil
}

func qname(n xml.Name) string {
	switch n.Space {
	case "":
		return n.Local
	case "http://www.w3.org/1999/xlink", "xlink":
		return "xlink:" + n.Local
	case "http://www.w3.org/2000/svg":
		return n.Local
	case "xmlns":
		return "xmlns:" + n.Local
	}
	return n.Space + ":" + n.Local
}

type comparer struct {
	opts     Options
	ignore   map[string]bool
	first    string
	count    int
	elements int
}

func (c *comparer) report(path, format string, args ...any) {
	c.count++
	if c.first == "" {
		c.first = path + ": " + fmt.Sprintf(format, args...)
	}
}

func (c *comparer) node(g, w *node, path string) {
	c.elements++
	here := path + "/" + w.name
	if cls := w.attrs["class"]; cls != "" {
		here += "." + strings.ReplaceAll(cls, " ", ".")
	}
	if g.name != w.name {
		c.report(path, "element <%s>, want <%s>", g.name, w.name)
		return
	}
	keys := make([]string, 0, len(w.attrs)+len(g.attrs))
	for k := range w.attrs {
		keys = append(keys, k)
	}
	for k := range g.attrs {
		if _, ok := w.attrs[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if c.ignore[k] {
			continue
		}
		gv, gok := g.attrs[k]
		wv, wok := w.attrs[k]
		switch {
		case !gok:
			c.report(here, "missing attribute %s=%q", k, elide(wv))
		case !wok:
			c.report(here, "unexpected attribute %s=%q", k, elide(gv))
		case !c.valuesEqual(gv, wv):
			c.report(here, "attribute %s=%q, want %q", k, elide(gv), elide(wv))
		}
	}
	if !c.valuesEqual(strings.TrimSpace(g.text), strings.TrimSpace(w.text)) {
		c.report(here, "text %q, want %q", g.text, w.text)
	}
	n := min(len(g.children), len(w.children))
	for i := 0; i < n; i++ {
		c.node(g.children[i], w.children[i], fmt.Sprintf("%s[%d]", here, i))
	}
	if len(g.children) != len(w.children) {
		c.report(here, "%d children, want %d", len(g.children), len(w.children))
	}
}

// elide shortens a long attribute value (an embedded image) for a report.
func elide(s string) string {
	if len(s) <= 160 {
		return s
	}
	return s[:120] + "..." + s[len(s)-20:]
}

const pngDataPrefix = "data:image/png;base64,"

// valuesEqual compares two attribute values token by token, numbers within
// tolerance and everything else exactly. Two embedded PNG images compare by
// their pixels (see samePNG).
func (c *comparer) valuesEqual(a, b string) bool {
	if a == b {
		return true
	}
	if strings.HasPrefix(a, pngDataPrefix) && strings.HasPrefix(b, pngDataPrefix) {
		return samePNG(a[len(pngDataPrefix):], b[len(pngDataPrefix):])
	}
	ta, tb := tokenize(a), tokenize(b)
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		x, y := ta[i], tb[i]
		if x.num && y.num {
			if !c.close(x.val, y.val) {
				return false
			}
			continue
		}
		if x.num != y.num || x.text != y.text {
			return false
		}
	}
	return true
}

func (c *comparer) close(a, b float64) bool {
	if a == b {
		return true
	}
	d := math.Abs(a - b)
	return d <= c.opts.Abs || d <= c.opts.Rel*math.Max(math.Abs(a), math.Abs(b))
}

type token struct {
	num  bool
	val  float64
	text string
}

// tokenize splits s into numbers and the text between them. A run of only
// separators (spaces and commas) is dropped, so "1,2" and "1 2" compare
// equal, as they do in path data and lists. Other text keeps its commas;
// its whitespace is trimmed and each run of it collapses to one space, as
// SVG collapses whitespace when it renders text.
func tokenize(s string) []token {
	var out []token
	i := 0
	for i < len(s) {
		if j := scanNumber(s, i); j > i {
			if f, err := strconv.ParseFloat(s[i:j], 64); err == nil {
				out = append(out, token{num: true, val: f})
				i = j
				continue
			}
		}
		j := i + 1
		for j < len(s) && scanNumber(s, j) == j {
			j++
		}
		if text := collapse(s[i:j]); text != "" {
			out = append(out, token{text: text})
		}
		i = j
	}
	return out
}

// collapse trims whitespace and collapses each inner run of it to one
// space; a run of nothing but separators is empty.
func collapse(s string) string {
	if strings.Trim(s, " ,\n\t\r") == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}

// scanNumber returns the end of a number starting at i, or i if none.
func scanNumber(s string, i int) int {
	j := i
	if j < len(s) && (s[j] == '-' || s[j] == '+') {
		j++
	}
	digits := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
		digits++
	}
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			digits++
		}
	}
	if digits == 0 {
		return i
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '-' || s[k] == '+') {
			k++
		}
		if k < len(s) && s[k] >= '0' && s[k] <= '9' {
			for k < len(s) && s[k] >= '0' && s[k] <= '9' {
				k++
			}
			j = k
		}
	}
	return j
}
