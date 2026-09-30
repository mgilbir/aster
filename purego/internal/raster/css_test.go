package raster

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSelector(t *testing.T) {
	valid := map[string][3]uint16{
		"rect":                   {0, 0, 1},
		"*":                      {0, 0, 0},
		".a":                     {0, 1, 0},
		"#x":                     {1, 0, 0},
		"rect.a#x":               {1, 1, 1},
		"g > rect":               {0, 0, 2},
		"g rect.a":               {0, 1, 2},
		"a+b":                    {0, 0, 2},
		"[data-x]":               {0, 1, 0},
		`[data-x="v w"]`:         {0, 1, 0},
		"[lang|=en]":             {0, 1, 0},
		"[a~='b']":               {0, 1, 0},
		"[a^=b][c$=d][e*=f]":     {0, 3, 0},
		"rect:first-child":       {0, 1, 1},
		"  svg   >   g   rect  ": {0, 0, 3},
	}
	for src, spec := range valid {
		sel := parseSelector(src)
		if sel == nil {
			t.Errorf("%q: rejected", src)
			continue
		}
		if sel.spec != spec {
			t.Errorf("%q: specificity %v, want %v", src, sel.spec, spec)
		}
	}
	invalid := []string{"", " ", ">", "a >", "> a", "a::before", "a:hover", "a:lang(en)", "a:not(b)", ".", "#", "[", "[a", "[=b]", "[a=]x", "a ~ b", "a,,", "a{", "a)", strings.Repeat("a ", 40) + "b"}
	for _, src := range invalid {
		if parseSelector(src) != nil {
			t.Errorf("%q: accepted", src)
		}
	}
}

func TestStyleSheetParsing(t *testing.T) {
	src := `
	/* comment { fill: red } */
	@import url("x.css");
	@font-face { font-family: X; src: url(data:font/woff;base64,AAAA) }
	@media print { rect { fill: red } }
	rect, .a , g > path { fill: blue; stroke: url("data:image/svg+xml;utf8,<svg/>") ; stroke-width: 2 !important }
	broken selector::before { fill: red }
	circle { fill: green
	`
	rules := parseStyleSheet(src, nil, 100)
	// rect, .a, g > path, and the unterminated circle rule (CSS recovers at EOF).
	if len(rules) != 4 {
		t.Fatalf("got %d rules, want 4", len(rules))
	}
	if len(rules[0].decls) != 3 || rules[0].decls[2].name != "stroke-width" || !rules[0].decls[2].imp {
		t.Errorf("declarations: %+v", rules[0].decls)
	}
	if rules[0].decls[1].val != `url("data:image/svg+xml;utf8,<svg/>")` {
		t.Errorf("semicolon inside url() split the declaration: %q", rules[0].decls[1].val)
	}
	if got := len(parseStyleSheet(src, nil, 2)); got != 2 {
		t.Errorf("rule cap: got %d rules", got)
	}
}

func TestForEachDecl(t *testing.T) {
	type d struct {
		n, v string
		i    bool
	}
	var got []d
	forEachDecl(`Fill : red ; stroke:blue!IMPORTANT;;bad; :x; y: ; marker: url(#m); fill-opacity: .5 !important `, func(n, v string, i bool) {
		got = append(got, d{n, v, i})
	})
	want := []d{{"fill", "red", false}, {"stroke", "blue", true}, {"marker", "url(#m)", false}, {"fill-opacity", ".5", true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestStyleAttributePresentationOnly(t *testing.T) {
	doc, err := parseDocument(`<svg xmlns="http://www.w3.org/2000/svg"><rect id="r" width="5" style="fill:red; width:99; id:zz; marker: url(#m); opacity:.5 !important; opacity:1"/></svg>`, Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	r := doc.ids["r"]
	if r == nil {
		t.Fatal("rect lost its id")
	}
	if v, _ := r.get(aWidth); v != "5" {
		t.Errorf("width from style attribute leaked: %q", v)
	}
	if v, _ := r.get(aOpacity); v != ".5" {
		t.Errorf("important declaration lost: %q", v)
	}
	if v, _ := r.get(aMarkerMid); v != "url(#m)" {
		t.Errorf("marker shorthand: %q", v)
	}
}

func TestCascadeOrder(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><style>
	rect { fill: red } .c { fill: green } #i { fill: blue } rect.c { fill: orange }
	.imp { fill: teal !important } .imp2 { fill: gold }
	</style>
	<rect id="a" class="c"/><rect id="b" class="c" style="fill:black"/><rect id="c" class="c" id="i"/>
	<rect id="d" class="imp" style="fill:black"/><rect id="e" class="imp imp2"/><rect id="f" fill="cyan"/></svg>`
	doc, err := parseDocument(svg, Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "orange", "b": "black", "d": "teal", "e": "teal", "f": "red"}
	for id, col := range want {
		n := doc.ids[id]
		if n == nil {
			t.Fatalf("no %s", id)
		}
		if v, _ := n.get(aFill); v != col {
			t.Errorf("%s: fill %q, want %q", id, v, col)
		}
	}
}

func TestCSSStructuralSelectors(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><style>
	g > rect { fill: red } g + rect { stroke: blue } rect:first-child { opacity: 0.5 } svg rect.x { fill-opacity: 0.25 }
	</style><g><rect id="a"/><rect id="b" class="x"/></g><rect id="c"/></svg>`
	doc, err := parseDocument(svg, Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	check := func(id string, a attrID, want string) {
		t.Helper()
		v, _ := doc.ids[id].get(a)
		if v != want {
			t.Errorf("%s: attr %d = %q, want %q", id, a, v, want)
		}
	}
	check("a", aFill, "red")
	check("a", aOpacity, "0.5")
	check("b", aOpacity, "")
	check("b", aFillOpacity, "0.25")
	check("c", aStroke, "blue")
	check("c", aFill, "")
}

func TestCSSLimits(t *testing.T) {
	// Many rules: capped, and the render still succeeds.
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><style>`)
	for i := 0; i < 5000; i++ {
		b.WriteString(".c")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(" rect { fill: red }\n")
	}
	b.WriteString(`</style><rect class="cx" width="10" height="10"/></svg>`)
	lim := Limits{MaxCSSRules: 100}
	if _, err := Render([]byte(b.String()), Options{Limits: lim}); err != nil {
		t.Fatal(err)
	}
	// Style sheets over the size limit are ignored, not fatal.
	doc, err := parseDocument(b.String(), Limits{MaxStyleBytes: 10}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range doc.ids {
		_ = n
	}
}
