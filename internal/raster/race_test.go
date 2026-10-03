//go:build race

package raster

// raceSlowdown scales the wall-clock limits of the security tests: the race
// detector makes the renderer several times slower, and CI runs every package
// under it at once on a small runner.
const raceSlowdown = 5
