package svg

import (
	"encoding/xml"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscapedOutputIsWellFormedXML(t *testing.T) {
	in := "a\x01b\x08c￾d￿e\xffg\xed\xa0\x80h\tij<&>\"ké\U0001F600"
	for _, attr := range []bool{false, true} {
		out := string(appendEscaped(nil, in, attr))
		if !utf8.ValidString(out) {
			t.Errorf("attr=%v: invalid UTF-8: %q", attr, out)
		}
		doc := `<a x="` + string(appendEscaped(nil, in, true)) + `">` + string(appendEscaped(nil, in, false)) + `</a>`
		if err := xml.Unmarshal([]byte(doc), new(struct{})); err != nil {
			t.Fatalf("not well-formed: %v\n%q", err, doc)
		}
		if strings.ContainsAny(out, "\x01\x08￾￿") {
			t.Errorf("forbidden characters survived: %q", out)
		}
	}
	if got := string(appendEscapedBytes(nil, []byte("a\x02<"))); got != "a&lt;" {
		t.Errorf("bytes variant: %q", got)
	}
	if got := string(appendEscaped(nil, "plain é text", false)); got != "plain é text" {
		t.Errorf("valid text must be untouched: %q", got)
	}
}

// A blend value is written as upstream writes it unless it could be more
// than one CSS value in the style attribute.
func TestBlendIsCSSSafe(t *testing.T) {
	for _, m := range []string{"multiply", "color-dodge", "1e+308", "[object Object]", "a,b", "", "Multiply"} {
		if !cssValueSafe(m) {
			t.Errorf("%q must be written", m)
		}
	}
	for _, m := range []string{"normal;background:url(http://x)", "a;b", "url(x)", "u\\72l(x)", "a}b", "x{", "a:b", "'x'", "\"x\"", "a/*b*/", "a\nb"} {
		if cssValueSafe(m) {
			t.Errorf("%q must be rejected", m)
		}
	}
}
