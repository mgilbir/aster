package vega

import (
	"slices"
	"sort"
	"testing"
)

// The corpus comparisons with upstream's recordings (TestCorpus,
// TestCorpusSVG, TestVegaLiteScenes) used to report a scoreboard and pass
// whatever it said, so a regression in any recorded chart went unnoticed
// (audit 2026-10-18, vector-drift follow-up). A spec that does not match is
// now a failure unless it is listed here with the reason; a listed spec that
// matches, or that is no longer recorded, fails too, so each list stays
// exact.

// knownSceneMismatches are the specs of testdata/scene the runtime does not
// reproduce.
var knownSceneMismatches = map[string]string{
	"contour-plot":       "recording: upstream draws the heatmap image with node-canvas, which gen_scenegraph.mjs records without (no-canvas-hook.mjs), so it recorded an empty scene",
	"density-heatmaps":   "recording: as contour-plot, the heatmap image needs node-canvas",
	"dorling-cartogram":  "divergence: upstream's items carry noBound (false), which the runtime's scene items lack",
	"legend-clip-height": "divergence: upstream's legend items carry noBound (false), which the runtime's scene items lack",
	"platformer":         "divergence: upstream keeps a colour object (d3.hsl) as an item's fill, where the runtime writes the colour's string (the SVG matches)",
}

// knownSVGMismatches are the specs of testdata/svg whose SVG the runtime and
// package svg do not reproduce.
var knownSVGMismatches = map[string]string{}

// knownVegaLiteSceneMismatches are the recordings of testdata/vl the runtime
// does not reproduce.
var knownVegaLiteSceneMismatches = map[string]string{}

// checkKnownMismatches fails for every name in failed that known does not
// list, and for every name known lists that ran and matched or did not run.
func checkKnownMismatches(t *testing.T, corpus string, ran, failed []string, known map[string]string) {
	t.Helper()
	sort.Strings(failed)
	for _, n := range failed {
		if known[n] == "" {
			t.Errorf("%s: %s does not match upstream's recording (VEGA_ONLY=%s shows how)", corpus, n, n)
		}
	}
	for n := range known {
		switch {
		case !slices.Contains(ran, n):
			t.Errorf("%s: %s is listed as not matching, but has no recording any more: remove it", corpus, n)
		case !slices.Contains(failed, n):
			t.Errorf("%s: %s now matches upstream's recording: remove it from the list", corpus, n)
		}
	}
}
