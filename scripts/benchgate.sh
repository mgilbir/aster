#!/bin/sh
# benchgate.sh: fail when a change makes the benchmarks below measurably
# slower or hungrier than its base.
#
#   scripts/benchgate.sh [base [head]]    head: default HEAD (a ref; uncommitted changes are not measured)
#                                         base: default origin/main (or main); the merge-base with head is used
#
# Both refs are checked out side by side and their test binaries built with
# the same command (go test -c). The binaries then run alternately, base and
# head, -count rounds, the order flipping every round, so that a runner that
# warms up, throttles or has a noisy neighbour hits both sides alike. The
# samples go to benchstat (pinned below); its CSV is parsed for the verdict
# and its table is printed, and added to the job summary in CI.
#
# A benchmark regresses when benchstat finds the difference significant
# (p < 0.05) and the increase is above the threshold for its unit:
#
#   allocs/op  > 2%    B/op  > 5%      near-deterministic; a small bar
#   ns/op      > 15%                   noisy on shared runners: three times the
#                                      worst spread (5.6%) of medians between
#                                      a commit and itself on a quiet laptop
#
# The stage times of BenchmarkScenes (json-ns/op, ...) are in the table but
# not gated: they are too short to be steady, and the packages' own
# benchmarks cover the stages.
#
# For an intended trade-off, BENCHGATE_ALLOW=true reports the regression and
# exits 0 (CI sets it from the PR label perf-regression-ok). Other settings:
# BENCHGATE_COUNT (rounds, default 8), BENCHGATE_BENCHTIME (default 300ms),
# BENCHGATE_NS_PCT, BENCHGATE_BYTES_PCT, BENCHGATE_ALLOCS_PCT, BENCHGATE_OUT
# (a directory to keep the samples and the benchstat output in) and BENCHSTAT
# (a benchstat binary, instead of installing the pinned version).
set -eu
cd "$(dirname "$0")/.."

count=${BENCHGATE_COUNT:-8}
benchtime=${BENCHGATE_BENCHTIME:-300ms}
ns_pct=${BENCHGATE_NS_PCT:-15}
bytes_pct=${BENCHGATE_BYTES_PCT:-5}
allocs_pct=${BENCHGATE_ALLOCS_PCT:-2}
benchstat_version=v0.0.0-20260929162123-406019bb8b68

# The benchmarks: a package and a -bench regex per line. Charts of small and
# medium size through every stage (BenchmarkScenes reports each stage's time
# too), and the packages the stages spend their time in: JSON parsing,
# expressions, scales, text, the SVG writer, the rasterizer and the PDF
# translator. The big scenes (100k rows) are left out to keep the run short.
benchmarks() {
	cat <<'EOF'
. ^BenchmarkScenes$/^(bars-100|bars-1000|symbols-10000|line-1000|series-20-with-legend|dense-labels-2000)$
internal/jsval ParseJSONRows|ParseJSONCoordinates|ObjectSetSmall
internal/expr ^BenchmarkEval$/(upper|format|year|sqrt)
internal/expr ^(BenchmarkFilter10k|BenchmarkCompileAndEval10k|BenchmarkCompile|BenchmarkParse)$
internal/scale ApplyFloat|ApplyValue|BandApply|OrdinalApply|LinearColorApply|LinearTicks|LogTicks
internal/text MeasureCached|MeasureUncached$|MeasureLong
internal/svg ^BenchmarkRender10k$
internal/raster ^BenchmarkGolden|^BenchmarkColourGlyphs$
internal/svgpdf ^BenchmarkConvert$|^BenchmarkConvertColour$
internal/vegalite CompileBar|CompileTrellis|CompileSplom
internal/vega Scatter1k|Scatter20k|Facet200x10
internal/transforms ^Benchmark(AggregateSumMean100k|Bin100k|Stack100k)$
EOF
}

if [ "$count" -lt 5 ]; then
	echo "benchgate: BENCHGATE_COUNT must be at least 5 (fewer samples cannot reach p < 0.05)" >&2
	exit 2
fi

head=${2:-HEAD}
base=${1:-$(git rev-parse --verify -q origin/main || git rev-parse --verify main)}
head=$(git rev-parse --verify "$head^{commit}")
base=$(git merge-base "$base" "$head") # a base that has moved on is not what the change was made against

work=$(mktemp -d "${TMPDIR:-/tmp}/benchgate.XXXXXX")
cleanup() {
	git worktree remove --force "$work/base" 2>/dev/null || true
	git worktree remove --force "$work/head" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

benchstat=${BENCHSTAT:-}
if [ -z "$benchstat" ]; then
	GOBIN="$work/bin" go install "golang.org/x/perf/cmd/benchstat@$benchstat_version"
	benchstat=$work/bin/benchstat
fi

git worktree add --detach --quiet "$work/base" "$base"
git worktree add --detach --quiet "$work/head" "$head"
echo "benchgate: base $(git log -1 --format='%h %s' "$base")"
echo "benchgate: head $(git log -1 --format='%h %s' "$head")"

# Build both sides before running either; a package missing from the base (a
# new one) or without the benchmark is left out, there is nothing to compare.
# -trimpath keeps the checkout directories out of the binaries, so that a
# package the change does not reach builds the same bytes on both sides; such
# a package is left out too, as two runs of one program can only differ by
# noise (a shared runner once showed one 23% apart, p < 0.001).
entries=$work/entries
: >"$entries"
n=0
benchmarks | while read -r pkg regex; do
	n=$((n + 1))
	for side in base head; do
		(cd "$work/$side" && [ -d "$pkg" ] && go test -c -trimpath -o "$work/$side.$n.test" "./$pkg" 2>/dev/null) || continue
	done
	if ! [ -x "$work/base.$n.test" ] || ! [ -x "$work/head.$n.test" ]; then
		echo "benchgate: skipping $pkg ($regex): no test binary on both sides"
	elif cmp -s "$work/base.$n.test" "$work/head.$n.test"; then
		echo "benchgate: skipping $pkg ($regex): unchanged by the change"
	else
		printf '%s %s %s\n' "$n" "$pkg" "$regex" >>"$entries"
	fi
done

if ! [ -s "$entries" ]; then
	verdict="benchgate: no regression (the change reaches none of the benchmarked packages)"
	echo "$verdict"
	[ -z "${GITHUB_STEP_SUMMARY:-}" ] || printf '### Benchmarks\n\n%s\n' "$verdict" >>"$GITHUB_STEP_SUMMARY"
	exit 0
fi

run() { # run side n pkg regex
	(cd "$work/$1/$3" && "$work/$1.$2.test" -test.run '^$' -test.bench "$4" -test.benchmem \
		-test.benchtime "$benchtime" -test.count 1 -test.timeout 30m) >>"$work/$1.txt"
}

round=0
while [ "$round" -lt "$count" ]; do
	round=$((round + 1))
	printf 'benchgate: round %s/%s\n' "$round" "$count"
	while read -r n pkg regex; do
		if [ $(((round + n) % 2)) = 0 ]; then
			run base "$n" "$pkg" "$regex"
			run head "$n" "$pkg" "$regex"
		else
			run head "$n" "$pkg" "$regex"
			run base "$n" "$pkg" "$regex"
		fi
	done <"$entries"
done

(cd "$work" && "$benchstat" -format csv base.txt head.txt >stat.csv 2>stat.err &&
	"$benchstat" base.txt head.txt >stat.txt 2>>stat.err) || { cat "$work/stat.err" >&2; exit 1; }
cat "$work/stat.txt"

# The CSV has, per table, a "pkg:" line, a header with the unit and then a row
# per benchmark whose "vs base" cell is "~" unless p < 0.05, else the change.
awk -F, -v ns="$ns_pct" -v by="$bytes_pct" -v al="$allocs_pct" '
substr($0, 1, 1) == "\"" { # a quoted name may hold commas
	i = index(substr($0, 2), "\"")
	name = substr($0, 2, i - 1)
	gsub(/,/, ";", name)
	$0 = name substr($0, i + 2)
}
/^pkg: / { pkg = $0; sub(/^pkg: .*aster\/?/, "", pkg); next }
$1 == "" && $6 == "vs base" { unit = $2; next }
$1 == "" || $1 == "geomean" || $6 == "" || $6 == "~" { next }
{
	limit = -1
	if (unit == "allocs/op") limit = al
	else if (unit == "B/op") limit = by
	else if (unit == "sec/op") limit = ns # not the stages (json-sec/op, ...): too short to be steady
	if (limit < 0) next
	pct = $6
	sub(/%$/, "", pct)
	# "?" is a significant change from zero (no ratio): any increase is over
	if ($6 == "?") over = ($4 + 0 > $2 + 0)
	else over = (pct ~ /Inf/) ? (pct ~ /^\+/) : (pct + 0 > limit)
	if (over) {
		bad++
		printf "REGRESSION %s %s %s %s (limit +%s%%, %s)\n", (pkg == "" ? "." : pkg), $1, unit, $6, limit, $7
	}
}
END { exit bad > 0 ? 1 : 0 }
' "$work/stat.csv" >"$work/verdict.txt" && rc=0 || rc=$?
[ "$rc" -le 1 ] || { echo "benchgate: cannot parse benchstat's CSV" >&2; exit 2; }

if [ -n "${BENCHGATE_OUT:-}" ]; then
	mkdir -p "$BENCHGATE_OUT"
	cp "$work/base.txt" "$work/head.txt" "$work/stat.csv" "$work/stat.txt" "$BENCHGATE_OUT"
fi

if [ $rc = 0 ]; then
	verdict="benchgate: no regression (allocs/op +${allocs_pct}%, B/op +${bytes_pct}%, ns/op +${ns_pct}%, p < 0.05)"
else
	cat "$work/verdict.txt"
	verdict="benchgate: regression over base $(git log -1 --format=%h "$base")"
fi
echo "$verdict"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	{
		printf '### Benchmarks, base %s vs head %s\n\n%s\n\n' "$(git log -1 --format=%h "$base")" "$(git log -1 --format=%h "$head")" "$verdict"
		[ $rc = 0 ] || printf '```\n%s\n```\n\n' "$(cat "$work/verdict.txt")"
		printf '<details><summary>benchstat</summary>\n\n```\n%s\n```\n</details>\n' "$(cat "$work/stat.txt")"
	} >>"$GITHUB_STEP_SUMMARY"
fi

if [ $rc != 0 ]; then
	if [ "${BENCHGATE_ALLOW:-}" = true ]; then
		echo "benchgate: regression allowed (BENCHGATE_ALLOW=true)"
		exit 0
	fi
	exit 1
fi
