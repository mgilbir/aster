package raster

import (
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/testutil"
	"github.com/mgilbir/aster/internal/text"
)

// TestLongFamilyListNotPerRun: a font-family of a megabyte is read once per
// element; shaping each run of its text does not go through it again (it
// built a CSS shorthand of it, and parsed that, per run: C23's class, which
// the structured font of C31 removes). Bytes allocated per run stay far
// below the family's size, for the converter's shaper and NewShaper's.
func TestLongFamilyListNotPerRun(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocation counts differ under -race")
	}
	long := strings.Repeat("X", 1<<20)
	m, err := text.New(text.WithExactAdvances())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := NewShaper()
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Shaper{"from a measurer": NewShaperFromMeasurer(m), "NewShaper": plain} {
		for _, families := range [][]string{{long, "sans-serif"}, {"NoSuch", long}} {
			req := FontRequest{Families: families, Weight: 400}
			s.Shape("warm", req, 12)
			var a, b runtime.MemStats
			runtime.ReadMemStats(&a)
			const runs = 200
			for range runs {
				s.Shape("label", req, 12)
			}
			runtime.ReadMemStats(&b)
			if per := (b.TotalAlloc - a.TotalAlloc) / runs; per > 64<<10 {
				t.Errorf("%s: %d bytes allocated a run with a %d byte family", name, per, len(long))
			}
		}
	}
}
