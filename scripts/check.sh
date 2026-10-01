#!/bin/sh
# check.sh: every gate this repository has, with the oracle required, and a
# ledger of what ran. A gate that skips is worse than one that fails, so the
# oracle is required (ASTER_ORACLE=require: a test that needs node fails
# rather than skipping) and the corpora are fetched first.
#
#   scripts/check.sh          everything (minutes on a warm oracle cache)
#   scripts/check.sh -fast    lint, vet and the -short suite only
set -u
{ # parsed whole before it runs, so editing this file mid-run is harmless
cd "$(dirname "$0")/.."
fast=0
[ "${1:-}" = "-fast" ] && fast=1
ledger=""
failed=0
gate() {
	name=$1
	shift
	printf '==> %s\n' "$name"
	if "$@"; then
		ledger="$ledger
  RAN     $name"
	else
		ledger="$ledger
  FAILED  $name"
		failed=1
	fi
}
skip() {
	ledger="$ledger
  SKIPPED $1 ($2)"
}

gate "gofmt" sh -c 'out=$(git ls-files -z "*.go" | xargs -0 gofmt -l); [ -z "$out" ] || { echo "$out"; exit 1; }'
gate "go vet" go vet ./...
gate "go mod tidy" sh -c 'go mod tidy && git diff --exit-code go.mod go.sum'
gate "tests (-short)" go test -short -timeout 30m ./...
if [ $fast = 1 ]; then
	skip "fmacheck, recursion audit, race, oracle, sweeps, corpora" "-fast"
else
	gate "fmacheck" scripts/fmacheck.sh
	gate "recursion audit" go run ./internal/cmd/recursionaudit
	gate "race (-short)" go test -short -race -timeout 30m ./...
	gate "oracle install" sh -c '(cd testdata/oracle-node && npm ci --silent) && (cd testdata/oracle-node-vl5 && npm ci --silent)'
	gate "corpora fetch" scripts/fetch-corpora.sh
	gate "full suite (oracle required)" env ASTER_ORACLE=require go test -timeout 90m ./...
fi
printf '\nledger:%s\n' "$ledger"
exit $failed
}
