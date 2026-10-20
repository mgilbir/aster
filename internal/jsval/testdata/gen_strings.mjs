// Regenerate strings.json.gz with: node testdata/gen_strings.mjs | gzip -n -9 > testdata/strings.json.gz
// (node 24.21.0, the oracle's). It records String.prototype.toLowerCase and
// toUpperCase of every code point whose mapping is not itself, and of the
// contexts the special casings depend on; the code points that are white
// space to String.prototype.trim; and the trim of strings with each kind of
// edge, so the engine's JSLower, JSUpper and IsJSSpace are checked against
// V8 on all of Unicode. It also records, for every code point c, whether the
// sigma of "A" c "Σ", of "AΣ" c and of "AΣ" c "A" lower-cases to a final
// sigma (as ranges), which is what the Cased and Case_Ignorable tables of
// casetables.go decide.
const lower = {}, upper = {}, space = [];
for (let cp = 0; cp <= 0x10ffff; cp++) {
  if (cp >= 0xd800 && cp <= 0xdfff) continue;
  const s = String.fromCodePoint(cp);
  if (s.toLowerCase() !== s) lower[cp] = s.toLowerCase();
  if (s.toUpperCase() !== s) upper[cp] = s.toUpperCase();
  if (cp <= 0xffff && ("a" + s + "a").trim() === "a" + s + "a" && (s + "a").trim() === "a") space.push(cp);
}
const sigma = { before: [], after: [], mid: [] };
const push = (list, c) => { const last = list[list.length - 1]; if (last && last[1] === c - 1) last[1] = c; else list.push([c, c]); };
for (let cp = 0; cp <= 0x10ffff; cp++) {
  if (cp >= 0xd800 && cp <= 0xdfff) continue;
  const s = String.fromCodePoint(cp);
  if (("A" + s + "Σ").toLowerCase().endsWith("ς")) push(sigma.before, cp);
  if (("AΣ" + s).toLowerCase()[1] === "ς") push(sigma.after, cp);
  if (("AΣ" + s + "A").toLowerCase()[1] === "ς") push(sigma.mid, cp);
}
const contexts = [
  "ΣΑΣ", "ΑΣ ΑΣ", "Σ", "ΑΣ.", "Α.Σ", "ΑΣ́", "ΑΣ\ud83d", "\ud83dΣα", "aΣ­b", "QUANTİTATIVE", "quantİtative",
  "Whİte", "LİNEN", "vİridis", "blacK", "ǅ", "ﬀ", "ŉ", "ß", "İstanbul", "İ̇", "a\ud83db", "\udc00Σ",
  "ΌΣΟΣ", "ΑΣΑ", "ὈΔΥΣΣΕΎΣ", "Ǆǅǆ", "ἀΙ", "ᾼ", "ͅ", "ΑΣͅ", "xİy", "ΑΣ\u0345Α", "Α\u0345Σ", "Α\u0345\u00adΣ\u00ad\u0345", "ΑΣ\u00ad.", "ΑΣ\ud83dΑ", "Α\udc00Σ", "Α😀Σ", "Σ\u0345", "Ａ\u3099Σ", "ΑΣΣ", "ΑΣ\u{1d7ce}", "aΣ\u{e0100}b", "ƛ", "Ꞌ", "ꟋΣ",
];
const ctx = contexts.map((s) => ({ s, lower: s.toLowerCase(), upper: s.toUpperCase() }));
const trims = [" a ", "\u0085a\u0085", "﻿a﻿", "᠎a᠎", "​a​", " a ", "　 a\t\n", " a "]
  .map((s) => ({ s, trim: s.trim(), start: s.trimStart(), end: s.trimEnd() }));
process.stdout.write(JSON.stringify({ node: process.version, lower, upper, space, sigma, ctx, trims }));
