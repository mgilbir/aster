package oracle

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/textcut"
)

// TestWarningsMatchThroughTheCut: a warning of upstream's longer than the
// engine's documented bound matches the engine's once cut as the engine cuts
// it; a difference inside the bound, a text the engine did not cut, or a
// missing warning still differs.
func TestWarningsMatchThroughTheCut(t *testing.T) {
	// A long warning with multi-byte characters about the cut point.
	node := `Dropping "` + strings.Repeat("ab—", textcut.MaxWarning) + `" from channel "description".`
	// The engine's text, made here without textcut: the longest prefix of
	// at most the bound that ends at a character boundary, and an ellipsis.
	n := textcut.MaxWarning
	for !utf8.RuneStart(node[n]) {
		n--
	}
	if n == textcut.MaxWarning {
		t.Fatal("the test text does not put a character across the bound")
	}
	engine := node[:n] + "…"
	if !WarningsMatch([]string{"short", engine}, []string{"short", node}) {
		t.Errorf("a warning past the bound does not match node's, cut:\n  engine %q\n  node   %q", engine, CutWarning(node))
	}
	at := 500 // inside the bound
	changed := engine[:at] + "X" + engine[at+1:]
	if WarningsMatch([]string{changed}, []string{node}) {
		t.Error("a difference inside the bound matched")
	}
	if WarningsMatch([]string{node}, []string{node}) {
		t.Error("an engine warning past the bound, uncut, matched")
	}
	if WarningsMatch([]string{engine}, []string{node, node}) || WarningsMatch(nil, []string{"a"}) {
		t.Error("a missing warning matched")
	}
	if !WarningsMatch([]string{"a", "b"}, []string{"a", "b"}) || WarningsMatch([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("short warnings are not compared exactly, in order")
	}
}
