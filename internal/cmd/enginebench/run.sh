#!/bin/sh
# run.sh: time node and the engine one after another (never concurrently, so
# they do not compete for the CPU) and write the merged report.
#   internal/cmd/enginebench/run.sh [outdir] [extra flags for every engine]
# Run from the repository root; needs node and testdata/oracle-node
# installed (cd testdata/oracle-node && npm ci). NODE overrides the
# node command, e.g. NODE="volta run --node 24 node".
set -eu
{ # parsed whole before it runs, so editing this file mid-run is harmless
out=${1:-enginebench-out}
[ $# -gt 0 ] && shift
mkdir -p "$out"
go build -o "$out/enginebench" ./internal/cmd/enginebench
TZ=UTC NODE_PATH=testdata/oracle-node/node_modules \
	${NODE:-node} internal/cmd/enginebench/bench.mjs "$@" >"$out/node.json"
"$out/enginebench" -out "$out/aster.json" "$@"
"$out/enginebench" -report "$out/node.json" "$out/aster.json" >"$out/report.md"
echo "wrote $out/report.md"
exit
}
