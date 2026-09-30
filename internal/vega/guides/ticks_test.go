package guides

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// decode maps the generator's JSON encoding back: {"$t": ms} to a timestamp
// and {"$n": "Infinity"} to non-finite numbers.
func decode(v jsval.Value) jsval.Value {
	switch {
	case v.IsObj() && v.ObjValue().Has("$t"):
		return jsval.Timestamp(v.Get("$t").NumValue())
	case v.IsObj() && v.ObjValue().Has("$n"):
		switch v.Get("$n").StrValue() {
		case "Infinity":
			return jsval.Num(math.Inf(1))
		case "-Infinity":
			return jsval.Num(math.Inf(-1))
		}
		return jsval.Num(math.NaN())
	case v.IsArr():
		items := make([]jsval.Value, v.Len())
		for i, it := range v.Items() {
			items[i] = decode(it)
		}
		return jsval.Arr(items)
	case v.IsObj():
		o := jsval.NewObject(v.Len())
		for i := 0; i < v.Len(); i++ {
			o.Set(v.ObjValue().KeyAt(i), decode(v.ObjValue().ValueAt(i)))
		}
		return jsval.Obj(o)
	}
	return v
}

func makeScale(t *testing.T, d jsval.Value) (scale.Scale, bool) {
	t.Helper()
	typ := d.Get("type").StrValue()
	s, ok := scale.New(typ)
	if !ok {
		return nil, false
	}
	if dom := d.Get("domain"); dom.IsArr() {
		s.SetDomain(decode(dom).Items())
	}
	if rng := d.Get("range"); rng.IsArr() {
		s.SetRange(decode(rng).Items())
	}
	if props := d.Get("props").ObjValue(); props != nil {
		for _, k := range props.Keys() {
			scale.Set(s, k, props.Lookup(k))
		}
	}
	if bins := d.Get("bins"); bins.IsArr() {
		fs := make([]float64, bins.Len())
		for i, b := range bins.Items() {
			fs[i] = b.NumValue()
		}
		s.(scale.Typed).SetBins(fs)
	}
	return s, true
}

// close compares decoded values with a small tolerance on numbers.
func closeValues(path string, got, want jsval.Value) string {
	if got.IsNum() && want.IsNum() {
		g, w := got.NumValue(), want.NumValue()
		if g == w || (math.IsNaN(g) && math.IsNaN(w)) || math.Abs(g-w) <= 1e-9*math.Max(1, math.Abs(w)) {
			return ""
		}
		return fmt.Sprintf("%s: got %v, want %v", path, g, w)
	}
	if got.Kind() != want.Kind() {
		return fmt.Sprintf("%s: got %s, want %s", path, got, want)
	}
	switch got.Kind() {
	case jsval.KindArr:
		if got.Len() != want.Len() {
			return fmt.Sprintf("%s: len %d != %d\n got %s\nwant %s", path, got.Len(), want.Len(), got, want)
		}
		for i := range got.Items() {
			if d := closeValues(fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i)); d != "" {
				return d
			}
		}
		return ""
	case jsval.KindObj:
		g, w := got.ObjValue(), want.ObjValue()
		for _, k := range w.Keys() {
			if !g.Has(k) {
				return fmt.Sprintf("%s.%s: missing (want %s)", path, k, w.Lookup(k))
			}
		}
		for _, k := range g.Keys() {
			if !w.Has(k) {
				return fmt.Sprintf("%s.%s: unexpected (got %s)", path, k, g.Lookup(k))
			}
			if d := closeValues(path+"."+k, g.Lookup(k), w.Lookup(k)); d != "" {
				return d
			}
		}
		return ""
	}
	if !jsval.Equal(got, want) {
		return fmt.Sprintf("%s: got %s, want %s", path, got, want)
	}
	return ""
}

func TestTickOperatorsAgainstUpstream(t *testing.T) {
	raw, err := os.ReadFile("testdata/ticks.json")
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	env := &Env{Locale: format.DefaultLocale()}
	skipped := 0
	for _, c := range root.Items() {
		name := c.Get("name").StrValue()
		t.Run(name, func(t *testing.T) {
			s, ok := makeScale(t, c.Get("scale"))
			if !ok {
				skipped++
				t.Skipf("scale type %s not registered", c.Get("scale").Get("type").StrValue())
			}
			params := decode(c.Get("params"))
			p := func(k string) jsval.Value { return params.Get(k) }
			wantErr := c.Get("error").IsStr()
			var got []jsval.Value
			switch c.Get("kind").StrValue() {
			case "axis":
				got, err = env.AxisTicks(AxisTicksInput{
					Scale:           s,
					Extra:           p("extra").IsTruthy(),
					Count:           p("count"),
					Values:          p("values"),
					MinStep:         p("minstep"),
					FormatType:      p("formatType").StrValue(),
					FormatSpecifier: p("formatSpecifier"),
				})
			default:
				in := LegendEntriesInput{
					Type:            p("type").StrValue(),
					Scale:           s,
					Count:           p("count"),
					Limit:           p("limit"),
					Values:          p("values"),
					MinStep:         p("minstep"),
					FormatType:      p("formatType").StrValue(),
					FormatSpecifier: p("formatSpecifier"),
				}
				switch sz := c.Get("size"); {
				case sz.IsStr(): // "sqrt": Math.sqrt(+v) * 3
					in.Size = func(v jsval.Value) jsval.Value {
						return jsval.Num(math.Sqrt(jsval.ToNumber(v)) * 3)
					}
				case sz.IsNum():
					in.SizeConst = sz
				}
				got, err = env.LegendEntries(in)
			}
			if wantErr {
				if err == nil {
					t.Fatalf("want error %q, got none", c.Get("error").StrValue())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := decode(c.Get("out"))
			if d := closeValues("out", jsval.Arr(got), want); d != "" {
				t.Fatal(d)
			}
		})
	}
	if skipped > 0 {
		t.Logf("%d cases skipped (scale type not registered)", skipped)
	}
}
