#!/bin/sh
# check.sh: the gates CI runs (.github/workflows/ci.yml), with the oracle
# required, and a ledger of what ran. A gate that skips is worse than one that
# fails, so the oracle is required (ASTER_ORACLE=require: a test that needs
# node fails rather than skipping), and the corpora and upstream's test
# vectors are fetched first. CI's macOS run and the nightly fuzzing are not
# repeated here; the PDFium gate runs when ASTER_PDFIUM names its Python
# (see docs/testing.md), and is listed as skipped otherwise.
#
#   scripts/check.sh          everything (minutes on a warm oracle cache)
#   scripts/check.sh -fast    gofmt, lint, vet, go mod tidy and the -short suite
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
if command -v golangci-lint >/dev/null 2>&1; then
	gate "golangci-lint" golangci-lint run ./...
else
	skip "golangci-lint" "not installed; CI runs v2.14.0"
fi
gate "go mod tidy" sh -c 'go mod tidy && git diff --exit-code go.mod go.sum'
gate "tests (-short)" go test -short -timeout 30m ./...
if [ $fast = 1 ]; then
	skip "linux/386, fmacheck, recursion audit, race, PDFium, oracle, upstream vectors, sweeps, corpora" "-fast"
else
	gate "linux/386 build, vet, tests (-short)" sh -c 'GOARCH=386 go build ./... && GOARCH=386 go vet ./... && GOARCH=386 go test -short -timeout 30m ./...'
	gate "fmacheck" scripts/fmacheck.sh
	gate "recursion audit" go run ./internal/cmd/recursionaudit
	gate "race (-short)" go test -short -race -timeout 30m ./...
	if [ -n "${ASTER_PDFIUM:-}" ]; then
		gate "PDFium" go test -count=1 -run TestPDFium ./internal/svgpdf
	else
		skip "PDFium" "ASTER_PDFIUM not set"
	fi
	gate "oracle install" sh -c '(cd testdata/oracle-node && npm ci --silent) && (cd testdata/oracle-node-vl5 && npm ci --silent) && (cd testdata/mermaid && npm ci --silent)'
	gate "upstream vectors" scripts/record-upstream-vectors.sh
	gate "corpora fetch" scripts/fetch-corpora.sh
	gate "full suite (oracle required)" env ASTER_ORACLE=require go test -timeout 90m ./...
fi
printf '\nledger:%s\n' "$ledger"
exit $failed
}
