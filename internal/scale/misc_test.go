package scale

import (
	"math"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

func TestRegistryMetadata(t *testing.T) {
	type flags struct{ cont, disc, dz, interp, log, temporal, quantile bool }
	want := map[string]flags{
		"identity":          {},
		"linear":            {cont: true},
		"log":               {cont: true, log: true},
		"pow":               {cont: true},
		"sqrt":              {cont: true},
		"symlog":            {cont: true},
		"time":              {cont: true, temporal: true},
		"utc":               {cont: true, temporal: true},
		"sequential":        {cont: true, interp: true},
		"sequential-linear": {cont: true, interp: true},
		"sequential-log":    {cont: true, interp: true, log: true},
		"sequential-pow":    {cont: true, interp: true},
		"sequential-sqrt":   {cont: true, interp: true},
		"sequential-symlog": {cont: true, interp: true},
		"diverging-linear":  {cont: true, interp: true},
		"diverging-log":     {cont: true, interp: true, log: true},
		"diverging-pow":     {cont: true, interp: true},
		"diverging-sqrt":    {cont: true, interp: true},
		"diverging-symlog":  {cont: true, interp: true},
		"quantile":          {dz: true, quantile: true},
		"quantize":          {dz: true},
		"threshold":         {dz: true},
		"bin-ordinal":       {disc: true, dz: true},
		"ordinal":           {disc: true},
		"band":              {disc: true},
		"point":             {disc: true},
	}
	for typ, w := range want {
		if !IsValidScaleType(typ) {
			t.Errorf("%s not registered", typ)
			continue
		}
		got := flags{IsContinuous(typ), IsDiscrete(typ), IsDiscretizing(typ), IsInterpolating(typ), IsLogarithmic(typ), IsTemporal(typ), IsQuantile(typ)}
		if got != w {
			t.Errorf("%s metadata = %+v, want %+v", typ, got, w)
		}
		s, ok := New(typ)
		if !ok || s.Type() != typ {
			t.Errorf("New(%s) = %v, %v", typ, s, ok)
		}
		if c := ScaleCopy(s); c.Type() != typ {
			t.Errorf("copy of %s lost its type: %s", typ, c.Type())
		}
	}
	if len(registry) != len(want) {
		t.Errorf("registry has %d types, want %d", len(registry), len(want))
	}
	if IsValidScaleType("nope") || IsContinuous("nope") {
		t.Error("unknown type must not be valid")
	}
	if _, ok := New("nope"); ok {
		t.Error("New of an unknown type succeeded")
	}
}

func TestContinuousFloatPaths(t *testing.T) {
	s := NewLinear()
	s.SetDomain(nums(0, 10))
	s.SetRange(nums(100, 200))
	for _, x := range []float64{-5, 0, 2.5, 10, 30} {
		if a, b := s.ApplyFloat(x), s.Apply(jsval.Num(x)).NumValue(); a != b {
			t.Errorf("ApplyFloat(%v) = %v, Apply = %v", x, a, b)
		}
	}
	if got := s.InvertNumber(150); got != 5 {
		t.Errorf("InvertNumber = %v", got)
	}
	if !math.IsNaN(s.ApplyFloat(math.NaN())) {
		t.Error("NaN in, NaN out")
	}
	if d := s.DomainNumbers(); len(d) != 2 || d[1] != 10 {
		t.Errorf("DomainNumbers = %v", d)
	}
	s.SetUnknown(jsval.Num(-1))
	if s.ApplyFloat(math.NaN()) != -1 || s.Unknown().NumValue() != -1 {
		t.Error("unknown value")
	}
	if s.Interpolate() == nil {
		t.Error("default interpolate")
	}
	s.SetRangeRound(nums(0, 10))
	if got := s.ApplyFloat(3.3); got != 3 {
		t.Errorf("rangeRound: %v", got)
	}
}

func TestBandAccessors(t *testing.T) {
	b := NewBand()
	b.SetDomain([]jsval.Value{jsval.Str("a"), jsval.Str("b"), jsval.Str("c")})
	b.SetRange(nums(0, 90))
	b.SetPaddingInner(0.5)
	b.SetPaddingOuter(0.25)
	if b.PaddingInner() != 0.5 || b.PaddingOuter() != 0.25 || b.IsPoint() {
		t.Error("padding accessors")
	}
	if p := b.Position(jsval.Str("b")); math.IsNaN(p) || b.Apply(jsval.Str("b")).NumValue() != p {
		t.Errorf("Position = %v", p)
	}
	if !math.IsNaN(b.Position(jsval.Str("zz"))) {
		t.Error("Position of unknown value is NaN")
	}
	p := NewPoint()
	p.SetPadding(0.5)
	if !p.IsPoint() || p.Padding() != 0.5 || p.PaddingInner() != 1 || p.Bandwidth() != 0 {
		t.Errorf("point: padding=%v inner=%v bw=%v", p.Padding(), p.PaddingInner(), p.Bandwidth())
	}
	if Set(p, "paddingInner", jsval.Num(0.3)) {
		t.Error("point scales have no paddingInner setter")
	}
	if !Set(b, "paddingInner", jsval.Num(0.3)) || b.PaddingInner() != 0.3 {
		t.Error("band paddingInner setter")
	}
	if BandSpace(0, 0.5, 0.5) != 0 || BandSpace(3, 5, 0) != 1 || BandSpace(4, 0.5, 1) != 5.5 {
		t.Error("BandSpace")
	}
}

func TestSetGet(t *testing.T) {
	lin := NewLinear()
	if Set(lin, "base", jsval.Num(2)) || Set(lin, "exponent", jsval.Num(2)) || Set(lin, "constant", jsval.Num(2)) {
		t.Error("linear scales lack base/exponent/constant")
	}
	if Set(lin, "padding", jsval.Num(1)) || Set(lin, "bogus", jsval.Num(1)) {
		t.Error("unsupported setters must report false")
	}
	lg := NewLog()
	if !Set(lg, "base", jsval.Num(2)) {
		t.Fatal("log base")
	}
	if v, ok := Get(lg, "base"); !ok || v.NumValue() != 2 {
		t.Errorf("Get base = %v %v", v, ok)
	}
	if _, ok := Get(lin, "base"); ok {
		t.Error("linear has no base getter")
	}
	if !Set(lin, "clamp", jsval.True) {
		t.Fatal("clamp")
	}
	if v, ok := Get(lin, "clamp"); !ok || !v.BoolValue() {
		t.Error("Get clamp")
	}
	if v, ok := Get(NewSequentialSqrt(), "exponent"); !ok || v.NumValue() != 0.5 {
		t.Errorf("sequential-sqrt exponent = %v %v", v, ok)
	}
	if v, ok := Get(NewDivergingSymlog(), "constant"); !ok || v.NumValue() != 1 {
		t.Errorf("diverging-symlog constant = %v %v", v, ok)
	}
}

func TestSequentialDivergingAccessors(t *testing.T) {
	s := NewSequential()
	s.SetDomain(nums(0, 10))
	s.SetRange([]jsval.Value{jsval.Str("red"), jsval.Str("blue")})
	if f := s.Fraction(5); f != 0.5 {
		t.Errorf("Fraction = %v", f)
	}
	if got := s.Apply(jsval.Num(0)).StrValue(); got != "rgb(255, 0, 0)" {
		t.Errorf("Apply(0) = %q", got)
	}
	if r := s.Range(); r[0].StrValue() != "rgb(255, 0, 0)" || r[1].StrValue() != "rgb(0, 0, 255)" {
		t.Errorf("Range = %v", r)
	}
	if s.Interpolator() == nil || len(s.DomainNumbers()) != 2 {
		t.Error("accessors")
	}
	s.SetUnknown(jsval.Str("?"))
	if s.Unknown().StrValue() != "?" || s.Apply(jsval.Null).StrValue() != "?" {
		t.Error("unknown")
	}
	s.SetClamp(true)
	if !s.Clamp() || s.Apply(jsval.Num(100)).StrValue() != "rgb(0, 0, 255)" {
		t.Error("clamp")
	}
	d := NewDiverging()
	d.SetDomain(nums(-1, 0, 1))
	if f := d.Fraction(0); f != 0.5 {
		t.Errorf("diverging Fraction = %v", f)
	}
	if d.Apply(jsval.Null).NumValue() != 0.5 {
		t.Error("diverging treats null as 0")
	}
	if d.Interpolator() == nil || len(d.DomainNumbers()) != 3 || d.Clamp() {
		t.Error("accessors")
	}
	if v, ok := d.Exponent(); ok || v != 1 {
		t.Error("exponent of a linear diverging scale")
	}
	d.SetUnknown(jsval.Num(9))
	if d.Unknown().NumValue() != 9 {
		t.Error("unknown")
	}
	d.SetClamp(true)
	d.SetInterpolator(func(t float64) jsval.Value { return jsval.Num(t * 2) })
	if d.Apply(jsval.Num(5)).NumValue() != 2 {
		t.Error("custom interpolator with clamp")
	}
}

func TestOrdinalAccessors(t *testing.T) {
	o := NewOrdinal()
	o.SetRange(nums(1, 2))
	if !o.Implicit() {
		t.Error("ordinal domains are implicit by default")
	}
	o.Apply(jsval.Str("a"))
	if i, ok := o.IndexOf(jsval.Str("a")); !ok || i != 0 {
		t.Error("IndexOf")
	}
	if _, ok := o.IndexOf(jsval.Str("zz")); ok {
		t.Error("IndexOf must not extend the domain")
	}
	o.SetUnknown(jsval.Num(-1))
	if o.Implicit() || o.Unknown().NumValue() != -1 || o.Apply(jsval.Str("new")).NumValue() != -1 {
		t.Error("explicit unknown")
	}
	arr := jsval.ArrOf(jsval.Num(1))
	o.SetImplicit()
	o.Apply(arr)
	if o.Apply(arr).NumValue() != o.Apply(arr).NumValue() || len(o.Domain()) != 2 {
		t.Errorf("array keys are interned by reference: domain %v", o.Domain())
	}
	o.Apply(jsval.ArrOf(jsval.Num(1))) // a different array
	if len(o.Domain()) != 3 {
		t.Errorf("distinct arrays are distinct keys: %v", o.Domain())
	}
}

func TestSchemeRegistry(t *testing.T) {
	names := SchemeNames()
	if len(names) < 60 {
		t.Errorf("only %d schemes", len(names))
	}
	RegisterScheme("MyScheme", Scheme{Colors: []string{"#000", "#fff"}})
	s, ok := LookupScheme("myscheme")
	if !ok || !s.IsDiscrete() || len(s.Colors) != 2 {
		t.Errorf("registered scheme = %+v %v", s, ok)
	}
	for _, name := range []string{"category10", "tableau10", "tableau20", "accent", "dark2", "paired", "pastel1", "pastel2", "set1", "set2", "set3", "category20", "category20b", "category20c", "observable10"} {
		if s, ok := LookupScheme(name); !ok || !s.IsDiscrete() {
			t.Errorf("%s should be a discrete scheme", name)
		}
	}
	for _, name := range []string{"blues", "viridis", "magma", "inferno", "plasma", "cividis", "turbo", "rainbow", "sinebow", "spectral", "redblue", "lightgreyred", "darkblue", "bluegreen", "yellowgreenblue"} {
		if s, ok := LookupScheme(name); !ok || s.IsDiscrete() || s.Interpolator == nil {
			t.Errorf("%s should be an interpolating scheme", name)
		}
	}
}

func TestNewInZone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("no tzdata")
	}
	z := format.Local(loc)
	s, ok := NewIn(TypeTime, z)
	if !ok || s.Type() != TypeTime {
		t.Fatal("NewIn")
	}
	day := 24 * 3600e3
	s.SetDomain([]jsval.Value{jsval.Timestamp(0), jsval.Timestamp(3 * day)})
	ticks := s.(Ticker).Ticks(Count(3))
	// Local midnights in IST (UTC+5:30) fall at 18:30 UTC of the previous day.
	if len(ticks) == 0 || math.Mod(ticks[0]+5.5*3600e3, day) != 0 {
		t.Errorf("ticks are not local midnights: %v", ticks)
	}
	u, _ := NewIn(TypeUTC, z)
	if u.(*Time).Zone().IsUTC() != true {
		t.Error("utc scale must stay UTC")
	}
}

func TestTickCountFor(t *testing.T) {
	s := NewLinear()
	s.SetDomain(nums(0, 100))
	s.SetType(TypeLinear)
	ms := 30.0
	tc, err := TickCountFor(s, jsval.Num(10), &ms)
	if err != nil || !tc.HasN || tc.N != 3 { // capped to floor(100/30)+1 = 4, then to 3 because tickStep(0,100,4) = 25 < 30
		t.Errorf("tickCount = %+v %v", tc, err)
	}
	tc, _ = TickCountFor(s, jsval.Undefined, nil)
	if tc.HasN || tc.Interval != nil {
		t.Error("undefined count stays unspecified")
	}
	if _, err := TickCountFor(s, jsval.Str("day"), nil); err == nil {
		t.Error("interval strings are only for time scales")
	}
	u, _ := New(TypeUTC)
	o := jsval.NewObject(2)
	o.Set("interval", jsval.Str("month"))
	o.Set("step", jsval.Num(3))
	tc, err = TickCountFor(u, jsval.Obj(o), nil)
	if err != nil || tc.Interval == nil {
		t.Errorf("interval count = %+v %v", tc, err)
	}
}

// d3's formatSpecifier matches its argument with a regular expression's exec,
// which converts it to a string: the number 42 is the specifier "42".
func TestNumberSpecifierConvertsToString(t *testing.T) {
	for _, c := range []struct {
		in   jsval.Value
		want string
	}{{jsval.Num(42), "42"}, {jsval.Str(".2f"), ".2f"}, {jsval.Null, ""}} {
		got, err := numberSpecifier(c.in)
		if err != nil || got != c.want {
			t.Errorf("numberSpecifier(%v) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// TestThresholdComparesAsJavaScript checks the thresholds compare with the
// input as d3.ascending does: strings as text, dates as numbers, and a null
// threshold with nothing.
func TestThresholdComparesAsJavaScript(t *testing.T) {
	s, n := jsval.Str, jsval.Num
	cases := []struct {
		domain []jsval.Value
		x      jsval.Value
		want   jsval.Value
	}{
		{[]jsval.Value{s("10"), s("2")}, s("12"), n(1)},
		{[]jsval.Value{s("10"), s("2")}, s("3"), n(2)},
		{[]jsval.Value{s("10"), s("2")}, n(3), n(2)},
		{[]jsval.Value{s("b")}, s("abc"), n(0)},
		{[]jsval.Value{s("b")}, s("c"), n(1)},
		{[]jsval.Value{s("b")}, jsval.Null, jsval.Undefined},
		{[]jsval.Value{jsval.Null, n(5)}, n(1), n(0)},
		{[]jsval.Value{jsval.Timestamp(1000), jsval.Timestamp(2000)}, n(1500), n(1)},
	}
	for _, c := range cases {
		th := NewThreshold()
		th.SetDomain(c.domain)
		th.SetRange([]jsval.Value{n(0), n(1), n(2)})
		if got := th.Apply(c.x); !jsval.Equal(got, c.want) {
			t.Errorf("domain %v: scale(%v) = %v, want %v", c.domain, c.x, got, c.want)
		}
	}
}
