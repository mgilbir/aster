#!/bin/sh
# check.sh: the gates CI runs (.github/workflows/ci.yml on pull requests,
# heavy.yml on main and release candidates), with the oracle
# required, and a ledger of what ran. A gate that skips is worse than one that
# fails, so a gate whose tool is missing fails too (golangci-lint, the Python
# PDFium needs), unless -allow-missing says to record it as skipped; the
# oracle is required (ASTER_ORACLE=require: a test that needs node fails
# rather than skipping), and the corpora and upstream's test vectors are
# fetched first. CI's macOS run and the nightly fuzzing are not repeated
# here. PDFium runs with the Python ASTER_PDFIUM names, or else with one this
# script installs into .pdfium (git-ignored) from the pinned, hashed
# requirements, as CI does.
#
#   scripts/check.sh                  everything (minutes on a warm oracle cache)
#   scripts/check.sh -fast            gofmt, lint, vet, go mod tidy and the -short suite
#   scripts/check.sh -allow-missing   a missing tool is a skip, not a failure
#
# No gate writes to the working tree: go mod tidy runs with -diff.
set -u
{ # parsed whole before it runs, so editing this file mid-run is harmless
cd "$(dirname "$0")/.."
fast=0
allow_missing=0
for arg in "$@"; do
	case $arg in
	-fast) fast=1 ;;
	-allow-missing) allow_missing=1 ;;
	*) echo "usage: scripts/check.sh [-fast] [-allow-missing]" >&2; exit 2 ;;
	esac
done
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
# missing records a gate that cannot run for want of a tool: a failure,
# unless -allow-missing.
missing() {
	if [ $allow_missing = 1 ]; then
		skip "$1" "$2; -allow-missing"
	else
		ledger="$ledger
  FAILED  $1 ($2; pass -allow-missing to skip it)"
		failed=1
	fi
}

gate "gofmt" sh -c 'out=$(git ls-files -z "*.go" | xargs -0 gofmt -l); [ -z "$out" ] || { echo "$out"; exit 1; }'
gate "go vet" go vet ./...
if command -v golangci-lint >/dev/null 2>&1; then
	gate "golangci-lint ($(golangci-lint version --short 2>/dev/null || echo '?'); CI runs v2.14.0)" golangci-lint run ./...
else
	missing "golangci-lint" "not installed; CI runs v2.14.0"
fi
gate "go mod tidy" go mod tidy -diff
gate "tests (-short)" sh -c 'go test -short -json -timeout 30m ./... | go run ./internal/cmd/testsummary'
gate "fixed bounds match docs/limits.md" go test -count=1 -run '^TestFixedLimitsDocMatchesCode$' .
if [ $fast = 1 ]; then
	skip "linux/386, windows and darwin builds, fmacheck, recursion audit, race, PDFium, oracle, upstream vectors, sweeps, corpora, heavy security tests" "-fast"
else
	gate "linux/386 build, vet, tests (-short)" sh -c 'GOARCH=386 go build ./... && GOARCH=386 go vet ./... && GOARCH=386 go test -short -timeout 30m ./...'
	gate "windows and darwin build, vet" sh -c 'for os in windows darwin; do GOOS=$os go build ./... && GOOS=$os go vet ./... || exit 1; done'
	gate "fmacheck" scripts/fmacheck.sh
	gate "recursion audit" go run ./internal/cmd/recursionaudit
	gate "race (-short)" go test -short -race -timeout 30m ./...
	gate "heavy security tests" env ASTER_SECURITY=1 ASTER_MEMCHECK=1 go test -count=1 -timeout 30m -run 'TestResource|TestTimeout|TestSVGToPNGIgnoresEntities|TestMemoryLimitHeap$' .
	gate "concurrency stress (whole corpus)" env ASTER_STRESS_FULL=1 go test -count=1 -timeout 20m -run TestConcurrentConverterSharesNothing .
	gate "expression metering (full size)" env ASTER_METERING_FULL=1 go test -count=1 -timeout 20m -run TestEveryBuiltinIsMetered ./internal/expr
	pdfium=${ASTER_PDFIUM:-}
	if [ -z "$pdfium" ] && command -v python3 >/dev/null 2>&1; then
		if { [ -x .pdfium/bin/python ] || python3 -m venv .pdfium; } &&
			.pdfium/bin/pip install --quiet --require-hashes -r scripts/pdfium/requirements.txt; then
			pdfium=$PWD/.pdfium/bin/python
		fi
	fi
	if [ -n "$pdfium" ]; then
		gate "PDFium" env ASTER_PDFIUM="$pdfium" go test -count=1 -run TestPDFium ./internal/svgpdf
	else
		missing "PDFium" "no ASTER_PDFIUM, and no python3 to install pypdfium2 into .pdfium"
	fi
	if command -v pdftotext >/dev/null 2>&1 && command -v pdftoppm >/dev/null 2>&1; then
		gate "poppler (text extraction, PDF and PNG drift)" sh -c 'ASTER_POPPLER=require go test -count=1 -run "TestPDFTextIsExtractable|TestVectorPDFMatchesPNG" . && ASTER_POPPLER=require go test -count=1 -run TestToUnicodeClusters ./internal/svgpdf'
	else
		missing "poppler" "pdftotext and pdftoppm not installed (poppler-utils)"
	fi
	gate "oracle install" sh -c '(cd testdata/oracle-node && npm ci --silent) && (cd testdata/oracle-node-vl5 && npm ci --silent) && (cd testdata/mermaid && npm ci --silent)'
	gate "upstream vectors" scripts/record-upstream-vectors.sh
	gate "corpora fetch" scripts/fetch-corpora.sh
	gate "full suite (oracle required)" env ASTER_ORACLE=require go test -timeout 90m ./...
fi
printf '\nledger:%s\n' "$ledger"
exit $failed
}
