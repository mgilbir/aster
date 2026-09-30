#!/bin/sh
# fmacheck.sh: guard against fused multiply-add (FMA) drift from JavaScript.
#
# On arm64 the Go compiler fuses x*y + z into FMADDD/FMSUBD/FNMADDD/FNMSUBD
# (even across statements) unless the product is rounded with an explicit
# float64(...) conversion. V8 never fuses, so a fused site can diverge from
# upstream Vega. This script compiles the engine for GOARCH=arm64 (works on any
# host), collects every fused instruction whose source position is in
# purego/internal, and compares the sites with fmacheck.allow.
#
# Excluded: internal/jsmath (fuses deliberately, mirroring V8's compiled
# fdlibm) and internal/raster (targets resvg, not V8 bit parity).
#
# A site is identified as "file<TAB>enclosing function<TAB>source text", so
# the allowlist survives line shifts. Usage:
#   scripts/fmacheck.sh            check against fmacheck.allow (exit 1 on new sites)
#   scripts/fmacheck.sh -list      print all current sites (allowlist format)
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)   # module root
allow="$here/fmacheck.allow"
mode=${1:-check}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cd "$root"
GOARCH=arm64 CGO_ENABLED=0 go build -a -gcflags='all=-S' ./purego/... >"$tmp/asm.txt" 2>&1 || {
	if ! grep -q STEXT "$tmp/asm.txt"; then
		cat "$tmp/asm.txt" >&2
		echo "fmacheck: build failed" >&2
		exit 2
	fi
}

# Pass 1: (file, line, function) of every fused instruction under purego/internal.
awk '
/ STEXT / {
	fn = $1
	gsub(/#[^#]*#/, "", fn)   # generic shape-instantiation hash suffix
	sub(/^github.com\/mgilbir\/aster\/purego\/internal\//, "", fn)
	next
}
/\t(FMADDD|FMSUBD|FNMADDD|FNMSUBD|FMADDS|FMSUBS|FNMADDS|FNMSUBS)\t/ {
	if (match($0, /\(([^()]*):[0-9]+\)/) == 0) next
	loc = substr($0, RSTART + 1, RLENGTH - 2)
	n = split(loc, p, ":"); line = p[n]; file = substr(loc, 1, length(loc) - length(line) - 1)
	i = index(file, "/purego/internal/"); if (i == 0) next
	rel = substr(file, i + 1)
	if (rel ~ /^purego\/internal\/(jsmath|raster)\//) next
	print rel "\t" line "\t" fn
}' "$tmp/asm.txt" | sort -u >"$tmp/raw.txt"

# Pass 2: attach the trimmed source text.
: >"$tmp/sites.txt"
while IFS="$(printf '\t')" read -r rel line fn; do
	text=$(sed -n "${line}p" "$root/$rel" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
	printf '%s\t%s\t%s\n' "$rel" "$fn" "$text"
done <"$tmp/raw.txt" | sort -u >"$tmp/sites.txt"

if [ "$mode" = "-list" ]; then
	cat "$tmp/sites.txt"
	exit 0
fi

# Allowlist lines: "file<TAB>function<TAB>text<TAB># justification".
grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$allow" 2>/dev/null |
	awk -F'\t' '{print $1 "\t" $2 "\t" $3}' | sort -u >"$tmp/allowed.txt" || true
[ -f "$tmp/allowed.txt" ] || : >"$tmp/allowed.txt"

comm -23 "$tmp/sites.txt" "$tmp/allowed.txt" >"$tmp/new.txt"
comm -13 "$tmp/sites.txt" "$tmp/allowed.txt" >"$tmp/stale.txt"

if [ -s "$tmp/stale.txt" ]; then
	echo "fmacheck: stale allowlist entries (no longer fused; remove them):" >&2
	sed 's/^/  /' "$tmp/stale.txt" >&2
fi
if [ -s "$tmp/new.txt" ]; then
	echo "fmacheck: new fused multiply-add sites (round the product with float64(a*b), or allowlist with a justification):" >&2
	awk -F'\t' '{printf "  %s [%s]\n      %s\n", $1, $2, $3}' "$tmp/new.txt" >&2
	exit 1
fi
echo "fmacheck: ok ($(wc -l <"$tmp/sites.txt" | tr -d ' ') allowlisted sites)"
