package aster

import "github.com/mgilbir/aster/internal/testutil"

// raceSlowdown scales the wall-clock limits of the hostile-input tests: the
// race detector makes rendering several times slower, and CI runs every
// package under it at once on a small runner.
const raceSlowdown = testutil.RaceSlowdown
