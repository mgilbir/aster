package aster_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// trackedFiles lists the files git tracks that match the pathspec glob
// pattern, relative to the repository root. The tests over the repository's
// own files (its Markdown, its tests) read only these, so a file left in the
// working tree, untracked, cannot change what they see. Outside a git
// checkout (a module download) they skip.
func trackedFiles(t testing.TB, pattern string) []string {
	t.Helper()
	if _, err := os.Stat(".git"); err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	out, err := exec.Command("git", "ls-files", "-z", "--", pattern).Output()
	if err != nil {
		t.Fatalf("git ls-files %s: %v", pattern, err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}
