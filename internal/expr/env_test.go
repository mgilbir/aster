package expr

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// fakeEnv implements every capability interface and records what it was asked.
type fakeEnv struct {
	signals map[string]jsval.Value
	calls   []string
	logs    []string
}

func (e *fakeEnv) rec(format string, args ...any) {
	e.calls = append(e.calls, fmt.Sprintf(format, args...))
}

func (e *fakeEnv) Signal(name string) (jsval.Value, bool) { v, ok := e.signals[name]; return v, ok }
func (e *fakeEnv) Data(name string) (jsval.Value, bool) {
	e.rec("data %s", name)
	if name == "t" {
		return jsval.ArrOf(jsval.Int(1), jsval.Int(2)), true
	}
	return jsval.Undefined, false
}
func (e *fakeEnv) InData(name, field string, v jsval.Value) (int, bool) {
	e.rec("indata %s %s %v", name, field, v)
	return 3, true
}
func (e *fakeEnv) SetData(name string, tuples jsval.Value) jsval.Value {
	e.rec("setdata %s %v", name, tuples)
	return jsval.Int(1)
}
func (e *fakeEnv) Modify(name string, insert, remove, toggle, modify, values jsval.Value) jsval.Value {
	e.rec("modify %s %v %v %v %v %v", name, insert, remove, toggle, modify, values)
	return jsval.Int(1)
}
func (e *fakeEnv) Scale(ref, v, group jsval.Value) jsval.Value {
	e.rec("scale %v %v %v", ref, v, group)
	return jsval.Num(v.AsDouble() * 10)
}
func (e *fakeEnv) Invert(ref, v, group jsval.Value) jsval.Value {
	e.rec("invert %v %v", ref, v)
	return jsval.Num(v.AsDouble() / 10)
}
func (e *fakeEnv) Domain(ref, group jsval.Value) jsval.Value {
	e.rec("domain %v", ref)
	return jsval.ArrOf(jsval.Int(0), jsval.Int(1))
}
func (e *fakeEnv) Range(ref, group jsval.Value) jsval.Value {
	e.rec("range %v", ref)
	return jsval.ArrOf(jsval.Int(0), jsval.Int(100))
}
func (e *fakeEnv) Bandwidth(ref, group jsval.Value) jsval.Value {
	e.rec("bandwidth %v", ref)
	return jsval.Num(7)
}
func (e *fakeEnv) Copy(ref, group jsval.Value) jsval.Value {
	e.rec("copy %v", ref)
	return jsval.Str("copied:" + ref.AsString())
}
func (e *fakeEnv) Gradient(ref, p0, p1, count, group jsval.Value) jsval.Value {
	e.rec("gradient %v %v %v %v", ref, p0, p1, count)
	return jsval.Str("gradient")
}
func (e *fakeEnv) GeoArea(p, g, grp jsval.Value) jsval.Value {
	e.rec("geoArea %v", p)
	return jsval.Num(1)
}
func (e *fakeEnv) GeoBounds(p, g, grp jsval.Value) jsval.Value {
	e.rec("geoBounds %v", p)
	return jsval.Num(2)
}
func (e *fakeEnv) GeoCentroid(p, g, grp jsval.Value) jsval.Value {
	e.rec("geoCentroid %v", p)
	return jsval.Num(3)
}
func (e *fakeEnv) GeoScale(p, grp jsval.Value) jsval.Value {
	e.rec("geoScale %v", p)
	return jsval.Num(4)
}
func (e *fakeEnv) GeoTranslate(p, grp jsval.Value) jsval.Value {
	e.rec("geoTranslate %v", p)
	return jsval.ArrOf(jsval.Num(5), jsval.Num(6))
}
func (e *fakeEnv) GeoShape(p, g, grp jsval.Value) jsval.Value {
	e.rec("geoShape %v", p)
	return jsval.Str("shape")
}
func (e *fakeEnv) PathShape(path jsval.Value) jsval.Value {
	e.rec("pathShape %v", path)
	return jsval.Str("path")
}
func (e *fakeEnv) TreePath(name string, s, t jsval.Value) jsval.Value {
	e.rec("treePath %s %v %v", name, s, t)
	return jsval.ArrOf(s, t)
}
func (e *fakeEnv) TreeAncestors(name string, n jsval.Value) jsval.Value {
	e.rec("treeAncestors %s %v", name, n)
	return jsval.ArrOf(n)
}
func (e *fakeEnv) EventFunction(name string, args []jsval.Value) jsval.Value {
	e.rec("event %s %d", name, len(args))
	return jsval.Str("ev:" + name)
}
func (e *fakeEnv) ContainerSize() jsval.Value { return jsval.ArrOf(jsval.Int(640), jsval.Int(480)) }
func (e *fakeEnv) Screen() jsval.Value        { return jsval.Obj(jsval.ObjectOf("width", jsval.Int(1920))) }
func (e *fakeEnv) WindowSize() jsval.Value    { return jsval.ArrOf(jsval.Int(800), jsval.Int(600)) }
func (e *fakeEnv) Encode(item, name, retval jsval.Value) jsval.Value {
	e.rec("encode %v %v", item, name)
	return jsval.Str("encoded")
}
func (e *fakeEnv) InScope(item jsval.Value) bool { return item.IsTruthy() }
func (e *fakeEnv) Intersect(b, opt, group jsval.Value) jsval.Value {
	return jsval.ArrOf(b)
}
func (e *fakeEnv) IntersectLasso(m, l, u jsval.Value) jsval.Value { return jsval.Str("lasso") }
func (e *fakeEnv) Log(level LogLevel, args []jsval.Value) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.AsString()
	}
	e.logs = append(e.logs, fmt.Sprintf("%d:%s", level, strings.Join(parts, ",")))
}
func (e *fakeEnv) IsTuple(v jsval.Value) bool { return v.IsObj() && v.Len() > 0 }

func evalWith(t *testing.T, env Env, src string) jsval.Value {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	v, err := p.Eval(NewScope(env))
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return v
}

func TestEnvHooks(t *testing.T) {
	env := &fakeEnv{signals: map[string]jsval.Value{"k": jsval.Num(4)}}
	for src, want := range map[string]string{
		"k * 2":                      "8",
		"data('t')":                  "[1,2]",
		"length(data('zzz'))":        "0",
		"indata('t', 'f', 9)":        "3",
		"setdata('t', [1])":          "1",
		"modify('t', 1, 2, 3, 4, 5)": "1",
		"scale('x', 5)":              "50",
		"scale(copy('x'), 5)":        "50",
		"copy('x')":                  `"copied:x"`,
		"invert('x', 50)":            "5",
		"domain('x')":                "[0,1]",
		"range('x')":                 "[0,100]",
		"bandwidth('x')":             "7",
		"_bandwidth('x') + _scale('x', 1) + _range('x')[1]":                         "117",
		"gradient('c', [0,0], [1,0], 5)":                                            `"gradient"`,
		"geoArea('p', 1) + geoBounds('p', 1) + geoCentroid('p', 1) + geoScale('p')": "10",
		"geoShape('p', 1)":                             `"shape"`,
		"pathShape('M0,0')":                            `"path"`,
		"treePath('t', 1, 2)":                          "[1,2]",
		"treeAncestors('t', 3)":                        "[3]",
		"view() + item() + group() + xy() + x() + y()": `"ev:viewev:itemev:groupev:xyev:xev:y"`,
		"containerSize()":                              "[640,480]",
		"windowSize()":                                 "[800,600]",
		"screen().width":                               "1920",
		"encode({a: 1}, 'x')":                          `"encoded"`,
		"inScope(datum)":                               "false",
		"intersect([1,2])":                             "[[1,2]]",
		"intersectLasso('m', [], {})":                  `"lasso"`,
		"isTuple({a: 1})":                              "true",
		"warn('a', 'b')":                               `"b"`,
		"info(1)":                                      "1",
		"debug()":                                      "undefined",
	} {
		got := evalWith(t, env, src)
		if got.String() != want {
			t.Errorf("%s = %s, want %s", src, got.String(), want)
		}
	}
	slices.Sort(env.logs)
	if got := strings.Join(env.logs, " "); got != "1:a,b 2:1 3:" {
		t.Errorf("logs = %q", got)
	}
	if !strings.Contains(strings.Join(env.calls, "\n"), "event view 0") {
		t.Errorf("event functions not forwarded: %v", env.calls)
	}
}

// Without capabilities every hook answers what upstream answers with an empty
// runtime.
func TestEnvDefaults(t *testing.T) {
	for src, want := range map[string]string{
		"data('t')":                "[]",
		"indata('t', 'f', 1)":      "undefined",
		"scale('x', 1)":            "undefined",
		"domain('x')":              "[]",
		"range('x')":               "[]",
		"bandwidth('x')":           "0",
		"copy('x')":                "undefined",
		"geoArea('p', 1)":          "undefined",
		"treePath('t', 1, 2)":      "undefined",
		"containerSize()":          "[null,null]",
		"windowSize()":             "[null,null]",
		"screen()":                 "{}",
		"encode(null, 'x', 5)":     "5",
		"inScope(null)":            "false",
		"intersect([1])":           "[]",
		"isTuple({})":              "false",
		"modify('t', 1)":           "0",
		"warn('x')":                `"x"`,
		"view()":                   "undefined",
		"vlSelectionTest('s', {})": "0",
		"a + 1":                    "null", // NaN
	} {
		p, err := Compile(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		v, err := p.Eval(NewScope(nil))
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if v.String() != want {
			t.Errorf("%s = %s, want %s", src, v.String(), want)
		}
	}
}

func TestErrorsNotPanics(t *testing.T) {
	for _, src := range []string{
		"datum.a.b", "null.x", "regexp('(')", "test('(', 'x')", "replace('a', 1, 'b')", "vlSelectionTuples(1, {})",
		"panLinear([], 1)", "zoomLinear(null, 1, 2)", "peek(null)", "join(1)", "indexof(1, 2)", "1 in 2", "1 instanceof 2",
		"btoa('\u0100')", "encodeURIComponent('\\ud800')", "atob('***')", "pluck(null, 'a')", "sequence(1e9)",
		"pad('a', 1e12)", "lassoPath([1])", "'a' in null",
	} {
		p, err := Compile(src)
		if err != nil {
			continue
		}
		s := NewScope(nil)
		if _, err := p.Eval(s); err == nil {
			t.Errorf("%s: expected an evaluation error", src)
		} else if _, ok := err.(*Error); !ok && !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: error %T %v is neither an *Error nor a limit", src, err, err)
		}
		if len(s.stack) != 0 {
			t.Errorf("%s: argument stack not restored (%d)", src, len(s.stack))
		}
	}
}

func TestScopeReuseAfterError(t *testing.T) {
	bad, _ := Compile("abs(datum.a.b)")
	good, _ := Compile("max(1, 2, 3) + abs(-4)")
	s := NewScope(nil)
	for i := 0; i < 3; i++ {
		if _, err := bad.Eval(s); err == nil {
			t.Fatal("expected error")
		}
		v, err := good.Eval(s)
		if err != nil || v.NumValue() != 7 {
			t.Fatalf("after error: %v %v", v, err)
		}
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, src := range []string{"length(sequence(1000000))", "length(sort(sequence(100000)))"} {
		p, err := Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScope(nil)
		s.Context = ctx
		if _, err := p.Eval(s); err != context.Canceled {
			t.Errorf("%s: err = %v, want context.Canceled", src, err)
		}
	}
}

func TestBounds(t *testing.T) {
	for _, src := range []string{"sequence(0, 1e10)", "pad('x', 1e9)", "pad('x', 1/0)", "truncate('abc', 1e30, 'left', 'x')"} {
		p, err := Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := p.Eval(NewScope(nil)); done <- err }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not terminate", src)
		}
	}
	// Oversized results are errors, not silently truncated.
	for _, src := range []string{"sequence(0, 1e10)", "pad('x', 1e9)"} {
		p, _ := Compile(src)
		if _, err := p.Eval(NewScope(nil)); err == nil {
			t.Errorf("%s: expected a bound error", src)
		}
	}
}

// One compiled program serves many goroutines, each with its own Scope.
func TestConcurrentEval(t *testing.T) {
	p, err := Compile("datum.x * 2 + length(toString(datum.x)) + (test('^\\\\d+$', toString(datum.x)) ? 1 : 0) + sampleUniform(0, 0)")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			s := NewScope(nil)
			for i := 0; i < 2000; i++ {
				s.Datum = jsval.Obj(jsval.ObjectOf("x", jsval.Int(i)))
				v, err := p.Eval(s)
				want := float64(2*i) + float64(len(fmt.Sprint(i))) + 1
				if err != nil || v.NumValue() != want {
					t.Errorf("g%d i%d: %v %v want %v", g, i, v, err, want)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestSeededRandom(t *testing.T) {
	p, _ := Compile("[random(), random(), sampleNormal(), sampleNormal()]")
	run := func(seed float64) string {
		s := NewScope(nil)
		s.Rand = &Random{Source: NewLCG(seed)}
		v, err := p.Eval(s)
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}
	if a, b, c := run(42), run(42), run(43); a != b || a == c {
		t.Errorf("seeded generator is not deterministic: %s / %s / %s", a, b, c)
	}
}

func TestClockAndZone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	l, _ := format.NewLocale(jsval.Undefined, jsval.Undefined, format.Local(loc))
	p, _ := Compile("[now(), hours(now()), utchours(now()), timezoneoffset(now()), timeFormat(now(), '%H:%M'), utcFormat(now(), '%H:%M')]")
	s := NewScope(nil)
	s.Locale = l
	s.Now = func() float64 { return 1592235000000 } // 2020-06-15T15:30:00Z
	v, err := p.Eval(s)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), `[1592235000000,11,15,240,"11:30","15:30"]`; got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestObjectAndArrayIdentity(t *testing.T) {
	// Expressions build fresh arrays and objects on each evaluation; results
	// must not alias between evaluations.
	p, _ := Compile("[1, {a: 2}]")
	s := NewScope(nil)
	a, _ := p.Eval(s)
	b, _ := p.Eval(s)
	if jsval.SameRef(a, b) || jsval.SameRef(a.Index(1), b.Index(1)) {
		t.Error("evaluations share arrays or objects")
	}
}

func TestPatternLiteralState(t *testing.T) {
	// A g-flagged literal has lastIndex state in JavaScript; each evaluation of
	// the literal creates a new regular expression, so test() never carries over.
	p, _ := Compile("test(/a/g, 'aa') && test(/a/g, 'aa')")
	s := NewScope(nil)
	for i := 0; i < 3; i++ {
		v, _ := p.Eval(s)
		if !v.BoolValue() {
			t.Fatal("g literal leaked lastIndex between evaluations")
		}
	}
	// regexp() built once and tested twice does carry lastIndex.
	p, _ = Compile("test(re, 'a') + ',' + test(re, 'a')")
	env := &fakeEnv{signals: map[string]jsval.Value{}}
	re, _ := jsval.NewPattern("a", "g")
	env.signals["re"] = jsval.PatternValue(re)
	v, err := p.Eval(NewScope(env))
	if err != nil || v.StrValue() != "true,false" {
		t.Errorf("stateful g pattern: %v %v", v, err)
	}
}
