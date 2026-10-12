//go:build !race

package testutil

// RaceSlowdown scales the wall-clock limits of tests; see race.go.
const RaceSlowdown = 1

// RaceEnabled reports a -race build, whose sync.Pool drops a random share of
// what is put back: a count of allocations (testing.AllocsPerRun) is not a
// property of the code there, so tests that assert one skip it.
const RaceEnabled = false
