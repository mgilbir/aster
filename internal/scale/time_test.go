package scale

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/mgilbir/aster/internal/format"
)

// TestTimeScalesGolden replays time and utc scales recorded with
// TZ=America/New_York, which exercises DST transitions for the local calendar.
func TestTimeScalesGolden(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	old := LocalZone
	LocalZone = format.Local(loc)
	defer func() { LocalZone = old }()
	runGolden(t, "testdata/time.json.gz")
}
