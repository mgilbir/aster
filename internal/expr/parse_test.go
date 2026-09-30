package expr

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// sexp prints an AST compactly for structural assertions.
func sexp(n *Node) string {
	switch n.Kind {
	case KindLiteral:
		if n.Regex != nil {
			return "/" + n.Regex.Source + "/" + n.Regex.Flags
		}
		if n.Value.IsStr() {
			return strconv.Quote(n.Value.StrValue())
		}
		return n.Value.AsString()
	case KindIdentifier:
		return n.Name
	case KindArray:
		return "[" + joinNodes(n.Elems, " ") + "]"
	case KindObject:
		return "{" + joinNodes(n.Elems, " ") + "}"
	case KindProperty:
		return sexp(n.Key) + ":" + sexp(n.Right)
	case KindUnary:
		return "(" + n.Op + " " + sexp(n.Left) + ")"
	case KindBinary, KindLogical:
		return "(" + n.Op + " " + sexp(n.Left) + " " + sexp(n.Right) + ")"
	case KindConditional:
		return "(? " + sexp(n.Test) + " " + sexp(n.Left) + " " + sexp(n.Right) + ")"
	case KindMember:
		if n.Computed {
			return "(idx " + sexp(n.Left) + " " + sexp(n.Right) + ")"
		}
		return "(. " + sexp(n.Left) + " " + sexp(n.Right) + ")"
	case KindCall:
		return "(call " + sexp(n.Left) + " " + joinNodes(n.Elems, " ") + ")"
	}
	return "?"
}

func joinNodes(ns []*Node, sep string) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = sexp(n)
	}
	return strings.Join(parts, sep)
}

func TestParseStructure(t *testing.T) {
	for src, want := range map[string]string{
		"1 + 2 * 3":                 "(+ 1 (* 2 3))",
		"1 * 2 + 3":                 "(+ (* 1 2) 3)",
		"1 - 2 - 3":                 "(- (- 1 2) 3)",
		"a || b && c":               "(|| a (&& b c))",
		"a && b || c":               "(|| (&& a b) c)",
		"a | b ^ c & d":             "(| a (^ b (& c d)))",
		"a == b < c":                "(== a (< b c))",
		"a << b + c":                "(<< a (+ b c))",
		"a + b << c":                "(<< (+ a b) c)",
		"a ? b : c ? d : e":         "(? a b (? c d e))",
		"a ? b ? c : d : e":         "(? a (? b c d) e)",
		"-a * b":                    "(* (- a) b)",
		"!a == b":                   "(== (! a) b)",
		"- -a":                      "(- (- a))",
		"a.b.c":                     "(. (. a b) c)",
		"a[1][2]":                   "(idx (idx a 1) 2)",
		"f(a, b)(c)":                "(call (call f a b) c)",
		"f(a)[0].x":                 "(. (idx (call f a) 0) x)",
		"[1, [2], {a: 3}]":          "[1 [2] {a:3}]",
		"[1, 2,]":                   "[1 2]",
		"{'a b': 1, 2: x, if: y}":   `{"a b":1 2:x if:y}`,
		"datum.if + datum.in":       "(+ (. datum if) (. datum in))",
		"if(a, b, c)":               "(call if a b c)",
		"a in b":                    "(in a b)",
		"a instanceof b":            "(instanceof a b)",
		"(a + b) * c":               "(* (+ a b) c)",
		"a === b !== c":             "(!== (=== a b) c)",
		"a >>> b >> c":              "(>> (>>> a b) c)",
		"1e3 + .5 + 5. + 0x1F":      "(+ (+ (+ 1000 0.5) 5) 31)",
		`'a\tb'`:                    `"a\tb"`,
		`"\u0041\x42\u{43}"`:        `"ABC"`,
		`'\uD83D\uDE00'`:            `"😀"`,
		"/a[/]b/gi":                 "/a[/]b/gi",
		"/=/":                       "/=/",
		"x / y / z":                 "(/ (/ x y) z)",
		"a/b/c":                     "(/ (/ a b) c)",
		"test(/a/, 'b') ? 1 : 2":    `(? (call test /a/ "b") 1 2)`,
		"a\u00a0+\u2003b":           "(+ a b)",
		"\\u0061bc":                 "abc",
		"ünï + $x + _y":             "(+ (+ ünï $x) _y)",
		"a.if.in":                   "(. (. a if) in)",
		"{a: 1}.a":                  "(. {a:1} a)",
		"1 + (2)":                   "(+ 1 2)",
		"'x'.length":                "(. \"x\" length)",
		"a ? b : c ? d : e ? f : g": "(? a b (? c d (? e f g)))",
		"a || b || c":               "(|| (|| a b) c)",
		"a + b * c - d / e % f":     "(- (+ a (* b c)) (% (/ d e) f))",
		"a < b == c > d":            "(== (< a b) (> c d))",
		"a & b == c":                "(& a (== b c))",
		"!!a":                       "(! (! a))",
		"~a + +b":                   "(+ (~ a) (+ b))",
		"datum['a b'].c":            `(. (idx datum "a b") c)`,
		"a[b ? c : d]":              "(idx a (? b c d))",
		"a[b, c]":                   "",
		"f(a ? b : c, [d])":         "(call f (? a b c) [d])",
	} {
		n, err := Parse(src)
		if want == "" {
			if err == nil {
				t.Errorf("%q: expected an error", src)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got := sexp(n); got != want {
			t.Errorf("%q:\n got %s\nwant %s", src, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		"":                "Unexpected end of input",
		" ":               "Unexpected end of input",
		"1 +":             "Unexpected end of input",
		"1 2":             "Unexpect token after expression.",
		"(1":              "Unexpected end of input",
		"1)":              "Unexpect token after expression.",
		"[1":              "Unexpected end of input",
		"{a: 1":           "Unexpected end of input",
		"{a}":             "Unexpected token }",
		"{a: 1, a: 2}":    "Duplicate data property",
		"{1: 1, '1': 2}":  "Duplicate data property",
		"[1,,2]":          "Unexpected token ,",
		"a, b":            "Disabled.",
		"a = 1":           "Unexpect token after expression.",
		"a += 1":          "Unexpect token after expression.",
		"a++":             "Disabled.",
		"--a":             "Disabled.",
		"typeof a":        "Disabled.",
		"void 0":          "Disabled.",
		"delete a.b":      "Disabled.",
		"this":            "Disabled.",
		"new Date":        "Disabled.",
		"function f() {}": "Disabled.",
		"var a":           "Disabled.",
		"a ?? b":          "Unexpected token ?",
		"a?.b":            "Unexpected token .",
		"a ** b":          "Unexpected token *",
		"a => a":          "Unexpect token after expression.",
		"1 // 2":          "Unexpected token ILLEGAL",
		"1 /* c */":       "Unexpected token *",
		"`a`":             "Unexpected token ILLEGAL",
		"#a":              "Unexpected token ILLEGAL",
		"'abc":            "Unexpected token ILLEGAL",
		"'a\nb'":          "Unexpected token ILLEGAL",
		"'\\xZ'":          "Unexpected token ILLEGAL",
		"'\\u12'":         "Unexpected token ILLEGAL",
		"'\\u{110000}'":   "Unexpected token ILLEGAL",
		"'\\101'":         "Octal literals are not allowed",
		"017":             "Octal literals are not allowed",
		"09":              "Unexpected token ILLEGAL",
		"1a":              "Unexpected token ILLEGAL",
		"1e":              "Unexpected token ILLEGAL",
		"0x":              "Unexpected token ILLEGAL",
		"3in a":           "Unexpected token ILLEGAL",
		"/a":              "Invalid regular expression: missing /",
		"/(/":             "Invalid regular expression",
		"/a/x":            "Invalid regular expression",
		"/a\n/":           "Invalid regular expression: missing /",
		"a.":              "Unexpected end of input",
		"a.1":             "Unexpect token after expression.",
		"a['x'":           "Unexpected end of input",
		"a[]":             "Unexpected token ]",
		"?":               "Unexpected token ?",
		"a ? b":           "Unexpected end of input",
		"a ? b :":         "Unexpected end of input",
		"f(a,)":           "Unexpected token )",
		"f(a b)":          "Unexpected identifier",
		"\\u0031":         "Unexpected token ILLEGAL",
		"a\\u002fb":       "Unexpected token ILLEGAL",
		"'\\u{}'":         "Unexpected token ILLEGAL",
		"@":               "Unexpected token ILLEGAL",
		"a b":             "Unexpect token after expression.",
		"{'a': 1 'b': 2}": "Unexpected string",
		"{.: 1}":          "Unexpected token .",
		"{if}":            "Unexpected token }",
		"if":              "",
		"0b11":            "Unexpected token ILLEGAL",
		"1_000":           "Unexpected token ILLEGAL",
		"'\\u{1F600'":     "Unexpected token ILLEGAL",
		"a instanceof":    "Unexpected end of input",
		"in":              "Disabled.",
		"else":            "Disabled.",
		"a.b(":            "Unexpected end of input",
		"\u2028":          "Unexpected end of input",
		"\u00e9\u0301":    "",
		"x\u200by":        "Unexpected token ILLEGAL",
	} {
		_, err := Parse(src)
		switch {
		case want == "":
			if err != nil && src != "if" {
				// "if" alone is an identifier; the second case is a valid identifier.
				t.Errorf("%q: unexpected error %v", src, err)
			}
		case err == nil:
			t.Errorf("%q: expected error %q", src, want)
		case !strings.Contains(err.Error(), want):
			t.Errorf("%q: error %q, want it to contain %q", src, err, want)
		}
	}
}

func TestParseLimits(t *testing.T) {
	long := strings.Repeat("1+", MaxLength/2) + "1"
	if _, err := Parse(long); err == nil {
		t.Error("expression longer than MaxLength accepted")
	}
	for name, src := range map[string]string{
		"parens":  strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000),
		"arrays":  strings.Repeat("[", 5000) + strings.Repeat("]", 5000),
		"objects": strings.Repeat("{a:", 5000) + "1" + strings.Repeat("}", 5000),
		"unary":   strings.Repeat("!", 5000) + "a",
		"calls":   strings.Repeat("f(", 5000) + "1" + strings.Repeat(")", 5000),
		"ternary": strings.Repeat("a?b:", 5000) + "c",
		"chain":   "a" + strings.Repeat("+a", 5000),
		"members": "a" + strings.Repeat(".b", 5000),
		"index":   "a" + strings.Repeat("[0]", 5000),
	} {
		_, err := Parse(src)
		if err == nil {
			t.Errorf("%s: nesting of 5000 accepted", name)
		} else if !strings.Contains(err.Error(), "nested too deeply") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Realistic depth is fine.
	ok := "a" + strings.Repeat("+a", 200)
	if _, err := Parse(ok); err != nil {
		t.Errorf("200-term chain: %v", err)
	}
}

func TestCompileErrors(t *testing.T) {
	for src, want := range map[string]string{
		"foo(1)":                    "Unrecognized function: foo",
		"datum.x(1)":                "Illegal callee type: MemberExpression",
		"abs(1)(2)":                 "Illegal callee type: CallExpression",
		"_":                         "Illegal identifier: _",
		"_.x":                       "Illegal identifier: _",
		"{toString: 1}":             "Illegal property: toString",
		"{constructor: 1}":          "Illegal property: constructor",
		"if(1)":                     "Missing arguments to if function.",
		"if(1,2,3,4)":               "Too many arguments to if function.",
		"clamp(1, 2)":               "Missing arguments to clamp function.",
		"clamp(1,2,3,4)":            "Too many arguments to clamp function.",
		"upper()":                   "Missing arguments to upper function.",
		"length()":                  "Missing arguments to length function.",
		"length(1)":                 "Invalid or unexpected token",
		"(1).x":                     "Invalid or unexpected token",
		"data()":                    "First argument to data functions must be a string literal.",
		"data(datum.x)":             "First argument to data functions must be a string literal.",
		"indata('a', b)":            "Second argument to indata must be a string literal.",
		"indata(a, 'b')":            "First argument to indata must be a string literal.",
		"vlSelectionTest(a, datum)": "First argument to selection functions must be a string literal.",
		"treePath(x, 1, 2)":         "First argument to data functions must be a string literal.",
		"scale()":                   "Missing scale argument.",
	} {
		_, err := Compile(src)
		switch {
		case want == "":
			if err == nil {
				t.Errorf("%q: expected an error", src)
			}
		case err == nil:
			t.Errorf("%q: expected error %q", src, want)
		case !strings.Contains(err.Error(), want):
			t.Errorf("%q: error %q, want %q", src, err, want)
		}
	}
}

func TestDeps(t *testing.T) {
	type deps = Deps
	for src, want := range map[string]deps{
		"a + b * a":                                {Signals: []string{"a", "b"}},
		"datum.x + datum['y z'] + datum.o.p":       {Fields: []string{"x", "y z", "o"}},
		"datum[k]":                                 {Signals: []string{"k"}},
		"datum[0] + datum[\"q\"]":                  {Fields: []string{"0", "q"}},
		"PI * NaN + E":                             {},
		"{a: b, 'c': d}":                           {Signals: []string{"b", "d"}},
		"abs(1)":                                   {},
		"abs(x) + max(y, z.w)":                     {Signals: []string{"x", "y", "z"}},
		"data('t')":                                {Data: []string{"t"}},
		"length(data('t')) + length(data('u'))":    {Data: []string{"t", "u"}},
		"indata('t', 'f', datum.v)":                {Data: []string{"t"}, RequiredData: []string{"t"}, Indexes: []IndexDep{{"t", "f"}}, Fields: []string{"v"}},
		"scale('x', datum.v)":                      {Scales: []string{"x"}, Fields: []string{"v"}},
		"scale(name, 1)":                           {Signals: []string{"name"}, AllScales: true},
		"bandwidth('x') + range('y')[0]":           {Scales: []string{"x", "y"}},
		"invert('x', 1) + domain('x')[0]":          {Scales: []string{"x"}},
		"gradient('c', [0,0], [1,0])":              {Scales: []string{"c"}},
		"geoShape('p', datum)":                     {Scales: []string{"p"}},
		"vlSelectionTest('s', datum)":              {Data: []string{"s"}, RequiredData: []string{"s"}},
		"vlSelectionTest('s', datum, 'intersect')": {Data: []string{"s"}, RequiredData: []string{"s"}, Indexes: []IndexDep{{"s", "unit"}}},
		"treeAncestors('h', 3)":                    {Data: []string{"h"}},
		"event.x + item.y":                         {},
		"if(a, b, c)":                              {Signals: []string{"a", "b", "c"}},
		"test(/x/, s)":                             {Signals: []string{"s"}},
	} {
		p, err := Compile(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		got := p.Deps()
		norm := func(d Deps) string {
			d2 := d
			if len(d2.Signals) == 0 {
				d2.Signals = nil
			}
			return fmt.Sprintf("%+v", d2)
		}
		if norm(got) != norm(want) {
			t.Errorf("%q:\n got %+v\nwant %+v", src, got, want)
		}
	}
}
