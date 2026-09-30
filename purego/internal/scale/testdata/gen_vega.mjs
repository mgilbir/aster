// Records vega-scale's tick, label and caption helpers (which take a vega-format
// locale): tickCount, tickValues, validTicks, tickFormat, labelValues,
// labelFormat, labelFraction and domainCaption.
// Run: TZ=UTC NODE_PATH=<node_modules> node gen_vega.mjs | gzip -9 > vega.json.gz
import { createRequire } from 'node:module';
import path from 'node:path';
import { build, enc, dec, NaNv, U, D } from './gen_common.mjs';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vs = await import(require.resolve('vega-scale'));
const vf = await import(require.resolve('vega-format'));
const loc = vf.locale();

const cases = [];
function T(name, type, ops, tests) {
  const c = { name, type, ops, tests: [] };
  let s;
  try { s = build({ type, ops }); } catch (e) { return; }
  for (const t of tests) {
    let r;
    try { r = enc(run(s, t)); } catch (e) { r = { $: 'throw', msg: String(e.message).slice(0, 80) }; }
    c.tests.push({ t, r });
  }
  cases.push(c);
}
const countOf = (s, t) => {
  const c = dec(t.count);
  return c;
};
function resolveCount(s, t) {
  let count = dec(t.count);
  if (t.tick) count = vs.tickCount(s, count, t.minStep === undefined || t.minStep === null ? undefined : t.minStep);
  return count;
}
function run(s, t) {
  switch (t.fn) {
    case 'tickCount': {
      const r = vs.tickCount(s, dec(t.count), t.minStep ?? undefined);
      return (r && typeof r === 'object') ? { fn: 'interval', ticks: r.range ? undefined : undefined } : r;
    }
    case 'tickValues': return vs.tickValues(s, resolveCount(s, t));
    case 'validTicks': return vs.validTicks(s, dec(t.ticks), dec(t.count));
    case 'tickFormat': {
      const f = vs.tickFormat(loc, s, resolveCount(s, t), dec(t.spec), t.formatType ?? undefined, t.noSkip);
      return dec(t.inputs).map((x) => f(x));
    }
    case 'labelValues': {
      const v = vs.labelValues(s, resolveCount(s, t));
      return { values: Array.from(v), max: v.max };
    }
    case 'labelFormat': {
      const f = vs.labelFormat(loc, s, resolveCount(s, t), t.type ?? undefined, dec(t.spec), t.formatType ?? undefined, t.noSkip);
      const vals = vs.labelValues(s, resolveCount(s, t));
      const arr = Array.from(vals); arr.max = vals.max;
      return arr.map((v, i) => f(v, i, arr));
    }
    case 'labelFraction': { const f = vs.labelFraction(s); return dec(t.inputs).map((x) => f(x)); }
    case 'd3TickFormat': {
      const c = dec(t.count);
      const f = s.tickFormat(c === null ? undefined : c, dec(t.spec) ?? undefined);
      return dec(t.inputs).map((x) => f(x));
    }
    case 'scaleFraction': { const f = vs.scaleFraction(s, dec(t.min), dec(t.max)); return dec(t.inputs).map((x) => f(x)); }
    case 'domainCaption': return vs.domainCaption(loc, s, dec(t.opt));
    default: throw new Error(t.fn);
  }
}

const COUNTS = [null, 1, 3, 5, 10, 30, NaNv];
const SPECS = [null, ',.2f', '.0%', '~s', '$,.0f', '.3r', 'e', '.1e', 'x', '', 'bad spec!'];
const TSPECS = [null, '%Y-%m-%d', '%b %d', '%H:%M', { $: 'obj', v: { year: '%y', month: '%b' } }, { $: 'obj', v: {} }];
const nums = (xs) => xs;
const mk = (extra) => extra;

// continuous numeric scales
const contDoms = [[0, 100], [0, 1], [-5, 5], [0.001, 0.009], [1e6, 5e7], [100, 0], [0, 0], [0, 10, 100]];
for (const type of ['linear', 'pow', 'sqrt', 'symlog', 'sequential-linear', 'diverging-linear']) for (const d of contDoms) {
  if (type === 'diverging-linear' && d.length !== 3) continue;
  const ticksIn = [-1, 0, 0.5, 1, 2, 5, 10, 25, 50, 100, 1e6];
  const tests = [];
  for (const c of COUNTS) tests.push({ fn: 'tickCount', count: c, minStep: null }, { fn: 'tickValues', count: c, tick: true });
  for (const c of [3, 10]) for (const ms of [1, 20]) tests.push({ fn: 'tickCount', count: c, minStep: ms }, { fn: 'tickValues', count: c, minStep: ms, tick: true });
  for (const sp of SPECS) for (const c of [null, 3, 10]) tests.push({ fn: 'tickFormat', count: c, spec: sp, inputs: ticksIn });
  tests.push({ fn: 'labelFraction', inputs: [0, 0.5, 1, 50, 100, NaNv] });
  tests.push({ fn: 'domainCaption', opt: null }, { fn: 'domainCaption', opt: { $: 'obj', v: { format: ',.1f' } } });
  tests.push({ fn: 'labelValues', count: 5 }, { fn: 'labelFormat', count: 5, type: 'symbol' }, { fn: 'labelFormat', count: 5, type: 'discrete' }, { fn: 'labelFormat', count: 5, type: 'gradient', spec: '.1f' });
  for (const r of [[0, 200], [200, 0]]) {
    tests.push({ fn: 'validTicks', ticks: [-10, 0, 25, 50, 100, 150, 300], count: 3 }, { fn: 'validTicks', ticks: [-10, 0, 25, 50, 100, 150, 300], count: null }, { fn: 'validTicks', ticks: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10], count: 4 });
  }
  T(`${type} ${JSON.stringify(d)}`, type, [['domain', d], ['range', type.startsWith('seq') || type.startsWith('div') ? [0, 1] : [0, 200]]], tests);
}
// log
for (const base of [10, 2, 1.5]) for (const d of [[1, 1000], [0.1, 1e5], [-1000, -1], [1, 10], [3, 7]]) {
  const tests = [];
  for (const c of COUNTS) tests.push({ fn: 'tickValues', count: c, tick: true }, { fn: 'labelValues', count: c }, { fn: 'tickFormat', count: c, spec: null, inputs: [0.5, 1, 2, 3, 5, 10, 20, 50, 100, 500, 1000, 1e4] }, { fn: 'tickFormat', count: c, spec: null, noSkip: true, inputs: [0.5, 1, 2, 3, 5, 10, 20, 50, 100, 500, 1000, 1e4] });
  for (const sp of [',.2f', '~s', '.0e']) tests.push({ fn: 'tickFormat', count: 10, spec: sp, inputs: [1, 2, 10, 100, 1000] });
  tests.push({ fn: 'labelFormat', count: 5, type: 'symbol' }, { fn: 'domainCaption', opt: null });
  T(`log base=${base} ${JSON.stringify(d)}`, 'log', [['base', base], ['domain', d], ['range', [0, 300]]], tests);
}
// discretizing
const qtests = (ins) => {
  const t = [];
  for (const c of [null, 5]) t.push({ fn: 'labelValues', count: c });
  for (const ty of ['symbol', 'discrete', 'gradient', null]) for (const sp of [null, '.1f', ',.0f']) {
    t.push({ fn: 'labelFormat', count: 5, type: ty, spec: sp });
    t.push({ fn: 'labelFormat', count: 5, type: ty, spec: sp, formatType: 'number' });
  }
  t.push({ fn: 'labelFraction', inputs: ins }, { fn: 'domainCaption', opt: null }, { fn: 'domainCaption', opt: { $: 'obj', v: { format: '.1f' } } });
  t.push({ fn: 'tickValues', count: 5, tick: true }, { fn: 'tickFormat', count: 5, spec: null, inputs: ins });
  return t;
};
T('quantile', 'quantile', [['domain', [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]], ['range', ['a', 'b', 'c', 'd']]], qtests([0, 3, 5.5, 10, 12]));
T('quantile-small', 'quantile', [['domain', [0.001, 0.002, 0.004, 0.009]], ['range', ['a', 'b']]], qtests([0, 0.003, 0.01]));
T('quantize', 'quantize', [['domain', [0, 100]], ['range', ['a', 'b', 'c', 'd', 'e']]], qtests([0, 50, 100, 120]));
T('quantize-small', 'quantize', [['domain', [0, 1]], ['range', [1, 2, 3]]], qtests([0, 0.5, 1]));
T('threshold', 'threshold', [['domain', [10, 20, 50]], ['range', ['a', 'b', 'c', 'd']]], qtests([5, 10, 15, 20, 55]));
T('threshold1', 'threshold', [['domain', [0.5]], ['range', ['a', 'b']]], qtests([0, 1]));
T('bin-ordinal', 'bin-ordinal', [['domain', [0, 10, 20, 30]], ['range', ['a', 'b', 'c']], ['bins', [0, 10, 20, 30]]], qtests([0, 5, 10, 30]));
T('bins linear', 'linear', [['domain', [0, 30]], ['range', [0, 300]], ['bins', [0, 10, 20, 30]]], [
  { fn: 'tickCount', count: 5, minStep: null }, { fn: 'tickCount', count: 2, minStep: null }, { fn: 'tickValues', count: 5, tick: true }, { fn: 'tickValues', count: 2, tick: true }, { fn: 'labelValues', count: 5 },
  { fn: 'labelFormat', count: 5, type: 'symbol' }, { fn: 'labelFormat', count: 5, type: 'discrete' }, { fn: 'tickFormat', count: 5, spec: null, inputs: [0, 10, 20, 30] }]);
// ordinal / band / point
for (const type of ['ordinal', 'band', 'point']) {
  const dom = [['a', 'b', 'c'], ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i'], ['x'], [], [1, 2, 3]];
  for (const d of dom) T(`${type} ${JSON.stringify(d)}`, type, [['domain', d], ['range', type === 'ordinal' ? ['r', 'g', 'b'] : [0, 100]]], [
    { fn: 'tickValues', count: 5, tick: true }, { fn: 'labelValues', count: 5 }, { fn: 'tickFormat', count: 5, spec: null, inputs: ['a', 1, null, U] },
    { fn: 'tickFormat', count: 5, spec: '.1f', inputs: [1, 2.5, 'a'] }, { fn: 'labelFormat', count: 5, type: 'discrete' }, { fn: 'labelFormat', count: 5, type: 'symbol' },
    { fn: 'domainCaption', opt: null }, { fn: 'domainCaption', opt: { $: 'obj', v: { maxlen: 5 } } }, { fn: 'domainCaption', opt: { $: 'obj', v: { maxlen: 1 } } }, { fn: 'domainCaption', opt: { $: 'obj', v: { format: '.1f' } } },
    { fn: 'validTicks', ticks: ['a', 'b', 'c', 'zz'], count: 2 }, { fn: 'validTicks', ticks: d, count: null }]);
}
// time
const t0 = Date.UTC(2020, 0, 1), DAY = 864e5;
const tdoms = [[t0, t0 + 365 * DAY], [t0, t0 + 3 * 3600e3], [t0, t0 + 30 * DAY], [t0, t0 + 8 * 365 * DAY], [t0 + 30 * DAY, t0]];
for (const type of ['time', 'utc']) for (const d of tdoms) {
  const tests = [];
  for (const c of [null, 3, 5, 10, 'day', 'month', 'hour', 'year', { $: 'obj', v: { interval: 'month', step: 3 } }, { $: 'obj', v: { interval: 'day', step: 2 } }, 'week', 'minute', 'second', 'millisecond', 'bogus']) {
    tests.push({ fn: 'tickCount', count: c, minStep: null }, { fn: 'tickValues', count: c, tick: true });
  }
  for (const sp of TSPECS) for (const c of [null, 5]) tests.push({ fn: 'tickFormat', count: c, spec: sp, inputs: [D(d[0]), D(d[0] + DAY * 0.5), D(d[1]), t0 + 3.5 * DAY] }); // (an invalid date is left out: the expected "0NaN" comes from d3 padding "NaN" to a 4-digit year)
  tests.push({ fn: 'tickFormat', count: 5, spec: '%Y', formatType: 'utc', inputs: [D(d[0])] }, { fn: 'tickFormat', count: 5, spec: '%Y', formatType: 'time', inputs: [D(d[0])] });
  tests.push({ fn: 'domainCaption', opt: null }, { fn: 'domainCaption', opt: { $: 'obj', v: { format: '%b %d' } } }, { fn: 'domainCaption', opt: { $: 'obj', v: { format: '%a, %b' } } });
  tests.push({ fn: 'labelFraction', inputs: [D(d[0]), D(d[1]), D((d[0] + d[1]) / 2)] });
  tests.push({ fn: 'labelValues', count: 5 }, { fn: 'labelFormat', count: 5, type: 'symbol' });
  T(`${type} ${d[0]}..${d[1]}`, type, [['domain', d.map((x) => D(x))], ['range', [0, 500]]], tests);
}
// d3's own tickFormat methods
for (const [type, ops, xs] of [
  ['linear', [['domain', [0, 100]]], [0, 20, 50, 100]], ['linear', [['domain', [0, 1]]], [0, 0.25, 0.5, 1]], ['linear', [['domain', [1e-4, 5e-4]]], [1e-4, 2e-4, 5e-4]],
  ['linear', [['domain', [0, 5e6]]], [0, 1e6, 5e6]], ['pow', [['exponent', 2], ['domain', [0, 10]]], [0, 2.5, 10]], ['symlog', [['domain', [-100, 100]]], [-100, 0, 100]],
  ['log', [['domain', [1, 1000]]], [0.5, 1, 2, 3, 5, 10, 20, 50, 100, 500, 1000]], ['log', [['base', 2], ['domain', [1, 64]]], [1, 2, 3, 4, 5, 8, 16, 32, 64]],
  ['log', [['base', 1.5], ['domain', [1, 20]]], [1, 1.5, 2.25, 3.375, 10]], ['log', [['domain', [-1000, -1]]], [-1000, -100, -10, -1]],
  ['sequential-linear', [['domain', [0, 1000]]], [0, 250, 1000]], ['sequential-log', [['domain', [1, 1e4]]], [1, 10, 100, 1e4]], ['diverging-linear', [['domain', [-10, 0, 10]]], [-10, 0, 10]],
  ['quantize', [['domain', [0, 100]]], [0, 50, 100]], ['identity', [['domain', [0, 10]]], [0, 5, 10]], ['bin-ordinal', [['domain', [0, 1.5, 3]]], [0, 1.5, 3]],
]) {
  const tests = [];
  for (const sp of [null, ',.2f', '.0%', '~s', '$,.0f', 'e', 'r', 'g', 'p', '.3s', '', 'x']) for (const c of [null, 3, 10, 1e9, 0.5]) tests.push({ fn: 'd3TickFormat', count: c, spec: sp, inputs: xs });
  T(`d3 tickFormat ${type}`, type, ops.concat([['range', type.startsWith('seq') || type.startsWith('div') ? [0, 1] : [0, 10]]]), tests);
}
const th = Date.UTC(2020, 0, 1);
for (const type of ['time', 'utc']) T(`d3 tickFormat ${type}`, type, [['domain', [D(th), D(th + 864e5 * 400)]]], [null, '%Y-%m-%d', '%b %d', '%H:%M'].map((sp) => ({ fn: 'd3TickFormat', count: 5, spec: sp, inputs: [D(th), D(th + 3600e3), D(th + 864e5 * 31), D(th + 864e5 * 366), D(th + 3 * 864e5)] })));
// scaleFraction
for (const [type, ops] of [['linear', []], ['pow', [['exponent', 2]]], ['sqrt', []], ['log', [['base', 2]]], ['log', []], ['symlog', [['constant', 10]]], ['linear', [['clamp', true]]],
  ['sequential-log', [['base', 10]]], ['sequential-pow', [['exponent', 0.3]]], ['diverging-symlog', []], ['sequential', []], ['time', []], ['utc', []]]) {
  const tests = [];
  const temporal = type === 'time' || type === 'utc';
  const ranges = temporal ? [[t0, t0 + 10 * DAY], [t0, t0]] : [[1, 100], [0, 10], [5, 5], [100, 1], [-10, 10], [0, Infinity]];
  for (const [lo, hi] of ranges) tests.push({ fn: 'scaleFraction', min: lo === Infinity ? { $: 'inf' } : lo, max: hi === Infinity ? { $: 'inf' } : hi, inputs: temporal ? [D(lo), D((lo + hi) / 2), D(hi), D(hi + DAY)] : [-5, 0, 1, 2, 5, 10, 50, 100, 1000, NaNv, null] });
  T(`scaleFraction ${type} ${JSON.stringify(ops)}`, type, ops, tests);
}
console.log(JSON.stringify(cases));
