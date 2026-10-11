package expr

import (
	"fmt"
	"runtime"
	"testing"
)

// The process-wide caches of compiled expressions and patterns are bounded by
// the bytes they hold, not by their number of entries: patterns of about 24
// MB each, compiled from a few bytes of specification or data (C8), must not
// stay alive after the render that made them.
func TestCachesRetainBoundedBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles about 1 GB of regular expressions")
	}
	retained := func(f func(i int)) int64 {
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		for i := range 20 {
			f(i)
		}
		runtime.GC()
		runtime.ReadMemStats(&b)
		return int64(b.HeapAlloc) - int64(a.HeapAlloc)
	}
	// At run time, from a string: each distinct, each about 24 MB.
	got := retained(func(i int) {
		p, err := Compile(fmt.Sprintf("test('(a{1000}){150}%d', 'x')", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Eval(NewScope(nil)); err != nil {
			t.Fatal(err)
		}
	})
	if got > patternCacheBytes+(16<<20) {
		t.Errorf("patternCache keeps %d MB after the renders", got>>20)
	}
	// As literals in compiled programs.
	got = retained(func(i int) {
		if _, err := CompileCached(fmt.Sprintf("test(/(a{1000}){150}%d/, 'x')", i)); err != nil {
			t.Fatal(err)
		}
	})
	if got > programCacheBytes+(16<<20) {
		t.Errorf("programCache keeps %d MB after the renders", got>>20)
	}
}

// Small programs and patterns are still shared.
func TestCachesKeepSmallEntries(t *testing.T) {
	p1, _ := CompileCached("datum.a + 1 > 2 && test(/x+y/, datum.b)")
	p2, _ := CompileCached("datum.a + 1 > 2 && test(/x+y/, datum.b)")
	if p1 == nil || p1 != p2 {
		t.Fatal("a small program must be cached")
	}
	s := NewScope(nil)
	if p, q := s.compilePattern("a+b", "i"), s.compilePattern("a+b", "i"); p != q {
		t.Fatal("a small pattern must be cached")
	}
	p, q := s.compilePattern("a+b", "g"), s.compilePattern("a+b", "g")
	if p == q || p.Re == q.Re {
		t.Fatal("a stateful pattern must not be shared")
	}
	p.Re.SetLastIndex(2)
	if r := s.compilePattern("a+b", "g"); r.Re.LastIndex() != 0 {
		t.Fatal("a stateful pattern must come fresh from the cache")
	}
	if s := p1.size(); s < 1024 || s > 1<<20 {
		t.Fatalf("size of a small program: %d", s)
	}
}
