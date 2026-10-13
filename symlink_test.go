package aster_test

import (
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
)

// No tracked symlink points outside the repository: a link to an absolute
// path, or one that climbs out of the tree, names a place on the machine that
// committed it (a local cache, a home directory), which a clone does not have.
// Two such links to local caches were once committed with a fix, past a
// .gitignore entry that matched only directories.
func TestNoTrackedSymlinkLeavesTheRepository(t *testing.T) {
	if _, err := os.Stat(".git"); err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	out, err := exec.Command("git", "ls-files", "-s", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files -s: %v", err)
	}
	links := 0
	for _, entry := range strings.Split(string(out), "\x00") {
		// "<mode> <object> <stage>\t<path>"
		meta, name, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasPrefix(meta, "120000 ") {
			continue
		}
		links++
		object := strings.Fields(meta)[1]
		target, err := exec.Command("git", "cat-file", "-p", object).Output()
		if err != nil {
			t.Fatalf("%s: git cat-file %s: %v", name, object, err)
		}
		to := string(target)
		if path.IsAbs(to) || strings.HasPrefix(path.Clean(path.Join(path.Dir(name), to)), "..") {
			t.Errorf("%s is a symlink to %q, outside the repository", name, to)
		}
	}
	t.Logf("%d tracked symlinks", links)
}
