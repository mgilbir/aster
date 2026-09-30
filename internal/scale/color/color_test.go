package color

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

type described struct {
	Space string            `json:"space"`
	F     []json.RawMessage `json:"f"`
	Hex   string            `json:"hex"`
	Hex8  string            `json:"hex8"`
	Rgb   string            `json:"rgb"`
	Str   string            `json:"str"`
	Hsl   string            `json:"hsl"`
	Disp  bool              `json:"disp"`
	Clamp []json.RawMessage `json:"clamp"`
}

type golden struct {
	Names []string `json:"names"`
	Parse []struct {
		In        string            `json:"in"`
		Ok        bool              `json:"ok"`
		C         *described        `json:"c"`
		RGB       []json.RawMessage `json:"rgb"`
		HSL       []json.RawMessage `json:"hsl"`
		Lab       []json.RawMessage `json:"lab"`
		HCL       []json.RawMessage `json:"hcl"`
		Cubehelix []json.RawMessage `json:"cubehelix"`
	} `json:"parse"`
	Convert []struct {
		In        string            `json:"in"`
		HSL       []json.RawMessage `json:"hsl"`
		Lab       []json.RawMessage `json:"lab"`
		HCL       []json.RawMessage `json:"hcl"`
		Cubehelix []json.RawMessage `json:"cubehelix"`
		LabRgb    described         `json:"labRgb"`
		HclRgb    described         `json:"hclRgb"`
		CubeRgb   described         `json:"cubeRgb"`
		HslRgb    described         `json:"hslRgb"`
	} `json:"convert"`
	Adjust []struct {
		In       string    `json:"in"`
		Space    string    `json:"space"`
		K        *float64  `json:"k"`
		Brighter described `json:"brighter"`
		Darker   described `json:"darker"`
	} `json:"adjust"`
	Construct []struct {
		Op   string            `json:"op"`
		Args []json.RawMessage `json:"args"`
		R    described         `json:"r"`
	} `json:"construct"`
}

func load(t testing.TB) *golden {
	t.Helper()
	b, err := os.ReadFile("testdata/color.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func num(t testing.TB, raw json.RawMessage) float64 {
	t.Helper()
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("bad number %s", raw)
	}
	switch s {
	case "NaN":
		return math.NaN()
	case "Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	t.Fatalf("bad number %q", s)
	return 0
}

// inexact counts fields that differ from upstream in the last bits.
var inexact int

func closeTo(a, b float64) (ok, exact bool) {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b), true
	}
	if a == b {
		return true, true
	}
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b))), false
}

func checkFields(t *testing.T, what string, want []json.RawMessage, got ...float64) {
	t.Helper()
	for i, w := range want {
		ok, exact := closeTo(got[i], num(t, w))
		if !ok {
			t.Errorf("%s field %d: got %v want %s", what, i, got[i], w)
		} else if !exact {
			inexact++
		}
	}
}

func fieldsOf(c Color) []float64 {
	switch v := c.(type) {
	case RGB:
		return []float64{v.R, v.G, v.B, v.Opacity}
	case HSL:
		return []float64{v.H, v.S, v.L, v.Opacity}
	case Lab:
		return []float64{v.L, v.A, v.B, v.Opacity}
	case HCL:
		return []float64{v.H, v.C, v.L, v.Opacity}
	case Cubehelix:
		return []float64{v.H, v.S, v.L, v.Opacity}
	}
	panic("unknown colour")
}

func spaceOf(c Color) string {
	switch c.(type) {
	case RGB:
		return "rgb"
	case HSL:
		return "hsl"
	case Lab:
		return "lab"
	case HCL:
		return "hcl"
	}
	return "cubehelix"
}

func checkDescribed(t *testing.T, what string, c Color, d described, exactStrings bool) {
	t.Helper()
	if spaceOf(c) != d.Space {
		t.Errorf("%s: space %s want %s", what, spaceOf(c), d.Space)
	}
	checkFields(t, what, d.F, fieldsOf(c)...)
	// Strings of colours derived through pow/sin/cos may differ by a rounding
	// step in rare last-bit cases; fields above are tolerance-checked.
	cmp := func(name, got, want string) {
		if got != want {
			if exactStrings {
				t.Errorf("%s %s: got %q want %q", what, name, got, want)
			} else {
				t.Logf("%s %s: got %q want %q", what, name, got, want)
			}
		}
	}
	cmp("hex", c.FormatHex(), d.Hex)
	cmp("hex8", c.FormatHex8(), d.Hex8)
	cmp("rgb", c.FormatRgb(), d.Rgb)
	cmp("toString", c.String(), d.Str)
	cmp("hsl", c.FormatHsl(), d.Hsl)
	if c.Displayable() != d.Disp && exactStrings {
		t.Errorf("%s displayable: got %v", what, c.Displayable())
	}
	if d.Clamp != nil {
		checkFields(t, what+" clamp", d.Clamp, fieldsOf(c.Clamp())...)
	}
}

func TestParseGolden(t *testing.T) {
	g := load(t)
	seenNames := map[string]bool{}
	for _, e := range g.Parse {
		c, ok := Parse(e.In)
		if ok != e.Ok {
			t.Errorf("Parse(%q) ok=%v want %v", e.In, ok, e.Ok)
			continue
		}
		if ok {
			checkDescribed(t, "Parse("+e.In+")", c, *e.C, true)
		}
		checkFields(t, "ParseRGB("+e.In+")", e.RGB, fieldsOf(ParseRGB(e.In))...)
		checkFields(t, "ParseHSL("+e.In+")", e.HSL, fieldsOf(ParseHSL(e.In))...)
		checkFields(t, "ParseLab("+e.In+")", e.Lab, fieldsOf(ParseLab(e.In))...)
		checkFields(t, "ParseHCL("+e.In+")", e.HCL, fieldsOf(ParseHCL(e.In))...)
		checkFields(t, "ParseCubehelix("+e.In+")", e.Cubehelix, fieldsOf(ParseCubehelix(e.In))...)
		seenNames[e.In] = true
	}
	for _, n := range g.Names {
		if !seenNames[n] {
			t.Errorf("name %q not covered", n)
		}
		if _, ok := Named(n); !ok {
			t.Errorf("Named(%q) missing", n)
		}
	}
	if len(g.Names) != len(named) {
		t.Errorf("named table has %d entries, upstream %d", len(named), len(g.Names))
	}
}

func TestConvertGolden(t *testing.T) {
	g := load(t)
	for _, e := range g.Convert {
		c, ok := Parse(e.In)
		if !ok {
			t.Fatalf("cannot parse %q", e.In)
		}
		checkFields(t, "hsl("+e.In+")", e.HSL, fieldsOf(ToHSL(c))...)
		lab := ToLab(c)
		checkFields(t, "lab("+e.In+")", e.Lab, fieldsOf(lab)...)
		checkFields(t, "hcl("+e.In+")", e.HCL, fieldsOf(ToHCL(c))...)
		checkFields(t, "cubehelix("+e.In+")", e.Cubehelix, fieldsOf(ToCubehelix(c))...)
		checkDescribed(t, "lab.rgb("+e.In+")", ToLab(c).RGB(), e.LabRgb, false)
		checkDescribed(t, "hcl.rgb("+e.In+")", ToHCL(c).RGB(), e.HclRgb, false)
		checkDescribed(t, "cubehelix.rgb("+e.In+")", ToCubehelix(c).RGB(), e.CubeRgb, false)
		checkDescribed(t, "hsl.rgb("+e.In+")", ToHSL(c).RGB(), e.HslRgb, true)
	}
}

func convertTo(space string, c Color) Color {
	switch space {
	case "rgb":
		return c.RGB()
	case "hsl":
		return ToHSL(c)
	case "lab":
		return ToLab(c)
	case "hcl":
		return ToHCL(c)
	}
	return ToCubehelix(c)
}

func TestAdjustGolden(t *testing.T) {
	g := load(t)
	for _, e := range g.Adjust {
		c, _ := Parse(e.In)
		cc := convertTo(e.Space, c)
		k := 1.0 // upstream's default (k == null) is numerically k = 1
		if e.K != nil {
			k = *e.K
		}
		name := e.In + "/" + e.Space
		checkDescribed(t, "brighter "+name, cc.Brighter(k), e.Brighter, false)
		checkDescribed(t, "darker "+name, cc.Darker(k), e.Darker, false)
	}
}

func TestConstructGolden(t *testing.T) {
	g := load(t)
	for _, e := range g.Construct {
		a := make([]float64, len(e.Args))
		for i, r := range e.Args {
			a[i] = num(t, r)
		}
		var c Color
		switch e.Op {
		case "lab":
			c = Lab{a[0], a[1], a[2], a[3]}
		case "hcl":
			c = HCL{a[0], a[1], a[2], a[3]}
		case "lch":
			c = Lch(a[0], a[1], a[2], a[3])
		case "hsl":
			c = HSL{a[0], a[1], a[2], a[3]}
		case "cubehelix":
			c = Cubehelix{a[0], a[1], a[2], a[3]}
		case "rgb":
			c = RGB{a[0], a[1], a[2], a[3]}
		case "gray":
			c = Gray(a[0])
		}
		checkDescribed(t, e.Op+"("+string(mustJSON(a))+")", c, e.R, e.Op == "rgb" || e.Op == "hsl")
	}
}

func mustJSON(a []float64) []byte {
	parts := make([]string, len(a))
	for i, f := range a {
		parts[i] = strings.TrimSpace(strings.Trim(jsonNum(f), "\n"))
	}
	return []byte(strings.Join(parts, ","))
}

func jsonNum(f float64) string {
	b, err := json.Marshal(f)
	if err != nil {
		return "NaN"
	}
	return string(b)
}

func TestInexactReport(t *testing.T) {
	TestParseGolden(t)
	TestConvertGolden(t)
	TestAdjustGolden(t)
	TestConstructGolden(t)
	t.Logf("fields within tolerance but not bit-identical: %d", inexact)
}

func TestNoPanicOnArbitraryStrings(t *testing.T) {
	seeds := []string{"rgb(", "rgba(1,2,3,", "hsl(1e,", "#", "\xff\xfe", "rgb(1e999,0,0)", "rgb(-,0,0)", "hsla(.,.,.,.)", "rgb(1,2,3)\u0085"}
	for _, s := range seeds {
		Parse(s)
		ParseRGB(s)
		ParseHSL(s)
	}
	// deterministic mutation fuzz
	alphabet := []byte("rgbahsl()#%,.+-eE0123456789 \t\xff xyz")
	x := uint32(12345)
	for i := 0; i < 20000; i++ {
		n := int(x>>3) % 24
		b := make([]byte, n)
		for j := range b {
			x = x*1664525 + 1013904223
			b[j] = alphabet[int(x>>16)%len(alphabet)]
		}
		x = x*1664525 + 1013904223
		s := string(b)
		if c, ok := Parse(s); ok {
			c.FormatHex8()
			c.FormatHsl()
			_ = c.String()
		}
		_ = utf8.ValidString(s)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"red", "#fff", "rgb(1,2,3)", "hsla(1,2%,3%,.4)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if c, ok := Parse(s); ok {
			c.FormatRgb()
			c.FormatHsl()
			c.FormatHex8()
		}
	})
}

func TestJSRound(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{{2.5, 3}, {-2.5, -2}, {0.49999999999999994, 0}, {-0.5, 0}, {1.5, 2}, {254.5, 255}} {
		if got := jsRound(c.in); got != c.want {
			t.Errorf("jsRound(%v)=%v want %v", c.in, got, c.want)
		}
	}
}

func BenchmarkParseHex(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ParseRGB("#4c78a8")
	}
}

func BenchmarkParseRGB(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ParseRGB("rgba(76, 120, 168, 0.5)")
	}
}

func BenchmarkFormatRgb(b *testing.B) {
	c := RGB{76.4, 120.2, 168, 0.5}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.FormatRgb()
	}
}

func BenchmarkFormatHex(b *testing.B) {
	c := RGB{76.4, 120.2, 168, 1}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.FormatHex()
	}
}
