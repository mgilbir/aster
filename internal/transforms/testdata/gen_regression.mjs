import { record } from './lib.mjs';

// Deterministic pseudo-noise so the vectors are reproducible.
let seed = 7;
const rnd = () => (seed = (seed * 16807) % 2147483647) / 2147483647;
const pts = [];
for (let i = 0; i < 60; i++) {
  const x = 1 + i * 0.5 + rnd();
  pts.push({ g: i % 2 ? 'a' : 'b', h: i % 3, x, y: 3 + 0.5 * x * x - 2 * x + 4 * (rnd() - 0.5), z: Math.exp(0.1 * x) * (1 + 0.1 * rnd()), w: 2 * Math.pow(x, 1.5) * (1 + 0.05 * rnd()) });
}
const dirty = pts.slice(0, 20).concat([{ g: 'a', x: null, y: 3, z: 1, w: 1 }, { g: 'a', x: 5, y: NaN, z: 1, w: 1 }, { g: 'a', x: 'abc', y: 2, z: 1, w: 1 }, { g: 'a', x: '7', y: '9', z: 2, w: 2 }, { g: 'b', y: 1 }]);
const dupx = [];
for (let i = 0; i < 30; i++) dupx.push({ x: Math.floor(i / 3), y: Math.sin(i / 4) * 3 + (i % 3) });
const small = [{ x: 1, y: 2 }, { x: 2, y: 3 }];
const line = [{ x: 1, y: 2 }, { x: 2, y: 4 }, { x: 3, y: 6 }, { x: 4, y: 8 }];
const flat = [{ x: 2, y: 2 }, { x: 2, y: 4 }, { x: 2, y: 6 }];
const dates = [{ d: { $: 'date', v: 1e12 }, y: 1 }, { d: { $: 'date', v: 1.001e12 }, y: 3 }, { d: { $: 'date', v: 1.002e12 }, y: 2 }, { d: { $: 'date', v: 1.003e12 }, y: 5 }];

const cases = [];
const add = (name, input, transform) => cases.push({ name, input, transform });
for (const [m, yf] of [['linear', 'y'], ['constant', 'y'], ['log', 'y'], ['exp', 'z'], ['pow', 'w'], ['quad', 'y'], ['poly', 'y']]) {
  add(`${m}`, pts, [{ type: 'regression', x: 'x', y: yf, method: m }]);
  add(`${m} groupby`, pts, [{ type: 'regression', x: 'x', y: yf, method: m, groupby: ['g'] }]);
  add(`${m} groupby2 as`, pts, [{ type: 'regression', x: 'x', y: yf, method: m, groupby: ['g', 'h'], as: ['xx', 'yy'] }]);
  add(`${m} extent`, pts, [{ type: 'regression', x: 'x', y: yf, method: m, extent: [2, 20] }]);
  add(`${m} params`, pts, [{ type: 'regression', x: 'x', y: yf, method: m, params: true }]);
  add(`${m} params groupby`, pts, [{ type: 'regression', x: 'x', y: yf, method: m, params: true, groupby: ['g'] }]);
  add(`${m} dirty`, dirty, [{ type: 'regression', x: 'x', y: yf, method: m, groupby: ['g'] }]);
}
for (const o of [0, 1, 2, 3, 4, 5, 7]) {
  add(`poly order ${o}`, pts, [{ type: 'regression', x: 'x', y: 'y', method: 'poly', order: o }]);
  add(`poly order ${o} params`, pts, [{ type: 'regression', x: 'x', y: 'y', method: 'poly', order: o, params: true }]);
}
add('log extent nonpositive', pts, [{ type: 'regression', x: 'x', y: 'y', method: 'log', extent: [0, 10] }]);
add('linear small skipped', small, [{ type: 'regression', x: 'x', y: 'y' }]);
add('linear perfect', line, [{ type: 'regression', x: 'x', y: 'y', params: true }]);
add('linear degenerate x', flat, [{ type: 'regression', x: 'x', y: 'y', params: true }]);
add('quad degenerate x', flat.concat([{ x: 2, y: 9 }]), [{ type: 'regression', x: 'x', y: 'y', method: 'quad', params: true }]);
add('empty', [], [{ type: 'regression', x: 'x', y: 'y' }]);
add('empty groupby', [], [{ type: 'regression', x: 'x', y: 'y', groupby: [] }]);
add('empty groupby list data', line, [{ type: 'regression', x: 'x', y: 'y', groupby: [], params: true }]);
add('dates linear', dates, [{ type: 'regression', x: 'd', y: 'y' }]);
// Non-linear methods over a date extent are meaningless upstream (minX + number concatenates strings), so not recorded.
add('linear nested', [{ p: { x: 1, y: 3 } }, { p: { x: 2, y: 5 } }, { p: { x: 4, y: 9.5 } }], [{ type: 'regression', x: 'p.x', y: 'p.y' }]);

for (const bw of [undefined, 0.1, 0.3, 0.5, 0.9, 1, 1.5, 0]) {
  const t = { type: 'loess', x: 'x', y: 'y' };
  if (bw !== undefined) t.bandwidth = bw;
  add(`loess bw ${bw}`, pts, [t]);
  add(`loess bw ${bw} groupby`, pts, [{ ...t, groupby: ['g'] }]);
  add(`loess bw ${bw} dupx`, dupx, [t]);
}
add('loess as', pts, [{ type: 'loess', x: 'x', y: 'y', groupby: ['g'], as: ['a', 'b'] }]);
add('loess dirty', dirty, [{ type: 'loess', x: 'x', y: 'y', groupby: ['g'] }]);
add('loess one point', [{ x: 1, y: 2 }], [{ type: 'loess', x: 'x', y: 'y' }]);
add('loess two points', small, [{ type: 'loess', x: 'x', y: 'y' }]);
add('loess line', line, [{ type: 'loess', x: 'x', y: 'y', bandwidth: 0.5 }]);
add('loess flat x', flat, [{ type: 'loess', x: 'x', y: 'y' }]);
add('loess empty', [], [{ type: 'loess', x: 'x', y: 'y' }]);
add('loess outlier', pts.slice(0, 30).map((d, i) => (i === 10 ? { ...d, y: 100 } : d)), [{ type: 'loess', x: 'x', y: 'y', bandwidth: 0.4 }]);

console.log(JSON.stringify(await record(cases)));
