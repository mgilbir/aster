package vega

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// inScope finds the item it is given by identity and walks up its groups, as
// upstream's `item = item.mark.group` does; a value that is not an item reads
// mark of undefined, a TypeError. It used to read a number off the object
// and convert it to an int, which Go does differently on amd64 (NaN is the
// smallest int) and arm64 (NaN is 0, the first item). Node draws the first
// text "true/false/false" and logs the TypeError for the second, which has
// no text to draw.
func TestInScopeIsByIdentity(t *testing.T) {
	res, msgs := mustRenderReporting(t, readFile(t, "testdata/inscope.vg.json"))
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "TypeError: Cannot read properties of undefined (reading 'group')") {
		t.Fatalf("reported %q, want the TypeError", msgs)
	}
	var texts []string
	for _, m := range marks(res)[0].Items[0].Items {
		for _, it := range m.Items {
			texts = append(texts, it.Text().AsString())
		}
	}
	// the second item's encoder threw: it has no text, and is not drawn
	if strings.Join(texts, "|") != "true/false/false|undefined" {
		t.Errorf("texts %q, want true/false/false and none", texts)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ResolveFilter reads `curr[t._index]`, a property read: only an _index whose
// String() is an array index in range selects a row of the bitmap. (Node
// cannot check this end to end: its crossfilter hands indices, not tuples,
// to the operators after it.)
func TestResolveFilterReadsIndexAsAPropertyKey(t *testing.T) {
	curr := []uint32{0, 0, 1}
	for _, c := range []struct {
		index jsval.Value
		pass  bool
	}{
		{jsval.Num(1), true}, {jsval.Str("1"), true}, {jsval.Num(math.Copysign(0, -1)), true},
		{jsval.Num(2), false}, // its bit is set
		{jsval.Num(1.5), false}, {jsval.Str("01"), false}, {jsval.Num(math.NaN()), false},
		{jsval.Num(math.Inf(1)), false}, {jsval.Num(1e300), false}, {jsval.Num(-1), false},
		{jsval.Num(3), false}, {jsval.Undefined, false},
	} {
		if got := resolvePasses(curr, ^uint32(0), c.index); got != c.pass {
			t.Errorf("_index %v: passes %v, want %v", c.index, got, c.pass)
		}
	}
}
