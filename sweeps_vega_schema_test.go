package aster_test

import "testing"

// TestSweepVegaSchema renders one chart per property value the Vega schema
// declares (axis, legend, title, scale types, projections, view, layout,
// config blocks, mark channels) and compares the engine with upstream. The
// generator is testdata/oracle-node/sweeps/vega-schema.mjs.
func TestSweepVegaSchema(t *testing.T) {
	runSweep(t, sweep{name: "vega-schema", mode: compareSVG, floor: 1})
}
