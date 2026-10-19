package main

import (
	"strings"
	"testing"
)

// The summary counts and groups the skipped top-level tests, passes the
// package lines through, prints a failed test's output, and fails the run
// when anything failed or nothing ran.
func TestRun(t *testing.T) {
	ev := func(s ...string) string { return strings.Join(s, "\n") + "\n" }
	in := ev(
		`{"Action":"run","Package":"p","Test":"TestA"}`,
		`{"Action":"output","Package":"p","Test":"TestA","Output":"=== RUN   TestA\n"}`,
		`{"Action":"output","Package":"p","Test":"TestA","Output":"    a_test.go:12: slow: renders the whole corpus\n"}`,
		`{"Action":"output","Package":"p","Test":"TestA","Output":"--- SKIP: TestA (0.00s)\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestA"}`,
		`{"Action":"output","Package":"p","Test":"TestB","Output":"    b_test.go:3: slow: renders the whole corpus\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestB"}`,
		`{"Action":"output","Package":"p","Test":"TestB/sub","Output":"    b_test.go:9: a subtest's skip is not counted\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestB/sub"}`,
		`{"Action":"pass","Package":"p","Test":"TestC"}`,
		`{"Action":"output","Package":"p","Output":"PASS\n"}`,
		`{"Action":"output","Package":"p","Output":"ok  \tp\t0.1s\n"}`,
		`{"Action":"pass","Package":"p"}`,
	)
	var out strings.Builder
	if code := run(strings.NewReader(in), &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"ok  \tp\t0.1s", "2 of 3 tests SKIPPED, in 1 packages", "   2  slow: renders the whole corpus  (TestA, TestB)"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "PASS\n") || strings.Contains(got, "subtest") {
		t.Errorf("output:\n%s", got)
	}

	failing := ev(
		`{"Action":"output","Package":"p","Test":"TestF","Output":"    f_test.go:1: boom\n"}`,
		`{"Action":"fail","Package":"p","Test":"TestF"}`,
		`{"Action":"fail","Package":"p"}`,
	)
	out.Reset()
	if code := run(strings.NewReader(failing), &out); code != 1 || !strings.Contains(out.String(), "boom") {
		t.Errorf("a failing test: exit %d, output %q", code, out.String())
	}
	out.Reset()
	if code := run(strings.NewReader(ev(`{"Action":"build-output","ImportPath":"p [p.test]","Output":"x.go:1: undefined: y\n"}`, `{"Action":"build-fail","ImportPath":"p [p.test]"}`, `{"Action":"fail","Package":"p"}`)), &out); code != 1 || !strings.Contains(out.String(), "undefined: y") {
		t.Errorf("a build failure: exit %d, output %q", code, out.String())
	}
	out.Reset()
	if code := run(strings.NewReader("flag provided but not defined: -x\n"), &out); code != 1 {
		t.Errorf("no results: exit %d", code)
	}
}
