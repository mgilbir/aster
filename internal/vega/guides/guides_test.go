package guides

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

type testScope struct {
	config Value
	types  map[string]string
}

func (s testScope) Config() Value { return s.config }
func (s testScope) ScaleType(n string) string {
	return s.types[n]
}

// diff returns the path of the first difference between two values ("" if equal).
func diff(path string, got, want Value) string {
	if got.Kind() != want.Kind() {
		return fmt.Sprintf("%s: kind %v != %v (got %s, want %s)", path, got.Kind(), want.Kind(), got, want)
	}
	switch got.Kind() {
	case jsval.KindArr:
		if got.Len() != want.Len() {
			return fmt.Sprintf("%s: len %d != %d (got %s, want %s)", path, got.Len(), want.Len(), got, want)
		}
		for i := range got.Items() {
			if d := diff(fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i)); d != "" {
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
			if d := diff(path+"."+k, g.Lookup(k), w.Lookup(k)); d != "" {
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

func loadGolden(t *testing.T) (map[string]Value, []Value) {
	t.Helper()
	raw, err := os.ReadFile("testdata/guides.json")
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	configs := map[string]Value{}
	co := root.Get("configs").ObjValue()
	for _, k := range co.Keys() {
		configs[k] = co.Lookup(k)
	}
	return configs, root.Get("cases").Items()
}

func typesOf(v Value) map[string]string {
	m := map[string]string{}
	if o := v.ObjValue(); o != nil {
		for _, k := range o.Keys() {
			m[k] = o.Lookup(k).StrValue()
		}
	}
	return m
}

func TestGuidesAgainstUpstream(t *testing.T) {
	configs, cases := loadGolden(t)
	counts := map[string]int{}
	for _, c := range cases {
		name := c.Get("name").StrValue()
		kind := c.Get("kind").StrValue()
		t.Run(name, func(t *testing.T) {
			counts[kind]++
			sc := testScope{config: configs[c.Get("config").StrValue()], types: typesOf(c.Get("scaleTypes"))}
			spec := c.Get("spec")
			want := c.Get("result")
			ref1, ref2 := objv("$ref", 1), objv("$ref", 2)
			var got Value
			var datum Value
			switch kind {
			case "axis":
				p, err := NewAxis(spec, sc)
				if err != nil {
					t.Fatalf("NewAxis: %v (want error %q)", err, want.Get("error"))
				}
				datum = p.Datum
				got = p.Mark(ref1, ref2)
				checkAxisTicks(t, p, want)
			case "legend":
				p, err := NewLegend(spec, sc)
				if err != nil {
					t.Fatalf("NewLegend: %v", err)
				}
				datum = p.Datum
				got = p.Mark(ref1, ref2)
				checkLegendEntries(t, p, want)
			default:
				p, err := NewTitle(spec, sc)
				if err != nil {
					t.Fatalf("NewTitle: %v", err)
				}
				datum = p.Datum
				got = p.Mark(ref1)
			}
			if d := diff("mark", got, want.Get("mark")); d != "" {
				t.Fatal(d)
			}
			wantDatum := want.Get("ops").Index(0).Get("value").Index(0)
			if d := diff("datum", datum, wantDatum); d != "" {
				t.Fatal(d)
			}
		})
	}
	for _, k := range []string{"axis", "legend", "title"} {
		if counts[k] == 0 {
			t.Errorf("no %s cases ran", k)
		}
	}
	_ = strings.TrimSpace
}

func opParams(want Value) Value { return want.Get("ops").Index(1).Get("params") }

func checkAxisTicks(t *testing.T, p *AxisPlan, want Value) {
	t.Helper()
	params := opParams(want)
	got := objv(
		"scale", objv("$scale", p.Ticks.Scale),
		"extra", p.Ticks.Extra,
		"count", p.Ticks.Count,
		"values", p.Ticks.Values,
		"minstep", p.Ticks.MinStep,
		"formatType", p.Ticks.FormatType,
		"formatSpecifier", p.Ticks.Format,
	)
	if d := diff("ticksParams", got, params); d != "" {
		t.Fatal(d)
	}
}

func checkLegendEntries(t *testing.T, p *LegendPlan, want Value) {
	t.Helper()
	params := opParams(want)
	got := objv(
		"type", p.Entries.Type,
		"scale", objv("$scale", p.Entries.Scale),
		"count", p.Entries.Count,
		"limit", p.Entries.Limit,
		"values", p.Entries.Values,
		"minstep", p.Entries.MinStep,
		"formatType", p.Entries.FormatType,
		"formatSpecifier", p.Entries.Format,
	)
	if p.Entries.SizeExpr != "" {
		got.ObjValue().Set("size", jsval.Str(p.Entries.SizeExpr))
	}
	if p.Entries.CountExpr != "" && !p.Entries.Count.IsTruthy() {
		got.ObjValue().Set("count", objv("signal", p.Entries.CountExpr))
	}
	if d := diff("entriesParams", got, params); d != "" {
		t.Fatal(d)
	}
}

// TestGuidesNeverPanic replaces every property of every recorded
// specification with hostile values; planning and building must not panic
// (errors are fine).
func TestGuidesNeverPanic(t *testing.T) {
	configs, cases := loadGolden(t)
	odd := []Value{
		jsval.Null, jsval.Num(1e9), jsval.Num(-1), jsval.Str("x"), jsval.Str(""), jsval.True, jsval.False,
		jsval.Arr(nil), jsval.ArrOf(jsval.Int(1), objv("a", 1)), objv(), objv("signal", "s"), objv("signal", 3),
		jsval.ArrOf(objv("signal", "s")),
	}
	for _, c := range cases {
		kind := c.Get("kind").StrValue()
		spec := c.Get("spec")
		so := spec.ObjValue()
		if so == nil {
			continue
		}
		sc := testScope{config: configs[c.Get("config").StrValue()], types: typesOf(c.Get("scaleTypes"))}
		for _, key := range so.Keys() {
			for _, o := range odd {
				m := so.Clone()
				m.Set(key, o)
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s: %s=%s panicked: %v", c.Get("name").StrValue(), key, o, r)
						}
					}()
					ref := objv("$ref", 1)
					switch kind {
					case "axis":
						if p, err := NewAxis(jsval.Obj(m), sc); err == nil {
							p.Mark(ref, ref)
						}
					case "legend":
						if p, err := NewLegend(jsval.Obj(m), sc); err == nil {
							p.Mark(ref, ref)
						}
					default:
						if p, err := NewTitle(jsval.Obj(m), sc); err == nil {
							p.Mark(ref)
						}
					}
				}()
			}
		}
	}
}
