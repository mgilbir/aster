package svg

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// estimateMeasurer measures like vega's fallback estimate, from the CSS font
// string alone, so a Measurer-driven run must equal a nil-Measurer run.
type estimateMeasurer struct{ calls int }

func (m *estimateMeasurer) MeasureText(text, cssFont string) float64 {
	m.calls++
	i := strings.Index(cssFont, "px ")
	j := i
	for j > 0 && (cssFont[j-1] == '.' || cssFont[j-1] >= '0' && cssFont[j-1] <= '9') {
		j--
	}
	size, _ := strconv.ParseFloat(cssFont[j:i], 64)
	n := 0
	for _, r := range text {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return float64(int32(0.8 * float64(n) * size))
}

func TestMeasurerMatchesEstimate(t *testing.T) {
	m := &estimateMeasurer{}
	for _, e := range loadCorpus(t) {
		if _, skip := rawValueSpecs[e.Name]; skip {
			continue
		}
		sg, err := scene.FromJSON(e.Scene)
		if err != nil {
			t.Fatal(err)
		}
		opt := e.options()
		opt.Measurer = m
		got, err := Render(context.Background(), sg, opt)
		if err != nil {
			t.Fatal(err)
		}
		if got != e.SVG {
			t.Fatalf("%s: measurer-driven SVG differs %s", e.Name, firstDiff(got, e.SVG))
		}
	}
	if m.calls == 0 {
		t.Fatal("measurer never called")
	}
}

func TestNilAndEmpty(t *testing.T) {
	if _, err := Render(context.Background(), nil, Options{}); err == nil {
		t.Fatal("nil scenegraph should fail")
	}
	sg := scene.New()
	got, err := Render(context.Background(), sg, Options{Width: 10, Height: 20, Scale: 2, Origin: [2]float64{1.5, 2}, Background: "#fff"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" version="1.1" class="marks" width="20" height="40" viewBox="0 0 10 20"><rect width="10" height="20" fill="#fff"/><g fill="none" stroke-miterlimit="4" transform="translate(1.5,2)"><g class="mark-group role-frame root" role="graphics-object" aria-roledescription="group mark container"><g transform="translate(0,0)"><path class="background" aria-hidden="true" d="M0,0h0v0h0Z"/><g/><path class="foreground" aria-hidden="true" d="" display="none"/></g></g></g></svg>`
	if got != want {
		t.Fatalf("empty scene:\n got %s\nwant %s", got, want)
	}
}

func TestEscaping(t *testing.T) {
	sg := scene.New()
	m := sg.AddMark(scene.MarkDef{Type: scene.MarkText, Name: `n"<&>`, Role: "r\t"}, nil, -1)
	it := &scene.Item{Mark: m, Description: "x\"y<z>&\r\n\t", Fill: scene.Color("#000")}
	_, _ = it.Set("text", jsval.Str("a&b<c>d\"e\tf\n"))
	_, _ = it.Set("font", jsval.Str(`"A B", 'c'`))
	m.Items = append(m.Items, it)
	got, err := Render(context.Background(), sg, Options{Width: 1, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="mark-text role-r&#x9; n&quot;&lt;&amp;&gt;"`,
		`aria-label="x&quot;y&lt;z&gt;&amp;&#xD;&#xA;&#x9;"`,
		`font-family="&quot;A B&quot;, 'c'"`,
		`>a&amp;b&lt;c&gt;d"e` + "\t" + `f<`, // trim removes the trailing newline; text keeps quotes and tabs
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestCancelAndDepth(t *testing.T) {
	sg := scene.New()
	m := sg.AddMark(scene.MarkDef{Type: scene.MarkRect}, nil, -1)
	for i := 0; i < 5000; i++ {
		m.Items = append(m.Items, &scene.Item{Mark: m, Width: scene.N(1), Height: scene.N(1)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render(ctx, sg, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}

	deep := scene.New()
	g := deep.RootItem()
	for i := 0; i < scene.MaxDepth+10; i++ {
		child := deep.AddMark(scene.MarkDef{Type: scene.MarkGroup}, g, -1)
		item := &scene.Item{Mark: child}
		child.Items = append(child.Items, item)
		g = item
	}
	if _, err := Render(context.Background(), deep, Options{}); !errors.Is(err, scene.ErrTooDeep) {
		t.Fatalf("want ErrTooDeep, got %v", err)
	}
}

func TestSanitizeURL(t *testing.T) {
	cases := []struct {
		in, out string
		ok      bool
	}{
		{"https://x.org/a b", "https://x.org/a b", true},
		{"/rel", "/rel", true},
		{"rel/path", "rel/path", true},
		{"//host/p", "http://host/p", true},
		{"file:///etc/x", "/etc/x", true},
		{"javascript:alert(1)", "", false},
		{"  JaVaScRiPt:alert(1)", "", false},
		{"java\tscript:alert(1)", "", false},
		{"data:text/html,x", "data:text/html,x", true},
		{"mailto:a@b", "mailto:a@b", true},
		{"#frag", "#frag", true},
		{"", "", false},
		{"vbscript:x", "", false},
	}
	for _, c := range cases {
		got, ok := SanitizeURL(c.in, URLOptions{})
		if ok != c.ok || (ok && got != c.out) {
			t.Errorf("SanitizeURL(%q) = %q, %v; want %q, %v", c.in, got, ok, c.out, c.ok)
		}
	}
	if got, _ := SanitizeURL("a/b", URLOptions{BaseURL: "https://h/base"}); got != "https://h/base/a/b" {
		t.Errorf("baseURL: %q", got)
	}
	if got, _ := SanitizeURL("//h/p", URLOptions{DefaultProtocol: "https"}); got != "https://h/p" {
		t.Errorf("defaultProtocol: %q", got)
	}
}

// bigScene builds a chart-like scene of n items spread over rect, symbol, text
// and line marks.
func bigScene(n int) *scene.Scenegraph {
	sg := scene.New()
	root := sg.RootItem()
	root.Width, root.Height = scene.N(800), scene.N(600)
	kinds := []scene.MarkType{scene.MarkRect, scene.MarkSymbol, scene.MarkText, scene.MarkLine}
	per := n / len(kinds)
	for _, k := range kinds {
		m := sg.AddMark(scene.MarkDef{Type: k, Role: "mark"}, nil, -1)
		for i := 0; i < per; i++ {
			it := &scene.Item{
				Mark: m, X: scene.N(float64(i%100) * 8.123), Y: scene.N(float64(i/100) * 5.0071),
				Fill: scene.Color("#4c78a8"), Opacity: scene.N(0.85),
			}
			switch k {
			case scene.MarkRect:
				it.Width, it.Height = scene.N(6.5), scene.N(3.25)
				it.Stroke, it.StrokeWidth = scene.Color("#222"), scene.N(0.5)
			case scene.MarkSymbol:
				it.Size = scene.N(30)
				it.Shape.Name = "diamond"
			case scene.MarkText:
				_, _ = it.Set("text", jsval.Str("label "+strconv.Itoa(i)))
				_, _ = it.Set("fontSize", jsval.Num(10))
				it.Align = "center"
				it.Baseline = "middle"
				_, _ = it.Set("limit", jsval.Num(40))
			case scene.MarkLine:
				it.Stroke = scene.Color("#c33")
			}
			m.Items = append(m.Items, it)
		}
	}
	return sg
}

func TestBigSceneRenders(t *testing.T) {
	sg := bigScene(10000)
	got, err := Render(context.Background(), sg, Options{Width: 800, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "<path"); n < 5000 {
		t.Fatalf("expected many paths, got %d", n)
	}
}

func BenchmarkRender10k(b *testing.B) {
	sg := bigScene(10000)
	opt := Options{Width: 800, Height: 600}
	var out []byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		out, err = AppendSVG(context.Background(), out[:0], sg, opt)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(len(out)))
}

func BenchmarkCorpus(b *testing.B) {
	corpus := loadCorpus(b)
	type prepared struct {
		sg  *scene.Scenegraph
		opt Options
	}
	var ps []prepared
	for _, e := range corpus {
		sg, err := scene.FromJSON(e.Scene)
		if err != nil {
			b.Fatal(err)
		}
		ps = append(ps, prepared{sg, e.options()})
	}
	b.ReportAllocs()
	b.ResetTimer()
	var buf []byte
	for i := 0; i < b.N; i++ {
		for _, p := range ps {
			buf, _ = AppendSVG(context.Background(), buf[:0], p.sg, p.opt)
		}
	}
}

func ExampleRender() {
	sg := scene.New()
	m := sg.AddMark(scene.MarkDef{Type: scene.MarkRect, Role: "mark"}, nil, -1)
	m.Items = append(m.Items, &scene.Item{Mark: m, X: scene.N(1), Y: scene.N(2), Width: scene.N(3), Height: scene.N(4), Fill: scene.Color("red")})
	out, _ := Render(context.Background(), sg, Options{Width: 10, Height: 10})
	fmt.Println(strings.Contains(out, `<path d="M1,2h3v4h-3Z" fill="red"/>`), math.Pi > 3)
	// Output: true true
}
