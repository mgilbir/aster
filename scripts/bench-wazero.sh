#!/usr/bin/env bash
# Compares aster benchmarks on the current WASM runtime (github.com/mgilbir/andsifr,
# from go.mod) against another revision of it, without touching go.mod/go.sum:
# the comparison run uses an alternate module file (wazero-fork.mod) via go's
# -modfile flag.
#
# Usage:
#   ./scripts/bench-wazero.sh                 # full run (count=10)
#   COUNT=3 BENCH=VegaLite ./scripts/bench-wazero.sh
#
# Environment variables:
#   COUNT   benchmark repetitions per side (default 10; benchstat wants >= 10)
#   BENCH   -bench regexp (default ".")
#   FORK    replacement module (default github.com/mgilbir/andsifr), or a
#           local directory (starts with / or ./) holding a checkout
#   REF     branch/tag/commit to compare against (default main; ignored
#           when FORK is a local directory)
set -euo pipefail

cd "$(dirname "$0")/.."

COUNT="${COUNT:-10}"
BENCH="${BENCH:-.}"
FORK="${FORK:-github.com/mgilbir/andsifr}"
REF="${REF:-main}"

# The fork is fetched directly from GitHub: module proxies (including
# corporate ones) typically cannot resolve branch names on forks.
export GOPRIVATE="${GOPRIVATE:+$GOPRIVATE,}$FORK"

BASELINE_OUT=bench-baseline.txt
FORK_OUT=bench-fork.txt
FORK_MOD=wazero-fork.mod
FORK_SUM=wazero-fork.sum

# JS modules are vendored on demand and gitignored.
if [ ! -d internal/js/modules ]; then
    echo "==> Vendoring JS modules"
    go run ./cmd/vendor-js
fi

echo "==> Baseline: $(go list -m github.com/mgilbir/andsifr)"
go test -run '^$' -bench "$BENCH" -benchmem -count "$COUNT" -timeout 60m . | tee "$BASELINE_OUT"

cp go.mod "$FORK_MOD"
cp go.sum "$FORK_SUM"
case "$FORK" in
/* | ./* | ../*)
    echo "==> Using local fork checkout: $FORK"
    go mod edit -replace "github.com/mgilbir/andsifr=$FORK" "$FORK_MOD"
    ;;
*)
    echo "==> Resolving $FORK@$REF"
    # Resolve the branch/tag to a canonical (pseudo-)version up front; replace
    # directives require one. Avoids `go mod tidy`, which would try to fetch
    # test dependencies of transitive modules through the proxy.
    FORK_VERSION=$(go list -m "$FORK@$REF" | awk '{print $2}')
    echo "    resolved to $FORK_VERSION"
    go mod edit -replace "github.com/mgilbir/andsifr=$FORK@$FORK_VERSION" "$FORK_MOD"
    ;;
esac
# Build with -mod=mod so go fetches the fork (and any dependencies it adds,
# e.g. golang.org/x/sys) and records them in wazero-fork.mod/.sum.
go build -modfile="$FORK_MOD" -mod=mod ./...

echo "==> Fork: $(go list -modfile="$FORK_MOD" -m github.com/mgilbir/andsifr)"
go test -modfile="$FORK_MOD" -run '^$' -bench "$BENCH" -benchmem -count "$COUNT" -timeout 60m . | tee "$FORK_OUT"

echo "==> benchstat (baseline vs fork)"
if command -v benchstat >/dev/null; then
    benchstat "$BASELINE_OUT" "$FORK_OUT"
elif [ -x "$(go env GOPATH)/bin/benchstat" ]; then
    "$(go env GOPATH)/bin/benchstat" "$BASELINE_OUT" "$FORK_OUT"
else
    go run golang.org/x/perf/cmd/benchstat@latest "$BASELINE_OUT" "$FORK_OUT"
fi
