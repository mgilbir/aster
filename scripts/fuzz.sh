#!/bin/sh
# fuzz.sh: run the coverage-guided fuzz targets listed in fuzz-targets.txt.
#
#   scripts/fuzz.sh                 every target in turn, for the minutes listed
#   FUZZTIME=30s scripts/fuzz.sh    every target for 30s
#   scripts/fuzz.sh -list           print the targets as "package target minutes"
#   scripts/fuzz.sh -one pkg target  one target, for FUZZTIME (CI's matrix runs this)
#
# go test runs one fuzz target per invocation, so this loops. A failing input
# is written under the package's testdata/fuzz/<Target>/; the script goes on
# with the other targets and exits 1 at the end if any failed.
#
# Each new input the fuzzer finds interesting, and a failing one, is
# minimized for up to FUZZMINIMIZETIME (10s by default, where go test's is
# 60s) before fuzzing goes on: no input is run meanwhile, and on a target
# whose inputs are slow, a minute a find was most of the run. A failing input
# is written all the same, minimized for less long.
#
# go test -fuzz can fail with only "context deadline exceeded" when -fuzztime
# runs out while it is still minimizing a new input (most likely on a cold
# corpus): nothing crashed and no input is written. Such a run counts as a
# pass.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
list="$here/fuzz-targets.txt"
tab=$(printf '\t')

targets() {
	grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$list"
}

if [ "${1:-}" = "-list" ]; then
	targets
	exit 0
fi

cd "$here/.."

fuzz_one() { # pkg target fuzztime
	out=$(mktemp)
	go test -run='^$' -fuzz="^$2\$" -fuzztime="$3" -fuzzminimizetime="${FUZZMINIMIZETIME:-10s}" "$1" 2>&1 | tee "$out"
	if grep -q '^ok' "$out"; then
		rm -f "$out"
		return 0
	fi
	if grep -q 'context deadline exceeded' "$out" && ! grep -q -e '^panic' -e 'Failing input written' "$out" &&
		[ -z "$(git ls-files --others --exclude-standard -- "$1/testdata/fuzz/$2")" ]; then
		echo "fuzz: $1 $2: the fuzz time ran out while minimizing; no failing input, counted as a pass"
		rm -f "$out"
		return 0
	fi
	rm -f "$out"
	return 1
}

if [ "${1:-}" = "-one" ]; then
	fuzz_one "$2" "$3" "${FUZZTIME:?FUZZTIME is required with -one}"
	exit
fi

failed=""
while IFS="$tab" read -r pkg target minutes; do
	echo "== $pkg $target (${FUZZTIME:-${minutes}m})"
	fuzz_one "$pkg" "$target" "${FUZZTIME:-${minutes}m}" || failed="$failed $pkg:$target"
done <<EOF
$(targets)
EOF
if [ -n "$failed" ]; then
	echo "fuzz: failed:$failed" >&2
	exit 1
fi
