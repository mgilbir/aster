package format

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestParseDateTokenizerEdges replays testdata/dateparse.jsonl.gz: V8's
// Date.parse of strings with each white space character, and with a NUL,
// which ends V8's input wherever it stands (C123, audit 2026-10-18).
func TestParseDateTokenizerEdges(t *testing.T) {
	b, err := os.ReadFile("testdata/dateparse.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		doc, err := jsval.ParseJSON(line)
		if err != nil {
			t.Fatal(err)
		}
		name := doc.Get("tz").StrValue()
		z := Local(mustLoc(t, name))
		for _, c := range doc.Get("cases").Items() {
			s := c.Index(0).StrValue()
			want := math.NaN()
			if v := c.Index(1); v.IsNum() {
				want = v.NumValue()
			}
			if got := ParseDate(s, z); got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
				t.Errorf("%s Date.parse(%q) = %v; V8 %v", name, s, got, want)
			}
		}
	}
}
