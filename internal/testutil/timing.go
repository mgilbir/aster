package testutil

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TimingPrefix starts every line Timing writes; scripts/timings.sh reads them.
const TimingPrefix = "ASTER_TIMING"

var (
	timingOnce sync.Once
	timingOn   bool
	timingFile string
)

func timingInit() {
	timingOn = os.Getenv("ASTER_TIMINGS") != ""
	timingFile = os.Getenv("ASTER_TIMINGS_FILE")
}

// Timing records, when ASTER_TIMINGS is set, what a wall-clock (or CPU-time)
// assertion measured and the bound it is held to, so one CI run can show the
// real value of every such bound on the runner. Call it right before the
// comparison, with the bound in force (after any race scaling). Unset, it
// costs one sync.Once check and a bool test.
//
// The line is tab separated: ASTER_TIMING, test name, site, measured seconds,
// bound seconds, race (0/1), GOARCH. It goes to stdout with fmt.Printf, which
// shows only under go test -v: go test hides the stdout of a passing test
// binary without it (checked, in package-list and in directory mode alike), and
// t.Logf is the same. So when ASTER_TIMINGS_FILE names an absolute path the
// line is also appended to that file, one O_APPEND write per line, which keeps
// lines whole across the test binaries (and child processes) running in
// parallel. The file is the channel that works without -v.
func Timing(t testing.TB, site string, measured, bound time.Duration) {
	timingOnce.Do(timingInit)
	if !timingOn {
		return
	}
	race := 0
	if RaceEnabled {
		race = 1
	}
	line := fmt.Sprintf("%s\t%s\t%s\t%.6f\t%.6f\t%d\t%s\n", TimingPrefix, t.Name(), site,
		measured.Seconds(), bound.Seconds(), race, runtime.GOARCH)
	fmt.Print(line)
	if timingFile != "" {
		if f, err := os.OpenFile(timingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
		}
	}
}

// TimingSeconds is Timing for a measure held as seconds.
func TimingSeconds(t testing.TB, site string, measured, bound float64) {
	Timing(t, site, time.Duration(measured*float64(time.Second)), time.Duration(bound*float64(time.Second)))
}

// Relay passes on a Timing line a child process printed, to this process's
// stdout. It does not write the file: the child, which inherits the
// environment, has appended the line there already.
func Relay(line string) {
	timingOnce.Do(timingInit)
	if timingOn {
		fmt.Println(line)
	}
}
