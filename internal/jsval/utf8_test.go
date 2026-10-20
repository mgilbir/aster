package jsval

import (
	"encoding/hex"
	"os"
	"testing"
)

// Malformed UTF-8 decodes as WHATWG's decoder (TextDecoder, Response.text())
// decodes it, one U+FFFD each maximal subpart (testdata/utf8.json, recorded
// from node 24 by testdata/gen_utf8.mjs), in AppendDecoded and in ParseJSON's
// strings, whose text is the same bytes.
func TestMalformedUTF8DecodesAsWHATWG(t *testing.T) {
	raw, err := os.ReadFile("testdata/utf8.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len() < 40 {
		t.Fatalf("%d vectors", doc.Len())
	}
	for _, c := range doc.Items() {
		in, _ := hex.DecodeString(c.Get("in").StrValue())
		want, _ := hex.DecodeString(c.Get("out").StrValue())
		if got := AppendDecoded(nil, in); string(got) != string(want) {
			t.Errorf("decode %x = %x, TextDecoder gives %x", in, got, want)
		}
		if n := DecodedLen(in); n != len(want) {
			t.Errorf("DecodedLen(%x) = %d, want %d", in, n, len(want))
		}
		for _, doc := range [][]byte{
			append(append([]byte(`"`), in...), '"'),          // the fast path
			append(append([]byte(`"\n`), in...), "\\n\""...), // the slow path, after an escape
		} {
			v, err := ParseJSON(doc)
			if err != nil {
				t.Fatalf("%x: %v", doc, err)
			}
			s := v.StrValue()
			if doc[1] == '\\' {
				s = s[1 : len(s)-1]
			}
			if s != string(want) {
				t.Errorf("ParseJSON(%x) = %x, want %x", doc, s, want)
			}
		}
	}
}
