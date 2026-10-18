package aster

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The settings of the robustness harnesses that docs/testing.md states, as
// the tests set them (see TestTestingDocCounts for the rest).
func TestTestingDocHarnessCounts(t *testing.T) {
	b, err := os.ReadFile("docs/testing.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	is := func(re string, want float64) {
		t.Helper()
		ms := regexp.MustCompile(re).FindAllStringSubmatch(doc, -1)
		if len(ms) != 1 {
			t.Errorf("docs/testing.md states no count matching %q (%d matches); reword the test with the doc", re, len(ms))
			return
		}
		s := strings.ReplaceAll(ms[0][1], ",", "")
		if w, ok := map[string]float64{"two": 2, "three": 3, "four": 4, "five": 5}[s]; ok {
			s = strconv.FormatFloat(w, 'g', -1, 64)
		}
		got, err := strconv.ParseFloat(s, 64)
		if err != nil || got != want {
			t.Errorf("docs/testing.md states %q, the test has %v", ms[0][0], want)
		}
	}
	is(`drives ([\d,]+) deeply nested shapes`, float64(len(deepShapes)))
	is(`to depths up to ([\d,]+)`, float64(slices.Max(deepDepths)))
	is(`under `+"`WithMemoryLimit\\(([\\d]+) MiB\\)`"+` and`, boundedMemLimit>>20)
	is("`WithTimeout\\(([\\d.]+) s\\)`", boundedTimeout.Seconds())
	is(`within the timeout plus ([\d.]+) s`, boundedSlack.Seconds())
	is(`peak heap under (\w+) times the limit`, boundedPeakFactor)
	is(`killed at its ([\d.]+) GiB heap cap`, boundedHeapCapMB/1024.0)
	is(`or ([\d.]+) s wall cap`, boundedWall.Seconds())
	is(`(\w+) children at a time`, boundedParallel)
}
