package aster_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Expectation files list the cases whose comparison with the node oracle is
// not "ok", one per line, with the reason:
//
//	id<TAB>status<TAB>reason
//
// A status is "ok" or "+"-joined problems (differ, engine-error, node-error,
// vega-differ). Any other status than the listed one fails the test, a better
// one too, so a list stays exact; an update flag rewrites it, keeping the
// reasons of entries whose status did not change.
//
// A status may list "|"-separated alternatives for the few cases whose oracle
// answer depends on the platform node runs on (its text shaping, or V8's
// last-bit trigonometry on x86-64); the reason says which gives which.

type expectation struct{ status, reason string }

func readExpectations(t *testing.T, file string) map[string]expectation {
	m := map[string]expectation{}
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return m
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			t.Fatalf("%s: malformed line %q", file, line)
		}
		e := expectation{status: parts[1]}
		if len(parts) == 3 {
			e.reason = parts[2]
		}
		m[parts[0]] = e
	}
	return m
}

func checkExpectations(t *testing.T, file string, update bool, results []specResult) {
	expect := readExpectations(t, file)
	if update {
		for _, r := range results {
			if r.status == "ok" && !strings.Contains(expect[r.id].status, "|") {
				delete(expect, r.id)
				continue
			}
			e := expect[r.id]
			if strings.Contains(e.status, "|") && slices.Contains(strings.Split(e.status, "|"), r.status) {
				continue // platform-dependent, and this platform is one of them
			}
			if e.reason == "" || e.status != r.status {
				e.reason = "TODO: " + r.detail
			}
			e.status = r.status
			expect[r.id] = e
		}
		ids := make([]string, 0, len(expect))
		for id := range expect {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var b strings.Builder
		b.WriteString("# Cases whose comparison with the node oracle is not \"ok\" (see expect_test.go).\n# id<TAB>status<TAB>reason\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", id, expect[id].status, expect[id].reason)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d entries)", file, len(ids))
		return
	}
	for _, r := range results {
		want := "ok"
		if e, ok := expect[r.id]; ok {
			want = e.status
		}
		switch {
		case slices.Contains(strings.Split(want, "|"), r.status):
		case r.status == "ok":
			t.Errorf("%s now matches node (was %s): remove it from %s", r.id, want, file)
		default:
			t.Errorf("%s: %s, want %s: %s", r.id, r.status, want, r.detail)
		}
	}
}
