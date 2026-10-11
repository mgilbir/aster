package aster

import (
	"errors"
	"strings"
	"testing"
)

// The call's ledger reaches the stages through the context: a path mark
// whose path data parses to more than WithMemoryLimit fails with ErrLimit
// from the ledger, before the scene holds it.
func TestLedgerSpansTheCall(t *testing.T) {
	c, err := New(WithMemoryLimit(8 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	path := "M0,0" + strings.Repeat("l1,1", 250_000) // 1 MB, 16 MB parsed
	spec := `{"width":10,"height":10,"marks":[{"type":"path","encode":{"enter":{"path":{"value":"` + path + `"}}}}]}`
	_, err = c.VegaToSVG([]byte(spec))
	if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "memory of") {
		t.Fatalf("want the ledger's ErrLimit, got %v", err)
	}
	// The same chart within the limit renders.
	path = "M0,0" + strings.Repeat("l1,1", 1000)
	spec = `{"width":10,"height":10,"marks":[{"type":"path","encode":{"enter":{"path":{"value":"` + path + `"}}}}]}`
	if _, err := c.VegaToSVG([]byte(spec)); err != nil {
		t.Fatal(err)
	}
}
