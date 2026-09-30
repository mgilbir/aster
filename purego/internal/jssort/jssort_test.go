package jssort

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strconv"
	"testing"
)

type sortCase struct {
	Cmp    string `json:"cmp"`
	Input  []int  `json:"input"`
	Output []int  `json:"output"`
}

// chaos mirrors gen_sort.mjs: a pseudo-random verdict per ordered pair.
func chaos(x, y int) int {
	h := (uint32(x) * 73856093) ^ (uint32(y) * 19349663)
	return int(h%3) - 1
}

func ties(x, y int) int { return x%7 - y%7 }

// mixedKey and mixed mirror JavaScript's < over a mix of strings and
// numbers, which is not transitive.
type key struct {
	s     string
	n     float64
	isStr bool
}

func mixedKey(i int) key {
	switch i % 3 {
	case 0:
		return key{s: strconv.Itoa((i * 37) % 101), isStr: true}
	case 1:
		return key{n: float64((i * 53) % 97)}
	default:
		return key{s: strconv.Itoa((i*11)%89) + "x", isStr: true}
	}
}

func less(a, b key) bool {
	if a.isStr && b.isStr {
		return a.s < b.s
	}
	num := func(k key) float64 {
		if !k.isStr {
			return k.n
		}
		f, err := strconv.ParseFloat(k.s, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return num(a) < num(b)
}

func mixed(x, y int) int {
	a, b := mixedKey(x), mixedKey(y)
	switch {
	case less(a, b):
		return -1
	case less(b, a):
		return 1
	}
	return 0
}

// TestMatchesV8 checks every recorded permutation from node's
// Array.prototype.sort, for inconsistent as well as consistent comparators.
func TestMatchesV8(t *testing.T) {
	f, err := os.Open("testdata/sort.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var cases []sortCase
	if err := json.NewDecoder(zr).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	cmps := map[string]func(int, int) int{"chaos": chaos, "ties": ties, "mixed": mixed}
	for i, c := range cases {
		got := slices.Clone(c.Input)
		Sort(got, cmps[c.Cmp])
		if !slices.Equal(got, c.Output) {
			t.Errorf("case %d (%s, n=%d): order differs from V8", i, c.Cmp, len(c.Input))
		}
	}
	if len(cases) < 100 {
		t.Fatalf("only %d cases loaded", len(cases))
	}
}

// TestHostileComparator runs comparators that lie in every way; the sort
// must terminate without indexing out of range and keep every element.
func TestHostileComparator(t *testing.T) {
	for _, n := range []int{2, 7, 64, 65, 1000, 4097} {
		for seed := uint32(1); seed <= 20; seed++ {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			state := seed
			Sort(s, func(a, b int) int {
				state = state*1664525 + 1013904223
				return int(state>>30) - 1
			})
			seen := make([]bool, n)
			for _, v := range s {
				if seen[v] {
					t.Fatalf("n=%d seed=%d: element %d duplicated", n, seed, v)
				}
				seen[v] = true
			}
		}
	}
}

func TestSortsLikeStableSort(t *testing.T) {
	s := make([]int, 10000)
	for i := range s {
		s[i] = (i * 7919) % 10007
	}
	want := slices.Clone(s)
	slices.SortStableFunc(want, func(a, b int) int { return a%100 - b%100 })
	Sort(s, func(a, b int) int { return a%100 - b%100 })
	if !slices.Equal(s, want) {
		t.Fatal("consistent comparator: order differs from a stable sort")
	}
}

func BenchmarkSort(b *testing.B) {
	src := make([]int, 10000)
	for i := range src {
		src[i] = (i * 7919) % 10007
	}
	s := make([]int, len(src))
	b.ReportAllocs()
	for b.Loop() {
		copy(s, src)
		Sort(s, func(a, b int) int { return a - b })
	}
}
