// Package testutil holds what tests share to stay independent of the machine
// they run on: a wall-clock limit exists to signal unbounded or quadratic
// behaviour, not to measure speed, so it is calibrated and never a guess.
//
//   - A limit is 2x the time CI measured (ASTER_TIMINGS, see Timing), floor
//     1 s, rounded up, with the baseline written next to it; slower than that
//     is a finding to investigate, not a reason to pad. A post-deadline
//     allowance is 2x the measured overshoot, never below 250 ms.
//   - A limit scales by RaceSlowdown; its baseline is the larger of the worst
//     time without -race and the worst under -race divided by RaceSlowdown.
//   - A pure hang guard (a child's kill wall, a wait that only stops a stuck
//     test) keeps a large value and says "hang guard, not a calibrated bound".
//   - A complexity guard ("linear, not quadratic") counts something the code
//     does deterministically (steps, comparisons, work charged to the ledger)
//     at size n and 2n, and bounds the ratio of the counts. It does not time
//     the work: a ratio of times flaked on CI runners even taken as the best of
//     several runs, so there is deliberately no helper for one here.
package testutil

import "time"

// Scaled is d scaled by RaceSlowdown.
func Scaled(d time.Duration) time.Duration { return d * RaceSlowdown }
