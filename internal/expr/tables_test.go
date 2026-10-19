package expr

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
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
		Functions   []string                      `json:"functions"`
		Constants   []string                      `json:"constants"`
		Prototypes  map[string]map[string]*string `json:"prototypes"`
		ColourForIn map[string][]string           `json:"colourForIn"`
		Disallowed  []string                      `json:"disallowedProperties"`
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
	sameTable := func(name string, got protoTable, want map[string]*string) {
		if len(got) != len(want) {
			t.Errorf("%s: %d members, upstream %d", name, len(got), len(want))
		}
		for k, w := range want {
			fn := ""
			if w != nil {
				fn = *w
			}
			if g, ok := got[k]; !ok || g != fn {
				t.Errorf("%s.%s: %q (present %v), upstream %q", name, k, g, ok, fn)
			}
		}
	}
	for kind, table := range map[string]protoTable{
		"object": objectProto, "array": arrayProto, "string": stringProto, "number": numberProto,
		"boolean": booleanProto, "regexp": regexpProto, "date": dateProto, "Color": colorProto,
		"Rgb": rgbProto, "Hsl": hslProto, "Lab": labProto, "Hcl": hclProto,
	} {
		sameTable(kind, table, want.Prototypes[kind])
	}
	if len(want.Prototypes) != 12 {
		t.Errorf("upstream records %d prototypes; the engine models 12", len(want.Prototypes))
	}
	for class, names := range want.ColourForIn {
		if !slices.Equal(colourForIn[class], names) {
			t.Errorf("for-in over a %s colour: %v, upstream %v", class, colourForIn[class], names)
		}
	}
	sameSet("DisallowedObjectProperties", disallowedProperties, want.Disallowed)
}
