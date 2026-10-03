package jsval

import (
	"fmt"
	"testing"
)

// A Cloner copies what Clone copies, and its copies are independent: setting,
// adding and deleting keys in one touches neither the source nor the objects
// cut from the same chunk.
func TestClonerMatchesClone(t *testing.T) {
	var cl Cloner
	var srcs, copies []*Object
	for round := 0; round < 40; round++ {
		for n := 0; n <= 40; n++ {
			o := NewObject(n)
			for i := 0; i < n; i++ {
				o.Set(fmt.Sprintf("k%d", i), Num(float64(i+round)))
			}
			o.SetTupleID(uint32(round*100 + n))
			srcs = append(srcs, o)
			c := cl.Clone(o)
			want := o.Clone()
			if c.Len() != want.Len() || c.TupleID() != want.TupleID() {
				t.Fatalf("n=%d: clone has %d keys, id %d; want %d, %d", n, c.Len(), c.TupleID(), want.Len(), want.TupleID())
			}
			for i := 0; i < n; i++ {
				if c.KeyAt(i) != want.KeyAt(i) || !Equal(c.ValueAt(i), want.ValueAt(i)) {
					t.Fatalf("n=%d: entry %d differs", n, i)
				}
			}
			copies = append(copies, c)
		}
	}
	// Write through every copy; no other object may notice.
	for i, c := range copies {
		c.Set("extra", Num(1))
		c.Set("extra2", Str("two"))
		if c.Len() > 2 {
			c.Delete(c.KeyAt(0))
		}
		if i%3 == 0 {
			c.Set("k0", Str("changed"))
		}
	}
	idx := 0
	for round := 0; round < 40; round++ {
		for n := 0; n <= 40; n++ {
			o := srcs[idx]
			idx++
			if o.Len() != n {
				t.Fatalf("source n=%d grew to %d keys", n, o.Len())
			}
			for i := 0; i < n; i++ {
				if v := o.ValueAt(i); !Equal(v, Num(float64(i+round))) {
					t.Fatalf("source n=%d entry %d changed to %v", n, i, v)
				}
			}
		}
	}
	// Each copy still holds its own values.
	idx = 0
	for round := 0; round < 40; round++ {
		for n := 0; n <= 40; n++ {
			c := copies[idx]
			if !Equal(c.Lookup("extra"), Num(1)) || !Equal(c.Lookup("extra2"), Str("two")) {
				t.Fatalf("copy %d lost its added keys", idx)
			}
			if n > 2 && c.Has("k0") && idx%3 != 0 {
				t.Fatalf("copy %d kept the deleted key", idx)
			}
			idx++
		}
	}
	var nilObj *Object
	if c := cl.Clone(nilObj); c == nil || c.Len() != 0 {
		t.Fatal("cloning a nil object gives an empty one")
	}
}
