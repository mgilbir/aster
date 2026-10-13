package expr

import (
	"encoding/json"
	"os"
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
		Functions  []string            `json:"functions"`
		Constants  []string            `json:"constants"`
		Prototypes map[string][]string `json:"prototypes"`
		Disallowed []string            `json:"disallowedProperties"`
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
	sameSet("Object.prototype", objectProtoMembers, want.Prototypes["object"])
	sameSet("Array.prototype", arrayProtoMembers, want.Prototypes["array"])
	sameSet("RegExp.prototype", regexpProtoMembers, want.Prototypes["regexp"])
	sameSet("Date.prototype", dateProtoMembers, want.Prototypes["date"])
	sameSet("DisallowedObjectProperties", disallowedProperties, want.Disallowed)
}
