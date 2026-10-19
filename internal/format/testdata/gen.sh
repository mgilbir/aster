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
for tz in America/New_York America/Sao_Paulo Australia/Lord_Howe Pacific/Apia Europe/London America/St_Johns Asia/Kolkata; do
  TZ=$tz node testdata/gen_weekevery.mjs
done | gzip -9 > testdata/weekevery.jsonl.gz
node testdata/gen_duck.mjs | gzip -n -9 > testdata/duck.json.gz
for tz in UTC America/New_York Africa/Monrovia; do
  TZ=$tz node testdata/gen_calendar.mjs
done | gzip -n -9 > testdata/calendar.jsonl.gz
for tz in UTC America/New_York; do
  TZ=$tz node testdata/gen_dateparse.mjs
done | gzip -n -9 > testdata/dateparse.jsonl.gz
# V8's zone names for String(date), from a host whose zoneinfo is node's tzdata release:
node testdata/gen_zonenames.mjs /usr/share/zoneinfo/tzdata.zi vectors | gzip -n -9 > testdata/zonenames.json.gz
node testdata/gen_zonenames.mjs /usr/share/zoneinfo/tzdata.zi samples > /tmp/zonenames-samples.json
go run zonenames_gen.go /tmp/zonenames-samples.json
