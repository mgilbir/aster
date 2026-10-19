// Records Date.parse of strings that probe V8's date tokenizer at its
// edges (C123 of the 2026-10-18 audit): which characters are white space
// (ECMA-262's WhiteSpace and LineTerminator, not U+0085), and that a NUL
// ends the input wherever it is.
//   TZ=America/New_York node testdata/gen_dateparse.mjs
const spaces = [0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x180e, 0x2000, 0x200a, 0x200b, 0x2028, 0x2029,
  0x202f, 0x205f, 0x3000, 0xfeff];
const strs = [];
for (const c of spaces) {
  const s = String.fromCharCode(c);
  strs.push(`Jan${s}1 2000`, `2000-01-01${s}`, `${s}2000-01-01`, `Jan 1 2000${s}10:00`, `1/2/2000${s}`);
}
for (const s of ['Jan 1 2000 (a\0b) 10:00', 'Jan 1 2000 (\0', 'Jan 1\0 2000', '\0Jan 1 2000', 'Jan 1 2000\0', 'Jan 1 2000 10:00\0:30',
  '2000-01-01T10:00\0:00Z', '2000-01-01\0T10:00Z', 'Jan 1 2000 10:00 GMT+05\0', 'Ja\0n 1 2000', 'Jan 1 2000 10:00 P\0M']) {
  strs.push(s);
}
process.stdout.write(JSON.stringify({ tz: process.env.TZ, cases: strs.map((s) => [s, Date.parse(s)]) }) + '\n');
