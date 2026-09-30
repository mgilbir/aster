// Records V8's Array.prototype.sort permutations for comparators the Go port
// must reproduce exactly, including inconsistent ones.
//
//   node testdata/gen_sort.mjs | gzip -9 > testdata/sort.json.gz
//
// Arrays hold element ids 0..n-1 in a seeded shuffled order; the recorded
// result is the order of ids after sorting.
function lcg(seed) {
  let s = seed >>> 0;
  return () => (s = (Math.imul(s, 1664525) + 1013904223) >>> 0) / 4294967296;
}
// An inconsistent comparator: a deterministic pseudo-random verdict per
// ordered pair, so no total order exists and every merge path is exercised.
const chaos = (x, y) => ((Math.imul(x, 73856093) ^ Math.imul(y, 19349663)) >>> 0) % 3 - 1;
// A consistent comparator with many ties, to check stability.
const ties = (x, y) => (x % 7) - (y % 7);
// JavaScript's < across mixed types, as vega-util's ascending compares
// strings and numbers: not transitive.
const mixedKey = (i) => (i % 3 === 0 ? String((i * 37) % 101) : i % 3 === 1 ? (i * 53) % 97 : ((i * 11) % 89) + 'x');
const mixed = (x, y) => {
  const a = mixedKey(x), b = mixedKey(y);
  return a < b ? -1 : b < a ? 1 : 0;
};
const cases = [];
const lengths = [0, 1, 2, 3, 5, 8, 31, 32, 33, 63, 64, 65, 100, 127, 128, 129, 200, 257, 500, 1000, 2049, 5000];
for (const [name, cmp] of [['chaos', chaos], ['ties', ties], ['mixed', mixed]]) {
  for (const n of lengths) {
    for (const seed of [1, 2, 3]) {
      const r = lcg(seed * 7919 + n);
      const a = Array.from({ length: n }, (_, i) => i);
      for (let i = n - 1; i > 0; i--) {
        const j = Math.floor(r() * (i + 1));
        [a[i], a[j]] = [a[j], a[i]];
      }
      const input = a.slice();
      a.sort(cmp);
      cases.push({ cmp: name, input, output: a });
    }
  }
  // Presorted and reversed inputs take the run-detection paths.
  for (const n of [70, 300, 1500]) {
    const asc = Array.from({ length: n }, (_, i) => i);
    const desc = asc.slice().reverse();
    for (const input of [asc, desc]) cases.push({ cmp: name, input: input.slice(), output: input.slice().sort(cmp) });
  }
}
process.stdout.write(JSON.stringify(cases));
