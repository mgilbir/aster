// Writes casetables.go: what String.prototype.toLowerCase and toUpperCase do
// to every code point, as node (V8, with ICU's root-locale full case mapping)
// does it: the one-to-one mappings as runs, the one-to-many mappings, and the
// Cased and Case_Ignorable sets that decide Final_Sigma. The tables are the
// pinned node's, so the engine's answers do not depend on the Unicode
// version of the Go toolchain that builds it (Go 1.26 has Unicode 15,
// Go 1.27 Unicode 17, node 24.21.0 Unicode 17).
//
// The script also checks the assumption the tables rest on, and fails if
// node breaks it: apart from U+03A3 in toLowerCase, a code point's mapping
// does not depend on its neighbours, and Final_Sigma is decided by Cased and
// Case_Ignorable as the Unicode standard defines it.
//
//   go run ./internal/cmd/vectors -write -run casetables   (the output is gofmt-clean as written)
const SIGMA = 0x3a3;
const isSurrogate = (c) => c >= 0xd800 && c <= 0xdfff;
const hex = (c) => '0x' + c.toString(16).toUpperCase().padStart(4, '0');
const esc = (s) => [...s].map((ch) => {
  const c = ch.codePointAt(0);
  return c > 0xffff ? '\\U' + c.toString(16).padStart(8, '0') : '\\u' + c.toString(16).padStart(4, '0');
}).join('');

// Mappings, and the check that nothing but the final sigma looks at a neighbour.
const neighbours = ['a', 'A', '1', ' ', '̇', 'ͅ', '­'];
const maps = { lower: [], upper: [] };
for (let c = 0; c <= 0x10ffff; c++) {
  if (isSurrogate(c)) continue;
  const s = String.fromCodePoint(c);
  for (const [name, fn] of [['lower', (x) => x.toLowerCase()], ['upper', (x) => x.toUpperCase()]]) {
    const m = fn(s);
    if (m !== s) maps[name].push([c, m]);
    if (name === 'lower' && c === SIGMA) continue;
    for (const n of neighbours) {
      if (fn(n + s) !== fn(n) + m || fn(s + n) !== m + fn(n)) throw new Error(`${name}case of U+${c.toString(16)} depends on its neighbour ${JSON.stringify(n)}`);
    }
  }
}

// Cased and Case_Ignorable, and the check that V8's Final_Sigma is the
// standard's, for every code point on either side of a sigma.
const ranges = (re) => {
  const out = [];
  for (let c = 0; c <= 0x10ffff; c++) {
    if (isSurrogate(c) || !re.test(String.fromCodePoint(c))) continue;
    const last = out[out.length - 1];
    if (last && last[1] === c - 1) last[1] = c; else out.push([c, c]);
  }
  return out;
};
const cased = ranges(/\p{Cased}/u), ignorable = ranges(/\p{Case_Ignorable}/u);
const inSet = (set) => (c) => { let lo = 0, hi = set.length; while (lo < hi) { const m = (lo + hi) >> 1; if (set[m][1] < c) lo = m + 1; else hi = m; } return lo < set.length && set[lo][0] <= c; };
const isCased = inSet(cased), isIgn = inSet(ignorable);
// finalAt1 asks about the sigma at index 1 (after the A), finalLast about the last character.
const finalAt1 = (str) => str.toLowerCase()[1] === 'ς';
const finalLast = (str) => str.toLowerCase().endsWith('ς');
for (let c = 0; c <= 0x10ffff; c++) {
  if (isSurrogate(c)) continue;
  const s = String.fromCodePoint(c);
  const cs = isCased(c), ig = isIgn(c);
  // Before Σ a character that is cased or case-ignorable (the latter skipped,
  // the A beyond it cased) makes a word; after it, one that is case-ignorable
  // is skipped to the end of the string, a cased one means not final, and
  // anything else ends the word; and with an A beyond, a case-ignorable one
  // is skipped to a cased letter.
  if (finalLast('A' + s + 'Σ') !== (cs || ig)) throw new Error(`Final_Sigma before U+${c.toString(16)} disagrees with Cased and Case_Ignorable`);
  if (finalAt1('AΣ' + s) !== !(cs && !ig)) throw new Error(`Final_Sigma after U+${c.toString(16)} disagrees with Cased and Case_Ignorable`);
  if (finalAt1('AΣ' + s + 'A') !== (!cs && !ig)) throw new Error(`Final_Sigma between U+${c.toString(16)} and A disagrees with Cased and Case_Ignorable`);
}
if (!finalAt1('AΣ') || finalAt1('AΣA') || finalLast('Σ') || !finalLast('AΣ')) throw new Error('Final_Sigma basics changed');
if ('Σ'.toLowerCase() !== 'σ') throw new Error('U+03A3 lower-cases to something other than σ');

// Runs of one-to-one mappings: code points step apart by 1 or 2 with the same
// delta; the one-to-many mappings are listed apart.
const split = (list) => {
  const runs = [], special = [];
  for (const [c, m] of list) {
    const cps = [...m];
    if (cps.length !== 1) { special.push([c, m]); continue; }
    const delta = cps[0].codePointAt(0) - c;
    const r = runs[runs.length - 1];
    if (r) {
      const gap = c - r.hi;
      if (r.delta === delta && ((r.n === 1 && (gap === 1 || gap === 2)) || gap === r.step)) {
        if (r.n === 1) r.step = gap;
        r.hi = c; r.n++;
        continue;
      }
    }
    runs.push({ lo: c, hi: c, step: 1, delta, n: 1 });
  }
  return { runs, special };
};
const runTable = (name, runs) => `var ${name} = []caseRun{\n` + runs.map((r) => `\t{${hex(r.lo)}, ${hex(r.hi)}, ${r.step}, ${r.delta}},\n`).join('') + '}\n';
// index[b] is the number of entries that start below code point (b+1)*256, so
// the entries that can hold a code point of block b are those from index[b-1]
// (or 0) to index[b], plus the one before them, which may reach into it (a
// run; a one-to-many mapping holds only its own code point).
const blockIndex = (name, starts) => {
  const n = (starts[starts.length - 1] >> 8) + 1;
  const idx = [];
  for (let b = 0, k = 0; b < n; b++) {
    while (k < starts.length && starts[k] < (b + 1) * 256) k++;
    idx.push(k);
  }
  const rows = [];
  for (let i = 0; i < idx.length; i += 16) rows.push('\t' + idx.slice(i, i + 16).join(', ') + ',\n');
  return `var ${name} = []uint16{\n` + rows.join('') + '}\n';
};
const specialTable = (name, sp) => `var ${name} = []caseSpecial{\n` + sp.map(([c, m]) => `\t{${hex(c)}, "${esc(m)}"},\n`).join('') + '}\n';
const rangeTable = (name, rs) => `var ${name} = [][2]rune{\n` + rs.map(([a, b]) => `\t{${hex(a)}, ${hex(b)}},\n`).join('') + '}\n';
const lower = split(maps.lower), upper = split(maps.upper);
process.stdout.write(`// Code generated by testdata/gen_casetables.mjs from node ${process.version} (Unicode ${process.versions.unicode}); DO NOT EDIT.

package jsval

// caseUnicodeVersion is the Unicode version of the node the tables below were
// recorded from. The Go toolchain's own tables do not enter into it.
const caseUnicodeVersion = "${process.versions.unicode}"

// lowerRuns and upperRuns are the one-to-one mappings of
// String.prototype.toLowerCase and toUpperCase: every code point from lo to hi
// in steps of step maps to itself plus delta. lowerSpecial and upperSpecial
// are the one-to-many mappings (U+0130 lower-cases to "i" and a combining dot
// above; ß upper-cases to "SS"). The *Index tables say where in a table to
// look for a code point: see lookupRun. All are sorted by code point and do not
// overlap. U+03A3's lower-case, σ, is here; its final form is a rule of the
// context (casedRanges and caseIgnorableRanges, the Unicode properties
// Cased and Case_Ignorable as inclusive [lo, hi] pairs,).

${runTable('lowerRuns', lower.runs)}
${blockIndex('lowerRunIndex', lower.runs.map((r) => r.lo))}
${specialTable('lowerSpecial', lower.special)}
${blockIndex('lowerSpecialIndex', lower.special.map(([c]) => c))}
${runTable('upperRuns', upper.runs)}
${blockIndex('upperRunIndex', upper.runs.map((r) => r.lo))}
${specialTable('upperSpecial', upper.special)}
${blockIndex('upperSpecialIndex', upper.special.map(([c]) => c))}
${rangeTable('casedRanges', cased)}
${rangeTable('caseIgnorableRanges', ignorable)}`);
