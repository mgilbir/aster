package vega

import (
	"context"
	"strings"
	"testing"
)

// Loaded text (CSV, TSV, DSV) is decoded as Response.text() decodes it,
// WHATWG UTF-8, one U+FFFD each maximal subpart of a malformed sequence
// (TextDecoder in node 24 gives these), also across textOf's chunks.
func TestLoadedTextDecodesAsWHATWG(t *testing.T) {
	for in, want := range map[string]string{
		"a,b\n\xf0\x9f\x98,1\n": "a,b\n�,1\n",
		"\xe2\x82\xe2\x82\xac":  "�€",
		"\xed\xa0\x80x":         "���x",
		"\xc0\xaf\x80":          "���",
		"ok é":                  "ok é",
	} {
		got, err := textOf(context.Background(), []byte(in))
		if err != nil || got != want {
			t.Errorf("textOf(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// A sequence across a chunk's end is decoded whole.
	in := strings.Repeat("a", textChunk-1) + "\xf0\x9f\x98\x80" + "\xf0\x9f" + "b"
	want := strings.Repeat("a", textChunk-1) + "😀�b"
	if got, _ := textOf(context.Background(), []byte(in)); got != want {
		t.Errorf("across a chunk: %d bytes, want %d", len(got), len(want))
	}
}
