// Regenerate numbers.json with: node testdata/gen_numbers.js > testdata/numbers.json
const vals = [0, -0, 1, -1, 0.1, 0.5, 1.5, 2.5, -2.5, 1.005, 1.45, 123.456, 1e21, 1e-7, 1.23e-7,
  123e20, 5e-324, 1.7976931348623157e308, 0.000001, 0.0000012, 99.995, 0.045, 1234.5678, -0.0005,
  9.995, 0.9999999, 42, 1 / 3, 2 / 3, 1e100, 12345678901234567890, 0.1 + 0.2, 255, 1e-10, 4.35, 1.255,
  -1e-7, 0.00001, 999.9999, 1e20, 123456789012345680000];
const numbers = vals.map((v) => {
  const r = { v: Object.is(v, -0) ? "-0" : String(v), s: String(v), fixed: {}, prec: {}, exp: {} };
  for (const d of [0, 1, 2, 3, 5, 10, 20]) r.fixed[d] = v.toFixed(d);
  for (const p of [1, 2, 3, 5, 10, 21]) r.prec[p] = v.toPrecision(p);
  for (const d of [-1, 0, 1, 2, 5, 15]) r.exp[d] = d < 0 ? v.toExponential() : v.toExponential(d);
  return r;
});
const tonumber = {};
for (const s of ["", " 12 ", "0x1f", "1e3", ".5", "5.", ".", "abc", "Infinity", "-Infinity", "1_0",
  "0b101", "+3", "-0x10", "1e", " 1", "0o17", "1e400", "\t-2.5e-3\n", "0x", "00012"])
  tonumber[s] = String(Number(s));
console.log(JSON.stringify({ numbers, tonumber }, null, 1));
