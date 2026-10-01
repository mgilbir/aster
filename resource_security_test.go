package aster

// Resource-exhaustion and cancellation tests. Each hostile input is tiny (the
// size is in the name of the file under testdata/security) and runs in a child
// process (see harness_security_test.go) that is killed on wall-clock time or
// when its heap passes a cap, so a finding fails the test instead of taking the
// test run down.
//
// They are heavy (GBs, tens of seconds) and therefore opt in:
//
//	ASTER_SECURITY=1 go test . -run 'TestResource|TestTimeout' -v
//
// Thresholds are deliberately far above what a legitimate chart needs: "peak
// heap over 1 GiB for a 250 byte specification" is a finding on any machine.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func heavy(t *testing.T) {
	t.Helper()
	if os.Getenv("ASTER_SECURITY") == "" {
		t.Skip("set ASTER_SECURITY=1 to run the resource-exhaustion tests")
	}
}

func secFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "security", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type resCase struct {
	name, file, kind string
	timeout          string // WithTimeout
	memLimit         uint64 // WithMemoryLimit
	loader           string
	maxPeakMB        float64 // 0 = 1024
	maxCPU           float64 // 0 = no CPU assertion
	wall             time.Duration
}

func runRes(t *testing.T, c resCase) {
	heavy(t)
	if c.maxPeakMB == 0 {
		c.maxPeakMB = 1024
	}
	job := secJob{Kind: c.kind, Input: secFile(t, c.file), Timeout: c.timeout, MemLimit: c.memLimit, Loader: c.loader, HeapCapMB: 4096}
	res, killed := runSec(t, job, c.wall)
	t.Logf("%s: %d byte input, wall %.1fs, cpu %.1fs, peak %.0f MiB, killed=%v, err=%.120q", c.file, len(job.Input), res.Seconds, res.CPU, res.PeakMB, killed, res.Err)
	if res.Panic != "" {
		t.Errorf("panic escaped the public API: %s", res.Panic)
	}
	if killed {
		t.Errorf("killed (%s): exceeded the wall-clock or %d MiB heap cap", res.Err, job.HeapCapMB)
		return
	}
	if res.PeakMB > c.maxPeakMB {
		t.Errorf("peak heap %.0f MiB exceeds %.0f MiB", res.PeakMB, c.maxPeakMB)
	}
	if c.maxCPU > 0 && res.CPU > c.maxCPU {
		t.Errorf("used %.1f CPU-seconds with WithTimeout(%s); want under %.1f", res.CPU, c.timeout, c.maxCPU)
	}
}

// --- memory -----------------------------------------------------------------

// A 174 byte spec makes the engine build 4M rows, 4M scene items and a 370 MB
// SVG with the default limits (MaxRows and MaxItems are 5M each): about 7.5 GiB.
func TestResourceDefaultLimitsBoundHeap(t *testing.T) {
	runRes(t, resCase{name: "seq", file: "seq-4m-rows.vg.json", kind: "vega", maxPeakMB: 1024, wall: 120 * time.Second})
}

// WithMemoryLimit(64 MiB) maps to 262144 rows, but sequence/fold/parse
// allocate the whole output before the row count is checked.
func TestResourceMemoryLimitIsEnforcedBeforeAllocation(t *testing.T) {
	t.Run("sequence", func(t *testing.T) {
		runRes(t, resCase{file: "seq-4m-rows.vg.json", kind: "vega", memLimit: 64 << 20, maxPeakMB: 256, wall: 120 * time.Second})
	})
	t.Run("fold", func(t *testing.T) {
		runRes(t, resCase{file: "fold-1m-rows.vg.json", kind: "vega", memLimit: 64 << 20, maxPeakMB: 256, wall: 120 * time.Second})
	})
	t.Run("csv-from-loader", func(t *testing.T) {
		runRes(t, resCase{file: "big-csv.vg.json", kind: "vega", memLimit: 64 << 20, loader: "bigcsv:32", timeout: "5s", maxPeakMB: 256, wall: 120 * time.Second})
	})
}

// Expression string concatenation is unbounded (only pad/truncate check
// MaxStringLength): twelve chained signals turn a 16 MB string into 32 GB.
func TestResourceStringConcatenationBounded(t *testing.T) {
	runRes(t, resCase{file: "string-doubling.vg.json", kind: "vega", maxPeakMB: 1024, wall: 120 * time.Second})
}

// impute emits one tuple per (group, key) pair, 4e8 tuples here, with no limit
// (cross and aggregate have MaxGroupCells).
func TestResourceImputeCrossProduct(t *testing.T) {
	runRes(t, resCase{file: "impute-cross-product.vg.json", kind: "vega", timeout: "2s", maxPeakMB: 1024, wall: 120 * time.Second})
}

// kde: steps (up to MaxSteps=1e6) per group, groups unlimited.
func TestResourceKDEGroupsTimesSteps(t *testing.T) {
	runRes(t, resCase{file: "kde-groups-steps.vg.json", kind: "vega", timeout: "2s", maxPeakMB: 1024, wall: 120 * time.Second})
}

// flatten of rows whose arrays come from sequence() (1<<20 each).
func TestResourceFlattenArrays(t *testing.T) {
	runRes(t, resCase{file: "flatten-sequence-arrays.vg.json", kind: "vega", timeout: "1s", maxPeakMB: 1024, wall: 120 * time.Second})
}

// Geo: adaptive resampling has no point budget; at a huge projection scale a
// 515 byte Vega-Lite spec writes a 700 MB SVG path.
func TestResourceProjectionScale(t *testing.T) {
	runRes(t, resCase{file: "projection-scale-1e12.vl.json", kind: "vl", timeout: "1s", maxPeakMB: 1024, maxCPU: 5, wall: 120 * time.Second})
}

// --- cancellation -----------------------------------------------------------

// WithTimeout(1s) must stop the render promptly. The allowance is 5 CPU
// seconds, several times the timeout, to tolerate polling granularity.
func TestTimeoutIsHonoured(t *testing.T) {
	for _, c := range []resCase{
		{name: "bootstrap confidence intervals (aggregate ci0/ci1)", file: "ci-bootstrap-200k.vg.json"},
		{name: "polynomial regression order 100", file: "poly-regression-200k.vg.json"},
		{name: "pivot with 200k distinct columns", file: "pivot-200k-columns.vg.json"},
		{name: "window median over a wide frame", file: "window-median-wide-frame.vg.json"},
		{name: "legend with 90k entries", file: "legend-90k-entries.vg.json"},
		{name: "force collide, one tick, 100k nodes", file: "force-collide-100k.vg.json"},
		{name: "formula building 16M-character strings", file: "formula-pad-20k-rows.vg.json"},
	} {
		c := c
		t.Run(c.file, func(t *testing.T) {
			c.kind, c.timeout, c.maxCPU, c.maxPeakMB, c.wall = "vega", "1s", 5, 2048, 100*time.Second
			t.Log(c.name)
			runRes(t, c)
		})
	}
}

// The Vega-Lite compile phase takes no context and WithTimeout only starts
// with the Vega render, so compile time is unbounded; params-4000 (135 KB) is
// quadratic in the number of params.
func TestTimeoutCoversVegaLiteCompile(t *testing.T) {
	runRes(t, resCase{file: "params-4000.vl.json", kind: "vl2vega", timeout: "500ms", maxCPU: 2.5, maxPeakMB: 512, wall: 100 * time.Second})
}

// SVGToPNG / VegaToPNG take no context: WithTimeout does not reach the
// rasterizer, and nothing bounds pixels x elements, so a few hundred bytes
// cost minutes of CPU or gigabytes (16 nested opacity groups on a
// 64-megapixel canvas allocate a 256 MiB layer each).
func TestResourceRasterizer(t *testing.T) {
	t.Run("layers", func(t *testing.T) {
		runRes(t, resCase{file: "layers-8k.svg", kind: "svgpng", timeout: "1s", maxPeakMB: 1536, maxCPU: 5, wall: 100 * time.Second})
	})
	t.Run("use-expansion-fullcanvas", func(t *testing.T) {
		runRes(t, resCase{file: "use-expansion-fullcanvas.svg", kind: "svgpng", timeout: "1s", maxCPU: 5, wall: 100 * time.Second})
	})
	t.Run("rects-1000-fullcanvas", func(t *testing.T) {
		runRes(t, resCase{file: "rects-1000-fullcanvas.svg", kind: "svgpng", timeout: "1s", maxCPU: 5, wall: 100 * time.Second})
	})
}

// Guard: XML entities are not expanded and external entities not read.
func TestSVGToPNGIgnoresEntities(t *testing.T) {
	runRes2 := func() {
		job := secJob{Kind: "svgpng", Input: secFile(t, "entities.svg"), HeapCapMB: 512}
		res, killed := runSec(t, job, 30*time.Second)
		if killed || res.PeakMB > 256 {
			t.Errorf("entity document: killed=%v peak %.0f MiB err=%s", killed, res.PeakMB, res.Err)
		}
	}
	runRes2()
}

// The Vega-Lite compiler polls the deadline as it walks a spec's views and
// selections: a spec with a hundred thousand layers or selection parameters
// took 18 s and 12 s against a 2 s timeout before (and minutes in full).
func TestTimeoutCoversLargeVegaLiteSpecs(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: builds and compiles specs with 100,000 views and parameters")
	}
	items := func(n int, item func(i int) string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = item(i)
		}
		return strings.Join(parts, ",")
	}
	for name, spec := range map[string]string{
		"layers": `{"data":{"values":[{"a":1}]},"layer":[` + items(100000, func(int) string { return `{"mark":"point"}` }) + `]}`,
		"params": `{"data":{"values":[{"a":1}]},"mark":"point","params":[` + items(100000, func(i int) string { return fmt.Sprintf(`{"name":"p%d","select":"point"}`, i) }) + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, killed := runSec(t, secJob{Kind: "vl2vega", Input: spec, Timeout: "1s"}, 60*time.Second)
			if killed || res.Seconds > 3 {
				t.Errorf("compiling took %.1fs with a 1s timeout (killed %v): %s", res.Seconds, killed, res.Err)
			}
		})
	}
}
