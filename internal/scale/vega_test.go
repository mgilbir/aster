package scale

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

type vegaTest struct {
	T map[string]any `json:"t"`
	R any            `json:"r"`
}

type vegaCase struct {
	Name  string     `json:"name"`
	Type  string     `json:"type"`
	Ops   [][]any    `json:"ops"`
	Tests []vegaTest `json:"tests"`
}

func loadVega(t testing.TB) []vegaCase {
	f, err := os.Open("testdata/vega.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var cases []vegaCase
	if err := json.NewDecoder(zr).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func tcOf(v any) TickCount {
	if v == nil {
		return TickCount{}
	}
	d := decVal(v)
	if d.IsNum() {
		return Count(d.NumValue())
	}
	return TickCount{}
}

func minStepOf(v any) *float64 {
	if v == nil {
		return nil
	}
	f := v.(float64)
	return &f
}

func TestVegaHelpersGolden(t *testing.T) {
	loc := format.DefaultLocale()
	var st cmpStats
	fail, total := 0, 0
	for _, c := range loadVega(t) {
		s, ok := applyOps(t, goldenCase{Name: c.Name, Type: c.Type, Ops: c.Ops})
		if !ok {
			continue
		}
		for _, tc := range c.Tests {
			total++
			throws := isThrow(tc.R)
			var want jsval.Value
			if !throws {
				want = decVal(tc.R)
			}
			var got jsval.Value
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s: panic in %v: %v", c.Name, tc.T, r)
						fail++
					}
				}()
				got, err = runVega(s, loc, tc.T)
			}()
			if throws {
				if err == nil {
					// upstream throws only for invalid specifiers; we must too
					fail++
					if fail < 30 {
						t.Errorf("%s: %v: want error, got %v", c.Name, tc.T, got)
					}
				}
				continue
			}
			if err != nil {
				fail++
				if fail < 30 {
					t.Errorf("%s: %v: unexpected error %v", c.Name, tc.T, err)
				}
				continue
			}
			if tc.T["fn"] == "tickCount" && want.IsNullish() {
				want = jsval.Undefined // null and undefined both mean "unspecified"
			}
			if !valuesMatch(got, want, &st) {
				fail++
				if fail < 30 {
					t.Errorf("%s: %v = %v, want %v", c.Name, tc.T, got, want)
				}
			}
		}
	}
	t.Logf("%d vega helper checks, %d failures, numbers exact=%d approx=%d", total, fail, st.exact, st.approx)
}

func resolveTC(s Scale, tm map[string]any) (TickCount, error) {
	if b, _ := tm["tick"].(bool); b {
		return TickCountFor(s, decVal(tm["count"]), minStepOf(tm["minStep"]))
	}
	return tcOf(tm["count"]), nil
}

func specOf(tm map[string]any) jsval.Value {
	if v, ok := tm["spec"]; ok {
		return decVal(v)
	}
	return jsval.Undefined
}

func fmtTypeOf(tm map[string]any) string {
	s, _ := tm["formatType"].(string)
	return s
}

func runVega(s Scale, loc *format.Locale, tm map[string]any) (jsval.Value, error) {
	switch tm["fn"] {
	case "tickCount":
		tc, err := TickCountFor(s, decVal(tm["count"]), minStepOf(tm["minStep"]))
		if err != nil {
			return jsval.Undefined, err
		}
		switch {
		case tc.HasN:
			return jsval.Num(tc.N), nil
		case tc.Interval != nil:
			return jsval.Str("<fn>"), nil // the generator's marker for a function
		}
		return jsval.Undefined, nil // upstream returns the null/undefined it was given
	case "tickValues":
		tc, err := resolveTC(s, tm)
		if err != nil {
			return jsval.Undefined, err
		}
		return jsval.Arr(TickValues(s, tc)), nil
	case "validTicks":
		return jsval.Arr(ValidTicks(s, decVal(tm["ticks"]).Items(), tcOf(tm["count"]))), nil
	case "tickFormat":
		tc, err := resolveTC(s, tm)
		if err != nil {
			return jsval.Undefined, err
		}
		f, err := TickFormat(loc, s, tc, specOf(tm), fmtTypeOf(tm), tm["noSkip"] == true)
		if err != nil {
			return jsval.Undefined, err
		}
		var out []jsval.Value
		for _, in := range decVal(tm["inputs"]).Items() {
			out = append(out, f(in))
		}
		return jsval.Arr(out), nil
	case "labelValues":
		tc, err := resolveTC(s, tm)
		if err != nil {
			return jsval.Undefined, err
		}
		l := LabelValues(s, tc)
		o := jsval.NewObject(2)
		o.Set("values", jsval.Arr(l.Values))
		if l.HasMax {
			o.Set("max", jsval.Num(l.Max))
		} else {
			o.Set("max", jsval.Undefined)
		}
		return jsval.Obj(o), nil
	case "labelFormat":
		tc, err := resolveTC(s, tm)
		if err != nil {
			return jsval.Undefined, err
		}
		typ, _ := tm["type"].(string)
		f, err := LabelFormat(loc, s, tc, typ, specOf(tm), fmtTypeOf(tm), tm["noSkip"] == true)
		if err != nil {
			return jsval.Undefined, err
		}
		l := LabelValues(s, tc)
		out := make([]jsval.Value, len(l.Values))
		for i, v := range l.Values {
			text, _ := f(v, i, l)
			out[i] = text
		}
		return jsval.Arr(out), nil
	case "labelFraction":
		f := LabelFraction(s)
		var out []jsval.Value
		for _, in := range decVal(tm["inputs"]).Items() {
			out = append(out, jsval.Num(f(jsval.ToNumber(in))))
		}
		return jsval.Arr(out), nil
	case "d3TickFormat":
		tc := tcOf(tm["count"])
		type tf interface {
			TickFormat(*format.Locale, TickCount, jsval.Value) (func(float64) string, error)
		}
		x, ok := s.(tf)
		if !ok {
			return jsval.Undefined, nil
		}
		f, err := x.TickFormat(loc, tc, specOf(tm))
		if err != nil {
			return jsval.Undefined, err
		}
		var out []jsval.Value
		for _, in := range decVal(tm["inputs"]).Items() {
			if s.Type() == TypeTime || s.Type() == TypeUTC {
				out = append(out, jsval.Str(f(dateNumber(in))))
			} else {
				out = append(out, jsval.Str(f(jsval.ToNumber(in))))
			}
		}
		return jsval.Arr(out), nil
	case "scaleFraction":
		f := ScaleFraction(s, jsval.ToNumber(decVal(tm["min"])), jsval.ToNumber(decVal(tm["max"])))
		var out []jsval.Value
		for _, in := range decVal(tm["inputs"]).Items() {
			out = append(out, f(in))
		}
		return jsval.Arr(out), nil
	case "domainCaption":
		var opt CaptionOptions
		o := decVal(tm["opt"])
		if o.IsObj() {
			opt.MaxLen = jsval.ToNumber(o.Get("maxlen"))
			if o.Get("maxlen").IsUndefined() {
				opt.MaxLen = 0
			}
			opt.Format = o.Get("format")
			opt.FormatType = o.Get("formatType").StrValue()
		}
		str, err := DomainCaption(loc, s, opt)
		return jsval.Str(str), err
	}
	return jsval.Undefined, nil
}

var _ = math.NaN
