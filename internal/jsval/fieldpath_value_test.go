package jsval

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestSplitAccessPathOfMatchesNode replays vega-util's splitAccessPath of
// values that are no string, recorded in node 24 (vega-util 2.1.3) in
// testdata/splitaccesspath.json, and checks that FieldPathText of the
// segments splits back to them.
func TestSplitAccessPathOfMatchesNode(t *testing.T) {
	b, err := os.ReadFile("testdata/splitaccesspath.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		V    json.RawMessage
		Segs []string
		Err  string
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		v, err := ParseJSON(c.V)
		if err != nil {
			t.Fatal(err)
		}
		segs, err := SplitAccessPathOf(v, v.AsString())
		switch {
		case c.Err != "":
			if err == nil || err.Error() != c.Err {
				t.Errorf("%s: err %v, want %q", c.V, err, c.Err)
			}
			continue
		case err != nil:
			t.Errorf("%s: %v", c.V, err)
			continue
		}
		if segs == nil {
			segs = []string{}
		}
		if !slices.Equal(segs, c.Segs) {
			t.Errorf("%s: %q, want %q", c.V, segs, c.Segs)
		}
		back, err := SplitFieldPath(FieldPathText(segs))
		if err != nil || len(segs) > 0 && !slices.Equal(back, segs) {
			t.Errorf("%s: FieldPathText %q splits to %q (%v)", c.V, FieldPathText(segs), back, err)
		}
	}
}
