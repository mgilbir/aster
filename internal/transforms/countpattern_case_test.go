package transforms

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// countpattern's case parameter is toUpperCase and toLowerCase of the pinned
// node, not the Go toolchain's Unicode: U+019B has an upper case since Unicode
// 16, which Go 1.26 did not know (the answers are node 24.21.0's).
func TestCountPatternCaseIsJavaScripts(t *testing.T) {
	for _, c := range []struct {
		mode, text string
		want       []string
	}{
		{"upper", "ß Straße ƛ ŉ", []string{"SS", "STRASSE", "Ƛ", "ʼN"}},
		{"lower", "İ ΟΔΥΣΣΕΎΣ Ɤ", []string{"i̇", "οδυσσεύς", "ɤ"}},
	} {
		out, err := CountPattern(context.Background(), []jsval.Value{obj("t", jsval.Str(c.text))},
			CountPatternParams{Field: FieldOf("t"), Case: c.mode, Pattern: `\S+`})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, row := range out {
			got = append(got, row.ObjValue().Lookup("text").StrValue())
		}
		if len(got) != len(c.want) {
			t.Fatalf("case %q: tokens %+q, want %+q", c.mode, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("case %q: tokens %+q, want %+q", c.mode, got, c.want)
				break
			}
		}
	}
}
