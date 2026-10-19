package svgdiff_test

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// The comparator is what every comparison with upstream rests on, so it is
// tested the way a test suite is mutation-tested: take real engine output,
// change one thing in it, and require svgdiff to report the change. A kind of
// change it cannot see is a blind spot in every comparison built on it. The
// changes it deliberately does not see (equivalences that render the same)
// are asserted both ways in TestEquivalences, so the list of them is exact.

// tolerance is the corpus comparison's (compare_test.go).
var tolerance = svgdiff.Options{Abs: 0.5, Rel: 1e-6}

// elem is a minimal ordered XML tree: svgdiff's own parse is unordered, and a
// mutation has to be serialized back.
type elem struct {
	name     xml.Name
	attrs    []xml.Attr
	text     string // character data before the first child
	children []*elem
}

func parseTree(t testing.TB, doc string) *elem {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	var stack []*elem
	var root *elem
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			e := &elem{name: tk.Name, attrs: append([]xml.Attr(nil), tk.Attr...)}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, e)
			} else {
				root = e
			}
			stack = append(stack, e)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 && len(stack[len(stack)-1].children) == 0 {
				stack[len(stack)-1].text += string(tk)
			}
		}
	}
	return root
}

func (e *elem) clone() *elem {
	c := &elem{name: e.name, attrs: append([]xml.Attr(nil), e.attrs...), text: e.text}
	for _, ch := range e.children {
		c.children = append(c.children, ch.clone())
	}
	return c
}

func qn(n xml.Name) string {
	switch n.Space {
	case "", "http://www.w3.org/2000/svg":
		return n.Local
	case "http://www.w3.org/1999/xlink":
		return "xlink:" + n.Local
	case "xmlns":
		return "xmlns:" + n.Local
	}
	return n.Local
}

func (e *elem) write(b *bytes.Buffer) {
	b.WriteString("<" + qn(e.name))
	for _, a := range e.attrs {
		b.WriteString(" " + qn(a.Name) + `="`)
		xml.EscapeText(b, []byte(a.Value))
		b.WriteString(`"`)
	}
	b.WriteString(">")
	xml.EscapeText(b, []byte(e.text))
	for _, c := range e.children {
		c.write(b)
	}
	b.WriteString("</" + qn(e.name) + ">")
}

func (e *elem) String() string {
	var b bytes.Buffer
	e.write(&b)
	return b.String()
}

// walk visits every element with a path of child indexes.
func (e *elem) walk(path []int, f func(*elem, []int)) {
	f(e, path)
	for i, c := range e.children {
		e.children[i].walk(append(append([]int(nil), path...), i), f)
		_ = c
	}
}

func (e *elem) at(path []int) *elem {
	for _, i := range path {
		e = e.children[i]
	}
	return e
}

// mutation names a change and applies it to a clone of the document.
type mutation struct {
	kind  string // what was changed, for the coverage table
	apply func(root *elem)
}

// firstNumber finds the first number in s.
func firstNumber(s string) (start, end int, v float64, ok bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '.' || (c >= '0' && c <= '9') {
			j := i
			if s[j] == '-' {
				j++
			}
			digits := 0
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				if s[j] != '.' {
					digits++
				}
				j++
			}
			if digits == 0 {
				continue
			}
			f, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				continue
			}
			return i, j, f, true
		}
	}
	return 0, 0, 0, false
}

// notPixelAttr reports whether the numbers of attribute name of an element
// tag are not pixels in what the engine writes (SVG 1.1 §13.2, §13.3): a
// gradient's coordinates are in bounding-box units, and so is everything in
// the content of a pattern Vega draws under viewBox="0,0,1,1"
// (inPatternContent). Written here independently of svgdiff's own rule, so
// the mutation test checks it rather than repeats it.
func notPixelAttr(tag, name string, inPatternContent bool) bool {
	if inPatternContent {
		return svgdiff.IsGeometry(name)
	}
	switch tag {
	case "linearGradient":
		return name == "x1" || name == "x2" || name == "y1" || name == "y2"
	case "radialGradient":
		return name == "cx" || name == "cy" || name == "r" || name == "fx" || name == "fy"
	case "pattern":
		return name == "viewBox"
	}
	return false
}

// transformArg finds argument i of the first call of fn in a transform list,
// as firstNumber does a number.
func transformArg(s, fn string, i int) (start, end int, v float64, ok bool) {
	k := strings.Index(s, fn+"(")
	if k < 0 {
		return 0, 0, 0, false
	}
	at := k + len(fn) + 1
	for n := 0; ; n++ {
		st, en, val, found := firstNumber(s[at:])
		if !found || strings.Contains(s[at:at+st], ")") {
			return 0, 0, 0, false
		}
		if n == i {
			return at + st, at + en, val, true
		}
		at += en
	}
}

// mutationsOf lists the single changes to the element at path that must be
// reported: every attribute changed and removed, its text changed, an
// attribute added, the element removed and duplicated, and two differing
// siblings swapped. inPatternContent says the element is drawn in a
// pattern's unit-box content.
func mutationsOf(e *elem, path []int, parent *elem, inPatternContent bool) []mutation {
	var ms []mutation
	tag := qn(e.name)
	for ai, a := range e.attrs {
		an := tag + "@" + qn(a.Name)
		ai := ai
		// A number that is not a pixel must not differ by what the pixel
		// tolerance allows (C52): a gradient running to 60% instead of
		// 100%, a label turned by a quarter of a degree.
		if notPixelAttr(tag, qn(a.Name), inPatternContent) {
			if s, en, v, ok := firstNumber(a.Value); ok {
				nudged := a.Value[:s] + strconv.FormatFloat(v+0.25, 'g', -1, 64) + a.Value[en:]
				ms = append(ms, mutation{an + " not a pixel: number+0.25", func(r *elem) { r.at(path).attrs[ai].Value = nudged }})
			}
		}
		if qn(a.Name) == "transform" {
			for _, arg := range []struct {
				fn   string
				i    int
				what string
			}{{"rotate", 0, "angle"}, {"scale", 0, "x factor"}, {"scale", 1, "y factor"}} {
				if s, en, v, ok := transformArg(a.Value, arg.fn, arg.i); ok {
					nudged := a.Value[:s] + strconv.FormatFloat(v+0.25, 'g', -1, 64) + a.Value[en:]
					ms = append(ms, mutation{an + " " + arg.fn + " " + arg.what + "+0.25", func(r *elem) { r.at(path).attrs[ai].Value = nudged }})
				}
			}
		}
		if s, en, v, ok := firstNumber(a.Value); ok {
			bumped := a.Value[:s] + strconv.FormatFloat(v+1, 'g', -1, 64) + a.Value[en:]
			ms = append(ms, mutation{an + " number+1", func(r *elem) { r.at(path).attrs[ai].Value = bumped }})
			nan := a.Value[:s] + "NaN" + a.Value[en:]
			ms = append(ms, mutation{an + " number→NaN", func(r *elem) { r.at(path).attrs[ai].Value = nan }})
			if !svgdiff.IsGeometry(qn(a.Name)) {
				// Within the geometry tolerance: only a geometry attribute
				// may differ by it (C66).
				nudged := a.Value[:s] + strconv.FormatFloat(v+0.25, 'g', -1, 64) + a.Value[en:]
				ms = append(ms, mutation{an + " number+0.25", func(r *elem) { r.at(path).attrs[ai].Value = nudged }})
			}
		}
		changed := a.Value + "x"
		ms = append(ms, mutation{an + " changed", func(r *elem) { r.at(path).attrs[ai].Value = changed }})
		ms = append(ms, mutation{an + " removed", func(r *elem) {
			x := r.at(path)
			x.attrs = append(x.attrs[:ai:ai], x.attrs[ai+1:]...)
		}})
	}
	ms = append(ms, mutation{tag + " attribute added", func(r *elem) {
		x := r.at(path)
		x.attrs = append(x.attrs, xml.Attr{Name: xml.Name{Local: "data-mutant"}, Value: "1"})
	}})
	if strings.TrimSpace(e.text) != "" {
		changed := e.text + "x"
		ms = append(ms, mutation{tag + " text changed", func(r *elem) { r.at(path).text = changed }})
		if _, _, v, ok := firstNumber(e.text); ok {
			s, en, _, _ := firstNumber(e.text)
			bumped := e.text[:s] + strconv.FormatFloat(v+1, 'g', -1, 64) + e.text[en:]
			ms = append(ms, mutation{tag + " text number+1", func(r *elem) { r.at(path).text = bumped }})
			nudged := e.text[:s] + strconv.FormatFloat(v+0.25, 'g', -1, 64) + e.text[en:]
			ms = append(ms, mutation{tag + " text number+0.25", func(r *elem) { r.at(path).text = nudged }})
			spaced := " " + e.text
			ms = append(ms, mutation{tag + " text leading space", func(r *elem) { r.at(path).text = spaced }})
		}
	}
	if parent != nil && len(path) > 0 {
		pp, i := path[:len(path)-1], path[len(path)-1]
		ms = append(ms, mutation{tag + " removed", func(r *elem) {
			p := r.at(pp)
			p.children = append(p.children[:i:i], p.children[i+1:]...)
		}})
		ms = append(ms, mutation{tag + " duplicated", func(r *elem) {
			p := r.at(pp)
			p.children = append(p.children[:i+1:i+1], append([]*elem{p.children[i].clone()}, p.children[i+1:]...)...)
		}})
		if i+1 < len(parent.children) && parent.children[i+1].String() != e.String() {
			ms = append(ms, mutation{tag + " swapped with next sibling", func(r *elem) {
				p := r.at(pp)
				p.children[i], p.children[i+1] = p.children[i+1], p.children[i]
			}})
		}
	}
	return ms
}

// engineCorpus renders the Vega-Lite examples and the Vega gallery with the
// engine: real output, every element and attribute kind it writes.
func engineCorpus(t testing.TB) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	c, err := aster.New(aster.WithLoader(&aster.FileLoader{BaseDir: filepath.Join(root, "testdata", "vega-datasets")}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out []string
	for _, g := range []struct {
		glob string
		lite bool
	}{
		{filepath.Join(root, "testdata", "vega-lite", "v6.4.3", "specs", "*.vl.json"), true},
		{filepath.Join(root, "testdata", "corpus", "vg-gallery", "*.vg.json"), false},
	} {
		files, _ := filepath.Glob(g.glob)
		sort.Strings(files)
		for _, f := range files {
			spec, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var svg string
			if g.lite {
				svg, err = c.VegaLiteToSVG(spec)
			} else {
				svg, err = c.VegaToSVG(spec)
			}
			if err == nil {
				out = append(out, svg)
			}
		}
	}
	if len(out) < 600 {
		t.Fatalf("only %d corpus renders", len(out))
	}
	return out
}

// TestMutationsAreReported applies, for every (element, attribute) kind the
// engine writes, each single mutation at up to perKind places across the
// corpus, and requires every one to be reported.
func TestMutationsAreReported(t *testing.T) {
	const perKind = 3
	docs := engineCorpus(t)
	tried := map[string]int{}
	missed := map[string][]string{}
	for di, doc := range docs {
		root := parseTree(t, doc)
		root.walk(nil, func(e *elem, path []int) {
			var parent *elem
			if len(path) > 0 {
				parent = root.at(path[:len(path)-1])
			}
			inPattern := false
			for k := range path {
				if qn(root.at(path[:k]).name) == "pattern" {
					inPattern = true
				}
			}
			for _, m := range mutationsOf(e, path, parent, inPattern) {
				if tried[m.kind] >= perKind {
					continue
				}
				tried[m.kind]++
				mut := root.clone()
				m.apply(mut)
				got := mut.String()
				r, err := svgdiff.Compare([]byte(got), []byte(doc), tolerance)
				if err != nil {
					continue // a mutation that breaks the XML is reported anyway
				}
				if r.Equal {
					missed[m.kind] = append(missed[m.kind], fmt.Sprintf("doc %d path %v", di, path))
				}
			}
		})
	}
	kinds := make([]string, 0, len(tried))
	for k := range tried {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		if w := missed[k]; len(w) > 0 {
			t.Errorf("not reported: %s (%d of %d), e.g. %s", k, len(w), tried[k], w[0])
		}
	}
	// The floor keeps the corpus from silently losing the kinds it exercises.
	const floor = 375
	if len(kinds) < floor {
		t.Errorf("only %d mutation kinds exercised, fewer than %d", len(kinds), floor)
	}
	t.Logf("%d mutation kinds over %d documents, every one reported", len(kinds), len(docs))
}

// TestEquivalences pins the differences svgdiff deliberately ignores, each of
// which renders the same, and their boundaries: a change just outside each is
// reported.
func TestEquivalences(t *testing.T) {
	doc := func(attr, text string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg"><path d="` + attr + `"/><text>` + text + `</text></svg>`
	}
	defs := func(d string) string { return `<svg xmlns="http://www.w3.org/2000/svg"><defs>` + d + `</defs></svg>` }
	cases := []struct {
		name      string
		got, want string
		equal     bool
	}{
		{"number within the absolute tolerance", doc("M0,0.4", "a"), doc("M0,0", "a"), true},
		{"number past the absolute tolerance", doc("M0,0.6", "a"), doc("M0,0", "a"), false},
		{"number within the relative tolerance", doc("M0,1000000.9", "a"), doc("M0,1000000", "a"), true},
		{"comma or space between numbers", doc("M0 0", "a"), doc("M0,0", "a"), true},
		{"numbers run together are a different number", doc("M00", "a"), doc("M0,0", "a"), false},
		{"exponent notation", doc("M1e2,0", "a"), doc("M100,0", "a"), true},
		{"negative zero", doc("M-0,0", "a"), doc("M0,0", "a"), true},
		{"NaN is not a number", doc("MNaN,0", "a"), doc("M0,0", "a"), false},
		{"NaN equals NaN", doc("MNaN,0", "a"), doc("MNaN,0", "a"), true},
		{"text is compared exactly: surrounding whitespace", doc("M0,0", " a "), doc("M0,0", "a"), false},
		{"text is compared exactly: inner whitespace runs", doc("M0,0", "a b"), doc("M0,0", "a  b"), false},
		{"text is compared exactly: a number within the tolerance", doc("M0,0", "0.25"), doc("M0,0", "0.5"), false},
		{"text is compared exactly: exponent notation", doc("M0,0", "1e+3"), doc("M0,0", "1000"), false},
		{"text is compared exactly: a sign", doc("M0,0", "+5%"), doc("M0,0", "5%"), false},
		{"text is compared exactly: a thousands separator", doc("M0,0", "1,000"), doc("M0,0", "1 000"), false},
		{"whitespace between elements is formatting", `<svg xmlns="http://www.w3.org/2000/svg"> <rect/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`, true},
		{"a non-geometry number is compared exactly", `<svg xmlns="http://www.w3.org/2000/svg"><rect opacity="0.2"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><rect opacity="0.6"/></svg>`, false},
		{"a non-geometry number is compared exactly: last-digit noise", `<svg xmlns="http://www.w3.org/2000/svg"><rect stroke-width="1.0000001"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><rect stroke-width="1"/></svg>`, false},
		{"a geometry number within the tolerance", `<svg xmlns="http://www.w3.org/2000/svg"><rect x="1.3" width="2"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><rect x="1" width="2.2"/></svg>`, true},
		{"a transform within the tolerance", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1.2,3)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1,3)"/></svg>`, true},
		{"a comma in text", doc("M0,0", "a, b"), doc("M0,0", "a b"), false},
		{"a comma beside a number in text", doc("M0,0", "1, a"), doc("M0,0", "1 a"), false},
		{"text that is a word, not a separator", doc("M0,0", "ab"), doc("M0,0", "a b"), false},
		{"attribute order", `<svg xmlns="http://www.w3.org/2000/svg"><rect x="1" y="2"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><rect y="2" x="1"/></svg>`, true},
		{"an absent attribute is not an empty one", `<svg xmlns="http://www.w3.org/2000/svg"><rect x=""/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`, false},
		{"element order", `<svg xmlns="http://www.w3.org/2000/svg"><rect/><circle/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><circle/><rect/></svg>`, false},
		{"infinity spelled out is text", doc("MInfinity,0", "a"), doc("M1e400,0", "a"), false},
		// Numbers that are not pixels (C52): compared within the relative
		// tolerance alone, so last-digit noise is equal and a visible change
		// is not.
		{"a bounding-box gradient coordinate", defs(`<linearGradient id="g" x1="0" x2="0.6" y1="0" y2="0"/>`), defs(`<linearGradient id="g" x1="0" x2="1" y1="0" y2="0"/>`), false},
		{"a bounding-box gradient direction", defs(`<linearGradient id="g" x1="0" x2="1" y1="0" y2="0.45"/>`), defs(`<linearGradient id="g" x1="0" x2="1" y1="0" y2="0"/>`), false},
		{"a bounding-box gradient radius", defs(`<radialGradient id="g" fx="0.5" fy="0.5" fr="0" cx="0.5" cy="0.5" r="0.125"/>`), defs(`<radialGradient id="g" fx="0.5" fy="0.5" fr="0" cx="0.5" cy="0.5" r="0.5"/>`), false},
		{"a bounding-box gradient centre", defs(`<radialGradient id="g" fx="0.1" fy="0.9" fr="0" cx="0.1" cy="0.9" r="0.5"/>`), defs(`<radialGradient id="g" fx="0.5" fy="0.5" fr="0" cx="0.5" cy="0.5" r="0.5"/>`), false},
		{"a bounding-box gradient coordinate's last digit", defs(`<linearGradient id="g" x1="0" x2="0.30000000000000004" y1="0" y2="0"/>`), defs(`<linearGradient id="g" x1="0" x2="0.3" y1="0" y2="0"/>`), true},
		{"a bounding-box coordinate near zero", defs(`<linearGradient id="g" x1="0.00001" x2="1" y1="0" y2="0"/>`), defs(`<linearGradient id="g" x1="0" x2="1" y1="0" y2="0"/>`), false},
		{"a user-space gradient is in pixels", defs(`<linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" x2="100.3" y1="0" y2="0"/>`), defs(`<linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" x2="100" y1="0" y2="0"/>`), true},
		{"a pattern's content under its unit viewBox", defs(`<pattern id="p" viewBox="0,0,1,1" width="100%" height="100%"><rect width="0.6" height="1"/></pattern>`), defs(`<pattern id="p" viewBox="0,0,1,1" width="100%" height="100%"><rect width="1" height="1"/></pattern>`), false},
		{"a pattern's bounding-box tile", defs(`<pattern id="p" x="0.3" width="1" height="1"/>`), defs(`<pattern id="p" x="0" width="1" height="1"/>`), false},
		{"an angle of rotate", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1,1) rotate(-44.55)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1,1) rotate(-45)"/></svg>`, false},
		{"an angle of rotate's last digit", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1,1) rotate(-45.00000000000001)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="translate(1,1) rotate(-45)"/></svg>`, true},
		{"rotate's centre is in pixels", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="rotate(30 10.3 20)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="rotate(30,10,20)"/></svg>`, true},
		{"a translate after a rotate is in pixels", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="rotate(30) translate(1.3,2)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="rotate(30) translate(1,2)"/></svg>`, true},
		{"a scale factor", `<svg xmlns="http://www.w3.org/2000/svg"><path transform="translate(1,1) scale(1.4,1)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><path transform="translate(1,1) scale(1,1)"/></svg>`, false},
		{"a skew angle", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="skewX(10.4)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="skewX(10)"/></svg>`, false},
		{"a matrix's linear part", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="matrix(1.3,0,0,1,5,5)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="matrix(1,0,0,1,5,5)"/></svg>`, false},
		{"a matrix's translation is in pixels", `<svg xmlns="http://www.w3.org/2000/svg"><g transform="matrix(1,0,0,1,5.3,5)"/></svg>`, `<svg xmlns="http://www.w3.org/2000/svg"><g transform="matrix(1,0,0,1,5,5)"/></svg>`, true},
	}
	for _, c := range cases {
		r, err := svgdiff.Compare([]byte(c.got), []byte(c.want), tolerance)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		// Both directions: an equivalence must be symmetric.
		r2, _ := svgdiff.Compare([]byte(c.want), []byte(c.got), tolerance)
		if r.Equal != c.equal || r2.Equal != c.equal {
			t.Errorf("%s: equal %v / %v, want %v (%s)", c.name, r.Equal, r2.Equal, c.equal, r.Diff)
		}
	}
}
