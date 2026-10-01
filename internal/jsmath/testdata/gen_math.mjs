// Records V8's results for the functions in jsmath, over pseudo-random inputs.
// Usage: node testdata/gen_math.mjs > testdata/math.json
//
// The pow rows must come from node on Linux: V8 leaves Math.pow to the C
// library's pow, and macOS's differs from glibc's (see pow.go).
let s = 12345;
const rnd = () => { s = (s * 1103515245 + 12345) & 0x7fffffff; return s / 0x7fffffff; };
const out = { sin: [], cos: [], atan: [], atan2: [], asin: [], acos: [], pow: [], hypot: [] };
for (let i = 0; i < 1500; i++) {
  const a = (rnd() - 0.5) * (i % 10 === 0 ? 4000 : 20);
  out.sin.push([a, Math.sin(a)]);
  out.cos.push([a, Math.cos(a)]);
  const t = (rnd() - 0.5) * (i % 7 === 0 ? 1e4 : 6);
  out.atan.push([t, Math.atan(t)]);
  const y = (rnd() - 0.5) * 200, x = (rnd() - 0.5) * 200;
  out.atan2.push([y, x, Math.atan2(y, x)]);
  const u = rnd() * 2 - 1;
  out.asin.push([u, Math.asin(u)]);
  out.acos.push([u, Math.acos(u)]);
  const b = rnd() * 1000, e = [0.5, 0.25, 0.75, 0.3, 1.5, 2, 3.7, -0.5, 10][i % 9];
  out.pow.push([b, e, Math.pow(b, e)]);
  out.hypot.push([y, x, Math.hypot(y, x)]);
}
// special values
for (const a of [0, -0, 1, -1, Math.PI, Math.PI / 2, Math.PI / 4, 2 * Math.PI, 1e-10, 1e300, Infinity, NaN, 0.5, -0.5, 0.975, 0.9999999]) {
  out.sin.push([a, Math.sin(a)]);
  out.cos.push([a, Math.cos(a)]);
  out.atan.push([a, Math.atan(a)]);
  out.asin.push([a, Math.asin(a)]);
  out.acos.push([a, Math.acos(a)]);
  for (const b of [0, -0, 1, -1, 2, 0.5, Infinity, -Infinity, NaN, 3]) {
    out.atan2.push([a, b, Math.atan2(a, b)]);
    out.pow.push([a, b, Math.pow(a, b)]);
    out.hypot.push([a, b, Math.hypot(a, b)]);
  }
}
const enc = (v) => (Number.isFinite(v) && !Object.is(v, -0)) ? v : (Object.is(v, -0) ? '-0' : String(v));
const conv = (rows) => rows.map((r) => r.map(enc));
for (const k of Object.keys(out)) out[k] = conv(out[k]);
console.log(JSON.stringify(out));
