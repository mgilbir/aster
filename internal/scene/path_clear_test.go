package scene

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// item.path = value assigns whatever the encoder produced: a null path
// replaces an earlier one (the voronoi transform nulls a cell it no longer
// draws on a later run), and the item then has no path to write.
func TestNullPathClearsPreviousPath(t *testing.T) {
	var it Item
	if _, err := it.Set("path", jsval.Str("M0,0L1,1Z")); err != nil {
		t.Fatal(err)
	}
	if !it.Path().Set || it.Path().D != "M0,0L1,1Z" {
		t.Fatalf("path = %+v", it.Path())
	}
	for _, v := range []jsval.Value{jsval.Null, jsval.Undefined} {
		it.Set("path", jsval.Str("M0,0L1,1Z"))
		it.Set("path", v)
		if it.Path().Set {
			t.Errorf("path survived %v: %+v", v, it.Path())
		}
		if got := it.Get("path"); !got.IsNullish() {
			t.Errorf("path reads back %v", got)
		}
	}
}
