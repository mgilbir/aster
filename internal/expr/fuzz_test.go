package expr

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fuzzutil"
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
			if _, ok := err.(*Error); !ok && !errors.Is(err, budget.ErrLimit) && s.Context == nil {
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
	for _, s := range corpusExpressions(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) { compileEvalNoPanic(t, src) })
}

// corpusExpressions are the distinct expressions (the `expr` and `signal`
// strings) of the Vega specifications in testdata/corpus that are at most 200
// bytes long: what real charts write, so the fuzzer starts from every syntax
// they use.
func corpusExpressions(tb testing.TB) []string {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", "vega", "*.vg.json"))
	if err != nil || len(files) == 0 {
		tb.Fatalf("no corpus specifications found (%v)", err)
	}
	seen := map[string]bool{}
	var walk func(v jsval.Value)
	walk = func(v jsval.Value) {
		switch v.Kind() {
		case jsval.KindArr:
			for _, it := range v.Items() {
				walk(it)
			}
		case jsval.KindObj:
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				if k, e := o.KeyAt(i), o.ValueAt(i); (k == "expr" || k == "signal") && e.IsStr() && len(e.StrValue()) <= 200 {
					seen[e.StrValue()] = true
				}
				walk(o.ValueAt(i))
			}
		}
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			tb.Fatal(err)
		}
		if v, err := jsval.ParseJSON(b); err == nil {
			walk(v)
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// shape is the syntax tree of n, one token per node, in Walk order.
func shape(n *Node) string {
	var b strings.Builder
	n.Walk(func(n *Node) bool {
		fmt.Fprintf(&b, "%d:%s:%s:%s:%t ", n.Kind, n.Op, n.Name, n.Raw, n.Computed)
		return false
	})
	return b.String()
}

// FuzzParse holds the parser alone to its contract: it fails only with a
// *SyntaxError (a resource limit wrapping budget.ErrLimit), it finishes, and
// the tree it returns is the same when the source is padded with white space
// (which upstream's tokenizer skips) and can be compiled without a panic.
// There is no printer to round-trip through.
func FuzzParse(f *testing.F) {
	for _, s := range corpusExpressions(f) {
		f.Add(s)
	}
	for _, s := range []string{
		"a ? b : c ? d : e", "-a ** 2", "a in b", "this", "x++", "0x1F + 0b11 + 0o7 + 017 + 1e3 + .5", `/(?<n>a)\k<n>/giu.test(s)`,
		`'\u{1F600}\x41\101\` + "\n'", "{a: 1, 'b': 2, 3: 4, if: 5}", "[,]", "a.if", "a[b][c](d)", "!~+-a", `a\u0062`, `\ud83d`, strings.Repeat("(", 600),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		fuzzutil.Within(t, 10*time.Second, fmt.Sprintf("Parse(%q)", src), func() {
			n, err := Parse(src)
			padded, err2 := Parse(" \t" + src + "\n ")
			if err != nil {
				var se *SyntaxError
				if !errors.As(err, &se) {
					t.Errorf("%q: error %T %v is not a *SyntaxError", src, err, err)
				} else if se.Limit != errors.Is(err, budget.ErrLimit) {
					t.Errorf("%q: Limit is %v but errors.Is(ErrLimit) is not", src, se.Limit)
				}
				if err2 == nil {
					t.Errorf("%q: fails, but succeeds padded with white space", src)
				}
				return
			}
			if n == nil {
				t.Fatalf("%q: nil tree without an error", src)
			}
			if err2 != nil && !errors.Is(err2, budget.ErrLimit) {
				t.Errorf("%q: parses, but fails padded with white space: %v", src, err2)
			} else if err2 == nil && shape(padded) != shape(n) {
				t.Errorf("%q: padded with white space it parses to a different tree:\n%s\n%s", src, shape(n), shape(padded))
			}
			_, _ = CompileNode(n)
		})
	})
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
