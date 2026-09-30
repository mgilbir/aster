package expr

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/mgilbir/aster/purego/internal/format"
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// goldenFile is the recorded output of testdata/gen_expr.mjs: every expression
// evaluated by upstream's generated code over fixed datums and signals.
type goldenFile struct {
	Stores  map[string][]json.RawMessage `json:"stores"`
	Datums  []json.RawMessage            `json:"datums"`
	Tbl     []json.RawMessage            `json:"tbl"`
	Signals map[string]json.RawMessage
	Cases   []goldenCase `json:"cases"`
}

type goldenCase struct {
	E          string            `json:"e"`
	Seed       int               `json:"seed"`
	Tol        int               `json:"tol"`
	Date       int               `json:"date"`
	Skip       int               `json:"skip"`
	ParseError string            `json:"parseError"`
	Error      string            `json:"error"`
	Results    []json.RawMessage `json:"results"`
}

// decodeTagged rebuilds a jsval.Value from the generator's tagged JSON.
func decodeTagged(raw json.RawMessage) (jsval.Value, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return jsval.Undefined, err
	}
	return fromTagged(v)
}

func fromTagged(v any) (jsval.Value, error) {
	switch x := v.(type) {
	case nil:
		return jsval.Null, nil
	case bool:
		return jsval.Bool(x), nil
	case string:
		return jsval.Str(x), nil
	case json.Number:
		f, err := x.Float64()
		return jsval.Num(f), err
	case []any:
		items := make([]jsval.Value, len(x))
		for i, e := range x {
			iv, err := fromTagged(e)
			if err != nil {
				return jsval.Undefined, err
			}
			items[i] = iv
		}
		return jsval.Arr(items), nil
	case map[string]any:
		if _, ok := x["u"]; ok {
			return jsval.Undefined, nil
		}
		if n, ok := x["n"]; ok {
			switch n.(string) {
			case "NaN":
				return jsval.Num(math.NaN()), nil
			case "Infinity":
				return jsval.Num(math.Inf(1)), nil
			case "-Infinity":
				return jsval.Num(math.Inf(-1)), nil
			case "-0":
				return jsval.Num(math.Copysign(0, -1)), nil
			}
		}
		if d, ok := x["d"]; ok {
			if s, ok := d.(string); ok && s == "NaN" {
				return jsval.Timestamp(math.NaN()), nil
			}
			f, _ := d.(json.Number).Float64()
			return jsval.Timestamp(f), nil
		}
		if re, ok := x["re"]; ok {
			pair := re.([]any)
			p, err := jsval.NewPattern(pair[0].(string), pair[1].(string))
			if err != nil {
				return jsval.Undefined, err
			}
			return jsval.PatternValue(p), nil
		}
		if s, ok := x["set"]; ok {
			return fromTagged(s)
		}
		if _, ok := x["fn"]; ok {
			return jsval.Str("<function>"), nil
		}
		if s, ok := x["x"]; ok {
			return jsval.Str(s.(string)), nil
		}
		if pairs, ok := x["o"]; ok {
			o := jsval.NewObject(0)
			for _, p := range pairs.([]any) {
				kv := p.([]any)
				val, err := fromTagged(kv[1])
				if err != nil {
					return jsval.Undefined, err
				}
				o.Set(kv[0].(string), val)
			}
			return jsval.Obj(o), nil
		}
	}
	return jsval.Undefined, fmt.Errorf("undecodable %v", v)
}

func numClose(a, b float64, tol bool) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	if a == b {
		return math.Signbit(a) == math.Signbit(b) || a != 0
	}
	if !tol {
		return false
	}
	d := math.Abs(a - b)
	return d <= 1e-12*math.Max(math.Abs(a), math.Abs(b)) || d < 1e-300
}

// sameValue compares an expected upstream value with a result.
func sameValue(want, got jsval.Value, tol bool) bool {
	if want.Kind() != got.Kind() {
		return false
	}
	switch want.Kind() {
	case jsval.KindNum, jsval.KindTimestamp:
		return numClose(want.NumValue(), got.NumValue(), tol)
	case jsval.KindArr:
		w, g := want.Items(), got.Items()
		if len(w) != len(g) {
			return false
		}
		for i := range w {
			if !sameValue(w[i], g[i], tol) {
				return false
			}
		}
		return true
	case jsval.KindObj:
		w, g := want.ObjValue(), got.ObjValue()
		if w.Len() != g.Len() {
			return false
		}
		for i := 0; i < w.Len(); i++ {
			if w.KeyAt(i) != g.KeyAt(i) || !sameValue(w.ValueAt(i), g.ValueAt(i), tol) {
				return false
			}
		}
		return true
	case jsval.KindPattern:
		return want.PatternOf().Source == got.PatternOf().Source && want.PatternOf().Flags == got.PatternOf().Flags
	}
	if want.IsStr() {
		return want.StrValue() == normalizeSurrogates(got.StrValue())
	}
	return jsval.Equal(want, got)
}

// normalizeSurrogates replaces lone surrogates (kept as WTF-8) with U+FFFD, as
// Go's JSON decoder does for the recorded expectations.
func normalizeSurrogates(s string) string {
	if isASCII(s) {
		return s
	}
	u := toUTF16(s)
	for i := 0; i < len(u); i++ {
		switch {
		case u[i] >= 0xD800 && u[i] < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000:
			i++
		case u[i] >= 0xD800 && u[i] < 0xE000:
			u[i] = 0xFFFD
		}
	}
	return fromUTF16(u)
}

type testEnv struct {
	signals map[string]jsval.Value
	tables  map[string]jsval.Value
	tuples  map[*jsval.Object]bool
}

// IsTuple reports the datums the harness registered as dataflow tuples.
func (e *testEnv) IsTuple(v jsval.Value) bool { return v.IsObj() && e.tuples[v.ObjValue()] }

func (e *testEnv) Signal(name string) (jsval.Value, bool) {
	v, ok := e.signals[name]
	return v, ok
}

func (e *testEnv) Data(name string) (jsval.Value, bool) {
	v, ok := e.tables[name]
	return v, ok
}

// UnitCounts is the `index:unit` index of a selection store: tuples per unit.
func (e *testEnv) UnitCounts(name string) (map[string]int, bool) {
	tbl, ok := e.tables[name]
	if !ok {
		return nil, false
	}
	counts := map[string]int{}
	for _, row := range tbl.Items() {
		counts[row.Get("unit").AsString()]++
	}
	return counts, true
}

func (e *testEnv) InData(name, field string, v jsval.Value) (int, bool) {
	t, ok := e.tables[name]
	if !ok {
		return 0, false
	}
	n := 0
	for _, row := range t.Items() {
		if sameValueZero(row.Get(field), v) {
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return n, true
}

func loadGolden(t testing.TB, name string) *goldenFile {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func runGolden(t *testing.T, file string, loc *time.Location) {
	g := loadGolden(t, file)
	datums := make([]jsval.Value, len(g.Datums))
	for i, d := range g.Datums {
		v, err := decodeTagged(d)
		if err != nil {
			t.Fatal(err)
		}
		datums[i] = v
	}
	env := &testEnv{signals: map[string]jsval.Value{}, tables: map[string]jsval.Value{}, tuples: map[*jsval.Object]bool{}}
	for _, d := range datums {
		env.tuples[d.ObjValue()] = true
	}
	for k, raw := range g.Signals {
		v, err := decodeTagged(raw)
		if err != nil {
			t.Fatal(err)
		}
		env.signals[k] = v
	}
	tbl := make([]jsval.Value, len(g.Tbl))
	for i, raw := range g.Tbl {
		v, err := decodeTagged(raw)
		if err != nil {
			t.Fatal(err)
		}
		tbl[i] = v
	}
	env.tables["tbl"] = jsval.Arr(tbl)
	for name, entries := range g.Stores {
		vals := make([]jsval.Value, len(entries))
		for i, raw := range entries {
			v, err := decodeTagged(raw)
			if err != nil {
				t.Fatal(err)
			}
			vals[i] = v
		}
		env.tables[name] = jsval.Arr(vals)
	}

	locale, err := format.NewLocale(jsval.Undefined, jsval.Undefined, format.Local(loc))
	if err != nil {
		t.Fatal(err)
	}
	fails, inexact := 0, 0
	const maxFails = 60
	report := func(format string, args ...any) {
		fails++
		if fails <= maxFails {
			t.Errorf(format, args...)
		}
	}
	for _, c := range g.Cases {
		if c.Skip != 0 {
			continue
		}
		p, err := Compile(c.E)
		if c.ParseError != "" {
			// vega-parser rejects identifiers that are not declared signals
			// ("Unrecognized signal name", or a runtime failure for names such
			// as `constructor`); the runtime does that from Deps.Signals.
			if err == nil && strings.Contains(c.ParseError, "Undefined data set name") {
				for _, name := range p.Deps().RequiredData {
					if _, ok := env.tables[name]; !ok {
						err = errors.New("undefined dataset") // reported by the runtime
					}
				}
				if err != nil {
					continue
				}
			}
			if err == nil && (strings.Contains(c.ParseError, "Unrecognized signal name") || strings.Contains(c.ParseError, "Operator not defined")) {
				undeclared := false
				for _, name := range p.Deps().Signals {
					if _, ok := env.signals[name]; !ok {
						undeclared = true
					}
				}
				if undeclared {
					continue
				}
			}
			if err == nil {
				report("%q: upstream rejects (%s) but it compiled", c.E, c.ParseError)
			}
			continue
		}
		if err != nil {
			report("%q: compile error %v; upstream accepts", c.E, err)
			continue
		}
		n := len(c.Results)
		if c.Error != "" {
			n = len(datums)
		}
		s := NewScope(env)
		s.Locale = locale
		s.Now = func() float64 { return 1e12 }
		if c.Seed != 0 {
			s.Rand = &Random{Source: NewLCG(float64(c.Seed))}
			n = 1
		}
		if c.Error != "" {
			// upstream failed for at least one datum
			failed := false
			for i := 0; i < n && !failed; i++ {
				s.Datum = datums[i]
				if _, err := p.Eval(s); err != nil {
					failed = true
				}
			}
			if !failed {
				report("%q: upstream error %q, got none", c.E, c.Error)
			}
			continue
		}
		for i := 0; i < n; i++ {
			s.Datum = datums[i]
			got, err := p.Eval(s)
			if err != nil {
				report("%q datum %d: error %v; want %s", c.E, i, err, c.Results[i])
				break
			}
			want, derr := decodeTagged(c.Results[i])
			if derr != nil {
				t.Fatal(derr)
			}
			if want.IsStr() && strings.Contains(strings.ToUpper(want.StrValue()), " GMT") && loc != time.UTC {
				break // Date.prototype.toString names the zone in words ("Eastern Standard Time")
			}
			if c.Tol != 0 && !sameValue(want, got, false) {
				inexact++
			}
			if !sameValue(want, got, c.Tol != 0) {
				report("%q datum %d: got %s (%s), want %s (%s)", c.E, i, got.String(), got.Kind(), want.String(), want.Kind())
				break
			}
		}
	}
	if fails > maxFails {
		t.Errorf("... and %d more failures", fails-maxFails)
	}
	t.Logf("%s: %d cases, %d results within tolerance but not bit-identical to V8", file, len(g.Cases), inexact)
}

func TestGoldenUTC(t *testing.T) { runGolden(t, "expr_utc.json.gz", time.UTC) }

// TestGoldenNewYork replays the date-related cases recorded with
// TZ=America/New_York, exercising DST-aware local time.
func TestGoldenNewYork(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	runGolden(t, "expr_ny.json.gz", loc)
}
