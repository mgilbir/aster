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
# Every benchmark runs with GOMAXPROCS 1 (-test.cpu 1). With more, B/op of
# BenchmarkScenes moves by about 5% from run to run of one binary (the GC
# runs alongside on other threads, and what sync.Pool holds when a render
# starts depends on when it last ran), which the 5% B/op bar cannot tell
# from a regression; with one it repeats to a few bytes in 8 MB. The engine
# renders on one goroutine (only fetching images runs in parallel), so the
# single-threaded times are the ones to gate.
#
# B/op and allocs/op are not taken from that timed pass. Even at -cpu 1 its
# B/op is not a property of the code: a timed run lasts as long as the
# benchtime does, the collector runs a varying number of times inside it,
# and each cycle empties sync.Pool, so what a render finds in the pools it
# reuses (and so what it allocates) depends on how many cycles came before.
# The samples of BenchmarkScenes/symbols-10000 cluster around values about
# 7 MB apart (42.5 MB, 43.6 MB and 50.8 MB in four runs of one binary), and
# a change of cluster passes for a 16% regression. So each round also runs
# every benchmark with the collector off (GOGC=off) for a fixed number of
# iterations, where the only collections are the ones the testing package
# forces before each run, at the same points on both sides, and the memory
# columns are gated on that pass alone.
#
# A fixed number of iterations N still counts what happens once per run
# (a cache filled on first use, the lazy setup of the first b.Loop
# iteration) divided by N, and for a benchmark whose steady state allocates
# almost nothing that one-time cost is most of what it reports. So the pass
# runs each benchmark twice, at N and at 2N iterations, and records the
# difference: per-iteration bytes are (B/op(2N)*2N - B/op(N)*N)/N =
# 2*B/op(2N) - B/op(N), and allocs/op the same, in which a one-time cost
# cancels exactly. Go truncates B/op and allocs/op to integers, so the
# difference is within (-1, +2) of the true value, an error under 3 per op
# (a negative result is recorded as 0). N is chosen per benchmark from the
# first round's timed pass: about BENCHGATE_MEM_TARGET (default 100ms) of
# work at its ns/op, clamped to [5, 100000], and lowered so that 2N times
# its B/op stays within BENCHGATE_MEM_CAP (default 1 GiB). Memory is
# bounded: the forced collection before each run frees the previous one, and
# GOMEMLIMIT (BENCHGATE_MEMLIMIT, default 4GiB) caps the process. A
# collection that the limit triggers would make the pass inexact again, so
# the pass runs with GODEBUG=gctrace=1 and any collection not marked
# "(forced)" fails the gate (exit 2) instead of measuring noise. The time
# columns (sec/op) are gated on the timed pass; its B/op is shown in its
# table but not gated.
#
# A memory regression also needs an absolute increase: at least
# BENCHGATE_BYTES_FLOOR (default 64) B/op or BENCHGATE_ALLOCS_FLOOR (default
# 3) allocs/op, besides the percentage. On a value near zero a percentage is
# noise: 3 bytes more of 5 is +60%. Each side's difference is off by less
# than 3 from its true value, and by different amounts on the two sides
# (where truncation lands depends on each binary's one-time cost), so an
# increase under 3 can be truncation alone: BenchmarkBin100k, whose true
# rate lies between 1 and 2 allocs/op, measured 1 on one side and 2 on the
# other of an unchanged loop. An added allocation of 64 bytes or more is
# still caught, through its bytes.
#
# For an intended trade-off, BENCHGATE_ALLOW=true reports the regression and
# exits 0 (CI sets it from the allow_regression input of the Heavy gates
# workflow). Other settings: BENCHGATE_COUNT (rounds, default 8),
# BENCHGATE_BENCHTIME (default 300ms), BENCHGATE_MEM_TARGET, BENCHGATE_MEM_CAP,
# BENCHGATE_MEMLIMIT, BENCHGATE_NS_PCT, BENCHGATE_BYTES_PCT,
# BENCHGATE_ALLOCS_PCT, BENCHGATE_BYTES_FLOOR, BENCHGATE_ALLOCS_FLOOR,
# BENCHGATE_OUT
# (a directory to keep the samples and the benchstat output in) and BENCHSTAT
# (a benchstat binary, instead of installing the pinned version).
set -eu
cd "$(dirname "$0")/.."

count=${BENCHGATE_COUNT:-8}
benchtime=${BENCHGATE_BENCHTIME:-300ms}
mem_target=${BENCHGATE_MEM_TARGET:-100ms}
mem_cap=${BENCHGATE_MEM_CAP:-1073741824}
memlimit=${BENCHGATE_MEMLIMIT:-4GiB}
bytes_floor=${BENCHGATE_BYTES_FLOOR:-64}
allocs_floor=${BENCHGATE_ALLOCS_FLOOR:-3}
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
	(cd "$work/$1/$3" && "$work/$1.$2.test" -test.run '^$' -test.bench "$4" -test.benchmem -test.cpu 1 \
		-test.benchtime "$benchtime" -test.count 1 -test.timeout 30m) >"$work/timed.out"
	cat "$work/timed.out" >>"$work/$1.txt"
	cat "$work/timed.out" >>"$work/$1.$2.timed"
}

# The nanoseconds of BENCHGATE_MEM_TARGET (a Go duration in ns, us, ms or s).
mem_target_ns=$(printf '%s\n' "$mem_target" | awk '
/^[0-9.]+ns$/ { print int($0 + 0); exit }
/^[0-9.]+(us|µs)$/ { print int($0 * 1e3); exit }
/^[0-9.]+ms$/ { print int($0 * 1e6); exit }
/^[0-9.]+s$/ { print int($0 * 1e9); exit }
{ exit 1 }') || { echo "benchgate: BENCHGATE_MEM_TARGET $mem_target is not a duration" >&2; exit 2; }

plan() { # n: "name N" for each benchmark of entry n, from both sides' timed runs
	awk -v target="$mem_target_ns" -v cap="$mem_cap" '
	$1 ~ /^Benchmark/ {
		ns = -1; by = 0
		for (i = 3; i < NF; i += 2) {
			if ($(i + 1) == "ns/op") ns = $i + 0
			else if ($(i + 1) == "B/op") by = $i + 0
		}
		if (ns < 0) next
		if (!($1 in maxns)) order[++k] = $1
		if (ns > maxns[$1]) maxns[$1] = ns
		if (by > maxby[$1]) maxby[$1] = by
	}
	END {
		for (j = 1; j <= k; j++) {
			b = order[j]
			n = maxns[b] > 0 ? int(target / maxns[b]) : 100000
			if (n < 5) n = 5
			if (n > 100000) n = 100000
			if (maxby[b] > 0 && 2 * n * maxby[b] > cap) n = int(cap / (2 * maxby[b]))
			if (n < 1) n = 1
			print b, n
		}
	}' "$work/base.$1.timed" "$work/head.$1.timed"
}

benchregex() { # name: a -bench regex matching that benchmark and no other
	printf '%s\n' "$1" | awk -F/ '{
		out = ""
		for (i = 1; i <= NF; i++) {
			e = $i
			gsub(/[][\\.^$*+?(){}|]/, "\\\\&", e)
			out = out (i > 1 ? "/" : "") "^" e "$"
		}
		print out
	}'
}

memone() { # side n pkg regex iterations: one run with the collector off, into mem.out
	if ! (cd "$work/$1/$3" && GOGC=off GOMEMLIMIT="$memlimit" GODEBUG=gctrace=1 "$work/$1.$2.test" \
		-test.run '^$' -test.bench "$4" -test.benchmem -test.cpu 1 -test.benchtime "$5x" \
		-test.count 1 -test.timeout 30m >"$work/mem.out" 2>"$work/gc.log"); then
		grep -v '^gc ' "$work/gc.log" >&2 || true
		echo "benchgate: $3 ($4): the memory pass failed on the $1 side" >&2
		exit 2
	fi
	if grep '^gc ' "$work/gc.log" | grep -qv '(forced)'; then
		echo "benchgate: $3 ($4): the collector ran by itself in the memory pass on the $1 side" \
			"(GOMEMLIMIT $memlimit reached), so its B/op is not exact; lower BENCHGATE_MEM_CAP or raise BENCHGATE_MEMLIMIT" >&2
		exit 2
	fi
}

memof() { # name: "B/op allocs/op" of name in mem.out, or nothing
	awk -v b="$1" '$1 == b {
		by = ""; al = ""
		for (i = 3; i < NF; i += 2) {
			if ($(i + 1) == "B/op") by = $i
			else if ($(i + 1) == "allocs/op") al = $i
		}
		if (by != "" && al != "") print by, al
		exit
	}' "$work/mem.out"
}

memrun() { # side n pkg: the two-point memory pass of each benchmark of entry n
	grep -m 1 '^pkg: ' "$work/$1.$2.timed" >>"$work/$1.mem.txt" || true
	while read -r name iters; do
		re=$(benchregex "$name")
		memone "$1" "$2" "$3" "$re" "$iters"
		one=$(memof "$name")
		memone "$1" "$2" "$3" "$re" $((2 * iters))
		two=$(memof "$name")
		# a benchmark the side does not have (new, or removed) has no line
		[ -n "$one" ] && [ -n "$two" ] || continue
		set -- "$1" "$2" "$3" $one $two
		by=$((2 * $6 - $4))
		al=$((2 * $7 - $5))
		[ "$by" -ge 0 ] || by=0
		[ "$al" -ge 0 ] || al=0
		printf '%s %s %s B/op %s allocs/op\n' "$name" "$iters" "$by" "$al" >>"$work/$1.mem.txt"
		set -- "$1" "$2" "$3"
	done <"$work/plan.$2"
}

round=0
while [ "$round" -lt "$count" ]; do
	round=$((round + 1))
	printf 'benchgate: round %s/%s\n' "$round" "$count"
	while read -r n pkg regex; do
		if [ $(((round + n) % 2)) = 0 ]; then
			run base "$n" "$pkg" "$regex"
			run head "$n" "$pkg" "$regex"
			[ "$round" -gt 1 ] || plan "$n" >"$work/plan.$n"
			memrun base "$n" "$pkg"
			memrun head "$n" "$pkg"
		else
			run head "$n" "$pkg" "$regex"
			run base "$n" "$pkg" "$regex"
			[ "$round" -gt 1 ] || plan "$n" >"$work/plan.$n"
			memrun head "$n" "$pkg"
			memrun base "$n" "$pkg"
		fi
	done <"$entries"
done

# The time tables come from the timed pass, the memory tables from the
# two-point one.
(cd "$work" && "$benchstat" -format csv base.txt head.txt >stat.csv 2>stat.err &&
	"$benchstat" base.txt head.txt >stat.txt 2>>stat.err &&
	"$benchstat" -format csv base.mem.txt head.mem.txt >mem.csv 2>>stat.err &&
	"$benchstat" base.mem.txt head.mem.txt >mem.txt 2>>stat.err) || { cat "$work/stat.err" >&2; exit 1; }
echo "benchgate: time (timed pass; its B/op is not gated)"
cat "$work/stat.txt"
echo "benchgate: memory (steady per-iteration, from runs of N and 2N iterations with GOGC=off)"
cat "$work/mem.txt"

# The CSV has, per table, a "pkg:" line, a header with the unit and then a row
# per benchmark whose "vs base" cell is "~" unless p < 0.05, else the change.
# sec/op is gated from the timed pass (stat.csv), B/op and allocs/op from the
# memory pass (mem.csv), where an increase must also reach its floor.
gate() { # csv pass
	awk -F, -v ns="$ns_pct" -v by="$bytes_pct" -v al="$allocs_pct" -v pass="$2" \
		-v byfloor="$bytes_floor" -v alfloor="$allocs_floor" '
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
	if (pass == "mem" && unit == "allocs/op") limit = al
	else if (pass == "mem" && unit == "B/op") limit = by
	else if (pass == "time" && unit == "sec/op") limit = ns # not the stages (json-sec/op, ...): too short to be steady
	if (limit < 0) next
	pct = $6
	sub(/%$/, "", pct)
	# "?" is a significant change from zero (no ratio): any increase is over
	if ($6 == "?") over = ($4 + 0 > $2 + 0)
	else over = (pct ~ /Inf/) ? (pct ~ /^\+/) : (pct + 0 > limit)
	if (over && pass == "mem") over = ($4 - $2 >= (unit == "B/op" ? byfloor : alfloor))
	if (over) {
		bad++
		printf "REGRESSION %s %s %s %s (limit +%s%%, %s)\n", (pkg == "" ? "." : pkg), $1, unit, $6, limit, $7
	}
}
END { exit bad > 0 ? 1 : 0 }
' "$1"
}
rc=0
: >"$work/verdict.txt"
for pass in time mem; do
	csv=$work/stat.csv
	[ "$pass" = time ] || csv=$work/mem.csv
	gate "$csv" "$pass" >>"$work/verdict.txt" && r=0 || r=$?
	[ "$r" -le 1 ] || { echo "benchgate: cannot parse benchstat's CSV" >&2; exit 2; }
	[ "$r" = 0 ] || rc=1
done

if [ -n "${BENCHGATE_OUT:-}" ]; then
	mkdir -p "$BENCHGATE_OUT"
	cp "$work/base.txt" "$work/head.txt" "$work/stat.csv" "$work/stat.txt" \
		"$work/base.mem.txt" "$work/head.mem.txt" "$work/mem.csv" "$work/mem.txt" "$BENCHGATE_OUT"
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
		printf '<details><summary>benchstat, time</summary>\n\n```\n%s\n```\n</details>\n' "$(cat "$work/stat.txt")"
		printf '<details><summary>benchstat, memory</summary>\n\n```\n%s\n```\n</details>\n' "$(cat "$work/mem.txt")"
	} >>"$GITHUB_STEP_SUMMARY"
fi

if [ $rc != 0 ]; then
	if [ "${BENCHGATE_ALLOW:-}" = true ]; then
		echo "benchgate: regression allowed (BENCHGATE_ALLOW=true)"
		exit 0
	fi
	exit 1
fi
