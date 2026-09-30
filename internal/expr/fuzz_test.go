package expr

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

func fuzzScope() *Scope {
	env := &fakeEnv{signals: map[string]jsval.Value{
		"a": jsval.Num(3), "s": jsval.Str("str"), "arr": jsval.ArrOf(jsval.Int(3), jsval.Int(1), jsval.Int(2)),
	}}
	s := NewScope(env)
	s.Datum = jsval.Obj(jsval.ObjectOf("x", jsval.Num(1), "s", jsval.Str("abc"), "a", jsval.ArrOf(jsval.Int(1), jsval.Int(2)),
		"d", jsval.Timestamp(1e12), "o", jsval.Obj(jsval.ObjectOf("p", jsval.Null))))
	s.Rand = &Random{Source: NewLCG(1)}
	s.Now = func() float64 { return 1e12 }
	return s
}

func compileEvalNoPanic(t testing.TB, src string) {
	p, err := Compile(src)
	if err != nil {
		return
	}
	s := fuzzScope()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := p.Eval(s); err != nil {
			if _, ok := err.(*Error); !ok && s.Context == nil {
				t.Errorf("%q: non-JS error %T %v", src, err, err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%q: did not terminate", src)
	}
}

func FuzzCompileEval(f *testing.F) {
	for _, s := range []string{
		"datum.x + 1", "a ? b : c", "pad(s, 40, '-', 'center')", "sequence(3)", "split('a,b', /,/)", "test(/(a+)+$/, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaab')",
		"timeFormat(datum.d, '%Y')", "format(1234.5, ',.2f')", "rgb(1,2,3)", "vlSelectionTest('s', datum)", "{a: [1, {b: 2}]}", "sort(arr)[0]",
		"'\\ud83d\\ude00'.length", "datetime(2020, 1e9)", "panLog([1, 10], 0.1)", "replace('abc', /b/g, '$&$&')", "truncate('abcdef', 3, 'center')",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) { compileEvalNoPanic(t, src) })
}

// TestMutatedGolden runs byte-level mutations of every recorded expression
// through the parser, compiler and evaluator: nothing may panic or hang.
func TestMutatedGolden(t *testing.T) {
	g := loadGolden(t, "expr_utc.json.gz")
	rng := rand.New(rand.NewPCG(1, 2))
	const junk = "()[]{}.,:;?!~+-*/%&|^<>='\"\\ \n\t0123456789abcxyz_$"
	n := 0
	for _, c := range g.Cases {
		if c.Seed != 0 {
			continue
		}
		src := []byte(c.E)
		if len(src) == 0 {
			continue
		}
		for m := 0; m < 2; m++ {
			b := append([]byte(nil), src...)
			switch rng.IntN(4) {
			case 0:
				i := rng.IntN(len(b))
				b = append(b[:i], b[i+1:]...)
			case 1:
				i := rng.IntN(len(b) + 1)
				b = append(b[:i], append([]byte{junk[rng.IntN(len(junk))]}, b[i:]...)...)
			case 2:
				i, j := rng.IntN(len(b)), rng.IntN(len(b))
				b[i], b[j] = b[j], b[i]
			case 3:
				b = b[:rng.IntN(len(b)+1)]
			}
			compileEvalNoPanic(t, string(b))
			n++
		}
	}
	t.Logf("%d mutated expressions", n)
}
