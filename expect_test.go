package aster_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// reasons of entries whose status did not change. An entry whose case no
// longer exists fails a complete (unfiltered) run, so a renamed case cannot
// leave a pre-accepted status behind for a later case of that name.
// "internal-error" (the engine panicked) is never accepted, listed or not.
//
// A few cases' oracle answer depends on the platform node runs on: its text
// shaping (the Pango node-canvas was built against), or V8's last-bit
// trigonometry on x86-64. Their status lists one alternative per platform,
// each keyed to a property of the oracle's version line:
//
//	ok@pango-1.57|differ@pango-1.48     by the Pango version
//	ok@arm64|differ@x64                 by node's process.arch
//
// (keys: pango-MAJOR.MINOR, an arch, or an OS: linux, darwin, win32). Exactly
// one alternative must apply to the platform the oracle reports, and that one
// status is the expectation there: a case is checked on every platform, not
// accepted with either outcome everywhere (C14, audit 2026-10-18). The reason
// says which platform gives which.

type expectation struct{ status, reason string }

// platformKey is the grammar of an alternative's key. A key outside it (a
// typo such as x86_64, which no oracle reports) fails when the file is read,
// rather than never applying.
var platformKey = regexp.MustCompile(`^(pango-\d+\.\d+|linux|darwin|win32|x64|arm64|arm|ia32|ppc64|s390x|riscv64|loong64)$`)

// oraclePlatform is what the oracle's version line says of the platform:
// pango-MAJOR.MINOR, node's process.platform and process.arch.
var (
	versionPango    = regexp.MustCompile(`pango (\d+\.\d+)`)
	versionPlatform = regexp.MustCompile(`platform ([a-z0-9]+)-([a-z0-9]+)`)
)

// platformKeys are the keys an expectation alternative may name that apply
// to the oracle whose version line is version.
func platformKeys(t testing.TB, version string) []string {
	t.Helper()
	pm, am := versionPango.FindStringSubmatch(version), versionPlatform.FindStringSubmatch(version)
	if pm == nil || am == nil {
		t.Fatalf("the oracle's version line names no pango version or platform: %q", version)
	}
	return []string{"pango-" + pm[1], am[1], am[2]}
}

// alternatives splits a keyed status into status → key, in order; nil for a
// plain status.
func (e expectation) alternatives() ([][2]string, error) {
	if !strings.Contains(e.status, "|") && !strings.Contains(e.status, "@") {
		return nil, nil
	}
	var out [][2]string
	for _, alt := range strings.Split(e.status, "|") {
		st, key, ok := strings.Cut(alt, "@")
		if !ok || st == "" || !platformKey.MatchString(key) {
			return nil, fmt.Errorf("alternative %q is not status@platform (a key such as pango-1.48, x64 or arm64)", alt)
		}
		for _, o := range out {
			if o[1] == key {
				return nil, fmt.Errorf("platform %s is listed twice", key)
			}
		}
		out = append(out, [2]string{st, key})
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("a platform-keyed status lists one platform; list every platform's status")
	}
	return out, nil
}

// want is the status the entry expects on a platform with the given keys.
func (e expectation) want(platform []string) (string, error) {
	alts, err := e.alternatives()
	if err != nil || alts == nil {
		return e.status, err
	}
	var match []string
	for _, a := range alts {
		if slices.Contains(platform, a[1]) {
			match = append(match, a[0])
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return "", fmt.Errorf("%q has no alternative for this platform (%s): add one", e.status, strings.Join(platform, " "))
	}
	return "", fmt.Errorf("%q has %d alternatives for this platform (%s)", e.status, len(match), strings.Join(platform, " "))
}

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
		if _, err := e.alternatives(); err != nil {
			t.Fatalf("%s: %s: %v", file, parts[0], err)
		}
		if strings.Contains(e.status, statusInternalError) {
			t.Fatalf("%s: %s lists %s: a panic of the engine is never an accepted outcome", file, parts[0], statusInternalError)
		}
		if _, dup := m[parts[0]]; dup {
			t.Fatalf("%s: %s is listed twice", file, parts[0])
		}
		m[parts[0]] = e
	}
	return m
}

// checkExpectations compares the results with the file's expectations on
// the platform (platformKeys) the oracle reports, or rewrites the file when
// update is set. complete says the run covered every case, so an entry no
// result names is an orphan.
func checkExpectations(t *testing.T, file string, update, complete bool, platform []string, results []specResult) {
	expect := readExpectations(t, file)
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[r.id] = true
		if strings.Contains(r.status, statusInternalError) {
			t.Errorf("%s: the engine panics: %s", r.id, r.detail)
		}
	}
	if update {
		for _, r := range results {
			if strings.Contains(r.status, statusInternalError) {
				continue // failed above; never written as an expectation
			}
			e, listed := expect[r.id]
			if alts, _ := e.alternatives(); alts != nil {
				// Only this platform's alternative is the run's to change.
				i := slices.IndexFunc(alts, func(a [2]string) bool { return slices.Contains(platform, a[1]) })
				if i < 0 {
					alts = append(alts, [2]string{r.status, platform[0]})
					e.reason = "TODO (platform " + platform[0] + "): " + r.detail
				} else if alts[i][0] != r.status {
					alts[i][0] = r.status
					e.reason = "TODO (platform " + alts[i][1] + "): " + r.detail
				}
				parts := make([]string, len(alts))
				allOK := true
				for j, a := range alts {
					parts[j] = a[0] + "@" + a[1]
					allOK = allOK && a[0] == "ok"
				}
				if allOK {
					delete(expect, r.id)
					continue
				}
				e.status = strings.Join(parts, "|")
				expect[r.id] = e
				continue
			}
			if r.status == "ok" {
				delete(expect, r.id)
				continue
			}
			if !listed || e.reason == "" || e.status != r.status {
				e.reason = "TODO: " + r.detail
			}
			e.status = r.status
			expect[r.id] = e
		}
		if complete {
			for id := range expect {
				if !seen[id] {
					delete(expect, id)
				}
			}
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
		if strings.Contains(r.status, statusInternalError) {
			continue // failed above
		}
		want := "ok"
		if e, ok := expect[r.id]; ok {
			w, err := e.want(platform)
			if err != nil {
				t.Errorf("%s: %s: %v", file, r.id, err)
				continue
			}
			want = w
		}
		switch r.status {
		case want:
		case "ok":
			t.Errorf("%s now matches node (was %s): remove it from %s", r.id, want, file)
		default:
			t.Errorf("%s: %s, want %s: %s", r.id, r.status, want, r.detail)
		}
	}
	if complete {
		var orphans []string
		for id := range expect {
			if !seen[id] {
				orphans = append(orphans, id)
			}
		}
		sort.Strings(orphans)
		for _, id := range orphans {
			t.Errorf("%s lists %s, which no case is named any more: remove it (a renamed case must not leave its status for a later case of that name)", file, id)
		}
	}
}
