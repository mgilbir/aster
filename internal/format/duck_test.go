package format

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// readDuck loads testdata/duck.json.gz (testdata/gen_duck.mjs) through
// jsval, which keeps a lone surrogate as the code unit it is.
func readDuck(t *testing.T) jsval.Value {
	t.Helper()
	b, err := os.ReadFile("testdata/duck.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestNumberLocaleDuckTyping is C34 and C162 (audit 2026-10-18): d3-format
// reads a locale's grouping and numerals with Array.prototype.map.call (any
// array-like: a string by its UTF-16 units, an object by its length) and its
// currency as currency[0] and currency[1], and groups with the sizes as
// numbers, fractions included. Every locale of the class matrix formats as
// d3 does, or fails where d3 throws.
func TestNumberLocaleDuckTyping(t *testing.T) {
	d := readDuck(t)
	specs, values := d.Get("numberSpecs").Items(), d.Get("numberValues").Items()
	for _, c := range d.Get("number").Items() {
		src := c.Get("locale").StrValue()
		def, err := jsval.ParseJSONString(src)
		if err != nil {
			t.Fatal(err)
		}
		l, err := NumberLocaleFromValue(def)
		if want := c.Get("error"); !want.IsUndefined() {
			if err == nil {
				t.Errorf("%s: built a locale; d3 throws %s", src, want.StrValue())
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		for i, row := range c.Get("rows").Items() {
			spec := specs[i].StrValue()
			f, err := l.Format(spec)
			if err != nil {
				t.Errorf("%s %q: %v", src, spec, err)
				continue
			}
			for j, want := range row.Get("out").Items() {
				x := jsval.StringToNumber(values[j].StrValue())
				if got := f.Format(x); got != want.StrValue() {
					t.Errorf("%s format(%q)(%v) = %q, d3 %q", src, spec, x, got, want.StrValue())
				}
			}
		}
	}
}

// An array-like's length is no measure of the work: grouping reads at most
// the sizes a number can reach, numerals the ten digits. (A hang guard: a
// reader that walked the length would not finish.)
func TestLocaleArrayLikeLengthIsNoWork(t *testing.T) {
	for _, src := range []string{
		`{"thousands":",","grouping":{"length":4000000000,"0":3}}`,
		`{"numerals":{"length":4000000000,"0":"a"}}`,
	} {
		v, err := jsval.ParseJSONString(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NumberLocaleFromValue(v); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

// A group size below one digit takes more steps than digits: d3 renders
// what that writes (testdata/duck.json.gz holds 0.5, 0.25, 0.7), and one too
// small to move the index (1e-300) never ends, so past MaxGroupSteps the
// call stops with budget.ErrLimit. Sizes of 0, a negative or NaN end
// grouping at once, as in d3.
func TestGroupStepsBounded(t *testing.T) {
	format := func(g []float64) (out string, err error) {
		defer func() {
			if s, ok := recover().(*budget.Stop); ok {
				err = s.Err
			}
		}()
		l, err := NewNumberLocale(NumberLocaleDef{Thousands: ptr(","), Grouping: g})
		if err != nil {
			return "", err
		}
		f, err := l.Format(",")
		if err != nil {
			return "", err
		}
		return f.Format(1234567), nil
	}
	for _, g := range [][]float64{{1e-300}, {1e-6}} {
		if _, err := format(g); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("grouping %v: %v; want a limit error", g, err)
		}
	}
	for g, want := range map[float64]string{0: "", -1: "", math.NaN(): "", 0.5: ",1,,2,,3,,4,,5,,6,,7"} { // node 24.21.0 (and [3, 1e-300] ends: its sizes alternate)
		if got, err := format([]float64{g}); err != nil || got != want {
			t.Errorf("grouping [%v] = %q, %v; d3 %q", g, got, err, want)
		}
	}
}

// TestTimeLocaleDuckTyping is C118 (audit 2026-10-18): d3-time-format needs
// a locale's name lists when it is built (it maps over them), but uses its
// dateTime, date and time patterns only when %c, %x or %X formats or parses,
// reading them as JavaScript reads `specifier.length`, `charCodeAt` and
// `slice`. So a locale without patterns works until one is used, an empty
// array is an empty pattern, and a cycle throws when it is reached.
func TestTimeLocaleDuckTyping(t *testing.T) {
	d := readDuck(t)
	at := d.Get("t").NumValue()
	for _, c := range d.Get("time").Items() {
		src := c.Get("locale").StrValue()
		def, err := jsval.ParseJSONString(src)
		if err != nil {
			t.Fatal(err)
		}
		l, err := TimeLocaleFromValue(def)
		if want := c.Get("error"); !want.IsUndefined() {
			if err == nil {
				t.Errorf("%.60s: built a locale; d3 throws %s", src, want.StrValue())
			}
			continue
		}
		if err != nil {
			t.Errorf("%.60s: %v", src, err)
			continue
		}
		for _, f := range c.Get("fmt").Items() {
			spec := f.Get("spec").StrValue()
			got, err := l.Format(spec, UTC).AppendFormatErr(nil, at)
			switch want := f.Get("error"); {
			case !want.IsUndefined():
				var th *jsval.Thrown
				if !errors.As(err, &th) {
					t.Errorf("%.60s utcFormat(%q) = %q, %v; d3 throws %s", src, spec, got, err, want.StrValue())
				}
			case err != nil:
				t.Errorf("%.60s utcFormat(%q): %v; d3 %q", src, spec, err, f.Get("out").StrValue())
			case string(got) != f.Get("out").StrValue():
				t.Errorf("%.60s utcFormat(%q) = %q; d3 %q", src, spec, got, f.Get("out").StrValue())
			}
		}
		for _, p := range c.Get("parse").Items() {
			spec, s := p.Get("spec").StrValue(), p.Get("s").StrValue()
			got, ok, err := l.Parse(spec, UTC).ParseErr(s)
			switch want := p.Get("error"); {
			case !want.IsUndefined():
				var th *jsval.Thrown
				if !errors.As(err, &th) {
					t.Errorf("%.60s utcParse(%q)(%q) = %v %v, %v; d3 throws %s", src, spec, s, got, ok, err, want.StrValue())
				}
			case err != nil:
				t.Errorf("%.60s utcParse(%q)(%q): %v", src, spec, s, err)
			case p.Get("out").IsNull():
				if ok {
					t.Errorf("%.60s utcParse(%q)(%q) = %v; d3 null", src, spec, s, got)
				}
			case !ok || got != p.Get("out").NumValue():
				t.Errorf("%.60s utcParse(%q)(%q) = %v %v; d3 %v", src, spec, s, got, ok, p.Get("out").NumValue())
			}
		}
	}
}
