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

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// The builtins differential (testdata/gen_builtins.mjs, recorded by upstream's
// View in the pinned node): every function of the expression language with
// nominal and edge arguments, the order it evaluates its arguments in (each
// wrapped in warn(), whose log is recorded), the members every kind of value
// inherits, and the identifier characters of the parser. Results compare
// exactly: kind, value, sign of zero, key order, function-ness and colour
// class.

type builtinsFile struct {
	Datum  json.RawMessage `json:"datum"`
	Tbl    []json.RawMessage
	Idents struct {
		Start, Part, StartEscaped, PartEscaped [][2]int
		Astral                                 [][3]any
	} `json:"idents"`
	Cases []builtinCase `json:"cases"`
}

type builtinCase struct {
	E         string            `json:"e"`
	Result    json.RawMessage   `json:"result"`
	Error     string            `json:"error"`
	Log       []json.RawMessage `json:"log"`
	LogOnly   int               `json:"logOnly"`
	Unbounded int               `json:"unbounded"`
	Tol       int               `json:"tol"`
}

func loadBuiltins(t *testing.T) *builtinsFile {
	t.Helper()
	f, err := os.Open("testdata/builtins.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var b builtinsFile
	if err := json.NewDecoder(zr).Decode(&b); err != nil {
		t.Fatal(err)
	}
	return &b
}

// logEnv is a testEnv that records what warn(), info() and debug() log.
type logEnv struct {
	*testEnv
	log [][]jsval.Value
}

func (e *logEnv) Log(level LogLevel, args []jsval.Value) {
	name := map[LogLevel]string{LogWarn: "warn", LogInfo: "info", LogDebug: "debug"}[level]
	e.log = append(e.log, append([]jsval.Value{jsval.Str(name)}, args...))
}

func decodeAny(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// colourClass is the d3 class of a colour value: "Rgb", "Hsl", "Lab", "Hcl",
// or "" for anything else.
func colourClass(v jsval.Value) string {
	if _, ok := asColor(v); !ok {
		return ""
	}
	o := v.ObjValue()
	switch o.KeyAt(0) + o.KeyAt(1) + o.KeyAt(2) {
	case "rgb":
		return "Rgb"
	case "hsl":
		return "Hsl"
	case "lab":
		return "Lab"
	case "hcl":
		return "Hcl"
	}
	return ""
}

// ulpsApart is how many doubles lie between a and b (of the same sign).
func ulpsApart(a, b float64) uint64 {
	if math.Signbit(a) != math.Signbit(b) || math.IsNaN(a) || math.IsNaN(b) {
		return math.MaxUint64
	}
	x, y := math.Float64bits(a), math.Float64bits(b)
	if x > y {
		return x - y
	}
	return y - x
}

// matchTagged reports whether got is the value the tagged JSON want records,
// and if not, why. With tol, a number may be two units in the last place
// off.
func matchTagged(want any, got jsval.Value, tol bool) error {
	mismatch := func() error { return fmt.Errorf("want %v, got %s (%s)", jsonText(want), got.String(), got.Kind()) }
	switch w := want.(type) {
	case nil:
		if !got.IsNull() {
			return mismatch()
		}
	case bool:
		if got.Kind() != jsval.KindBool || got.BoolValue() != w {
			return mismatch()
		}
	case string:
		if !got.IsStr() || normalizeSurrogates(got.StrValue()) != w {
			return mismatch()
		}
	case json.Number:
		f, _ := w.Float64()
		if !got.IsNum() || got.NumValue() == 0 && math.Signbit(got.NumValue()) {
			return mismatch()
		}
		if g := got.NumValue(); g != f && !(tol && ulpsApart(f, g) <= 2) {
			return mismatch()
		}
	case []any:
		if !got.IsArr() || got.Len() != len(w) {
			return mismatch()
		}
		for i, x := range w {
			if err := matchTagged(x, got.Index(i), tol); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case map[string]any:
		switch {
		case w["u"] != nil:
			if !got.IsUndefined() {
				return mismatch()
			}
		case w["n"] != nil:
			x := got.NumValue()
			var ok bool
			switch w["n"] {
			case "NaN":
				ok = math.IsNaN(x)
			case "Infinity":
				ok = math.IsInf(x, 1)
			case "-Infinity":
				ok = math.IsInf(x, -1)
			case "-0":
				ok = x == 0 && math.Signbit(x)
			}
			if !got.IsNum() || !ok {
				return mismatch()
			}
		case w["fn"] != nil:
			if !jsval.IsFunction(got) {
				return mismatch()
			}
		case w["d"] != nil:
			if !got.IsTimestamp() {
				return mismatch()
			}
			if s, ok := w["d"].(string); ok && s == "NaN" {
				if !math.IsNaN(got.NumValue()) {
					return mismatch()
				}
			} else if f, _ := w["d"].(json.Number).Float64(); got.NumValue() != f {
				return mismatch()
			}
		case w["re"] != nil:
			pair := w["re"].([]any)
			if got.Kind() != jsval.KindPattern || got.PatternOf().Source != pair[0] || got.PatternOf().Flags != pair[1] {
				return mismatch()
			}
		case w["set"] != nil:
			// vlSelectionResolve's Sets are arrays here (doc.go).
			return matchTagged(w["set"], got, tol)
		case w["x"] != nil:
			return fmt.Errorf("want the object %v, which the differential does not model; got %s", w["x"], got.String())
		case w["o"] != nil:
			pairs := w["o"].([]any)
			if !got.IsObj() || got.ObjValue().Len() != len(pairs) || jsval.IsFunction(got) {
				return mismatch()
			}
			class, _ := w["c"].(string)
			if colourClass(got) != class {
				return fmt.Errorf("want a %q object, got the %q %s", class, colourClass(got), got.String())
			}
			o := got.ObjValue()
			for i, p := range pairs {
				kv := p.([]any)
				if o.KeyAt(i) != kv[0] {
					return fmt.Errorf("key %d: want %v, got %q", i, kv[0], o.KeyAt(i))
				}
				if err := matchTagged(kv[1], o.ValueAt(i), tol); err != nil {
					return fmt.Errorf(".%v: %w", kv[0], err)
				}
			}
		default:
			return fmt.Errorf("undecodable %v", w)
		}
	default:
		return fmt.Errorf("undecodable %v", w)
	}
	return nil
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestBuiltinsDifferential(t *testing.T) {
	b := loadBuiltins(t)
	datum, err := decodeTagged(b.Datum)
	if err != nil {
		t.Fatal(err)
	}
	tbl := make([]jsval.Value, len(b.Tbl))
	for i, raw := range b.Tbl {
		if tbl[i], err = decodeTagged(raw); err != nil {
			t.Fatal(err)
		}
	}
	env := &logEnv{testEnv: &testEnv{
		signals: map[string]jsval.Value{},
		tables:  map[string]jsval.Value{"tbl": jsval.Arr(tbl)},
		tuples:  map[*jsval.Object]bool{datum.ObjValue(): true},
	}}
	fails, maxFails := 0, 80
	if os.Getenv("BUILTINS_ALL") != "" {
		maxFails = math.MaxInt
	}
	report := func(format string, args ...any) {
		if fails++; fails <= maxFails {
			t.Errorf(format, args...)
		}
	}
	for _, c := range b.Cases {
		env.log = env.log[:0]
		var got jsval.Value
		p, err := Compile(c.E)
		if err == nil && undeclaredSignal(p, c.Error) {
			// vega-parser rejects a reference to a signal the specification
			// does not declare; the runtime does that from Deps.Signals.
			continue
		}
		if err == nil {
			s := NewScope(env)
			s.Now = func() float64 { return 1e12 }
			s.Rand = &Random{Source: NewLCG(1)}
			s.Datum = datum
			s.NoEvent, s.NoItem = true, true
			s.Strings = NewStringBudget(64 << 20)
			got, err = p.Eval(s)
		}
		switch {
		case c.Unbounded != 0:
			// Upstream never finishes (it runs out of memory); the engine
			// must refuse.
			if err == nil {
				report("%s: upstream does not finish; the engine returned %s", c.E, got.String())
			}
			continue
		case c.Error != "" && c.LogOnly == 0:
			if err == nil {
				report("%s: upstream fails (%s); got %s", c.E, c.Error, got.String())
				continue
			}
			if errors.Is(err, budget.ErrLimit) {
				report("%s: upstream throws (%s); got the engine limit %v", c.E, c.Error, err)
				continue
			}
		case err != nil && c.LogOnly == 0:
			report("%s: got error %v; want %s", c.E, err, c.Result)
			continue
		case c.LogOnly == 0:
			want, derr := decodeAny(c.Result)
			if derr != nil {
				t.Fatal(derr)
			}
			if merr := matchTagged(want, got, c.Tol != 0); merr != nil {
				report("%s: %v", c.E, merr)
				continue
			}
		}
		if len(c.Log) != len(env.log) {
			report("%s: logged %d messages %s; upstream logs %d %s", c.E, len(env.log), showLog(env.log), len(c.Log), c.Log)
			continue
		}
		for i, raw := range c.Log {
			want, derr := decodeAny(raw)
			if derr != nil {
				t.Fatal(derr)
			}
			if merr := matchTagged(want, jsval.Arr(env.log[i]), c.Tol != 0); merr != nil {
				report("%s: log %d: %v (logged %s)", c.E, i, merr, showLog(env.log))
				break
			}
		}
	}
	if fails > maxFails {
		t.Errorf("... and %d more", fails-maxFails)
	}
	t.Logf("%d cases", len(b.Cases))
}

// undeclaredSignal reports whether upstream's parse error is the reference to
// an undeclared signal p makes: every identifier the differential uses is
// datum or a constant, except a string literal that names an
// Object.prototype member, which upstream reads as an identifier.
func undeclaredSignal(p *Program, upstreamErr string) bool {
	if !strings.Contains(upstreamErr, "Unrecognized signal name") && !strings.Contains(upstreamErr, "Operator not defined") {
		return false
	}
	return len(p.Deps().Signals) > 0
}

// TestBuiltinsDifferentialCoversEveryFunction checks that the differential
// calls every function of the table with each argument wrapped in warn(), so
// that the order the function evaluates them in is compared with upstream's:
// a function added to the table needs its case in gen_builtins.mjs.
func TestBuiltinsDifferentialCoversEveryFunction(t *testing.T) {
	b := loadBuiltins(t)
	for name := range funcTable {
		found := false
		for _, c := range b.Cases {
			if strings.HasPrefix(c.E, name+"(") && strings.Contains(c.E, "warn('a") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no case of the differential checks the argument order of %s", name)
		}
	}
}

func showLog(log [][]jsval.Value) string {
	var parts []string
	for _, l := range log {
		parts = append(parts, jsval.Arr(l).String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// TestIdentifierCharacters checks every code unit of the Basic Multilingual
// Plane against the parser's tables: as an identifier's first character and
// as a later one, written raw and as a \u escape. Upstream tests UTF-16 code
// units, so a character outside the plane (two units, the first a high
// surrogate) is never part of an identifier.
func TestIdentifierCharacters(t *testing.T) {
	b := loadBuiltins(t)
	set := func(ranges [][2]int) []bool {
		in := make([]bool, 0x10000)
		for _, r := range ranges {
			for c := r[0]; c <= r[1]; c++ {
				in[c] = true
			}
		}
		return in
	}
	isIdent := func(src, name string) bool {
		n, err := Parse(src)
		return err == nil && n.Kind == KindIdentifier && n.Name == name
	}
	check := func(what string, want []bool, accepts func(c int) bool) {
		var bad []string
		for c := 0x80; c <= 0xFFFF; c++ {
			if got := accepts(c); got != want[c] {
				bad = append(bad, fmt.Sprintf("U+%04X(%v)", c, want[c]))
			}
		}
		if len(bad) > 0 {
			t.Errorf("%s: %d code units differ from upstream (upstream's verdict in parentheses): %s", what, len(bad), strings.Join(bad[:min(len(bad), 40)], " "))
		}
	}
	raw := func(prefix string) func(c int) bool {
		return func(c int) bool {
			if c >= 0xD800 && c < 0xE000 {
				return false // not representable raw in UTF-8 source; checked escaped
			}
			s := prefix + string(rune(c))
			return isIdent(s, s)
		}
	}
	esc := func(prefix string) func(c int) bool {
		return func(c int) bool {
			if c >= 0xD800 && c < 0xE000 {
				_, err := Parse(fmt.Sprintf(`%s\u%04x`, prefix, c))
				return err == nil
			}
			return isIdent(fmt.Sprintf(`%s\u%04x`, prefix, c), prefix+string(rune(c)))
		}
	}
	check("start", set(b.Idents.Start), raw(""))
	check("part", set(b.Idents.Part), raw("a"))
	check("escaped start", set(b.Idents.StartEscaped), esc(""))
	check("escaped part", set(b.Idents.PartEscaped), esc("a"))
	for _, a := range b.Idents.Astral {
		c := rune(a[0].(float64))
		if got := isIdent(string(c), string(c)); got != a[1].(bool) {
			t.Errorf("U+%X as an identifier: got %v, upstream %v", c, got, a[1])
		}
		if got := isIdent("a"+string(c), "a"+string(c)); got != a[2].(bool) {
			t.Errorf("a + U+%X as an identifier: got %v, upstream %v", c, got, a[2])
		}
	}
}
