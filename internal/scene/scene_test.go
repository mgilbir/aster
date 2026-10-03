package scene

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

func TestNum(t *testing.T) {
	var zero Num
	if zero.Set() || !math.IsNaN(zero.Val()) || zero.Zero() != 0 || zero.Or(7) != 7 || zero.Truthy() {
		t.Fatal("zero Num must be unset")
	}
	for _, v := range []float64{0, math.Copysign(0, -1), 1, -2.5, math.Inf(1), math.Inf(-1), math.MaxFloat64, math.SmallestNonzeroFloat64} {
		n := N(v)
		if !n.Set() || n.Val() != v || math.Signbit(n.Val()) != math.Signbit(v) {
			t.Errorf("N(%v) round trip: %v", v, n.Val())
		}
	}
	nan := N(math.NaN())
	if !nan.Set() || !math.IsNaN(nan.Val()) || nan.Zero() != 0 || nan.Truthy() || !math.IsNaN(nan.Or(3)) {
		t.Fatal("explicit NaN must be set and read NaN")
	}
	// A NaN carrying the unset payload cannot turn a value into "unset".
	odd := math.Float64frombits(unsetBits)
	if !N(odd).Set() {
		t.Fatal("N must canonicalise NaN payloads")
	}
	if !N(3).Truthy() || N(0).Truthy() {
		t.Fatal("Truthy")
	}
}

func TestPaint(t *testing.T) {
	var p Paint
	if p.Present() || p.IsNull() || p.Truthy() {
		t.Fatal("zero paint is unset")
	}
	if !NullPaint().IsNull() || NullPaint().Present() {
		t.Fatal("null paint")
	}
	if !Color("").Present() || Color("").Truthy() || !Color("red").Truthy() {
		t.Fatal("colour paint")
	}
	g := &Gradient{}
	if GradientPaint(g).Gradient() != g || !GradientPaint(g).Truthy() || GradientPaint(nil).Present() {
		t.Fatal("gradient paint")
	}
}

func TestAppendNumberMatchesJS(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	check := func(f float64) {
		t.Helper()
		got := string(AppendNumber(nil, f))
		want := jsval.JSNumberString(f)
		if got != want {
			t.Fatalf("AppendNumber(%v) = %q, want %q", f, got, want)
		}
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 0.5, 1e21, 1e-7, 123456789012345680000, 1e-6, 5e-324, math.MaxFloat64,
		math.Inf(1), math.Inf(-1), math.NaN(), 100, 0.000001, 1.5e300, 4.35, 2.675, 1 / 3.0} {
		check(f)
	}
	for i := 0; i < 20000; i++ {
		check(r.NormFloat64() * math.Pow(10, float64(r.Intn(40)-20)))
		check(float64(r.Intn(100000)) / 1000)
		check(math.Float64frombits(r.Uint64()))
	}
}

func TestStringPath(t *testing.T) {
	var p StringPath
	p.MoveTo(0, 0)
	p.LineTo(1.5, -2)
	p.QuadraticCurveTo(1, 2, 3, 4)
	p.BezierCurveTo(1, 2, 3, 4, 5, 6)
	p.ClosePath()
	if got, want := p.String(), "M0,0L1.5,-2Q1,2,3,4C1,2,3,4,5,6Z"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	p.Reset()
	p.Rect(1, 2, 3, 4)
	if got, want := p.String(), "M1,2h3v4h-3Z"; got != want {
		t.Fatalf("rect: %s", got)
	}
	p.Reset()
	p.Arc(0, 0, 5, 0, tau, false) // full circle: two half arcs
	if got, want := p.String(), "M5,0A5,5,0,1,1,-5,0A5,5,0,1,1,5,0"; got != want {
		t.Fatalf("circle: %s", got)
	}
	p.Reset()
	p.MoveTo(0, 0)
	p.Arc(0, 0, -1, 0, 1, false)
	if p.Err() == nil {
		t.Fatal("negative radius must record an error")
	}
	// Rounding to three digits like d3-shape's generators.
	p.Reset()
	p.SetDigits(3)
	p.MoveTo(1.23456, -0.0004)
	p.LineTo(2.0005, 10)
	if got, want := p.String(), "M1.235,0L2.001,10"; got != want {
		t.Fatalf("rounded: %s", got)
	}
}

func TestParseAndRenderPath(t *testing.T) {
	cmds, err := ParsePath("M0,0 L10 10 20 20 h5 v-5 z m1,1 2,2 A5,5 0 1,0 10,10a1 1 0 0110 10")
	if err != nil {
		t.Fatal(err)
	}
	var letters strings.Builder
	for _, c := range cmds {
		letters.WriteByte(c.Cmd)
	}
	// Implicit repeats: `L10 10 20 20` is two L; `m1,1 2,2` becomes m then l.
	if got, want := letters.String(), "MLLhvzmlAa"; got != want {
		t.Fatalf("commands %s want %s", got, want)
	}
	var sp StringPath
	if err := RenderPath(&sp, cmds, 100, 200, 1, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sp.String(), "M100,200L110,210L120,220L125,220L125,215Z") {
		t.Fatalf("render: %s", sp.String())
	}
	for _, bad := range []string{"M", "M1", "L1,2,3", "A1,1,0,2,0,5,5", "M1,x"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("ParsePath(%q) should fail", bad)
		}
	}
	// Leading junk is skipped, an empty path is fine.
	if c, err := ParsePath("  ??? M1,1"); err != nil || len(c) != 1 {
		t.Errorf("junk prefix: %v %v", c, err)
	}
	if c, err := ParsePath(""); err != nil || len(c) != 0 {
		t.Errorf("empty: %v %v", c, err)
	}
	// A path starting with a smooth quadratic has no previous command.
	tc, _ := ParsePath("t1,1")
	if err := RenderPath(&sp, tc, 0, 0, 1, 1); err == nil {
		t.Error("leading t must fail like upstream's TypeError")
	}
}

func TestTruncate(t *testing.T) {
	it := &Item{text: &textAttrs{FontSize: N(10), Limit: N(30)}}
	m := Metrics{}
	// estimateWidth = ~~(0.8*len*10): 8px per unit; limit 30, ellipsis costs 8.
	if got := m.TextValue(it, "abcdefgh"); got != "ab…" {
		t.Errorf("ltr: %q", got)
	}
	it.textW().Dir = "rtl"
	if got := m.TextValue(it, "abcdefgh"); got != "…gh" {
		t.Errorf("rtl: %q", got)
	}
	it.textW().Dir = ""
	it.textW().Ellipsis = "."
	if got := m.TextValue(it, "  abcdefgh "); got != "ab." {
		t.Errorf("custom ellipsis: %q", got)
	}
	it.textW().Limit = N(0)
	if got := m.TextValue(it, "  keep  "); got != "keep" {
		t.Errorf("no limit still trims: %q", got)
	}
	// Non-BMP characters count as two UTF-16 units and are never split into
	// invalid UTF-8.
	it.textW().Limit = N(40)
	it.textW().Ellipsis = ""
	// 8 UTF-16 units of width 8 each; the ellipsis leaves 32: three units fit,
	// which splits the second emoji, as JavaScript's slice does.
	got := m.TextValue(it, "\U0001F600\U0001F600\U0001F600\U0001F600")
	if got != "\U0001F600\uFFFD…" {
		t.Errorf("surrogate split: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8: %q", got)
	}
}

func TestCSSFont(t *testing.T) {
	it := &Item{text: &textAttrs{FontStyle: "italic", FontVariant: "small-caps", FontWeight: "bold", FontSize: N(10.5), Font: `"A B"`}}
	if got, want := CSSFont(it, false), `italic small-caps bold 10.5px "A B"`; got != want {
		t.Errorf("%s", got)
	}
	if got, want := CSSFont(it, true), `italic small-caps bold 10.5px 'A B'`; got != want {
		t.Errorf("%s", got)
	}
	if got, want := CSSFont(&Item{}, false), "11px sans-serif"; got != want {
		t.Errorf("%s", got)
	}
}

func TestBaselineOffset(t *testing.T) {
	cases := map[string]float64{"top": 8, "middle": 3, "bottom": -2, "line-top": 9, "line-bottom": -3, "": 0, "alphabetic": 0}
	for b, want := range cases {
		it := &Item{Baseline: b, text: &textAttrs{FontSize: N(10)}}
		if got := BaselineOffset(it); got != want {
			t.Errorf("baseline %q = %v want %v", b, got, want)
		}
	}
}

func TestBoundsBasics(t *testing.T) {
	b := NewBounds()
	if !b.Empty() {
		t.Fatal("fresh bounds are empty")
	}
	b.Add(1, 2).Add(-3, 4)
	if b != (Bounds{-3, 2, 1, 4}) {
		t.Fatalf("%v", b)
	}
	c := Bounds{0, 0, 2, 2}
	if !b.Intersects(&c) || b.Encloses(&c) || !b.Contains(0, 3) || b.Contains(2, 3) {
		t.Fatal("predicates")
	}
	b.Rotate(math.Pi/2, 0, 0)
	if math.Abs(b.X1-(-4)) > 1e-12 || math.Abs(b.X2-(-2)) > 1e-12 {
		t.Fatalf("rotate %v", b)
	}
}

func TestSymbolShapesAllDraw(t *testing.T) {
	var sp StringPath
	for _, name := range []string{"circle", "square", "cross", "diamond", "triangle-up", "triangle-down", "triangle-left", "triangle-right", "triangle", "arrow", "wedge", "stroke", "M0,0L1,1", ""} {
		sp.Reset()
		if err := Symbol(&sp, &Item{Shape: Shape{Name: name}, Size: N(100)}); err != nil || sp.Len() == 0 {
			t.Errorf("%q: %v %q", name, err, sp.String())
		}
	}
	if err := Symbol(&sp, &Item{Shape: Shape{Name: "M0"}}); err == nil {
		t.Error("invalid custom path must fail")
	}
}

func TestLineAreaErrors(t *testing.T) {
	items := []*Item{{line: &lineAttrs{Interpolate: "no-such"}, X: N(1)}, {X: N(2)}}
	var sp StringPath
	if err := Line(&sp, items); err != ErrUnknownInterpolate {
		t.Errorf("line: %v", err)
	}
	items[0].lineW().Interpolate = "bundle"
	if err := Area(&sp, items); err != ErrCurveNoArea {
		t.Errorf("bundle area: %v", err)
	}
	if err := Line(&sp, nil); err != nil {
		t.Error(err)
	}
}

// `item.interpolate || 'linear'` and `entry[orient || 'vertical']`: falsy
// values of any type (NaN, 0, false, null) mean unset, not an unknown curve.
func TestFalsyInterpolateAndOrientAreUnset(t *testing.T) {
	for _, v := range []jsval.Value{jsval.Num(math.NaN()), jsval.Num(0), jsval.Bool(false), jsval.Null, jsval.Str("")} {
		it := &Item{}
		for _, k := range []string{"interpolate", "orient"} {
			if _, err := it.Set(k, v); err != nil {
				t.Fatal(err)
			}
		}
		var sp StringPath
		if err := Line(&sp, []*Item{it, it}); err != nil {
			t.Errorf("%v: %v", v, err)
		}
	}
	it := &Item{}
	if _, err := it.Set("interpolate", jsval.Num(1)); err != nil {
		t.Fatal(err)
	}
	var sp StringPath
	if err := Line(&sp, []*Item{it}); err != ErrUnknownInterpolate {
		t.Errorf("truthy non-string interpolate: %v", err)
	}
}

func TestOrdered(t *testing.T) {
	m := &Mark{}
	for _, z := range []float64{0, 3, 1, 0, -2, 1} {
		it := &Item{Mark: m}
		if z != 0 {
			it.Zindex = N(z)
		}
		m.Items = append(m.Items, it)
	}
	got := m.Ordered()
	var idx []int
	for _, it := range got {
		for i, x := range m.Items {
			if x == it {
				idx = append(idx, i)
			}
		}
	}
	want := []int{0, 3, 4, 2, 5, 1}
	for i := range want {
		if idx[i] != want[i] {
			t.Fatalf("order %v want %v", idx, want)
		}
	}
}

func TestFromJSONRejectsGarbage(t *testing.T) {
	for _, in := range []string{
		``, `null`, `[]`, `{}`, `{"marktype":"nope"}`, `{"marktype":"rect","items":[1]}`,
		`{"marktype":"group","items":[{"items":[{"marktype":"rect","items":[{"x":"abc","fill":{"gradient":"linear"}}]}]}]}`,
		`{"marktype":"path","items":[{"path":"M1"}]}`,
	} {
		sg, err := FromJSON([]byte(in))
		if err == nil && sg != nil {
			// Accepted inputs must still bound and render without panicking.
			_ = NewBounder(nil).BoundTree(sg.Root)
		}
	}
	// Bounded recursion.
	deep := strings.Repeat(`{"marktype":"group","items":[{"items":[`, 300) + strings.Repeat(`]}]}`, 300)
	if _, err := FromJSON([]byte(deep)); err == nil {
		t.Error("deeply nested scene must be rejected")
	}
}

func TestBoundClipFunc(t *testing.T) {
	sg := New()
	root := sg.RootItem()
	root.Width, root.Height = N(100), N(50)
	m := sg.AddMark(MarkDef{Type: MarkRect, Clip: true}, nil, -1)
	m.Items = append(m.Items, &Item{Mark: m, X: N(-10), Y: N(-10), Width: N(500), Height: N(500)})
	if err := NewBounder(nil).BoundTree(sg.Root); err != nil {
		t.Fatal(err)
	}
	if m.Bounds != (Bounds{0, 0, 100, 50}) {
		t.Fatalf("clipped bounds %v", m.Bounds)
	}
	m.ClipPath = func(ctx PathContext) string {
		if ctx != nil {
			ctx.Rect(10, 10, 20, 20)
		}
		return ""
	}
	if err := NewBounder(nil).BoundMark(m); err != nil {
		t.Fatal(err)
	}
	if m.Bounds != (Bounds{10, 10, 30, 30}) {
		t.Fatalf("clip path bounds %v", m.Bounds)
	}
}

func BenchmarkBoundTree(b *testing.B) {
	corpus := loadCorpus(b)
	var roots []*Mark
	for _, e := range corpus {
		sg, err := FromJSON(e.Scene)
		if err != nil {
			b.Fatal(err)
		}
		roots = append(roots, sg.Root)
	}
	bd := NewBounder(nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, r := range roots {
			_ = bd.BoundTree(r)
		}
	}
}

func BenchmarkPaths(b *testing.B) {
	items := make([]*Item, 1000)
	for i := range items {
		items[i] = &Item{X: N(float64(i) * 1.37), Y: N(float64(i%17) * 3.1), line: &lineAttrs{Interpolate: "monotone"}, Height: N(20), geom: &geomAttrs{StartAngle: N(float64(i) * 0.01), EndAngle: N(float64(i)*0.01 + 1), OuterRadius: N(30), InnerRadius: N(10), PadAngle: N(0.02), CornerRadius: N(3)}, Size: N(100)}
	}
	var sp StringPath
	b.Run("line-monotone", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sp.Reset()
			sp.SetDigits(3)
			_ = Line(&sp, items)
		}
	})
	b.Run("area-basis", func(b *testing.B) {
		for _, it := range items {
			it.lineW().Interpolate = "basis"
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sp.Reset()
			sp.SetDigits(3)
			_ = Area(&sp, items)
		}
	})
	b.Run("arc", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, it := range items {
				sp.Reset()
				sp.SetDigits(3)
				_ = Arc(&sp, it)
			}
		}
	})
	b.Run("symbol", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, it := range items {
				sp.Reset()
				sp.SetDigits(3)
				_ = Symbol(&sp, it)
			}
		}
	})
}

func TestItemSetGet(t *testing.T) {
	it := &Item{}
	set := func(k string, v jsval.Value) {
		t.Helper()
		if known, err := it.Set(k, v); err != nil || !known {
			t.Fatalf("Set(%s): known=%v err=%v", k, known, err)
		}
	}
	set("x", jsval.Str("12.5"))
	set("y", jsval.Null)
	set("fill", jsval.Str("red"))
	set("stroke", jsval.Null)
	set("strokeDash", jsval.ArrOf(jsval.Num(4), jsval.Num(2)))
	set("aria", jsval.False)
	set("text", jsval.ArrOf(jsval.Str("a"), jsval.Str("b")))
	set("fontWeight", jsval.Num(700))
	set("clip", jsval.True)
	set("fill", jsval.Obj(jsval.ObjectOf("gradient", jsval.Str("radial"), "stops", jsval.ArrOf(jsval.Obj(jsval.ObjectOf("offset", jsval.Num(0), "color", jsval.Str("#fff")))))))
	if known, _ := it.Set("madeUp", jsval.Num(1)); known {
		t.Error("unknown property reported as known")
	}
	set("mark", jsval.Undefined)

	if it.X.Val() != 12.5 || it.Y.Set() || !it.Stroke.IsNull() || !it.Aria.IsFalse() || it.FontWeight() != "700" || !it.Clip.IsTrue() {
		t.Errorf("unexpected item state: %+v", it)
	}
	if g := it.Fill.Gradient(); g == nil || !g.Radial || len(g.Stops) != 1 {
		t.Errorf("gradient: %+v", it.Fill)
	}
	// A string given for a numeric property reads back as the string, as a
	// JavaScript property would, while the number it converts to drives layout.
	if v := it.Get("x"); !v.IsStr() || v.StrValue() != "12.5" {
		t.Errorf("Get x = %v", v)
	}
	if !it.Get("y").IsUndefined() || !it.Get("stroke").IsNull() || it.Get("madeUp").NumValue() != 1 {
		t.Error("Get semantics")
	}
	if d := it.Get("strokeDash"); d.Len() != 2 {
		t.Errorf("Get strokeDash = %v", d)
	}
	if fill := it.Get("fill"); fill.Get("gradient").AsString() != "radial" {
		t.Errorf("Get fill = %v", fill)
	}
}

// A word given where a number goes is truthy, and converts to NaN: upstream
// draws a rectangle with a corner radius of "round" as a path of NaN corners
// and reads a width of "wide" as NaN where a numeric NaN would read as 0.
func TestWordsWhereNumbersGo(t *testing.T) {
	var it Item
	for k, v := range map[string]jsval.Value{
		"width": jsval.Num(40), "height": jsval.Num(20), "cornerRadius": jsval.Str("round"),
	} {
		if _, err := it.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if !it.HasCornerRadius() {
		t.Error(`cornerRadius "round" is truthy`)
	}
	var p StringPath
	Rectangle(&p, &it)
	if got := p.String(); !strings.HasPrefix(got, "MNaN,0LNaN,0C") {
		t.Errorf("path %s, want one of NaN corners", got)
	}

	var plain Item
	plain.Set("cornerRadius", jsval.Num(math.NaN()))
	if plain.HasCornerRadius() {
		t.Error("a numeric NaN radius is falsy")
	}

	var w Item
	w.Set("width", jsval.Str("wide"))
	if got := w.OrZero("width"); !math.IsNaN(got) {
		t.Errorf(`width "wide" reads %v, want NaN`, got)
	}
	w.Set("width", jsval.Num(math.NaN()))
	if got := w.OrZero("width"); got != 0 {
		t.Errorf("width NaN reads %v, want 0", got)
	}
	w.Set("width", jsval.Str(""))
	if got := w.OrZero("width"); got != 0 {
		t.Errorf(`width "" reads %v, want 0`, got)
	}
}
