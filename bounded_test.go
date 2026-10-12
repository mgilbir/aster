package aster

// The bounded-input corpus: the guard that every bound holds where the work
// happens (ADR 0010). Each file under testdata/security/bounded is one input,
// rendered to every output its kind has, each in a child process (see
// harness_security_test.go) under WithMemoryLimit(boundedMemLimit) and
// WithTimeout(boundedTimeout). A render must end in time and within a small
// multiple of the memory limit, with a result or with an error that is a
// limit or the deadline: never a kill, a panic or an internal error.
//
// It runs by default, in -short too: adding a case is adding a file. How, and
// what the kinds are, is in docs/testing.md.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	boundedMemLimit = 64 << 20
	boundedTimeout  = 2 * time.Second
	// boundedPeakFactor is how many times the memory limit the child's peak
	// heap may reach: the budgets bound what a render holds (memcheckK
	// times the limit, see memcheck_test.go), and the heap also holds the
	// fonts and the garbage the collector has not reached yet.
	boundedPeakFactor = 4
	// boundedSlack is how long after the deadline a render may still return:
	// the time a loop takes to reach its next poll, and the unwinding.
	boundedSlack = 500 * time.Millisecond
	// The child is killed past these: a finding, reported as such.
	boundedHeapCapMB = 1024
	boundedWall      = 15 * time.Second
	// boundedParallel children run at once, each allowed boundedHeapCapMB.
	boundedParallel = 4
)

// boundedOutputs are the outputs of each kind of input, by file extension.
var boundedOutputs = []struct {
	ext   string
	kinds []string
}{
	{".vg.json", []string{"vega", "vegapng", "vegapdf"}},
	{".vl.json", []string{"vl2vega", "vl", "vlpng", "vlpdf"}},
	// SVG that only the rasterizer draws (filters, CSS, gradient
	// transforms: what SVGToPDF refuses as outside the SVG it accepts).
	{".png.svg", []string{"svgpng"}},
	{".svg", []string{"svgpng", "svgpdf"}},
}

type boundedCase struct {
	name, kind, input string
	font              []byte
	extra             []string
}

// boundedCases reads the corpus: one case per input file and output kind. A
// file <base>.ttf beside <base>.<ext> is registered as the font family
// "Evil" for that input. A base name ending in ".quantize" renders its PNG
// outputs with WithQuantizePNG(256), the post-processing that runs after the
// rasterizer.
func boundedCases(t *testing.T) []boundedCase {
	t.Helper()
	dir := filepath.Join("testdata", "security", "bounded")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var cases []boundedCase
	for _, f := range files {
		name := f.Name()
		if strings.HasSuffix(name, ".ttf") || name == "README.md" {
			continue
		}
		known := false
		for _, o := range boundedOutputs {
			base, ok := strings.CutSuffix(name, o.ext)
			if !ok || known {
				continue
			}
			known = true
			in, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			font, err := os.ReadFile(filepath.Join(dir, base+".ttf"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			var extra []string
			if strings.HasSuffix(base, ".quantize") {
				extra = append(extra, "quantize")
			}
			for _, k := range o.kinds {
				cases = append(cases, boundedCase{name: base, kind: k, input: string(in), font: font, extra: extra})
			}
		}
		if !known {
			t.Errorf("%s: not an input the bounded corpus knows (want %v)", name, []string{".vg.json", ".vl.json", ".png.svg", ".svg", ".ttf"})
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].name+cases[i].kind < cases[j].name+cases[j].kind })
	if len(cases) == 0 {
		t.Fatal("the bounded corpus is empty")
	}
	return cases
}

func TestBoundedInputs(t *testing.T) {
	sem := make(chan struct{}, boundedParallel)
	for _, c := range boundedCases(t) {
		t.Run(c.name+"/"+c.kind, func(t *testing.T) {
			t.Parallel()
			sem <- struct{}{}
			defer func() { <-sem }()
			job := secJob{
				Kind:      c.kind,
				Input:     c.input,
				Timeout:   boundedTimeout.String(),
				MemLimit:  boundedMemLimit,
				HeapCapMB: boundedHeapCapMB,
				FontB64:   c.font,
				Extra:     c.extra,
			}
			res, killed := runSec(t, job, boundedWall*raceSlowdown)
			checkBounded(t, res, killed)
		})
	}
}

// checkBounded holds one render to the bounds; see the file comment.
func checkBounded(t *testing.T, res secResult, killed bool) {
	t.Helper()
	if killed {
		t.Fatalf("killed: %s (heap cap %d MiB, wall %v)", res.Err, boundedHeapCapMB, boundedWall*raceSlowdown)
	}
	if res.Panic != "" {
		t.Errorf("panic escaped the public API: %s", res.Panic)
	}
	if res.Err != "" && !res.Limit && !res.Deadline {
		t.Errorf("an error that is neither ErrLimit nor the deadline: %s", res.Err)
	}
	if strings.Contains(res.Err, "internal error") {
		t.Errorf("internal error: %s", res.Err)
	}
	if limit := (boundedTimeout + boundedSlack*raceSlowdown).Seconds(); res.Seconds > limit {
		t.Errorf("took %.2fs under WithTimeout(%v); at most %.2fs", res.Seconds, boundedTimeout, limit)
	}
	if limit := float64(boundedPeakFactor * boundedMemLimit >> 20); res.PeakMB > limit {
		t.Errorf("peak heap %.0f MiB under WithMemoryLimit(%d MiB); at most %.0f MiB", res.PeakMB, boundedMemLimit>>20, limit)
	}
	if t.Failed() || testing.Verbose() {
		t.Logf("%.2fs, peak %.0f MiB, err=%.200q", res.Seconds, res.PeakMB, res.Err)
	}
}
