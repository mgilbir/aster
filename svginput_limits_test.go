package aster

import (
	"errors"
	"strings"
	"testing"
)

// SVG input to SVGToPNG is attacker-controlled (docs/security.md): the tree
// it parses to, and its text laid out, are charged to the call's ledger,
// and its size and element count derive from WithMemoryLimit, as the
// specification's size does (C8, C23, audit 2026-10-18). Each input here is
// small next to what it made the engine hold.
func TestSVGInputBoundedByMemoryLimit(t *testing.T) {
	c, err := New(WithMemoryLimit(16 << 20))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for name, svg := range map[string]string{
		// 400,000 runs of text, 2 MB: a node each, 56 MB of tree.
		"text runs": `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text display="none">` +
			strings.Repeat("a<b/>", 400_000) + `</text></svg>`,
		// 300,000 empty groups, 1.2 MB: 40 MB of tree.
		"elements": `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><g display="none">` +
			strings.Repeat("<g/>", 300_000) + `</g></svg>`,
		// One text of 500,000 characters, each placed on its own, 1.5 MB:
		// over 100 MB of characters and glyphs laid out.
		"text layout": `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text x="` +
			strings.Repeat("0 ", 500_000) + `" font-size="1" fill="none">` + strings.Repeat("a", 500_000) + `</text></svg>`,
		// 5 MB of input, past the 4 MiB a 16 MiB limit admits.
		"input": `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><!--` + strings.Repeat("x", 5<<20) + `--></svg>`,
	} {
		if _, err := c.SVGToPNG(svg); !errors.Is(err, ErrLimit) {
			t.Errorf("%s: err = %v, want ErrLimit", name, err)
		}
	}
}
