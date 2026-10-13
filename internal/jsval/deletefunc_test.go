package jsval

import (
	"fmt"
	"testing"
)

// DeleteFunc removes what deleting each chosen key in turn removes, keeps
// the order of the rest, and leaves lookups (through the index of a large
// object, or the scan of a small one) finding every key that is left.
func TestDeleteFuncIsDeleteInTurn(t *testing.T) {
	for _, n := range []int{0, 1, 5, indexThreshold, indexThreshold + 1, 200} {
		for _, every := range []int{1, 2, 3, 7} {
			a, b := NewObject(n), NewObject(n)
			for i := range n {
				k := fmt.Sprintf("k%d", i)
				a.Set(k, Int(i))
				b.Set(k, Int(i))
			}
			drop := func(i int) bool { return i%every == 0 }
			a.DeleteFunc(func(_ string, v Value) bool { return drop(int(v.NumValue())) })
			for i := range n {
				if drop(i) {
					b.Delete(fmt.Sprintf("k%d", i))
				}
			}
			if got, want := string(AppendJSON(nil, Obj(a))), string(AppendJSON(nil, Obj(b))); got != want {
				t.Fatalf("n %d every %d: %s, want %s", n, every, got, want)
			}
			for i := range n {
				k := fmt.Sprintf("k%d", i)
				if a.Has(k) == drop(i) {
					t.Fatalf("n %d every %d: Has(%s) = %v after deleting it %v", n, every, k, a.Has(k), drop(i))
				}
			}
			a.Set("new", True)
			if !a.Lookup("new").IsTruthy() || a.Len() != b.Len()+1 {
				t.Fatalf("n %d every %d: a key set after DeleteFunc is not found", n, every)
			}
		}
	}
}
