#!/bin/sh
# Summarise the ASTER_TIMING lines the tests print when ASTER_TIMINGS is set
# (internal/testutil/timing.go): per site, the worst measured value, the bound
# in force in that run, the ratio of the two, and a proposed bound of twice the
# worst (never below 0.5 s, where a wall clock measures the runner's scheduling,
# not the code).
#
# Usage: scripts/timings.sh FILE... | scripts/timings.sh < log
#
# Reads job logs (a GitHub timestamp or other prefix before ASTER_TIMING is
# ignored) or the timings.tsv artifact. A site is a (site, race, GOARCH) triple;
# the worst line's test name is shown. The bounds in the table are the ones in
# force in the run, already scaled by the race slowdown; a proposal for an
# unscaled bound needs dividing by it (5 under -race).
#
# Line format, tab separated:
#   ASTER_TIMING <test> <site> <measured s> <bound s> <race 0/1> <GOARCH>
set -eu

if [ $# -gt 0 ]; then
	cat -- "$@"
else
	cat
fi | tr -d '\r' | awk -F'\t' '
{
	for (i = 1; i <= NF; i++) if ($i ~ /ASTER_TIMING$/) break
	if (i > NF - 6) next
	test = $(i+1); site = $(i+2); m = $(i+3) + 0; b = $(i+4) + 0; race = $(i+5); arch = $(i+6)
	key = site "\t" race "\t" arch
	n[key]++
	if (!(key in worst) || m > worst[key]) { worst[key] = m; bound[key] = b; who[key] = test }
}
END {
	for (key in worst) {
		split(key, k, "\t")
		r = bound[key] > 0 ? worst[key] / bound[key] : 0
		p = 2 * worst[key]; if (p < 0.5) p = 0.5
		printf "%.6f\t%-72s %4s %-5s %6d %9.3f %9.3f %7.3f %9.3f  %s\n", r, substr(k[1], 1, 72), k[2], k[3], n[key], worst[key], bound[key], r, p, who[key]
	}
}' | sort -rn -k1,1 | cut -f2- | {
	printf '%-72s %4s %-5s %6s %9s %9s %7s %9s  %s\n' site race arch n worst_s bound_s ratio propose_s "worst test"
	cat
}
