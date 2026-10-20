package vegalite

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
)

// The compiler reads an expression for the fields it depends on with a
// grammar of its own (chains taken at any length, C10) over internal/expr's
// tokenizer. Upstream reads it with vega-expression's parser, which
// internal/expr's Parse is: so the compiler must accept exactly the
// expressions Parse accepts, and find the fields a walk of Parse's tree
// finds, in the same order (C47 of the 2026-10-18 audit: a lexer of its own
// had drifted, refusing identifiers vega-expression accepts).

// parityExprs are expressions at the edges of the grammar: identifier
// characters (letter numbers, connector punctuation, ZWJ, escapes),
// literals (octal, legacy octal escapes, line separators in strings, hex,
// exponents), reserved words, regular expressions, object literals.
var parityExprs = []string{
	"datum.a + 1", "datum.Ⅻ * 2", "datum.a‿b", "datum.a\u200db", "datum.\\u0061b", "datum.\\u{61}b",
	"datum['a.b'].c", "datum[\"x\"][0]", "datum[010]", "datum[0x10]", "datum[1e3]", "datum[.5]", "datum[5.]",
	"datum[1e]", "09", "0x", "'\\101'", "'\\0'", "'\\08'", "'a\u2028b'", "'a\\\nb'", "\"\\x41\\u0042\\u{43}\"",
	"datum.in", "datum.if", "datum.null", "datum.true", "in", "if(a,b,c)", "this", "typeof a", "void 0", "delete a",
	"a in b", "a instanceof b", "a++", "--a", "a = 1", "a += 1", "a, b", "(a, b)", "a ? b : c ? d : e",
	"/ab+c/g.test(datum.x)", "/[/]/", "/a/x", "/(/", "/a\\/b/i", "a / b / c", "datum.a /= 2",
	"{a: 1, b: datum.c}", "{a: 1, a: 2}", "{1: 1, '1': 2}", "{if: 1}", "{'toString': datum.x}", "{a: 1,}", "{,}",
	"[1, 2,]", "[1,,2]", "[,]", "f(a,)", "f(,)", "f()", "datum['toString']", "'toString'", "'then'", "toString",
	"datum.a.b.c", "datum[datum.k]", "datum[datum.a + datum.b].c", "datum.__proto__", "datum['__proto__'].x",
	"a =>b", "`x`", "@", "#", "a\u00a0+\ufeffb", "a\u2028+b", "a\u0085+b", "1 // c", "1 /* c */",
	"!datum.a && datum.b || datum.c", "~datum.x", "-datum['a-b']", "datum.a<datum.b", "a >>> b", "a >>>= b",
	"", " ", "datum.", "1 +", "(1", "a b", "'abc", "datum[", "{a:}", "1.e5", "1e+", "0b11", "0o7", "1_000",
}

// depsByParse is getDependentFields over internal/expr's tree.
func depsByParse(n *expr.Node) []string {
	var name func(n *expr.Node) []string
	name = func(n *expr.Node) []string {
		switch n.Kind {
		case expr.KindIdentifier:
			return []string{n.Name}
		case expr.KindLiteral:
			if n.Regex != nil {
				return []string{n.Raw}
			}
			return []string{n.Value.AsString()}
		case expr.KindMember:
			return append(name(n.Left), name(n.Right)...)
		}
		return nil
	}
	startsWithDatum := func(n *expr.Node) bool {
		for n.Kind == expr.KindMember {
			n = n.Left
		}
		return n.Kind == expr.KindIdentifier && n.Name == "datum"
	}
	deps := newSset()
	n.Walk(func(n *expr.Node) bool {
		if n.Kind == expr.KindMember && startsWithDatum(n) {
			deps.add(strings.Join(name(n)[1:], "."))
		}
		return false
	})
	return ssetTexts(deps)
}

// exprParity reports how the compiler's reading of src differs from Parse's
// ("" when it does not). A text Parse refuses for its own limits (length,
// depth) is not compared: the compiler takes chains at any length.
func exprParity(src string) string {
	n, perr := expr.Parse(src)
	if perr != nil && errors.Is(perr, budget.ErrLimit) {
		return ""
	}
	got, err := func() (deps []string, err error) {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					err = e
					return
				}
				panic(r)
			}
		}()
		return ssetTexts(getDependentFields(nil, src)), nil
	}()
	switch {
	case perr != nil && err == nil:
		return "Parse refuses it (" + perr.Error() + "), the compiler accepts it"
	case perr == nil && err != nil:
		return "the compiler refuses it (" + err.Error() + "), Parse accepts it"
	case perr != nil:
		return ""
	}
	if want := depsByParse(n); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		return "fields " + strings.Join(got, ", ") + ", want " + strings.Join(want, ", ")
	}
	return ""
}

func TestExprDepsAcceptsAsParse(t *testing.T) {
	exprs := append([]string(nil), parityExprs...)
	// And every expression of the specification corpus.
	files, _ := filepath.Glob("testdata/specs/*/*.json")
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(data)
		if err != nil {
			continue
		}
		var walk func(v Value)
		walk = func(v Value) {
			switch {
			case v.IsObj():
				o := v.ObjValue()
				for i := 0; i < o.Len(); i++ {
					switch k := o.KeyAt(i); {
					case (k == "calculate" || k == "filter" || k == "expr" || k == "test" || k == "signal" || k == "labelExpr") && o.ValueAt(i).IsStr():
						exprs = append(exprs, o.ValueAt(i).StrValue())
					default:
						walk(o.ValueAt(i))
					}
				}
			case v.IsArr():
				for _, it := range v.Items() {
					walk(it)
				}
			}
		}
		walk(spec)
	}
	if len(exprs) < len(parityExprs)+100 {
		t.Fatalf("only %d expressions: the corpus walk is broken", len(exprs))
	}
	for _, e := range exprs {
		if d := exprParity(e); d != "" {
			t.Errorf("%q: %s", e, d)
		}
	}
}

func FuzzExprDepsAcceptsAsParse(f *testing.F) {
	for _, e := range parityExprs {
		f.Add(e)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<12 {
			return
		}
		if d := exprParity(src); d != "" {
			t.Errorf("%q: %s", src, d)
		}
	})
}
