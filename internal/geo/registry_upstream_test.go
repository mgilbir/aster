package geo

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestProjectionTypesMatchUpstream checks that the registry holds the
// projections vega-projection registers, no more and no fewer, where the
// oracle's modules are installed (a projection d3-geo-projection has but Vega
// does not register, such as airy, is an error upstream too).
func TestProjectionTypesMatchUpstream(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "oracle-node", "node_modules", "vega-projection", "src", "projections.js")
	b, err := os.ReadFile(src)
	if err != nil {
		if os.Getenv("ASTER_ORACLE") == "require" {
			t.Fatal(err)
		}
		t.Skip("oracle node_modules not installed")
	}
	_, table, ok := strings.Cut(string(b), "const projections = {")
	table, _, ok2 := strings.Cut(table, "};")
	if !ok || !ok2 {
		t.Fatalf("%s: no projections table", src)
	}
	var want []string
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+):\s*geo\w+,?\s*$`).FindAllStringSubmatch(table, -1) {
		// projection(type, ...) lower-cases the name it registers.
		want = append(want, strings.ToLower(m[1]))
	}
	slices.Sort(want)
	if len(want) < 17 {
		t.Fatalf("%s: read only %d projections: %v", src, len(want), want)
	}
	if got := ProjectionTypes(); !slices.Equal(got, want) {
		t.Errorf("registry %v,\nvega-projection registers %v", got, want)
	}
}
