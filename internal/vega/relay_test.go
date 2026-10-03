package vega

import (
	"math/rand"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// tuplePulses makes pulses of tuples drawn from one pool: tuples with ids of
// their own, tuples that share an id, tuples without one and non-objects,
// each pulse a random selection in random order with some tuples twice.
func tuplePulses(r *rand.Rand, pulses int) [][]jsval.Value {
	pool := make([]jsval.Value, 40)
	own := make([]bool, len(pool)) // an object with an id of its own
	for i := range pool {
		if i%13 == 12 {
			pool[i] = jsval.Num(float64(i))
			continue
		}
		o := jsval.ObjectOf("k", jsval.Num(float64(i)))
		switch {
		case i%11 == 10: // no id
		case i%7 == 6 && pool[i-1].ObjValue() != nil:
			o.SetTupleID(pool[i-1].ObjValue().EnsureTupleID())
			own[i-1] = false
		default:
			o.EnsureTupleID()
			own[i] = true
		}
		pool[i] = jsval.Obj(o)
	}
	out := make([][]jsval.Value, pulses)
	for p := range out {
		// Some pulses hold only objects with ids of their own: the case a
		// tuple id tells apart.
		mixed := r.Intn(3) > 0
		var ts []jsval.Value
		for _, i := range r.Perm(len(pool)) {
			if r.Intn(3) == 0 || !mixed && !own[i] {
				continue
			}
			ts = append(ts, pool[i])
			if r.Intn(10) == 0 {
				ts = append(ts, pool[i])
			}
		}
		out[p] = ts
	}
	return out
}

// relayByMap is the derived relay as it was with a map of every source tuple
// to its copy, kept across pulses and pruned of the tuples a pulse lacks.
func relayByMap() func(src []jsval.Value) []jsval.Value {
	lut := map[*jsval.Object]jsval.Value{}
	return func(src []jsval.Value) []jsval.Value {
		out := make([]jsval.Value, len(src))
		live := map[*jsval.Object]struct{}{}
		for i, t := range src {
			o := t.ObjValue()
			if o == nil {
				out[i] = t
				continue
			}
			live[o] = struct{}{}
			d, ok := lut[o]
			if !ok {
				d = jsval.Obj(o.Clone())
				lut[o] = d
			} else {
				do := d.ObjValue()
				for j := 0; j < o.Len(); j++ {
					do.Set(o.KeyAt(j), o.ValueAt(j))
				}
			}
			out[i] = d
		}
		for o := range lut {
			if _, ok := live[o]; !ok {
				delete(lut, o)
			}
		}
		return out
	}
}

// The derived relay hands on the copies a map of source to copy gives: the
// same copy for a tuple that comes twice, in a pulse or in the next one, with
// what downstream wrote into it, and a fresh one for a tuple that left.
func TestRelayDeriveMatchesMap(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	p := newParams()
	p.set("derive", jsval.Bool(true), true)
	for round := 0; round < 200; round++ {
		_, tr, _ := facRelay(nil, nil, nil)
		ref := relayByMap()
		copies := map[*jsval.Object]*jsval.Object{} // reference copy -> relay copy
		for pi, src := range tuplePulses(r, 4) {
			want := ref(src)
			got := tr.transform(&opNode{}, p, &flowPulse{tuples: src}).tuples
			if len(got) != len(want) {
				t.Fatalf("round %d pulse %d: %d tuples, want %d", round, pi, len(got), len(want))
			}
			for i := range want {
				w, g := want[i].ObjValue(), got[i].ObjValue()
				if w == nil {
					if got[i] != want[i] {
						t.Fatalf("round %d pulse %d tuple %d: %v, want %v", round, pi, i, got[i], want[i])
					}
					continue
				}
				if g == nil || g == src[i].ObjValue() {
					t.Fatalf("round %d pulse %d tuple %d: not a copy", round, pi, i)
				}
				if c, ok := copies[w]; !ok {
					for _, c := range copies {
						if c == g {
							t.Fatalf("round %d pulse %d tuple %d: a copy handed on before for another tuple", round, pi, i)
						}
					}
					copies[w] = g
				} else if c != g {
					t.Fatalf("round %d pulse %d tuple %d: a new copy, want the one handed on before", round, pi, i)
				}
				if g.Len() != w.Len() || g.TupleID() != w.TupleID() {
					t.Fatalf("round %d pulse %d tuple %d: copy differs", round, pi, i)
				}
				for j := 0; j < w.Len(); j++ {
					if g.KeyAt(j) != w.KeyAt(j) || !jsval.Equal(g.ValueAt(j), w.ValueAt(j)) {
						t.Fatalf("round %d pulse %d tuple %d: copy differs at %q", round, pi, i, w.KeyAt(j))
					}
				}
				// downstream writes into the copy
				if i%3 == 0 {
					w.Set("w", jsval.Num(float64(pi)))
					g.Set("w", jsval.Num(float64(pi)))
				}
			}
		}
	}
}
