package jsval

import "testing"

// SplitFieldPath reports what vega-util's splitAccessPath throws, with the
// segments ParseFieldPath makes of the same text.
func TestSplitFieldPathErrors(t *testing.T) {
	for path, want := range map[string]string{
		"a.b":            "",
		"a[0]['x.y']":    "",
		`a\[b`:           "",
		"a[":             "Access path missing closing bracket: a[",
		"a['x":           "Access path missing closing bracket: a['x",
		`"abc`:           "Access path missing closing quote: \"abc",
		"a]":             "Access path missing open bracket: a]",
		"a[0]]":          "Access path missing open bracket: a[0]]",
		"a].b[":          "Access path missing open bracket: a].b[",
		"monthly(x['y')": "Access path missing closing bracket: monthly(x['y')",
	} {
		segs, err := SplitFieldPath(path)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("%q: error %q, want %q", path, got, want)
		}
		if want != "" && len(segs) == 0 {
			t.Errorf("%q: no segments", path)
		}
	}
}
