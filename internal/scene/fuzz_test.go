package scene

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fuzzutil"
)

// sentinels are the errors a scene that upstream cannot draw gives.
var sentinels = []error{ErrUnknownInterpolate, ErrNoShapeGenerator, ErrCurveNoArea, ErrInvalidPath, errPathParamCount, errPathParamType, errNegativeRadius}

// FuzzFromJSON reads arbitrary documents as serialized scenegraphs and bounds
// what it accepts, as the renderer does: nothing panics, nothing runs away,
// and a scene that cannot be bounded fails with a limit (ErrTooDeep wraps
// budget.ErrLimit) or one of the sentinels.
func FuzzFromJSON(f *testing.F) {
	corpus := loadCorpus(f)
	slices.SortFunc(corpus, func(a, b corpusEntry) int { return len(a.Scene) - len(b.Scene) })
	// the 24 smallest scenes of the upstream corpus
	for _, e := range corpus[:24] {
		if len(e.Scene) <= fuzzutil.MaxSeedBytes {
			f.Add([]byte(e.Scene))
		}
	}
	for _, s := range []string{
		`{"marktype":"path","clip":{"path":"M0,0L10,10Z"},"items":[{"path":"M0,0A5,5,0,1,1,10,0","fill":{"gradient":"linear","stops":[{"offset":0,"color":"red"}]}}]}`,
		`{"marktype":"group","items":[{"x":1,"width":"5","items":[{"marktype":"text","items":[{"text":"hi","angle":45,"limit":10,"align":"center","font":"serif"}]}]}]}`,
		`{"marktype":"shape","items":[{"shape":{"path":"M1,1L2,2"},"stroke":{"$gid":1,"gradient":"radial","stops":[]}},{"stroke":{"$gref":1}}]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzutil.Within(t, 10*time.Second, string(data), func() {
			sg, err := FromJSON(data)
			if err != nil {
				return
			}
			err = NewBounder(nil).BoundTree(sg.Root)
			if err != nil && !errors.Is(err, budget.ErrLimit) && !slices.ContainsFunc(sentinels, func(e error) bool { return errors.Is(err, e) }) {
				t.Errorf("%q: bounds: unexpected error %v", data, err)
			}
		})
	})
}
