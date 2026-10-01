package aster_test

import "testing"

// TestSweepVegaLiteSchema compiles one specification per property value
// Vega-Lite's own schema declares (marks, encoding channels, guides and
// configuration blocks) and compares the compiled Vega with upstream's.
func TestSweepVegaLiteSchema(t *testing.T) {
	runSweep(t, sweep{name: "vega-lite-schema", mode: compareVega, floor: 27513})
}
