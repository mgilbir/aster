package raster

import (
	"strings"
	"testing"
)

var fuzzLimits = Limits{MaxPixels: 1 << 18, MaxDimension: 2048, MaxElements: 5000, MaxRenderNodes: 20000, MaxInputBytes: 1 << 20}

func FuzzRender(f *testing.F) {
	for _, s := range synths {
		f.Add(s.svg)
	}
	for _, s := range featureSynths {
		f.Add(s.svg)
	}
	f.Add(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><path d="M0 0L10 10" stroke="red"/></svg>`)
	f.Fuzz(func(t *testing.T, svg string) {
		img, err := Render([]byte(svg), Options{Limits: fuzzLimits})
		if err == nil && img == nil {
			t.Fatal("nil image without error")
		}
	})
}

func FuzzParseDocument(f *testing.F) {
	for _, s := range synths {
		f.Add(s.svg)
	}
	for _, s := range featureSynths {
		f.Add(s.svg)
	}
	f.Add(`<!DOCTYPE svg [<!ENTITY a "b">]><svg><text>&a;&#x41;</text></svg>`)
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseDocument(s, fuzzLimits.withDefaults())
	})
}

func FuzzPathData(f *testing.F) {
	f.Add("M10 10 L20 20 C1 2 3 4 5 6 A5 5 0 1 0 10 0 z")
	f.Add("m1e3 -1e-3 .5.5.5 q1 1 2 2t3 3s1 1 2 2a1 1 0 10 5 5")
	f.Fuzz(func(t *testing.T, d string) {
		var p path
		parsePathData(d, &p)
		var fl flat
		fl.flatten(&p, identity, 0.1)
		var st stroker
		var out flat
		st.stroke(&fl, strokeStyle{width: 3, join: joinRound, cap: capRound, miterLimit: 4, dash: []float64{2, 1}}, 1, &out)
	})
}

func FuzzColorAndTransform(f *testing.F) {
	f.Add("rgb(1,2,3)", "translate(1 2) rotate(30)")
	f.Fuzz(func(t *testing.T, c, tf string) {
		parseColor(c)
		parseTransform(tf)
		parsePaint(c)
		parseFontFamilies(c)
	})
}

// FuzzCSS exercises the style-sheet, selector and declaration parsers.
func FuzzCSS(f *testing.F) {
	f.Add("rect, .a > g + path:first-child[x~=\"y z\"] { fill: red !important; stroke: url(data:a;b) }")
	f.Add("@media print { a { b: c } } @import 'x'; /* c */ #i { marker: url(#m) }")
	f.Add("[a|=b][c^=d][e$=f][g*=h] { }")
	f.Add("\\\"\x00{{{}}}}}(((\\")
	f.Fuzz(func(t *testing.T, src string) {
		rules := parseStyleSheet(src, nil, 1000)
		for _, r := range rules {
			if r.sel == nil || len(r.sel.comps) == 0 || len(r.sel.comps) != len(r.sel.comb) {
				t.Fatalf("malformed selector from %q", src)
			}
		}
		parseSelector(src)
		forEachDecl(src, func(name, val string, imp bool) {})
		parseStyleDecls(src, nil)
		splitFilterList(src)
	})
}

// FuzzFeatureAttrs feeds arbitrary attribute values to the mask, filter,
// pattern, marker and text features.
func FuzzFeatureAttrs(f *testing.F) {
	f.Add("stdDeviation", "3 4", "x", "-10%")
	f.Add("values", "1 0 0 0 0 0 1 0 0 0 0 0 1 0 0 0 0 0 1 0", "orient", "auto")
	f.Add("patternTransform", "rotate(30) scale(2)", "viewBox", "0 0 10 10")
	f.Add("rotate", "1 2 3", "textLength", "50")
	f.Fuzz(func(t *testing.T, name1, val1, name2, val2 string) {
		esc := func(s string) string {
			return strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
		}
		if strings.ContainsAny(name1+name2, " =\"'<>/&") {
			return
		}
		a := name1 + `="` + esc(val1) + `" ` + name2 + `="` + esc(val2) + `"`
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="60" height="60">` +
			`<defs><filter id="f" ` + a + `><feGaussianBlur ` + a + `/><feColorMatrix ` + a + `/><feComposite ` + a + `/><feFlood ` + a + `/><feOffset ` + a + `/><feBlend ` + a + `/></filter>` +
			`<mask id="m" ` + a + `><rect width="60" height="60" fill="#888"/></mask>` +
			`<pattern id="p" width="10" height="10" ` + a + `><rect width="5" height="5"/></pattern>` +
			`<marker id="k" markerWidth="3" markerHeight="3" ` + a + `><rect width="3" height="3"/></marker>` +
			`<clipPath id="c" ` + a + `><circle r="20"/></clipPath></defs>` +
			`<path d="M5 5L30 40L55 5" fill="url(#p)" stroke="black" marker-mid="url(#k)" marker-start="url(#k)" filter="url(#f)" mask="url(#m)" clip-path="url(#c)"/>` +
			`<text x="5" y="50" ` + a + `>Hi<tspan ` + a + `>there</tspan></text></svg>`
		img, err := Render([]byte(svg), Options{Limits: fuzzLimits})
		if err == nil && img == nil {
			t.Fatal("nil image without error")
		}
	})
}

func FuzzFontMetrics(f *testing.F) {
	f.Add([]byte("\x00\x01\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00hhea"), 1000.0)
	f.Fuzz(func(t *testing.T, b []byte, upem float64) {
		parseFaceMetrics(b, upem)
		parseAngle(string(b))
		parseNumList(string(b))
	})
}
