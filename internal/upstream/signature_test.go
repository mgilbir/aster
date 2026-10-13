package upstream

import (
	"strings"
	"testing"
)

// Two calls that differ only past the cut have two signatures (C87): a long
// signature ends in a hash of the whole call.
func TestLongSignaturesDoNotCollide(t *testing.T) {
	long := strings.Repeat("1,", 400)
	a := Call{Fn: "f", Args: []any{long + "2"}}
	b := Call{Fn: "f", Args: []any{long + "3"}}
	sa, sb := a.Signature(), b.Signature()
	if sa == sb {
		t.Fatalf("calls differing past the cut share the signature %q", sa)
	}
	if len(sa) > maxSignature+40 {
		t.Errorf("signature of %d bytes, want it cut near %d", len(sa), maxSignature)
	}
}

// An entry pins its divergence: a different engine answer, or a throw, is a
// different divergence.
func TestAnswersHashPinsBothSides(t *testing.T) {
	h := answersHash(1.0, false, 2.0, false)
	for _, other := range []string{
		answersHash(1.0, false, 3.0, false),
		answersHash(4.0, false, 2.0, false),
		answersHash(1.0, false, 2.0, true),
		answersHash(1.0, true, 2.0, false),
	} {
		if other == h {
			t.Errorf("two divergences hash alike: %s", h)
		}
	}
	if answersHash(1.0, false, 2.0, false) != h {
		t.Error("the hash is not deterministic")
	}
}
