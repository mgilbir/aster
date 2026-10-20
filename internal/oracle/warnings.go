package oracle

import "github.com/mgilbir/aster/internal/textcut"

// CutWarning is a warning of upstream's as the engine's public API reports
// it: cut to textcut.MaxWarning bytes (vega.MaxWarningText) at a character boundary, with an
// ellipsis (textcut.Cut), the bound docs/limits.md documents for every
// warning a handler hears. Upstream logs its warnings whole, so a
// differential that compares warnings compares the engine's with node's
// through this cut, never with node's text as logged.
func CutWarning(w string) string { return textcut.Cut(w, textcut.MaxWarning) }

// CutWarnings is CutWarning of each of ws, in order.
func CutWarnings(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = CutWarning(w)
	}
	return out
}

// WarningsMatch says whether the engine's warnings are node's, in order and
// count, once node's are cut as the engine's are (CutWarning). A difference
// inside the bound still differs.
func WarningsMatch(engine, node []string) bool {
	if len(engine) != len(node) {
		return false
	}
	for i := range engine {
		if engine[i] != CutWarning(node[i]) {
			return false
		}
	}
	return true
}
