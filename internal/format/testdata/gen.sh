#!/bin/sh
# Regenerates every golden file in this directory from upstream d3 / vega,
# through the repository's vector runner (testdata/vectors.txt), which runs
# the pinned node, refuses a recording on a platform the manifest does not
# allow, and updates testdata/vectors.lock.
set -e
cd "$(dirname "$0")/../../.."
exec go run ./internal/cmd/vectors -write -run internal/format/ "$@"
