package aster_test

import (
	"os"
	"testing"
)

// The external corpora: Vega-Lite specifications other people wrote for their
// own charts ("wild", hyungkwonko/chart-llm) and the Deneb Vega templates
// (avatorl/Deneb-Vega-Templates). Both are fetched at a pinned commit into
// testdata/corpora-cache by scripts/fetch-corpora.sh, verified against the
// manifests in testdata/corpora, and turned into cases by the generators
// testdata/oracle-node/sweeps/{wild,deneb}.mjs. They run as sweeps: the
// scoreboard and the ranked causes are logged, and testdata/sweeps/<name>.txt
// records every case that is not "ok" (rewritten by -sweep.update).
//
// The claim is agreement with upstream, not success: a spec upstream refuses
// and the engine refuses too counts as ok, and a remote URL fails to load in
// both (the loaders are offline).

// requireCorpus skips the test when the corpus has not been fetched, unless
// the oracle is required (as in CI, which fetches first).
func requireCorpus(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Stat("testdata/corpora-cache/" + name + "/.commit"); err == nil {
		return
	}
	const msg = "corpus %q not fetched; run scripts/fetch-corpora.sh"
	if os.Getenv("ASTER_ORACLE") == "require" {
		t.Fatalf(msg, name)
	}
	t.Skipf(msg, name)
}

// TestCorpusWild compares the compiled Vega and the SVG of the 1981 wild
// Vega-Lite specifications with upstream's.
func TestCorpusWild(t *testing.T) {
	requireCorpus(t, "wild")
	runSweep(t, sweep{name: "wild", mode: compareLite, floor: 1900})
}

// TestCorpusDeneb compares the SVG of the Deneb templates, prepared as plain
// Vega, with upstream's.
func TestCorpusDeneb(t *testing.T) {
	requireCorpus(t, "deneb")
	runSweep(t, sweep{name: "deneb", mode: compareSVG, floor: 55})
}
