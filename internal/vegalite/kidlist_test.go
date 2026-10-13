package vegalite

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestKidListMatchesArray drives kidList and a plain slice (upstream's array,
// spliced the way upstream splices it) through the same random operations and
// requires the same contents after each one, across the switch from the flat
// slice to blocks and through block splits.
func TestKidListMatchesArray(t *testing.T) {
	pool := make([]dfNode, 3000)
	for i := range pool {
		pool[i] = &outputNode{}
	}
	for seed := uint64(1); seed <= 40; seed++ {
		r := rand.New(rand.NewPCG(seed, 7))
		var k kidList
		var want []dfNode
		in := map[dfNode]bool{}
		maxLen := []int{10, 40, 200, 2500}[seed%4]
		for step := 0; step < 6000; step++ {
			switch op := r.IntN(10); {
			case op < 5 && len(want) < maxLen:
				c := pool[r.IntN(len(pool))]
				if in[c] {
					break
				}
				i := len(want)
				if r.IntN(3) > 0 {
					i = r.IntN(len(want) + 1)
				}
				k.insert(i, c)
				want = slices.Insert(want, i, c)
				in[c] = true
			case op < 8 && len(want) > 0:
				i := r.IntN(len(want))
				if r.IntN(2) == 0 {
					// remove by identity, as removeChild does
					i = k.indexOf(want[i])
				}
				delete(in, want[i])
				k.removeAt(i)
				want = slices.Delete(want, i, i+1)
			case op == 8:
				c := pool[r.IntN(len(pool))]
				if got, exp := k.indexOf(c), slices.Index(want, c); got != exp {
					t.Fatalf("seed %d step %d: indexOf = %d, want %d", seed, step, got, exp)
				}
				if k.has(c) != in[c] {
					t.Fatalf("seed %d step %d: has = %v, want %v", seed, step, k.has(c), in[c])
				}
			default:
				if len(want) > 0 {
					i := r.IntN(len(want))
					if k.at(i) != want[i] {
						t.Fatalf("seed %d step %d: at(%d) differs", seed, step, i)
					}
				}
			}
			if k.len() != len(want) {
				t.Fatalf("seed %d step %d: len = %d, want %d", seed, step, k.len(), len(want))
			}
			if step%97 == 0 || len(want) < 50 {
				if !slices.Equal(k.all(), want) {
					t.Fatalf("seed %d step %d: contents differ", seed, step)
				}
			}
		}
	}
}

// TestKidListSnapshotIsStable checks that a blocks-mode snapshot handed to a
// caller is not changed by later edits (callers iterate one while splicing).
func TestKidListSnapshotIsStable(t *testing.T) {
	var k kidList
	nodes := make([]dfNode, 100)
	for i := range nodes {
		nodes[i] = &outputNode{}
		k.insert(i, nodes[i])
	}
	snap := k.all()
	before := slices.Clone(snap)
	k.removeAt(0)
	k.insert(5, &outputNode{})
	if !slices.Equal(snap, before) {
		t.Fatal("snapshot changed under later edits")
	}
}
