package vegalite

import (
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Pan and zoom handlers, and the bound-scales push in a concat, look their
// signal up by name; upstream throws a TypeError when it is missing (a
// latitude channel on an interval selection bound to scales). The compiler
// returns that as an error, never as an internal error.
func TestSelectionMissingSignalIsAnError(t *testing.T) {
	cases := map[string]string{
		"../../testdata/corpus/regress/pan-zoom-on-channel-without-signal.vl.json":               "Cannot read properties of undefined (reading 'on')",
		"../../testdata/corpus/regress/bound-scales-in-concat-on-channel-without-signal.vl.json": "Cannot set properties of undefined (setting 'push')",
	}
	for file, want := range cases {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := jsval.ParseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Compile(spec, Options{})
		if err == nil || err.Error() != want {
			t.Errorf("%s: error %v, want %q", file, err, want)
		}
	}
}
