// Records the datums vega-encode's AxisTicks and LegendEntries operators
// produce for a set of scales and parameter combinations.
//
//   TZ=UTC NODE_PATH=<node_modules> node gen_ticks.mjs > ticks.json
//
// Timestamps travel as {"$t": epochMillis}, non-finite numbers as
// {"$n": "Infinity" | "-Infinity" | "NaN"}.
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const { Dataflow, Parameters, Pulse } = await import(require.resolve('vega-dataflow'));
const { axisticks: AxisTicks, legendentries: LegendEntries } = await import(require.resolve('vega-encode'));
const { scale: vscale } = await import(require.resolve('vega-scale'));

const dec = v => v && typeof v === 'object' && !Array.isArray(v) && '$t' in v ? new Date(v.$t)
  : Array.isArray(v) ? v.map(dec) : v;
const enc = (k, v) => typeof v === 'number' && !Number.isFinite(v) ? { $n: String(v) }
  : v instanceof Date ? { $t: v.getTime() } : v;
// JSON.stringify calls Date.prototype.toJSON before the replacer; undo it
const replacer = function (k, v) {
  const raw = this[k];
  return raw instanceof Date ? { $t: raw.getTime() } : enc(k, v);
};

function makeScale(d) {
  const s = vscale(d.type)();
  if (d.domain) s.domain(dec(d.domain));
  if (d.range) s.range(dec(d.range));
  for (const [k, v] of Object.entries(d.props || {})) s[k](v);
  if (d.bins) s.bins = d.bins;
  return s;
}

const lin = { type: 'linear', domain: [0, 100], range: [0, 500] };
const scales = {
  lin,
  linNeg: { type: 'linear', domain: [-3.7, 9.2], range: [0, 300] },
  linRev: { type: 'linear', domain: [0, 100], range: [500, 0] },
  linTiny: { type: 'linear', domain: [0, 0.0004], range: [0, 300] },
  linBig: { type: 'linear', domain: [0, 12000000], range: [0, 300] },
  log: { type: 'log', domain: [1, 1000], range: [0, 300] },
  logWide: { type: 'log', domain: [0.01, 1e7], range: [0, 300] },
  log2: { type: 'log', domain: [1, 64], range: [0, 300], props: { base: 2 } },
  sqrt: { type: 'sqrt', domain: [0, 100], range: [0, 300] },
  pow: { type: 'pow', domain: [0, 100], range: [0, 300], props: { exponent: 2 } },
  symlog: { type: 'symlog', domain: [-100, 100], range: [0, 300] },
  seq: { type: 'sequential', domain: [0, 10], range: ['red', 'blue'] },
  time: { type: 'time', domain: [{ $t: Date.UTC(2020, 0, 1) }, { $t: Date.UTC(2021, 0, 1) }], range: [0, 400] },
  utc: { type: 'utc', domain: [{ $t: Date.UTC(2020, 0, 1) }, { $t: Date.UTC(2020, 0, 3) }], range: [0, 400] },
  band: { type: 'band', domain: ['a', 'b', 'c', 'd', 'e'], range: [0, 200] },
  point: { type: 'point', domain: ['a', 'b', 'c'], range: [0, 200] },
  ord: { type: 'ordinal', domain: ['a', 'b', 'c'], range: ['red', 'green', 'blue'] },
  binned: { type: 'linear', domain: [0, 40], range: [0, 400], bins: [0, 10, 20, 30, 40] },
  binOrd: { type: 'bin-ordinal', domain: [0, 10, 20, 30], range: ['a', 'b', 'c'], bins: [0, 10, 20, 30] },
  quantize: { type: 'quantize', domain: [0, 100], range: ['a', 'b', 'c', 'd'] },
  quantile: { type: 'quantile', domain: [1, 2, 3, 5, 8, 13, 21, 34, 55, 89], range: ['a', 'b', 'c', 'd'] },
  threshold: { type: 'threshold', domain: [10, 20, 50], range: ['a', 'b', 'c', 'd'] },
  quantizeFrac: { type: 'quantize', domain: [0, 1], range: ['a', 'b', 'c', 'd', 'e'] },
};

const cases = [];
const df = new Dataflow();

async function run(kind, name, sdef, params, sizeSpec) {
  const s = makeScale(sdef);
  const p = { scale: s };
  for (const [k, v] of Object.entries(params)) p[k] = dec(v);
  let op;
  let size = null;
  if (kind === 'legend') {
    if (sizeSpec === 'sqrt') { p.size = v => Math.sqrt(+v) * 3; size = 'sqrt'; }
    else if (sizeSpec !== undefined) { p.size = sizeSpec; size = sizeSpec; }
  }
  let out, err;
  try {
    const T = kind === 'axis' ? AxisTicks : LegendEntries;
    // call the transform directly with fresh, fully modified parameters
    op = new T();
    const _ = new Parameters();
    for (const [k, v] of Object.entries(p)) _.set(k, null, v, true);
    const pulse = new Pulse(df, 1);
    op.transform(_, pulse);
    out = op.value;
  } catch (e) {
    err = String(e.message || e);
  }
  cases.push({ kind, name, scale: sdef, params, size, out: out ? JSON.parse(JSON.stringify(out, replacer)) : undefined, error: err });
}

// ---- axes
const axisParams = [
  {},
  { count: 5 },
  { count: 20 },
  { count: 1 },
  { count: 0 },
  { minstep: 30 },
  { count: 12, minstep: 25 },
  { values: [10, 50, 200, -5, 75] },
  { values: [0, 20, 40, 60, 80, 100], count: 3 },
  { values: [80, 20, 40], count: 10 },
  { values: [] },
  { formatSpecifier: '.1f' },
  { formatSpecifier: '$,.0f' },
  { formatSpecifier: '%' },
  { formatSpecifier: 's' },
  { extra: true },
  { extra: true, count: 4 },
];
for (const [sn, sd] of Object.entries(scales)) {
  if (['time', 'utc'].includes(sn)) continue;
  for (const [i, ap] of axisParams.entries()) {
    // restrict value lists to scales they make sense for
    if (ap.values && ['band', 'point', 'ord'].includes(sn)) {
      await run('axis', `axis-${sn}-${i}`, sd, { ...ap, values: ['a', 'c', 'zz'] });
      continue;
    }
    await run('axis', `axis-${sn}-${i}`, sd, ap);
  }
}
for (const sn of ['time', 'utc']) {
  for (const [i, ap] of [
    { count: 4, formatSpecifier: '%Y-%m-%d' },
    { count: 10, formatSpecifier: '%b %d' },
    { formatSpecifier: '%H:%M' },
    { values: [{ $t: Date.UTC(2020, 5, 1) }, { $t: Date.UTC(2020, 8, 1) }, { $t: Date.UTC(2025, 1, 1) }], formatSpecifier: '%Y-%m' },
    { count: 5, minstep: 5, formatSpecifier: '%d' },
  ].entries()) {
    await run('axis', `axis-${sn}-${i}`, scales[sn], { ...ap, formatType: sn });
  }
}
await run('axis', 'axis-lin-formatType-time', scales.lin, { formatType: 'time', formatSpecifier: '%Y' });
await run('axis', 'axis-lin-formatType-number', scales.lin, { formatType: 'number', formatSpecifier: '.2f' });
await run('axis', 'axis-time-count-interval-error', scales.lin, { count: 'month' });

// ---- legends
const legendParams = [
  {},
  { count: 3 },
  { count: 10 },
  { values: [10, 30, 90] },
  { formatSpecifier: '.1f' },
  { formatSpecifier: '$,.0f', count: 4 },
  { limit: 2 },
  { limit: 3, count: 8 },
  { limit: 100 },
  { minstep: 30 },
];
const legendScales = ['lin', 'linNeg', 'log', 'sqrt', 'seq', 'band', 'point', 'ord', 'binOrd', 'binned', 'quantize', 'quantile', 'threshold', 'quantizeFrac'];
for (const sn of legendScales) {
  for (const type of ['symbol', 'gradient', 'discrete']) {
    for (const [i, lp] of legendParams.entries()) {
      const params = { type, ...lp };
      if (lp.values && ['band', 'point', 'ord'].includes(sn)) params.values = ['a', 'c'];
      await run('legend', `legend-${sn}-${type}-${i}`, scales[sn], params);
    }
  }
}
for (const sn of ['lin', 'sqrt', 'quantize']) {
  for (const type of ['symbol']) {
    await run('legend', `legend-${sn}-size-sqrt`, scales[sn], { type, count: 5 }, 'sqrt');
    await run('legend', `legend-${sn}-size-const`, scales[sn], { type, count: 5 }, 20);
  }
}
await run('legend', 'legend-lin-zero-size', { type: 'linear', domain: [0, 100], range: [0, 100] }, { type: 'symbol', count: 5 }, 'sqrt');
await run('legend', 'legend-time-gradient', scales.time, { type: 'gradient', formatType: 'time', formatSpecifier: '%b' });
await run('legend', 'legend-time-symbol', scales.time, { type: 'symbol', formatType: 'time', formatSpecifier: '%Y-%m', count: 3 });
await run('legend', 'legend-utc-discrete', scales.utc, { type: 'discrete', formatType: 'utc', formatSpecifier: '%H' });
await run('legend', 'legend-quantize-timefmt', scales.quantize, { type: 'symbol', formatType: 'number', formatSpecifier: '.0f' });
// single-valued domains and degenerate gradients
await run('legend', 'legend-degenerate-gradient', { type: 'linear', domain: [5, 5], range: [0, 1] }, { type: 'gradient' });
await run('legend', 'legend-two-value-gradient', { type: 'linear', domain: [0, 1], range: [0, 1] }, { type: 'gradient', count: 1 });

process.stdout.write(JSON.stringify(cases, replacer));
