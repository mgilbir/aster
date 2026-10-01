package expr

import "testing"

// Upstream tests `legalKeywords[lookahead.value]` on a plain object for every
// token, so a string literal named "if" or like an Object.prototype property
// parses as an identifier. Property keys and member names are scanned
// elsewhere and stay as written.
func TestStringLiteralNamedLikeObjectPropertyIsIdentifier(t *testing.T) {
	for src, want := range map[string]string{
		`'toString'`:         "toString",
		`"constructor"`:      "constructor",
		`'if'`:               "if",
		`datum['valueOf']`:   "(idx datum valueOf)",
		`{'toString': 1}`:    `{"toString":1}`,
		`'other'`:            `"other"`,
		`datum.constructor`:  "(. datum constructor)",
		`['__proto__', 'a']`: `[__proto__ "a"]`,
	} {
		n, err := Parse(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got := sexp(n); got != want {
			t.Errorf("%s: parsed as %s, want %s", src, got, want)
		}
	}
}
