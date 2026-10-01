package aster_test

import "testing"

// TestSweepLocales formats and parses through every d3 number and time
// locale; see testdata/oracle-node/sweeps/locales.mjs.
func TestSweepLocales(t *testing.T) {
	runSweep(t, sweep{name: "locales", mode: compareSVG, floor: 372})
}
