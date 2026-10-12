//go:build !race

package testutil

// RaceSlowdown scales the wall-clock limits of tests; see race.go.
const RaceSlowdown = 1
