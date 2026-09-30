// Generates bin_stats.json (pure functions of vega-statistics) and
// bin_transforms.json (bin, dotbin, density, kde, quantile transforms).
// NODE_PATH=... node gen_stats_b.mjs
import fs from 'node:fs';
import { vega, record, encode } from './lib.mjs';

// ---------- bin() ----------
const binCases = [];
const exts = [[0, 100], [0, 1], [-5, 5], [3, 3], [0, 0], [-7.5, 92.3], [1e-4, 3.3e-4], [0, 1e6], [123, 98765], [-1000, -10], [5, 6.0001], [0.1, 0.7]];
for (const extent of exts) {
  for (const extra of [{}, { maxbins: 5 }, { maxbins: 50 }, { maxbins: 10, nice: false }, { step: 3 }, { steps: [1, 2, 5, 10], maxbins: 8 },
    { minstep: 7 }, { base: 2, maxbins: 16 }, { divide: [3, 2] }, { divide: [] }, { span: 40 }, { steps: [] }, { step: 0.25, nice: false }]) {
    const cfg = { extent, ...extra };
    binCases.push({ cfg, out: encode(vega.bin(cfg)) });
  }
}

// ---------- distributions ----------
const distCases = [];
const grid = (a, b, n) => Array.from({ length: n }, (_, i) => a + (b - a) * i / (n - 1));
const ps = [-0.1, 0, 1e-9, 0.001, 0.025, 0.1, 0.25, 0.5, 0.75, 0.9, 0.975, 0.999, 1 - 1e-9, 1, 1.1];
function dcase(name, spec, d, xs, opts = {}) {
  const c = { name, spec, xs, pdf: xs.map(x => d.pdf(x)), cdf: xs.map(x => d.cdf(x)) };
  if (!opts.noICDF) { c.ps = ps; c.icdf = ps.map(p => d.icdf(p)); }
  distCases.push(encode(c));
  return c;
}
const wide = grid(-12, 12, 49).concat([NaN, Infinity, -Infinity, 40, -40, 0]);
dcase('normal std', { kind: 'normal' }, vega.randomNormal(), wide);
dcase('normal 3,2', { kind: 'normal', mean: 3, stdev: 2 }, vega.randomNormal(3, 2), wide);
dcase('normal 0 stdev0.1', { kind: 'normal', mean: 0, stdev: 0.1 }, vega.randomNormal(0, 0.1), wide);
dcase('lognormal', { kind: 'lognormal' }, vega.randomLogNormal(), grid(-1, 20, 43));
dcase('lognormal 1,0.5', { kind: 'lognormal', mean: 1, stdev: 0.5 }, vega.randomLogNormal(1, 0.5), grid(-1, 20, 43));
dcase('uniform default', { kind: 'uniform' }, vega.randomUniform(), grid(-0.5, 1.5, 21));
dcase('uniform 2,5', { kind: 'uniform', min: 2, max: 5 }, vega.randomUniform(2, 5), grid(0, 7, 29));
const kdeData = [1, 2, 2.5, 3, 7, 8, 8.2, 9, null, 'x', 10, 12.5];
const kdeVals = kdeData.filter(x => x !== 'x');
{
  const d = vega.randomKDE(kdeVals, 0);
  distCases.push(encode({ name: 'kde est', spec: { kind: 'kde', data: kdeVals, bandwidth: 0 }, bandwidth: d.bandwidth(), xs: grid(-5, 20, 51), pdf: grid(-5, 20, 51).map(d.pdf), cdf: grid(-5, 20, 51).map(d.cdf) }));
  const e = vega.randomKDE(kdeVals, 1.5);
  distCases.push(encode({ name: 'kde bw1.5', spec: { kind: 'kde', data: kdeVals, bandwidth: 1.5 }, bandwidth: e.bandwidth(), xs: grid(-5, 20, 51), pdf: grid(-5, 20, 51).map(e.pdf), cdf: grid(-5, 20, 51).map(e.cdf) }));
}
{
  const mk = () => vega.randomMixture([vega.randomNormal(0, 1), vega.randomNormal(5, 2), vega.randomUniform(-2, 2)], [1, 2, null]);
  dcase('mixture', { kind: 'mixture', parts: [{ kind: 'normal', mean: 0, stdev: 1 }, { kind: 'normal', mean: 5, stdev: 2 }, { kind: 'uniform', min: -2, max: 2 }], weights: [1, 2, null] }, mk(), grid(-8, 14, 45), { noICDF: true });
}
// integer
{
  const d = vega.randomInteger(2, 8);
  const xs = grid(0, 10, 21);
  distCases.push(encode({ name: 'integer', spec: { kind: 'integer', min: 2, max: 8 }, xs, pdf: xs.map(d.pdf), cdf: xs.map(d.cdf), ps, icdf: ps.map(d.icdf) }));
  const d2 = vega.randomInteger(5);
  distCases.push(encode({ name: 'integer max only', spec: { kind: 'integer', min: 0, max: 5 }, xs, pdf: xs.map(d2.pdf), cdf: xs.map(d2.cdf), ps, icdf: ps.map(d2.icdf) }));
}

// ---------- bandwidth ----------
const bwCases = [[1, 2, 3, 4, 5], [1], [], [5, 5, 5, 5], [1, 2, 3, 4, 100], [null, 2, 4, 'a', 8], [0, 0, 0, 1], [-3, -3, -3.5, 10]]
  .map(a => encode({ data: a, bw: vega.bandwidthNRD(a) }));

// ---------- sampleCurve ----------
const curveCases = [];
for (const [ext, mn, mx] of [[[-4, 4], undefined, undefined], [[-4, 4], 10, 10], [[-4, 4], 25, 200], [[-4, 4], 5, 20], [[0, 1], 3, 3], [[2, 2], 5, 8], [[-6, 6], 50, 100]]) {
  const f = vega.randomNormal(0, 1).pdf;
  const pts = vega.sampleCurve(f, ext, mn, mx);
  curveCases.push(encode({ name: `normal pdf ${ext} ${mn} ${mx}`, extent: ext, min: mn, max: mx, pts }));
}
{
  const f = vega.randomMixture([vega.randomNormal(-2, 0.4), vega.randomNormal(2, 0.4)]).pdf;
  curveCases.push(encode({ name: 'bimodal', kind: 'mixture2', extent: [-5, 5], min: 25, max: 200, pts: vega.sampleCurve(f, [-5, 5], 25, 200) }));
  const g = vega.randomUniform(0, 1).pdf;
  curveCases.push(encode({ name: 'uniform step', kind: 'uniform01', extent: [-1, 2], min: 25, max: 200, pts: vega.sampleCurve(g, [-1, 2], 25, 200) }));
}

// ---------- seeded sampling ----------
const sampleCases = [];
function scase(name, spec, mk, n, seed) {
  vega.setRandom(vega.randomLCG(seed));
  const d = mk();
  sampleCases.push(encode({ name, spec, seed, samples: Array.from({ length: n }, () => d.sample()) }));
}
scase('normal', { kind: 'normal', mean: 2, stdev: 3 }, () => vega.randomNormal(2, 3), 10, 42);
scase('lognormal', { kind: 'lognormal', mean: 0.5, stdev: 0.7 }, () => vega.randomLogNormal(0.5, 0.7), 10, 7);
scase('uniform', { kind: 'uniform', min: 1, max: 4 }, () => vega.randomUniform(1, 4), 10, 99);
scase('integer', { kind: 'integer', min: 3, max: 9 }, () => vega.randomInteger(3, 9), 10, 5);
scase('kde', { kind: 'kde', data: kdeVals, bandwidth: 1.2 }, () => vega.randomKDE(kdeVals, 1.2), 10, 11);
scase('mixture uniform', { kind: 'mixture', parts: [{ kind: 'uniform', min: 0, max: 1 }, { kind: 'uniform', min: 10, max: 11 }], weights: [1, 3] },
  () => vega.randomMixture([vega.randomUniform(0, 1), vega.randomUniform(10, 11)], [1, 3]), 12, 3);
scase('mixture normal+uniform', { kind: 'mixture', parts: [{ kind: 'normal', mean: 0, stdev: 1 }, { kind: 'uniform', min: 10, max: 11 }], weights: [1, 1] },
  () => vega.randomMixture([vega.randomNormal(0, 1), vega.randomUniform(10, 11)], [1, 1]), 12, 21);
// bootstrapCI-free LCG check
vega.setRandom(vega.randomLCG(123));
const lcg = Array.from({ length: 5 }, () => vega.random());

fs.writeFileSync('bin_stats.json', JSON.stringify({ bin: binCases, dists: distCases, bandwidth: bwCases, curves: curveCases, samples: sampleCases, lcg }));

// ---------- transforms ----------
const data = [];
for (let i = 0; i < 60; i++) data.push({ g: ['a', 'b', 'c'][i % 3], x: Math.round(((i * 7919) % 1000) / 10) / 3 - 10 + (i % 3) * 4, y: (i * 31) % 17 });
data.push({ g: 'a', x: null, y: 1 }, { g: 'b', y: 2 }, { g: 'c', x: NaN, y: 3 }, { g: 'a', x: '4.5', y: 4 }, { g: 'a', x: '', y: 5 });
const small = [3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9, 3, 2, 3, 8, 4, 6].map(x => ({ x }));

const cases = [];
const add = (name, input, transform, signals) => cases.push({ name, input, transform, signals });
// bin transform
const bins = [
  {}, { maxbins: 5 }, { step: 2.5 }, { steps: [1, 2, 5], maxbins: 8 }, { nice: false, maxbins: 7 }, { anchor: 1 }, { anchor: -3.3, step: 2 },
  { interval: false }, { name: 'foo' }, { as: ['lo', 'hi'] }, { base: 2, maxbins: 16 }, { minstep: 3 }, { divide: [3], maxbins: 12 }, { extent: [0, 10], maxbins: 4 }, { extent: [-2, 6], step: 1.5 },
];
for (const b of bins) add('bin ' + JSON.stringify(b), data, [{ type: 'bin', field: 'x', extent: [-10, 25], ...b }]);
add('bin extent sub', data, [{ type: 'bin', field: 'x', extent: [0, 10], maxbins: 10 }]);
add('bin y', data, [{ type: 'bin', field: 'y', extent: [0, 17], maxbins: 6 }]);
// dotbin
for (const o of [{}, { step: 1 }, { step: 0.5, smooth: true }, { smooth: true }, { groupby: ['g'] }, { groupby: ['g'], step: 2, smooth: true }, { as: 'dot' }])
  add('dotbin ' + JSON.stringify(o), data.filter(d => typeof d.x === 'number' && d.x === d.x), [{ type: 'dotbin', field: 'x', ...o }]);
add('dotbin small', small, [{ type: 'dotbin', field: 'x', step: 1.5, smooth: true }]);
add('dotbin small nosmooth', small, [{ type: 'dotbin', field: 'x', step: 1.5 }]);
// density
const dens = [
  { distribution: { function: 'normal' }, extent: [-4, 4] },
  { distribution: { function: 'normal', mean: 1, stdev: 2 }, extent: [-6, 8], method: 'cdf' },
  { distribution: { function: 'normal' }, extent: [-4, 4], steps: 30 },
  { distribution: { function: 'lognormal', mean: 0.2, stdev: 0.6 }, extent: [0, 8], minsteps: 10, maxsteps: 60 },
  { distribution: { function: 'uniform', min: 1, max: 3 }, extent: [0, 4] },
  { distribution: { function: 'uniform', min: 1, max: 3 }, extent: [0, 4], method: 'cdf', as: ['v', 'p'] },
  { distribution: { function: 'uniform' }, extent: [-1, 2], steps: 12 },
  { distribution: { function: 'kde', field: 'x', bandwidth: 0 } },
  { distribution: { function: 'kde', field: 'x', bandwidth: 2 }, extent: [-15, 30], steps: 40 },
  { distribution: { function: 'kde', field: 'x' }, method: 'cdf' },
  { distribution: { function: 'mixture', distributions: [{ function: 'normal', mean: -2, stdev: 0.5 }, { function: 'normal', mean: 2, stdev: 1 }], weights: [1, 3] }, extent: [-5, 6] },
  { distribution: { function: 'mixture', distributions: [{ function: 'normal' }, { function: 'uniform', min: 0, max: 3 }] }, extent: [-4, 4], method: 'cdf' },
  { distribution: { function: 'normal' } },
  { distribution: { function: 'bogus' }, extent: [0, 1] },
  { distribution: { function: 'normal' }, extent: [0, 1], method: 'xyz' },
  { distribution: { function: 'mixture', distributions: [{ function: 'kde', field: 'x', bandwidth: 1.5 }, { function: 'normal', mean: 5, stdev: 2 }], weights: [2, 1] }, extent: [-10, 25] },
];
for (const d of dens) add('density ' + JSON.stringify(d), small.concat(data.slice(0, 20)), [{ type: 'density', ...d }]);
// kde
const kdes = [
  {}, { bandwidth: 1.5 }, { cumulative: true }, { counts: true }, { extent: [-20, 30], steps: 25 }, { groupby: ['g'] }, { groupby: ['g'], resolve: 'shared' },
  { groupby: ['g'], resolve: 'shared', steps: 30 }, { groupby: ['g'], resolve: 'shared', extent: [-12, 20], maxsteps: 40 }, { groupby: ['g'], counts: true, cumulative: true },
  { groupby: ['g'], minsteps: 10, maxsteps: 50, as: ['v', 'd'] }, { groupby: ['g', 'y'], steps: 5 }, { resolve: 'shared' },
];
const dataNoStr = data.filter(d => d.x !== '4.5'); // upstream leaks string extents as x
for (const k of kdes) add('kde ' + JSON.stringify(k), k.groupby && k.groupby.includes('y') ? dataNoStr : data, [{ type: 'kde', field: 'x', ...k }]);
add('kde y', data, [{ type: 'kde', field: 'y', bandwidth: 0, steps: 20 }]);
add('kde empty', [], [{ type: 'kde', field: 'x' }]);
add('kde single', [{ x: 5 }], [{ type: 'kde', field: 'x', steps: 5 }]);
add('kde const', [{ x: 5 }, { x: 5 }, { x: 5 }], [{ type: 'kde', field: 'x', steps: 5 }]);
// quantile
for (const q of [{}, { step: 0.1 }, { step: 0.25 }, { probs: [0.1, 0.5, 0.9] }, { probs: [0, 1, 0.5], groupby: ['g'] }, { groupby: ['g'], step: 0.05, as: ['p', 'v'] }, { groupby: ['g', 'y'], probs: [0.5] }, { step: 0.3 }, { probs: [] }])
  add('quantile ' + JSON.stringify(q), data, [{ type: 'quantile', field: 'x', ...q }]);
add('quantile empty', [], [{ type: 'quantile', field: 'x', probs: [0.5] }]);
add('quantile y', data, [{ type: 'quantile', field: 'y', step: 0.2 }]);

fs.writeFileSync('bin_transforms.json', JSON.stringify(await record(cases)));
