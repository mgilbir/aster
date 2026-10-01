package aster_test

import "testing"

// TestSweepTimezones compares local-time behaviour in nine zones; see
// testdata/oracle-node/sweeps/timezones.mjs.
func TestSweepTimezones(t *testing.T) {
	runSweep(t, sweep{name: "timezones", mode: compareSVG, floor: 801})
}
