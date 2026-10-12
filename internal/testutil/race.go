//go:build race

package testutil

// RaceSlowdown scales the wall-clock limits of tests: the race detector makes
// the engine several times slower, and CI runs every package under it at once
// on a small runner.
const RaceSlowdown = 5
