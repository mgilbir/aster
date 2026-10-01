package aster_test

import "testing"

// TestSweepSignals renders a chart, writes one signal into the running view
// and renders again, over one chart per thing a signal can reach and a list of
// deliberately not-all-sensible values (see sweeps/signals.mjs).
func TestSweepSignals(t *testing.T) {
	runSweep(t, sweep{name: "signals", mode: compareSignals, floor: 166})
}
