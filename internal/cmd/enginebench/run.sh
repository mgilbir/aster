#!/bin/sh
# run.sh: time the three engines one after another (never concurrently, so
# they do not compete for the CPU) and write the merged report.
#   internal/cmd/enginebench/run.sh [outdir] [extra flags for every engine]
# Run from the repository root; needs node and purego/testdata/oracle-node
# installed (cd purego/testdata/oracle-node && npm ci). NODE overrides the
# node command, e.g. NODE="volta run --node 24 node".
set -eu
{ # parsed whole before it runs, so editing this file mid-run is harmless
out=${1:-enginebench-out}
[ $# -gt 0 ] && shift
mkdir -p "$out"
go build -o "$out/enginebench" ./internal/cmd/enginebench
TZ=UTC NODE_PATH=purego/testdata/oracle-node/node_modules \
	${NODE:-node} internal/cmd/enginebench/bench.mjs "$@" >"$out/node.json"
"$out/enginebench" -engine aster -out "$out/aster.json" "$@"
"$out/enginebench" -engine purego -out "$out/purego.json" "$@"
"$out/enginebench" -report "$out/node.json" "$out/aster.json" "$out/purego.json" >"$out/report.md"
echo "wrote $out/report.md"
exit
}
