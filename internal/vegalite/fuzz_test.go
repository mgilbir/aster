package vegalite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fuzzutil"
	"github.com/mgilbir/aster/internal/jsval"
)

// eventSelectors are the event selector strings of the checked-in
// specifications: a Vega-Lite selection's on, clear, translate and zoom, and a
// Vega stream's events.
func eventSelectors(tb testing.TB) []string {
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
				switch k, e := o.KeyAt(i), o.ValueAt(i); k {
				case "on", "clear", "translate", "zoom", "events":
					if e.IsStr() && len(e.StrValue()) <= 200 {
						seen[e.StrValue()] = true
					}
				}
				walk(o.ValueAt(i))
			}
		}
	}
	for _, pat := range []string{"vega/*.vg.json", "vegalite/*.vl.json", "vg-gallery/*.json"} {
		files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", pat))
		if err != nil || len(files) == 0 {
			tb.Fatalf("no corpus specifications match %q (%v)", pat, err)
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
	}
	for _, c := range loadHelperVectors(tb).Get("selectors").Items() {
		seen[c.Get("input").StrValue()] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// tryParseSelector is parseSelector with the panic recovered: the error is
// what Compile would return for it.
func tryParseSelector(selector, source string) (out []Value, err error) {
	defer func() {
		switch r := recover().(type) {
		case nil:
		case compileError:
			err = r
		case limitError:
			err = r
		default:
			panic(r)
		}
	}()
	return parseSelector(selector, source), nil
}

// FuzzEventSelector feeds arbitrary selector strings to the event selector
// parser. It may only fail the way Compile reports (a compileError, or a
// limitError wrapping budget.ErrLimit), in bounded time; what it returns has
// one stream per top-level comma, each with a source and a type; and white
// space around the selector changes nothing.
func FuzzEventSelector(f *testing.F) {
	for _, s := range eventSelectors(f) {
		f.Add(s, "scope")
	}
	for _, s := range []string{
		"[[a, b] > c, d] > e", "[a, b]", "[a, b] >", "{", "}", "a{", "a}", "a{1,2,3}", "a{NaN}", "@:", "@a:", ":", "a[", "a[]", "a]", "a[b]c",
		"!", "a!!", "*", "@*:click", "[[[[a,b]>c,d]>e,f]>g,h]>i", "a, ,b", "window:pointermove[event.x>1]{10}",
	} {
		f.Add(s, "view")
	}
	f.Fuzz(func(t *testing.T, selector, source string) {
		fuzzutil.Within(t, 10*time.Second, fmt.Sprintf("parseSelector(%q, %q)", selector, source), func() {
			got, err := tryParseSelector(selector, source)
			padded, err2 := tryParseSelector(" \t"+selector+"\n ", source)
			if (err == nil) != (err2 == nil) {
				t.Errorf("%q: error %v, but padded with white space %v", selector, err, err2)
				return
			}
			if err != nil {
				if !errors.As(err, new(compileError)) && !errors.Is(err, budget.ErrLimit) {
					t.Errorf("%q: error %T %v", selector, err, err)
				}
				return
			}
			if a, b := jsval.AppendJSON(nil, jsval.Arr(got)), jsval.AppendJSON(nil, jsval.Arr(padded)); string(a) != string(b) {
				t.Errorf("%q: padded with white space it parses to\n%s\nnot\n%s", selector, b, a)
			}
			if len(got) == 0 {
				t.Errorf("%q: no streams", selector)
			}
			for _, s := range got {
				// a between stream that wraps another has a stream instead
				if !s.IsObj() || !s.Get("stream").IsObj() && !(s.Get("source").IsStr() && s.Get("type").IsStr()) {
					t.Errorf("%q: malformed stream %s", selector, jsval.AppendJSON(nil, s))
				}
			}
		})
	})
}
