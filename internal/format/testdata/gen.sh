#!/bin/sh
# Regenerates every golden file in this directory from upstream d3 / vega.
# Needs node and NODE_PATH pointing at the pinned oracle (testdata/oracle-node/node_modules).
set -e
cd "$(dirname "$0")/.."
: "${NODE_PATH:?set NODE_PATH to the pinned oracle, testdata/oracle-node/node_modules}"
export NODE_PATH
node testdata/gen_number.mjs | gzip -9 > testdata/number.json.gz
for tz in UTC America/New_York Europe/Amsterdam America/Sao_Paulo Asia/Kolkata Australia/Lord_Howe; do
  TZ=$tz node testdata/gen_time.mjs | gzip -9 > "testdata/time_$(echo $tz | tr / _).json.gz"
done
