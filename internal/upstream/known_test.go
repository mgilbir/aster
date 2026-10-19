package upstream

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The known divergences load, every package they name is one the recorder
// records and every architecture one node reports. It needs no recorded
// vectors, so a mistyped line fails every run, -short included.
func TestKnownDivergencesNameRecordedPackages(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadKnown(root); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"vega-statistic", "d3-geo@x86_64", "d3-geo@", "nope@x64"} {
		if checkPackageKey(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"vega-expression", "d3-geo@x64", "vega-statistics@arm64"} {
		if err := checkPackageKey(good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

// Packages is the recorder's list: the two arrays of
// scripts/record-upstream-vectors.sh.
func TestPackagesMatchTheRecorder(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "scripts", "record-upstream-vectors.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`(?s)(?:VEGA|D3)_PACKAGES=\((.*?)\)`).FindAllStringSubmatch(string(b), -1) {
		got = append(got, strings.Fields(m[1])...)
	}
	if len(got) == 0 || !slices.Equal(got, Packages) {
		t.Errorf("the recorder records %v; upstream.Packages is %v", got, Packages)
	}
}
