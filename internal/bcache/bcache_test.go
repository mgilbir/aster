package bcache

import (
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"
)

// Entries go into the current generation; when it is full it becomes the
// previous one, and the previous one goes whole. An entry used from the
// previous generation is promoted and survives the turn.
func TestGenerations(t *testing.T) {
	oh := New[string, int](0, 0).EntryOverhead()
	c := New[string, int](2*3*(10+oh), 0) // three entries a generation
	for i, k := range []string{"a", "b", "c"} {
		if !c.Put(k, i, 10) {
			t.Fatalf("%s not cached", k)
		}
	}
	c.Put("d", 3, 10) // turns: a, b, c are the previous generation
	if c.Len() != 4 || c.Bytes() != 4*(10+oh) {
		t.Fatalf("len %d bytes %d", c.Len(), c.Bytes())
	}
	if v, ok := c.Get("a"); !ok || v != 0 { // promoted next to d
		t.Fatal("a missing")
	}
	c.Put("e", 4, 10) // the current generation is full: a, d, e
	c.Put("f", 5, 10) // turns: b and c go
	for k, want := range map[string]bool{"a": true, "b": false, "c": false, "d": true, "e": true, "f": true} {
		if _, ok := c.Get(k); ok != want {
			t.Errorf("%s cached = %v, want %v", k, ok, want)
		}
	}
	if c.Bytes() > c.MaxBytes() {
		t.Fatalf("bytes %d past %d", c.Bytes(), c.MaxBytes())
	}
	c.Clear()
	if c.Len() != 0 || c.Bytes() != 0 {
		t.Fatal("Clear")
	}
	if !c.Put("g", 5, 10) {
		t.Fatal("Put after Clear")
	}
}

// Entries used between two turns outlive those that were not, whatever their
// age: a hot working set stays while a stream of one-off keys passes through.
func TestHotEntriesSurviveAStream(t *testing.T) {
	oh := New[int, int](0, 0).EntryOverhead()
	c := New[int, int](20*(100+oh), 0)
	for k := range 5 {
		c.Put(k, k, 100)
	}
	for i := 100; i < 10_000; i++ {
		for k := range 5 {
			if _, ok := c.Get(k); !ok {
				t.Fatalf("hot key %d evicted after %d one-off keys", k, i-100)
			}
		}
		c.Put(i, i, 100)
	}
	if c.Bytes() > c.MaxBytes() {
		t.Fatal("bound")
	}
}

// A value replaced is never served again, from either generation.
func TestReplacedValueIsGone(t *testing.T) {
	oh := New[int, int](0, 0).EntryOverhead()
	c := New[int, int](2*2*(10+oh), 0) // two entries a generation
	c.Put(1, 100, 10)
	c.Put(2, 200, 10)
	c.Put(3, 300, 10) // turns: 1 and 2 are previous
	c.Put(1, 101, 10) // current: 3, 1
	for range 3 {
		if v, ok := c.Get(1); !ok || v != 101 {
			t.Fatalf("Get(1) = %d, %v", v, ok)
		}
		c.Put(1, 101, 10)
		c.Put(9, 9, 10) // turns
	}
	if c.Put(1, 0, 1<<40) || func() bool { _, ok := c.Get(1); return ok }() {
		t.Fatal("a value too large drops what the key held")
	}
}

func TestMaxEntryAndReplace(t *testing.T) {
	c := New[int, string](1000, 300)
	oh := c.EntryOverhead()
	if c.Put(1, "big", 301-oh) {
		t.Fatal("an entry past maxEntry must not be cached")
	}
	if !c.Put(1, "fits", 300-oh) || c.Bytes() != 300 {
		t.Fatalf("an entry of maxEntry bytes is cached: %d", c.Bytes())
	}
	c.Put(1, "smaller", 10)
	if v, _ := c.Get(1); v != "smaller" || c.Bytes() != 10+oh || c.Len() != 1 {
		t.Fatalf("replace: %q %d", v, c.Bytes())
	}
	c.Put(1, "huge", 1<<40)
	if _, ok := c.Get(1); ok || c.Bytes() != 0 {
		t.Fatal("the old value must go")
	}
	if c.Put(2, "neg", -5) != true || c.Bytes() != oh {
		t.Fatal("a negative size counts as zero")
	}
	if New[int, int](10, 0).Put(1, 1, 0) {
		t.Fatal("a cache smaller than one entry's overhead keeps nothing")
	}
	if New[int, int](1000, 900).Put(1, 1, 600) {
		t.Fatal("an entry larger than a generation is not kept")
	}
}

// A random sequence of operations against a reference model, the two
// generations written plainly: the same values found, the same bytes held,
// and the bound always held.
func TestAgainstModel(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	c := New[int, int](2000, 700)
	oh := int(c.EntryOverhead())
	type ent struct{ v, size int }
	curr, prev := map[int]ent{}, map[int]ent{}
	cb, pb := 0, 0
	add := func(k int, e ent) {
		if o, ok := curr[k]; ok {
			cb -= o.size
			delete(curr, k)
		}
		if cb+e.size > 1000 {
			prev, pb = curr, cb
			curr, cb = map[int]ent{}, 0
		}
		curr[k] = e
		cb += e.size
	}
	published := 0
	for i := range 200000 {
		if c.snap.Load() != nil {
			published++
		}
		k := r.IntN(40)
		if r.IntN(4) != 0 {
			v, ok := c.Get(k)
			e, in := curr[k]
			if !in {
				if e, in = prev[k]; in {
					add(k, e)
				}
			}
			if ok != in || ok && v != e.v {
				t.Fatalf("op %d: Get(%d) = %d,%v; model %v,%v", i, k, v, ok, e, in)
			}
		} else {
			size := r.IntN(800)
			ok := c.Put(k, i, int64(size))
			if want := size+oh <= 700; ok != want {
				t.Fatalf("op %d: Put size %d cached=%v", i, size, ok)
			}
			if ok {
				add(k, ent{i, size + oh})
			} else {
				if o, in := curr[k]; in {
					cb -= o.size
					delete(curr, k)
				}
				if o, in := prev[k]; in {
					pb -= o.size
					delete(prev, k)
				}
			}
		}
		// The snapshot holds nothing the generations do not, at their values.
		if snap := c.snap.Load(); snap != nil {
			for k, e := range *snap {
				if m, ok := c.curr[k]; ok {
					if m.val != e.val {
						t.Fatalf("op %d: snapshot serves %d for %d, current %d", i, e.val, k, m.val)
					}
				} else if m, ok := c.prev[k]; !ok || m.val != e.val {
					t.Fatalf("op %d: snapshot holds %d=%d the cache does not", i, k, e.val)
				}
			}
		}
		if c.Len() != len(curr)+len(prev) || c.Bytes() != int64(cb+pb) || cb+pb > 2000 {
			t.Fatalf("op %d: len %d/%d bytes %d/%d", i, c.Len(), len(curr)+len(prev), c.Bytes(), cb+pb)
		}
	}
	if published < 1000 {
		t.Fatalf("the snapshot was published for only %d operations", published)
	}
}

func TestConcurrent(t *testing.T) {
	c := New[string, int](10_000, 0)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 5000 {
				k := strconv.Itoa((i * (g + 1)) % 300)
				if v, ok := c.Get(k); ok && strconv.Itoa(v) != k {
					t.Errorf("Get(%s) = %d", k, v)
				}
				n, _ := strconv.Atoi(k)
				c.Put(k, n, int64(len(k)+i%50))
			}
		})
	}
	wg.Wait()
	if c.Bytes() > 10_000 {
		t.Fatalf("bound broken: %d", c.Bytes())
	}
}

func BenchmarkGetHit(b *testing.B) {
	c := New[string, int](1<<20, 0)
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = "key" + strconv.Itoa(i)
		c.Put(keys[i], i, 16)
	}
	i := 0
	for b.Loop() {
		if _, ok := c.Get(keys[i&255]); !ok {
			b.Fatal()
		}
		i++
	}
}

func BenchmarkGetHitParallel(b *testing.B) {
	c := New[string, int](1<<20, 0)
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = "key" + strconv.Itoa(i)
		c.Put(keys[i], i, 16)
	}
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if _, ok := c.Get(keys[i&255]); !ok {
				b.Fatal()
			}
			i++
		}
	})
}

func BenchmarkPutEvict(b *testing.B) {
	c := New[int, int](64*1024, 0)
	i := 0
	for b.Loop() {
		c.Put(i, i, 64)
		i++
	}
}
