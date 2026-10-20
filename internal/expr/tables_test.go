package expr

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestFunctionTable checks that every function and constant of upstream's
// expression table (testdata/gen_functions.mjs) is present, and that nothing
// else is.
func TestFunctionTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/functions.json")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Functions  []string `json:"functions"`
		Constants  []string `json:"constants"`
		Prototypes map[string]map[string]*struct {
			Name   string `json:"name"`
			Length int    `json:"length"`
		} `json:"prototypes"`
		ColourForIn map[string][]string `json:"colourForIn"`
		Disallowed  []string            `json:"disallowedProperties"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for name := range funcTable {
		have[name] = true
	}
	for _, name := range want.Functions {
		if !have[name] {
			t.Errorf("missing function %s", name)
		}
		delete(have, name)
	}
	for name := range have {
		t.Errorf("function %s is not in upstream's table", name)
	}
	gotConsts := Constants()
	if len(gotConsts) != len(want.Constants) {
		t.Fatalf("constants %v, upstream %v", gotConsts, want.Constants)
	}
	for i := range gotConsts {
		if gotConsts[i] != want.Constants[i] {
			t.Errorf("constants %v, upstream %v", gotConsts, want.Constants)
		}
	}
	sameSet := func(name string, got map[string]bool, want []string) {
		if len(got) != len(want) {
			t.Errorf("%s: %d names, upstream %d (%v)", name, len(got), len(want), want)
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("%s: missing %q", name, w)
			}
		}
	}
	// The d3 colours' prototypes are the expression language's own; the
	// built-in prototypes' members are jsval's, checked there against node's
	// (TestPrototypeNamesMatchNode).
	sameTable := func(name string, got jsval.ProtoTable, want map[string]*struct {
		Name   string `json:"name"`
		Length int    `json:"length"`
	}) {
		if len(got) != len(want) {
			t.Errorf("%s: %d members, upstream %d", name, len(got), len(want))
		}
		for k, w := range want {
			g, ok := got[k]
			switch {
			case !ok:
				t.Errorf("%s.%s: missing", name, k)
			case w == nil:
				if g.Fn != "" {
					t.Errorf("%s.%s: function %q, upstream none", name, k, g.Fn)
				}
			case g.Fn != w.Name || g.Arity != w.Length:
				t.Errorf("%s.%s: function %q length %d, upstream %q length %d", name, k, g.Fn, g.Arity, w.Name, w.Length)
			}
		}
	}
	for kind, table := range map[string]jsval.ProtoTable{
		"Color": colorProto, "Rgb": rgbProto, "Hsl": hslProto, "Lab": labProto, "Hcl": hclProto,
	} {
		sameTable(kind, table, want.Prototypes[kind])
	}
	if len(want.Prototypes) != 5 {
		t.Errorf("upstream records %d colour prototypes; the engine models 5", len(want.Prototypes))
	}
	for class, names := range want.ColourForIn {
		if !slices.Equal(colourForIn[class], names) {
			t.Errorf("for-in over a %s colour: %v, upstream %v", class, colourForIn[class], names)
		}
	}
	sameSet("DisallowedObjectProperties", disallowedProperties, want.Disallowed)
}
