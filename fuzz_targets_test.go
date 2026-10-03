package aster

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestFuzzTargetsListed keeps scripts/fuzz-targets.txt, which the nightly fuzz
// workflow and `make fuzz` run, in step with the Fuzz functions of the tests:
// a new target that is not listed would never be fuzzed.
func TestFuzzTargetsListed(t *testing.T) {
	list, err := os.ReadFile("scripts/fuzz-targets.txt")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(list), "\n") {
		if line = strings.TrimSpace(line); line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			t.Errorf("fuzz-targets.txt: %q is not <package> TAB <target> TAB <minutes>", line)
			continue
		}
		listed = append(listed, filepath.Clean(f[0])+" "+f[1])
	}
	var declared []string
	re := regexp.MustCompile(`(?m)^func (Fuzz\w*)\(`)
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == "node_modules" || d.Name() == ".claude") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			declared = append(declared, filepath.Dir(path)+" "+m[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(listed)
	slices.Sort(declared)
	if !slices.Equal(listed, declared) {
		t.Errorf("scripts/fuzz-targets.txt lists %q, the tests declare %q", listed, declared)
	}
}
