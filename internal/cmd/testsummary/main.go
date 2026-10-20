// Command testsummary reads `go test -json` on stdin, prints what plain
// `go test` would (each package's ok/FAIL line, and the output of what
// failed), and ends with a summary of the tests that skipped, grouped by
// their reason.
//
//	go test -short -json ./... | go run ./internal/cmd/testsummary
//
// The summary is the point: in package-list mode `go test` prints nothing of
// a passing package but its ok line, so a gate that skipped (every oracle
// comparison under -short, every upstream-vector replay on a checkout that
// has not recorded the vectors) is invisible without -v, and a run that
// checked much less than it seems to reads the same as one that checked
// everything (C78, audit 2026-10-18).
//
// It exits 1 when a test, a package or a build failed, or when the input
// holds no test result at all (go test did not run).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// event is one line of `go test -json` (cmd/test2json).
type event struct {
	Action     string
	Package    string
	Test       string
	Output     string
	ImportPath string
}

// skipReason strips the file:line prefix test2json leaves on a t.Skip line.
var skipReason = regexp.MustCompile(`^\s*\S+\.go:\d+: `)

func main() {
	os.Exit(run(os.Stdin, os.Stdout))
}

// printer writes to out and keeps the first write error.
type printer struct {
	out io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if _, err := fmt.Fprintf(p.out, format, args...); err != nil && p.err == nil {
		p.err = err
	}
}

// key names a test.
type key struct{ pkg, test string }

func run(in io.Reader, w io.Writer) int {
	out := &printer{out: w}
	logs := map[key][]string{}      // the output of each test, while it runs
	builds := map[string][]string{} // build output, by import path
	var skipped []key
	reasons := map[key]string{}
	failed, results, tests := false, 0, 0
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var e event
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &e) != nil {
			out.printf("%s\n", line) // not an event: pass it through
			continue
		}
		k := key{e.Package, e.Test}
		switch e.Action {
		case "build-output":
			builds[e.ImportPath] = append(builds[e.ImportPath], e.Output)
		case "build-fail":
			failed = true
			out.printf("%s", strings.Join(builds[e.ImportPath], ""))
		case "output":
			if e.Test == "" {
				// The package's own lines: ok, FAIL, a panic outside a test;
				// not the PASS that plain go test leaves out.
				if e.Output != "PASS\n" {
					out.printf("%s", e.Output)
				}
				continue
			}
			logs[k] = append(logs[k], e.Output)
		case "pass", "skip", "fail":
			if e.Test == "" {
				results++
				if e.Action == "fail" {
					failed = true
				}
				continue
			}
			if !strings.Contains(e.Test, "/") {
				tests++
			}
			switch e.Action {
			case "fail":
				failed = true
				out.printf("%s", strings.Join(logs[k], ""))
			case "skip":
				if !strings.Contains(e.Test, "/") {
					skipped = append(skipped, k)
					reasons[k] = reasonOf(logs[k])
				}
			}
			delete(logs, k)
		}
	}
	if err := sc.Err(); err != nil {
		out.printf("testsummary: %v\n", err)
		return 1
	}
	if results == 0 {
		out.printf("testsummary: no test results in the input (did go test run, with -json?)\n")
		return 1
	}
	summarize(out, tests, skipped, reasons)
	if out.err != nil {
		fmt.Fprintln(os.Stderr, "testsummary:", out.err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

// reasonOf is the first line a skipped test wrote that is not test2json's
// framing: its t.Skip message.
func reasonOf(lines []string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "=== ") || strings.HasPrefix(t, "--- ") {
			continue
		}
		return skipReason.ReplaceAllString(strings.TrimRight(l, "\n"), "")
	}
	return "(no reason given)"
}

// summarize prints how many top-level tests skipped and why, the most
// common reasons first.
func summarize(out *printer, tests int, skipped []key, reasons map[key]string) {
	if len(skipped) == 0 {
		out.printf("testsummary: %d tests, none skipped\n", tests)
		return
	}
	pkgs := map[string]bool{}
	by := map[string][]string{}
	for _, k := range skipped {
		pkgs[k.pkg] = true
		r := reasons[k]
		by[r] = append(by[r], k.test)
	}
	rs := make([]string, 0, len(by))
	for r := range by {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool {
		if len(by[rs[i]]) != len(by[rs[j]]) {
			return len(by[rs[i]]) > len(by[rs[j]])
		}
		return rs[i] < rs[j]
	})
	out.printf("testsummary: %d of %d tests SKIPPED, in %d packages (go test -v shows each); by reason:\n", len(skipped), tests, len(pkgs))
	const shown = 20
	for i, r := range rs {
		if i == shown {
			out.printf("  ... and %d more reasons\n", len(rs)-shown)
			break
		}
		names := by[r]
		sort.Strings(names)
		list := strings.Join(names[:min(3, len(names))], ", ")
		if len(names) > 3 {
			list += ", ..."
		}
		out.printf("  %4d  %s  (%s)\n", len(names), r, list)
	}
}
