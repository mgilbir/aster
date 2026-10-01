package aster_test

import "testing"

// TestSweepValues holds the specification still and varies the data: one
// chart per (journey, column of unusual values). See
// testdata/oracle-node/sweeps/values.mjs for the columns and the journeys.
func TestSweepValues(t *testing.T) {
	runSweep(t, sweep{name: "values", mode: compareSVG, floor: 500})
}
