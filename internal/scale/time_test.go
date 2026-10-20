package scale

import (
	"testing"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/nodetz"
)

// TestTimeScalesGolden replays time and utc scales recorded with
// TZ=America/New_York, which exercises DST transitions for the local calendar.
func TestTimeScalesGolden(t *testing.T) {
	runGolden(t, "testdata/time.json.gz", format.Local(nodetz.MustLoad(t, "America/New_York")))
}
