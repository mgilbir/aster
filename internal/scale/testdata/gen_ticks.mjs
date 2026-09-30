// Records d3-array ticks / tickIncrement / tickStep / nice for many inputs.
// Run: NODE_PATH=<node_modules with d3-array> node gen_ticks.mjs | gzip -9 > ticks.json.gz
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const d3 = await import(require.resolve('d3-array'));

function mulberry32(a) {
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rnd = mulberry32(12345);
const enc = (x) => (Number.isNaN(x) ? 'NaN' : x === Infinity ? 'Inf' : x === -Infinity ? '-Inf' : x);

const edges = [0, 1, -1, 10, 100, 0.1, 0.01, 0.001, 1e-7, 1e-12, 1e7, 1e15, 1e21, -5, -0.5, 3.7, 12345.678, 0.3, 2.5, 1000, 7, 99.99, 0.9999, 1e-3 * 3, 25, 365, 86400000];
const counts = [-1, 0, 0.3, 0.5, 0.99, 1, 1.5, 2, 3, 4, 5, 7, 10, 20, 50, 100];
const cases = [];
function add(a, b, c) {
  cases.push({ a: enc(a), b: enc(b), c: enc(c), ticks: d3.ticks(a, b, c).map(enc),
    inc: enc(d3.tickIncrement(a, b, c)), step: enc(d3.tickStep(a, b, c)), nice: d3.nice(a, b, c).map(enc) });
}
for (const a of edges) for (const b of edges) {
  for (const c of counts) {
    if (Math.abs(a - b) > 0 && rnd() < 0.18) add(a, b, c);
  }
}
for (const c of [10, 5, 1, 0.5]) for (const [a, b] of [[NaN, 1], [0, NaN], [-Infinity, 1], [0, Infinity], [NaN, NaN], [Infinity, Infinity], [3, 3], [0, 0]]) add(a, b, c);
add(0, 1, NaN); add(0, 1, Infinity);
for (let i = 0; i < 2500; i++) {
  const mag = Math.pow(10, Math.floor(rnd() * 14) - 7);
  const a = (rnd() - 0.5) * mag * (rnd() < 0.3 ? 1 : 100);
  const b = a + (rnd() < 0.15 ? -1 : 1) * rnd() * mag * (rnd() < 0.3 ? 1 : 100);
  const c = Math.max(1, Math.floor(rnd() * 40));
  add(a, b, c);
}
console.log(JSON.stringify(cases));
