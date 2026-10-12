// Package testutil holds what tests share to stay independent of the machine
// they run on: no test asserts a fixed wall-clock bound for a computation,
// which fails on a loaded runner and under -race.
//
//   - A deadline or hang guard scales its slack by RaceSlowdown; the property
//     it checks is "stops", not "fast".
//   - A complexity guard ("linear, not quadratic") counts something the code
//     does deterministically (steps, comparisons, work charged to the ledger)
//     at size n and 2n, and bounds the ratio of the counts. It does not time
//     the work: a ratio of times flaked on CI runners even taken as the best of
//     several runs, so there is deliberately no helper for one here.
package testutil

import "time"

// Scaled is d scaled by RaceSlowdown.
func Scaled(d time.Duration) time.Duration { return d * RaceSlowdown }
