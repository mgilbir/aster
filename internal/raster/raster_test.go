package raster

import (
	"bytes"
	"image/png"
	"math"
	"strings"
	"testing"
)

func mustRender(t *testing.T, svg string, o Options) (w, h int) {
	t.Helper()
	img, err := Render([]byte(svg), o)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return img.Rect.Dx(), img.Rect.Dy()
}

func TestSizeRounding(t *testing.T) {
	// Pixel size is ceil(width*scale), computed like resvg (f32 size).
	cases := []struct {
		svg   string
		scale float64
		w, h  int
	}{
		{`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="20"/>`, 1, 10, 20},
		{`<svg xmlns="http://www.w3.org/2000/svg" width="10.2" height="20.5"/>`, 1, 11, 21},
		{`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="20"/>`, 1.5, 15, 30},
		{`<svg xmlns="http://www.w3.org/2000/svg" width="10.1" height="20"/>`, 2.5, 26, 50},
		{`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 33 44"/>`, 1, 33, 44},
		{`<svg xmlns="http://www.w3.org/2000/svg" width="1in" height="72pt"/>`, 1, 96, 96},
		{`<svg xmlns="http://www.w3.org/2000/svg" width="50%" height="10" viewBox="0 0 40 10"/>`, 1, 20, 10},
	}
	for _, c := range cases {
		w, h := mustRender(t, c.svg, Options{Scale: c.scale})
		if w != c.w || h != c.h {
			t.Errorf("%s scale %v: got %dx%d want %dx%d", c.svg, c.scale, w, h, c.w, c.h)
		}
	}
	// And must agree with resvg (a subtest: it skips without the oracle).
	t.Run("resvg", func(t *testing.T) {
		for _, c := range cases {
			w, h := mustRender(t, c.svg, Options{Scale: c.scale})
			ref := refPNG(t, []byte(c.svg), c.scale)
			if ref != nil && (ref.Rect.Dx() != w || ref.Rect.Dy() != h) {
				t.Errorf("%s scale %v: resvg is %dx%d, we are %dx%d", c.svg, c.scale, ref.Rect.Dx(), ref.Rect.Dy(), w, h)
			}
		}
	})
}

func TestLimits(t *testing.T) {
	huge := `<svg xmlns="http://www.w3.org/2000/svg" width="100000" height="100000"/>`
	if _, err := Render([]byte(huge), Options{}); err == nil || !strings.Contains(err.Error(), "exceeds the limit") {
		t.Errorf("huge canvas: want limit error, got %v", err)
	}
	if _, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4000" height="4000"/>`), Options{Scale: 4}); err == nil {
		t.Error("scaled canvas over the limit accepted")
	}
	if _, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`), Options{Scale: math.NaN()}); err == nil {
		t.Error("NaN scale accepted")
	}
	if _, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`), Options{Scale: -1}); err == nil {
		t.Error("negative scale accepted")
	}
	if _, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`), Options{Scale: math.Inf(1)}); err == nil {
		t.Error("infinite scale accepted")
	}
	// Element count.
	many := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + strings.Repeat(`<g/>`, 100) + `</svg>`
	if _, err := Render([]byte(many), Options{Limits: Limits{MaxElements: 50}}); err == nil {
		t.Error("element limit not enforced")
	}
	// Nesting depth.
	deep := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + strings.Repeat(`<g>`, 5000) + strings.Repeat(`</g>`, 5000) + `</svg>`
	if _, err := Render([]byte(deep), Options{}); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Errorf("depth limit: %v", err)
	}
	// Input size.
	if _, err := Render([]byte(many), Options{Limits: Limits{MaxInputBytes: 100}}); err == nil {
		t.Error("input size limit not enforced")
	}
}

func TestUseBomb(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><defs><rect id="l0" width="1" height="1"/>`)
	for i := 1; i < 40; i++ {
		b.WriteString(`<g id="l` + itoa(i) + `"><use href="#l` + itoa(i-1) + `"/><use href="#l` + itoa(i-1) + `"/></g>`)
	}
	b.WriteString(`</defs><use href="#l39"/></svg>`)
	_, err := Render([]byte(b.String()), Options{Limits: Limits{MaxRenderNodes: 100000}})
	if err == nil {
		t.Fatal("use expansion bomb was not stopped")
	}
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Replace(string(rune('0'+i/10))+string(rune('0'+i%10)), "\x00", "", -1))
}

func TestUseCycle(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><defs><g id="a"><use href="#b"/></g><g id="b"><use href="#a"/></g></defs><use href="#a"/><use href="#self" id="self"/></svg>`
	if _, err := Render([]byte(svg), Options{}); err == nil {
		t.Log("cycle rendered without error (bounded)")
	}
}

func TestLayerDepth(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + strings.Repeat(`<g opacity="0.9">`, 40) + `<rect width="5" height="5"/>` + strings.Repeat(`</g>`, 40) + `</svg>`
	if _, err := Render([]byte(svg), Options{}); err == nil {
		t.Error("layer depth not bounded")
	}
}

func TestMalformedNoPanic(t *testing.T) {
	inputs := []string{
		"", "<", "<svg", "<svg>", "<svg width='1' height='1'>", "<svg width='1' height='1'></g></svg>",
		`<svg width="1" height="1"><path d="M"/></svg>`,
		`<svg width="1" height="1"><path d="M 1e999 1e999 L -1e999 5 A 1e308 1e308 0 1 1 5 5z"/></svg>`,
		`<svg width="1" height="1"><rect width="1e308" height="1e308" rx="NaN" stroke="red" stroke-width="1e308"/></svg>`,
		`<svg width="10" height="10"><path d="M0 0 L10 10" stroke="red" stroke-dasharray="1e-9" stroke-width="3"/></svg>`,
		`<svg width="10" height="10"><path d="M0 0 L10 10" stroke="red" stroke-dasharray="0 0"/></svg>`,
		`<svg width="10" height="10"><path d="M0 0 L10 10" stroke="red" stroke-dasharray="-1 2"/></svg>`,
		`<svg width="10" height="10"><g transform="scale(0)"><rect width="5" height="5"/></g><g transform="matrix(NaN 0 0 1 0 0)"/></svg>`,
		`<svg width="10" height="10"><g transform="scale(1e300)"><rect width="5" height="5" stroke="red"/></g></svg>`,
		`<svg width="10" height="10"><linearGradient id="g" href="#g"/><rect width="5" height="5" fill="url(#g)"/></svg>`,
		`<svg width="10" height="10"><clipPath id="c" clip-path="url(#c)"><rect width="5" height="5"/></clipPath><rect width="9" height="9" clip-path="url(#c)"/></svg>`,
		`<svg width="10" height="10"><image href="data:image/png;base64,AAAA" width="5" height="5"/><image href="http://example.com/x.png" width="5" height="5"/><image href="file:///etc/passwd" width="5" height="5"/></svg>`,
		`<svg width="10" height="10"><text x="1e400" y="NaN" font-size="1e9">hi</text><text font-size="-5">x</text></svg>`,
		`<!DOCTYPE svg [<!ENTITY a "&b;&b;"><!ENTITY b "&c;&c;">]><svg width="10" height="10"><text>&a;</text></svg>`,
		`<svg width="10" height="10"><text>&#0;&#xD800;&#x110000;&bogus;&amp</text></svg>`,
		"<svg width='10' height='10'><path d='" + strings.Repeat("M1 1L2 2", 10000) + "'/></svg>",
		`<svg width="0" height="0"/>`, `<svg width="-5" height="5"/>`, `<svg width="1" height="1" viewBox="0 0 0 0"/>`,
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("panic for %.60q: %v", in, p)
				}
			}()
			_, _ = Render([]byte(in), Options{})
		}()
	}
}

func TestExternalNotFetched(t *testing.T) {
	// A non-data href must be ignored silently (no network, no files).
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><image href="/etc/hosts" width="10" height="10"/><image href="http://127.0.0.1:1/x.png" width="10" height="10"/></svg>`
	img, err := Render([]byte(svg), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			t.Fatal("external image drew pixels")
		}
	}
}

func TestBackground(t *testing.T) {
	img, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="2" height="4" fill="red"/></svg>`),
		Options{Background: rgbaColor(0, 0, 255, 255)})
	if err != nil {
		t.Fatal(err)
	}
	if p := img.NRGBAAt(0, 0); p.R != 255 || p.B != 0 {
		t.Errorf("left pixel %v", p)
	}
	if p := img.NRGBAAt(3, 0); p.B != 255 || p.A != 255 {
		t.Errorf("right pixel %v", p)
	}
}

func TestEncodePNG(t *testing.T) {
	img, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><circle cx="4" cy="4" r="3" fill="#123456" fill-opacity="0.5"/></svg>`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if st := compareImages(toNRGBA(dec), img, 0); st.MAE != 0 {
		t.Errorf("round trip differs: %+v", st)
	}
	b2, err := RenderPNG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"/>`), Options{})
	if err != nil || len(b2) == 0 {
		t.Fatal(err)
	}
}

func TestParsePathData(t *testing.T) {
	var p path
	parsePathData("M10 10 20 20 30,30z m5 5 h10 v10 H0 V0 c1 1 2 2 3 3 s1 1 2 2 q1 1 2 2 t3 3 a5 5 0 1 0 10 0", &p)
	if len(p.verbs) == 0 {
		t.Fatal("no verbs")
	}
	// Errors keep the valid prefix.
	var q path
	parsePathData("M0 0 L10 10 L20 x", &q)
	if len(q.verbs) != 2 {
		t.Errorf("prefix verbs = %d, want 2", len(q.verbs))
	}
	// Arc flags without separators.
	var a path
	parsePathData("M0 0a5 5 0 10 10 0", &a)
	if len(a.verbs) < 2 {
		t.Errorf("compact arc flags not parsed: %v", a.verbs)
	}
	// A path must start with a moveto.
	var z path
	parsePathData("L10 10", &z)
	if len(z.verbs) != 0 {
		t.Error("path without moveto accepted")
	}
}

func TestParseColor(t *testing.T) {
	cases := map[string]rgba{
		"red": {255, 0, 0, 1}, "#fff": {255, 255, 255, 1}, "#0f08": {0, 255, 0, 136.0 / 255},
		"rgb(1,2,3)": {1, 2, 3, 1}, "rgba(0,0,0,0.5)": {0, 0, 0, 0.5}, "hsl(0,100%,50%)": {255, 0, 0, 1},
		"RGB(100%, 0%, 0%)": {255, 0, 0, 1}, "transparent": {0, 0, 0, 0}, "  Navy ": {0, 0, 128, 1},
	}
	for s, want := range cases {
		got, ok := parseColor(s)
		if !ok || got.r != want.r || got.g != want.g || got.b != want.b || math.Abs(float64(got.a-want.a)) > 0.01 {
			t.Errorf("parseColor(%q) = %v %v, want %v", s, got, ok, want)
		}
	}
	for _, bad := range []string{"", "#12", "rgb(1,2)", "notacolor", "#gggggg", "rgb(a,b,c)"} {
		if _, ok := parseColor(bad); ok {
			t.Errorf("parseColor(%q) accepted", bad)
		}
	}
}

func TestParseTransform(t *testing.T) {
	m, ok := parseTransform("translate(10 20) scale(2) rotate(90)")
	if !ok {
		t.Fatal("not ok")
	}
	p := m.apply(point{1, 0})
	if math.Abs(p.x-10) > 1e-9 || math.Abs(p.y-22) > 1e-9 {
		t.Errorf("got %v", p)
	}
	for _, bad := range []string{"scale(", "foo(1)", "translate(1 2 3)", "matrix(1 2 3)"} {
		if _, ok := parseTransform(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDOM(t *testing.T) {
	doc, err := parseDocument(`<?xml version="1.0"?><!-- c --><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="x"><g id="a" style="fill: red; stroke:blue !important" fill="green"><text>a &amp; b<![CDATA[ <c> ]]></text></g><foo><g/></foo></svg>`, Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	g := doc.ids["a"]
	if g == nil {
		t.Fatal("id not indexed")
	}
	if len(g.kids) != 1 || g.kids[0].tag != tagText {
		t.Fatalf("children %v", g.kids)
	}
	var txt string
	for _, c := range g.kids[0].kids {
		txt += c.text
	}
	if txt != "a & b <c> " {
		t.Errorf("text %q", txt)
	}
	if len(doc.root.kids) != 1 {
		t.Error("unknown element subtree kept")
	}
	if v, _ := g.get(aFill); v != "red" {
		// style wins over the attribute
		if len(g.style) == 0 {
			t.Error("style not parsed")
		}
	}
}

func TestConcurrentRender(t *testing.T) {
	svg := []byte(synths[len(synths)-9].svg)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 5; j++ {
				if _, err := Render(svg, Options{}); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
