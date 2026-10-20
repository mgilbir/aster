package svgpdf

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"

	"github.com/mgilbir/aster/internal/raster"
)

// painted is a Do or sh of a PDF's content and the graphics state PDF
// composites it with: what is under the caller's control, and what PDF
// applies to the object as a whole (ISO 32000-1, 11.6.4).
type painted struct {
	op, name string
	ca       float64
	bm       string // "Normal" when none was set
	smask    bool
	depth    int // forms deep: 0 is the page
}

// paintedObjects interprets the page's content stream, and every form it
// paints, tracking the alpha, blend mode and soft mask through q, Q and gs,
// and returns every Do and sh in the order a reader paints them. A form's
// content starts from the state a transparency group starts from (11.6.6):
// alpha 1, blend mode Normal, no soft mask.
func paintedObjects(t *testing.T, pdf []byte) []painted {
	t.Helper()
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	root := doc.ResolveDict(doc.Trailer.Get("Root"))
	page := doc.ResolveDict(doc.ResolveDict(root.Get("Pages")).Get("Kids").(pdf0.Array)[0])
	res := doc.ResolveDict(page.Get("Resources"))
	gstates := doc.ResolveDict(res.Get("ExtGState"))
	xobjects := doc.ResolveDict(res.Get("XObject"))
	var out []painted
	type state struct {
		ca    float64
		bm    string
		smask bool
	}
	var run func(content string, depth int)
	run = func(content string, depth int) {
		if depth > 16 {
			t.Fatal("forms nested past 16")
		}
		st := state{1, "Normal", false}
		var stack []state
		toks := strings.Fields(content)
		for i, tok := range toks {
			switch tok {
			case "q":
				stack = append(stack, st)
			case "Q":
				st, stack = stack[len(stack)-1], stack[:len(stack)-1]
			case "gs":
				d := doc.ResolveDict(gstates.Get(pdf0.Name(strings.TrimPrefix(toks[i-1], "/"))))
				if v := d.Get("ca"); v != nil {
					st.ca = number(t, v)
				}
				if v, ok := d.Get("BM").(pdf0.Name); ok {
					st.bm = string(v)
				}
				if v := d.Get("SMask"); v != nil {
					st.smask = v != pdf0.Name("None")
				}
			case "Do", "sh":
				name := strings.TrimPrefix(toks[i-1], "/")
				out = append(out, painted{tok, name, st.ca, st.bm, st.smask, depth})
				if tok != "Do" {
					continue
				}
				s, ok := doc.Resolve(xobjects.Get(pdf0.Name(name))).(*pdf0.Stream)
				if !ok || s.Dict.Get("Subtype") != pdf0.Name("Form") {
					continue
				}
				c, err := flateDecode(s.Data)
				if err != nil {
					t.Fatal(err)
				}
				run(string(c), depth+1)
			}
		}
	}
	run(contentStreamOf(t, doc), 0)
	return out
}

func number(t *testing.T, v pdf0.Object) float64 {
	t.Helper()
	switch n := v.(type) {
	case pdf0.Real:
		return float64(n)
	case pdf0.Integer:
		return float64(n)
	}
	t.Fatalf("not a number: %v", v)
	return 0
}

// scene is a random row of overlapping squares, each with a random fill,
// opacity, fill-opacity and blend mode, some of them in a group with an
// opacity: the siblings a mark with a blend mode or a translucent group
// follows and precedes are anything Vega can write (C30, C115).
type square struct {
	fill                 string
	opacity, fillOpacity float64
	blend                string
	group, groupBlend    bool
	groupOpacity         float64
	strokeWidth          float64
	stroke               string
	x                    int
}

var blends = []string{"", "", "multiply", "screen", "darken", "difference"}

func randomScene(rng *rand.Rand) []square {
	colours := []string{"#ff0000", "#00ff00", "#0000ff", "#ffcc00", "#000000", "#ffffff"}
	n := 2 + rng.IntN(5)
	out := make([]square, n)
	for i := range out {
		s := square{fill: colours[rng.IntN(len(colours))], opacity: 1, fillOpacity: 1, groupOpacity: 1, x: 10 + 30*i}
		switch rng.IntN(4) {
		case 0:
			s.opacity = float64(rng.IntN(11)) / 10
		case 1:
			s.fillOpacity = float64(rng.IntN(11)) / 10
		}
		s.blend = blends[rng.IntN(len(blends))]
		if rng.IntN(3) == 0 {
			s.group = true
			s.groupOpacity = float64(1+rng.IntN(9)) / 10
			s.groupBlend = rng.IntN(2) == 0
		}
		if rng.IntN(4) == 0 {
			s.stroke, s.strokeWidth = colours[rng.IntN(len(colours))], float64(1+rng.IntN(4))
		}
		out[i] = s
	}
	return out
}

// leafGroup reports whether the square's own path is a group: an element
// that both fills and strokes at an opacity below one is composited once,
// so that its stroke does not show its fill through (C115's class).
func (s square) leafGroup() bool {
	return s.stroke != "" && s.opacity > 0 && s.opacity < 1 && s.fillOpacity > 0
}

// groups are the groups a square paints, in order: a translucent <g> at its
// opacity (under its blend mode when it has one), an element with a blend
// mode at alpha 1 under it, holding its own alphas, and a translucent
// element that fills and strokes at its opacity.
func (s square) groups() []painted {
	var out []painted
	depth := 0
	if s.group {
		bm := "Normal"
		if s.groupBlend && s.blend != "" {
			bm = pdfBlendModes[s.blend]
		}
		out = append(out, painted{op: "Do", ca: s.groupOpacity, bm: bm})
		depth++
	}
	if s.blend != "" && !(s.group && s.groupBlend) {
		out = append(out, painted{op: "Do", ca: 1, bm: pdfBlendModes[s.blend], depth: depth})
		depth++
	}
	if s.leafGroup() {
		out = append(out, painted{op: "Do", ca: s.opacity, bm: "Normal", depth: depth})
	}
	return out
}

func (s square) svg() string {
	el := fmt.Sprintf(`<path d="M%d,10h40v40h-40Z" fill="%s"`, s.x, s.fill)
	if s.opacity != 1 {
		el += fmt.Sprintf(` opacity="%g"`, s.opacity)
	}
	if s.fillOpacity != 1 {
		el += fmt.Sprintf(` fill-opacity="%g"`, s.fillOpacity)
	}
	if s.stroke != "" {
		el += fmt.Sprintf(` stroke="%s" stroke-width="%g"`, s.stroke, s.strokeWidth)
	}
	if s.blend != "" && !(s.group && s.groupBlend) {
		el += fmt.Sprintf(` style="mix-blend-mode: %s"`, s.blend)
	}
	el += `/>`
	if !s.group {
		return el
	}
	// A second square inside the group overlaps the first: a group drawn
	// once shows no darker overlap.
	g := fmt.Sprintf(`<g opacity="%g"`, s.groupOpacity)
	if s.groupBlend && s.blend != "" {
		g += fmt.Sprintf(` style="mix-blend-mode: %s"`, s.blend)
	}
	return g + `>` + el + fmt.Sprintf(`<path d="M%d,30h40v40h-40Z" fill="%s"/>`, s.x+10, s.fill) + `</g>`
}

func sceneSVG(sq []square) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="240" height="80"><rect width="240" height="80" fill="#808080"/>`)
	for _, s := range sq {
		b.WriteString(s.svg())
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// TestCompositedAtTheirOwnState is C30's structural half (audit
// 2026-10-18): whatever the siblings before it leave in the stream, every
// group, image and shading is composited at the alpha, blend mode and soft
// mask meant for it. A group of an element with a blend mode carries the
// element's own alphas inside it, so it is composited at alpha 1 under the
// element's blend mode, with no soft mask; a translucent <g> is a group
// composited at its opacity (C115).
func TestCompositedAtTheirOwnState(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261018, 30))
	for i := range 300 {
		sq := randomScene(rng)
		svg := sceneSVG(sq)
		pdf, err := Convert(svg, nil, Options{})
		if err != nil {
			t.Fatal(err)
		}
		var want []painted
		for _, s := range sq {
			want = append(want, s.groups()...)
		}
		var got []painted
		for _, p := range paintedObjects(t, pdf) {
			if p.op == "Do" {
				got = append(got, p)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("scene %d: %d groups painted, want %d\n%s", i, len(got), len(want), svg)
		}
		for k := range got {
			g, w := got[k], want[k]
			if g.ca != w.ca || g.bm != w.bm || g.smask || g.depth != w.depth {
				t.Fatalf("scene %d: group %d painted at alpha %g, blend %s, soft mask %v, depth %d; want alpha %g, %s, none, depth %d\n%s",
					i, k, g.ca, g.bm, g.smask, g.depth, w.ca, w.bm, w.depth, svg)
			}
		}
	}
}

// TestCompositingMatchesPNG is C30 and C115 read back (audit 2026-10-18):
// random scenes of squares with opacities, blend modes, strokes and
// translucent groups, drawn by poppler from the PDF and by the rasterizer
// from the SVG, have the same colour at the middle of every square and of
// every overlap.
func TestCompositingMatchesPNG(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		if os.Getenv("ASTER_POPPLER") == "require" {
			t.Fatal("pdftoppm (poppler) required (ASTER_POPPLER=require) but not installed")
		}
		t.Skip("pdftoppm (poppler) not installed")
	}
	rng := rand.New(rand.NewPCG(20261018, 115))
	dir := t.TempDir()
	scenes := 40
	if testing.Short() {
		scenes = 10
	}
	for i := range scenes {
		sq := randomScene(rng)
		switch i {
		case 0:
			// The audit's: a red square with a blend mode after one at
			// 20% (C30), and after one at opacity 0, which Vega writes for
			// a hidden mark.
			sq = []square{
				{fill: "#0000ff", opacity: 0.2, fillOpacity: 1, groupOpacity: 1, x: 10},
				{fill: "#ff0000", opacity: 1, fillOpacity: 1, groupOpacity: 1, x: 40, blend: "multiply"},
				{fill: "#0000ff", opacity: 0, fillOpacity: 1, groupOpacity: 1, x: 70},
				{fill: "#ff0000", opacity: 1, fillOpacity: 1, groupOpacity: 1, x: 100, blend: "multiply"},
			}
		case 1:
			// A translucent <g> around overlapping squares (C115).
			sq = []square{{fill: "#0000ff", opacity: 1, fillOpacity: 1, group: true, groupOpacity: 0.5, x: 10}}
		}
		svg := sceneSVG(sq)
		pdf, err := Convert(svg, nil, Options{})
		if err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, "s.pdf")
		if err := os.WriteFile(in, pdf, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("pdftoppm", "-r", "72", "-png", "-singlefile", in, filepath.Join(dir, "s")).CombinedOutput(); err != nil {
			t.Fatalf("pdftoppm: %v\n%s", err, out)
		}
		f, err := os.Open(filepath.Join(dir, "s.png"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		want, err := raster.Render([]byte(svg), raster.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range sq {
			// Inside the square, where it overlaps the ones before and
			// after it, and where a group's two overlap: edges are ten
			// pixels apart, and a pixel five from one is clear of
			// anti-aliasing (poppler's and the rasterizer's differ) and of
			// strokes, at most 4 wide.
			for _, p := range []image.Point{{s.x + 5, 20}, {s.x + 15, 40}, {s.x + 25, 40}, {s.x + 35, 20}} {
				if !sameColour(got.At(p.X, p.Y), want.At(p.X, p.Y)) {
					gr, gg, gb, _ := got.At(p.X, p.Y).RGBA()
					wr, wg, wb, _ := want.At(p.X, p.Y).RGBA()
					t.Fatalf("scene %d at %v: PDF (%d,%d,%d), PNG (%d,%d,%d)\n%s", i, p, gr>>8, gg>>8, gb>>8, wr>>8, wg>>8, wb>>8, svg)
				}
			}
		}
	}
}

// sameColour allows for rounding (an alpha is a byte in the PNG, a real in
// the PDF): the two, each over white as a page is, are within 3 of each
// other in every channel.
func sameColour(a, b interface{ RGBA() (r, g, b, a uint32) }) bool {
	ar, ag, ab := overWhite(a)
	br, bg, bb := overWhite(b)
	d := func(x, y float64) bool { return math.Abs(x-y) <= 3 }
	return d(ar, br) && d(ag, bg) && d(ab, bb)
}

// overWhite is c, premultiplied, over white, in 0..255.
func overWhite(c interface{ RGBA() (r, g, b, a uint32) }) (r, g, b float64) {
	cr, cg, cb, ca := c.RGBA()
	w := float64(0xffff - ca)
	return (float64(cr) + w) / 257, (float64(cg) + w) / 257, (float64(cb) + w) / 257
}

// paintOp finds a Do or sh operator in a string.
var paintOp = regexp.MustCompile(`(^|\s)(Do|sh)(\s|$)`)

// TestPaintedOnlyThroughComposite is C30's class guard (audit 2026-10-18):
// PDF composites a Do or a sh with the graphics state's alpha, blend mode
// and soft mask, which svgpdf leaves set from leaf to leaf, so every one of
// them goes through contentWriter.composite, which sets all three. The
// operators are written in exactly one place each; a string with either
// anywhere else (a form's content written by hand) is listed here with why
// it is safe.
func TestPaintedOnlyThroughComposite(t *testing.T) {
	allowed := map[string]bool{
		"paintXObject": true, // the primitive's
		"paintShading": true,
		"formShading":  true, // a form's first operator: the state a group starts with
	}
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Every top-level declaration, not only functions: a package-level
		// var or constant (or a function literal held by one) writes
		// content as well as a function does.
		for _, d := range f.Decls {
			owner := "package-level declaration"
			switch d := d.(type) {
			case *ast.FuncDecl:
				if allowed[d.Name.Name] {
					continue
				}
				owner = d.Name.Name
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						owner = vs.Names[0].Name
						break
					}
				}
			}
			ast.Inspect(d, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err == nil && paintOp.MatchString(s) {
					t.Errorf("%s: %s writes %q itself: paint through contentWriter.paintObject", fset.Position(lit.Pos()), owner, s)
				}
				return true
			})
		}
	}
}

// TestLeafGroupsShared: the group of a translucent mark that fills and
// strokes holds the mark in its own space, its position outside it, so the
// marks of one shape and paint share one form: a chart of many such marks
// is not a form per mark (C115's cost).
func TestLeafGroupsShared(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000">`)
	for i := range 500 {
		fmt.Fprintf(&b, `<path transform="translate(%d,%d)" d="M5,0A5,5,0,1,1,-5,0A5,5,0,1,1,5,0" fill="#4c78a8" stroke="black" opacity="0.8"/>`, i%31*30, i/31*30)
	}
	b.WriteString(`</svg>`)
	pdf, err := Convert(b.String(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var does, forms int
	for _, p := range paintedObjects(t, pdf) {
		if p.op == "Do" {
			does++
		}
	}
	forms = bytes.Count(pdf, []byte("/Subtype /Form"))
	if does != 500 || forms != 1 {
		t.Errorf("500 marks painted %d groups from %d forms, want 500 from 1", does, forms)
	}
}
