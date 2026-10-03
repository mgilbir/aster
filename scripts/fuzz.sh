#!/bin/sh
# fuzz.sh: run the coverage-guided fuzz targets listed in fuzz-targets.txt.
#
#   scripts/fuzz.sh                 every target in turn, for the minutes listed
#   FUZZTIME=30s scripts/fuzz.sh    every target for 30s
#   scripts/fuzz.sh -list           print the targets as "package target minutes"
#
# go test runs one fuzz target per invocation, so this loops. A failing input
# is written under the package's testdata/fuzz/<Target>/; the script goes on
# with the other targets and exits 1 at the end if any failed.
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
failed=""
while IFS="$tab" read -r pkg target minutes; do
	echo "== $pkg $target (${FUZZTIME:-${minutes}m})"
	go test -run='^$' -fuzz="^$target\$" -fuzztime="${FUZZTIME:-${minutes}m}" "$pkg" || failed="$failed $pkg:$target"
done <<EOF
$(targets)
EOF
if [ -n "$failed" ]; then
	echo "fuzz: failed:$failed" >&2
	exit 1
fi
