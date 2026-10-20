package aster_test

import (
	"os/exec"
	"path"
	"strings"
	"testing"
)

// `go build ./cmd/NAME` in the repository root writes the binary NAME there:
// .gitignore must keep each out of `git add .` (C147, audit 2026-10-18: the
// 43 MB aster binary was not ignored).
func TestBuiltCommandsAreIgnored(t *testing.T) {
	cmds := map[string]bool{}
	for _, f := range trackedFiles(t, "cmd/*/main.go") {
		cmds[path.Base(path.Dir(f))] = true
	}
	if len(cmds) == 0 {
		t.Fatal("no commands under cmd/")
	}
	for name := range cmds {
		if err := exec.Command("git", "check-ignore", "-q", "--no-index", "--", name).Run(); err != nil {
			t.Errorf("the binary %q that go build ./cmd/%s writes is not git-ignored (%v)", name, name, err)
		}
	}
}

// Every command refuses an argument it does not take: the flag package stops
// at the first argument that is not a flag and leaves the rest, flags
// included, unparsed, so a command that never looks at what is left ignores
// them without a word (C49, audit 2026-10-18: `aster svg chart.vl.json -o
// out.svg` read standard input and dropped -o). A command that parses flags
// must look at NArg; one that takes none must look at os.Args.
func TestCommandsRefuseStrayArguments(t *testing.T) {
	mains := append(trackedFiles(t, "cmd/*/main.go"), trackedFiles(t, "internal/cmd/*/main.go")...)
	if len(mains) < 3 {
		t.Fatalf("found %d commands, expected at least 3", len(mains))
	}
	for _, f := range mains {
		src := readDoc(t, f)
		switch {
		case strings.Contains(src, ".Parse("):
			if !strings.Contains(src, "NArg()") {
				t.Errorf("%s parses flags but never checks NArg: arguments after the flags are ignored", f)
			}
		case !strings.Contains(src, "len(os.Args)"):
			t.Errorf("%s takes no flags but never checks os.Args: any argument is ignored", f)
		}
	}
}
