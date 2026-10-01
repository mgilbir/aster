#!/usr/bin/env bash
# Fetches the two external corpora the corpus sweeps compare with the node
# oracle, each at a pinned commit, into testdata/corpora-cache/ (ignored by
# version control). Nothing fetched is committed: what is checked in is
# testdata/corpora/*.sha256, the sha256 of every extracted file, and a cache
# that does not match its manifest is refused, so a corpus cannot drift.
#
#   wild   hyungkwonko/chart-llm, docs/data/chart: Vega-Lite specifications
#          collected from public repositories (the benchmark/ directories are
#          left out on purpose: they duplicate Vega-Lite's own examples).
#   deneb  avatorl/Deneb-Vega-Templates: Vega templates for Deneb.
#
# Usage: scripts/fetch-corpora.sh [--update-manifest] [wild|deneb ...]
#
# --update-manifest rewrites the manifests from what was fetched; use it only
# when a pin is deliberately changed (a missing manifest is an error without
# it). See testdata/corpora/README.md.
set -euo pipefail
cd "$(dirname "$0")/.."

WILD_COMMIT="6e3d3e2bf1c30aa6df7b916289f42a6ed7721a24"
WILD_REPO="hyungkwonko/chart-llm"
DENEB_COMMIT="2f4a29c1af6556ab0e58e3584a7b7dc54073bfae"
DENEB_REPO="avatorl/Deneb-Vega-Templates"

CACHE="testdata/corpora-cache"
MANIFESTS="testdata/corpora"

update=false
names=()
for arg in "$@"; do
  case "$arg" in
    --update-manifest) update=true ;;
    wild | deneb) names+=("$arg") ;;
    *) echo "usage: $0 [--update-manifest] [wild|deneb ...]" >&2; exit 2 ;;
  esac
done
[[ ${#names[@]} -gt 0 ]] || names=(wild deneb)

# sha256 of every file under $1, as "<hash>  <relative path>", in a stable order.
manifest_of() {
  (cd "$1" && find . -type f ! -name .commit | LC_ALL=C sort | while IFS= read -r f; do
    shasum -a 256 "$f"
  done | sed 's|  \./|  |')
}

# fetch NAME REPO COMMIT DIR: downloads the pinned archive and copies the
# *.json files under DIR of the archive into $CACHE/NAME, with the licence.
fetch() {
  local name=$1 repo=$2 commit=$3 sub=$4
  local dir="$CACHE/$name" manifest="$MANIFESTS/$name.sha256"
  if [[ -f $dir/.commit && "$(cat "$dir/.commit")" == "$commit" && -f $manifest ]] \
    && [[ "$(manifest_of "$dir")" == "$(cat "$manifest")" ]]; then
    echo "==> $name: cache matches its manifest ($(wc -l <"$manifest" | tr -d ' ') files)"
    return
  fi
  echo "==> $name: fetching $repo at ${commit:0:8}"
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  curl -fsSL "https://codeload.github.com/$repo/tar.gz/$commit" | tar -xz -C "$tmp" --strip-components=1
  rm -rf "$dir"
  mkdir -p "$dir"
  (cd "$tmp" && find "$sub" -type f -name '*.json' | LC_ALL=C sort) | while IFS= read -r f; do
    mkdir -p "$dir/$(dirname "$f")"
    cp "$tmp/$f" "$dir/$f"
  done
  for lic in LICENSE LICENSE.md LICENSE.txt; do
    if [[ -f $tmp/$lic ]]; then cp "$tmp/$lic" "$dir/$lic"; fi
  done
  local got
  got="$(manifest_of "$dir")"
  if [[ $update == true ]]; then
    mkdir -p "$MANIFESTS"
    printf '%s\n' "$got" >"$manifest"
    echo "==> $name: wrote $manifest"
  elif [[ ! -f $manifest ]]; then
    rm -rf "$dir"
    echo "$manifest is missing; run with --update-manifest to create it" >&2
    exit 1
  elif [[ "$got" != "$(cat "$manifest")" ]]; then
    diff <(printf '%s\n' "$got") "$manifest" | head -20 >&2 || true
    rm -rf "$dir"
    echo "$name: the fetched files do not match $manifest" >&2
    exit 1
  fi
  echo "$commit" >"$dir/.commit"
  echo "==> $name: $(wc -l <"$manifest" | tr -d ' ') files verified"
}

for n in "${names[@]}"; do
  case "$n" in
    wild) fetch wild "$WILD_REPO" "$WILD_COMMIT" docs/data/chart ;;
    deneb) fetch deneb "$DENEB_REPO" "$DENEB_COMMIT" . ;;
  esac
done
