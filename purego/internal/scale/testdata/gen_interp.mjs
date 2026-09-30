// Records d3-interpolate, vega-scale interpolate helpers and the scheme registry.
// Run: NODE_PATH=<node_modules> node gen_interp.mjs | gzip -9 > interp.json.gz
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vs = await import(require.resolve('vega-scale'));
const di = await import(require.resolve('d3-interpolate'));
const pal = await import(path.join(process.env.NODE_PATH, 'vega-scale/src/palettes.js'));

function enc(v) {
  if (v === undefined) return { $: 'u' };
  if (typeof v === 'number') {
    if (Number.isNaN(v)) return { $: 'nan' };
    if (v === Infinity) return { $: 'inf' };
    if (v === -Infinity) return { $: '-inf' };
    return v;
  }
  if (v instanceof Date) return { $: 'date', v: Number.isNaN(+v) ? null : +v };
  if (Array.isArray(v) || ArrayBuffer.isView(v)) return Array.from(v).map(enc);
  if (v && typeof v === 'object') {
    const o = {};
    for (const k of Object.keys(v)) o[k] = enc(v[k]);
    return { $: 'obj', v: o };
  }
  return v;
}
function dec(v) {
  if (Array.isArray(v)) return v.map(dec);
  if (v && typeof v === 'object') {
    switch (v.$) {
      case 'u': return undefined;
      case 'nan': return NaN;
      case 'inf': return Infinity;
      case '-inf': return -Infinity;
      case 'date': return new Date(v.v === null ? NaN : v.v);
      case 'obj': { const o = {}; for (const k of Object.keys(v.v)) o[k] = dec(v.v[k]); return o; }
    }
  }
  return v;
}
const NaNv = { $: 'nan' };
const TS = [-0.5, -0.1, 0, 0.05, 0.1, 0.25, 0.333, 0.5, 0.6, 0.75, 0.9, 1, 1.1, 1.5, NaNv];
const out = { interps: [], schemes: [], helpers: [] };
const run = (i) => { try { return TS.map((t) => enc(i(dec(t)))); } catch (e) { return 'throw'; } };

// two-argument interpolators
const two = {
  number: di.interpolateNumber, round: di.interpolateRound, rgb: di.interpolateRgb, hsl: di.interpolateHsl, hslLong: di.interpolateHslLong,
  lab: di.interpolateLab, hcl: di.interpolateHcl, hclLong: di.interpolateHclLong, cubehelix: di.interpolateCubehelix,
  cubehelixLong: di.interpolateCubehelixLong, string: di.interpolateString, array: di.interpolateArray, object: di.interpolateObject,
  date: di.interpolateDate, hue: di.interpolateHue, value: di.interpolate,
  rgb2: di.interpolateRgb.gamma(2.2), cubehelix2: di.interpolateCubehelix.gamma(2), cubehelixLong3: di.interpolateCubehelixLong.gamma(0.5),
};
const O = (v) => ({ $: 'obj', v });
const pairs = [
  [0, 10], [10, 0], [-5, 5.5], ['5', '15'], [null, 10], [0, NaNv], [NaNv, 4],
  ['red', 'blue'], ['#f00', '#0f0'], ['rgba(255,0,0,0.2)', 'rgba(0,0,255,0.8)'], ['hsl(0,100%,50%)', 'hsl(240,100%,50%)'], ['hsl(350,80%,40%)', 'hsl(20,20%,90%)'],
  ['#000', '#fff'], ['white', 'black'], ['transparent', 'red'], ['red', 'transparent'], ['nocolor', 'red'], ['red', 'nocolor'], ['steelblue', 'orange'],
  ['gray', 'gray'], ['#123456', '#123456'], ['#000', 'hsl(120, 0%, 0%)'], ['hsl(0,0%,50%)', 'hsl(120,0%,50%)'],
  ['0px', '100px'], ['10 20 30', '20 40 60'], ['a1b2', 'a3b4c5'], ['x', 'y'], ['1e3', '2e3'], ['M0,0L10,10', 'M5,5L1,2'], ['-1.5e-3', '+2.5E+2'], ['abc', '12'], ['12', 'abc'], ['.5', '1.5'], ['1.', '2.'],
  [[1, 2, 3], [4, 5, 6]], [[1, 2], [4, 5, 6]], [[1, 2, 3], [4, 5]], [['red', 1], ['blue', 3]], [[[0, 0]], [[10, 20]]],
  [O({ a: 0, b: 'red' }), O({ a: 10, b: 'blue' })], [O({ a: 0 }), O({ a: 10, c: 5 })], [O({}), O({ x: 1 })], [null, O({ x: 1 })],
  [{ $: 'date', v: 0 }, { $: 'date', v: 1e12 }], [{ $: 'date', v: 0 }, { $: 'date', v: null }], [{ $: 'date', v: 8.639e15 }, { $: 'date', v: 8.64e15 }],
  [true, false], [1, true], [0, null], [0, { $: 'u' }],
  [359, 1], [1, 359], [0, 180], [0, 181], [-90, 270], [NaNv, 100],
];
for (const [name, f] of Object.entries(two)) for (const [a, b] of pairs) {
  // String(Date) depends on the host time zone; not recordable portably.
  if (name === 'string' && (a?.$ === 'date' || b?.$ === 'date')) continue;
  // interpolateArray is only ever chosen by d3.interpolate for array ends.
  if (name === 'array' && !Array.isArray(b)) continue;
  if (name === 'object' && (Array.isArray(a) || Array.isArray(b))) continue;
  let res;
  try { const i = f(dec(a), dec(b)); res = TS.map((t) => enc(i(dec(t)))); } catch (e) { res = 'throw'; }
  out.interps.push({ f: name, a, b, ts: TS, r: res });
}

// one-argument splines / piecewise / discrete
const nums = [[0, 1, 4, 9], [5], [1, 2], [3, 1, 4, 1, 5, 9, 2, 6], []];
for (const v of nums) {
  for (const [name, f] of [['basis', di.interpolateBasis], ['basisClosed', di.interpolateBasisClosed]]) {
    let res; try { const i = f(v); res = TS.map((t) => enc(i(dec(t)))); } catch (e) { res = 'throw'; }
    out.helpers.push({ f: name, args: v, ts: TS, r: res });
  }
}
const colorLists = [['red', 'blue'], ['red', 'green', 'blue', 'yellow'], ['#000', '#fff', '#f00'], ['red'], ['nocolor', 'red', 'rgba(0,0,255,0.3)']];
for (const v of colorLists) {
  for (const [name, f] of [['rgbBasis', di.interpolateRgbBasis], ['rgbBasisClosed', di.interpolateRgbBasisClosed]]) {
    let res; try { const i = f(v); res = TS.map((t) => enc(i(dec(t)))); } catch (e) { res = 'throw'; }
    out.helpers.push({ f: name, args: v, ts: TS, r: res });
  }
}
for (const v of [['a', 'b', 'c'], [1, 2, 3, 4], ['x'], []]) {
  const i = di.interpolateDiscrete(v);
  out.helpers.push({ f: 'discrete', args: v, ts: TS, r: run(i) });
}
for (const [v, name, f] of [[[0, 10, 5], 'piecewise', null], [['red', 'blue', 'green'], 'piecewise', null], [[1, 2], 'piecewise', null]]) {
  const i = di.piecewise(v);
  out.helpers.push({ f: name, args: v, ts: TS, r: run(i) });
}
out.helpers.push({ f: 'quantize', args: [3], r: enc(di.quantize(di.interpolateRgb('red', 'blue'), 3)) });
out.helpers.push({ f: 'quantize', args: [5], r: enc(di.quantize(di.interpolateNumber(0, 1), 5)) });
out.helpers.push({ f: 'quantizeInterpolator', args: [3], r: enc(vs.quantizeInterpolator(di.interpolateRgb('red', 'blue'), 3)) });
out.helpers.push({ f: 'quantizeInterpolator', args: [5], r: enc(vs.quantizeInterpolator(di.interpolateNumber(0, 1), 5)) });
out.helpers.push({ f: 'quantizeInterpolator', args: [0], r: enc(vs.quantizeInterpolator(di.interpolateNumber(0, 1), 0)) });
// zoom
for (const [p0, p1, rho] of [[[0, 0, 1], [10, 10, 100], null], [[0, 0, 10], [0, 0, 1000], null], [[5, 5, 50], [5, 5, 50], null], [[0, 0, 1], [300, 0, 2], 1], [[0, 0, 1], [300, 0, 2], 4]]) {
  const z = rho == null ? di.interpolateZoom : di.interpolateZoom.rho(rho);
  const i = z(p0, p1);
  out.helpers.push({ f: 'zoom', args: [p0, p1, rho], duration: i.duration, ts: TS, r: run(i) });
}
// vega interpolateColors / interpolateRange
for (const [colors, type, gamma] of [[['red', 'blue'], undefined, undefined], [['red', 'green', 'blue'], 'hsl', undefined], [['red', 'green', 'blue'], 'lab', undefined],
  [['#000', '#fff'], 'hcl', undefined], [['#000', '#fff'], 'cubehelix', 2], [['#000', '#fff'], 'rgb', 2.2], [['#000', '#f00', '#fff'], 'hcl-long', undefined], [['red'], undefined, undefined]]) {
  const i = vs.interpolateColors(colors, type, gamma);
  out.helpers.push({ f: 'interpolateColors', args: [colors, type ?? null, gamma ?? null], ts: TS, r: run(i) });
}
{
  const i = vs.interpolateRange(di.interpolateNumber(0, 100), [0.25, 0.75]);
  out.helpers.push({ f: 'interpolateRange', args: [[0.25, 0.75]], ts: TS, r: run(i) });
}

// scheme registry
const T2 = [-0.2, 0, 0.001, 0.1, 0.2, 0.3, 0.37, 0.5, 0.63, 0.7, 0.8, 0.9, 0.999, 1, 1.2, NaNv];
const names = [...Object.keys(pal.discrete), ...Object.keys(pal.continuous)].map((n) => n.toLowerCase());
for (const n of names) {
  const s = vs.scheme(n);
  if (Array.isArray(s)) out.schemes.push({ name: n, colors: s });
  else out.schemes.push({ name: n, ts: T2, r: T2.map((t) => { try { return enc(s(dec(t))); } catch (e) { return { $: 'throw' }; } }) });
}
out.schemeCase = ['Category10', 'BLUES', 'nonexistent'].map((n) => [n, vs.scheme(n) === undefined ? 'undef' : typeof vs.scheme(n) === 'function' ? 'fn' : 'arr']);
console.log(JSON.stringify(out));
