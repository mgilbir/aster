// Shared helpers of the scale generators: value encoding and the case runner.
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vs_ = await import(require.resolve('vega-scale'));

// ---- value encoding -------------------------------------------------------
export function enc(v) {
  if (v === undefined) return { $: 'u' };
  if (typeof v === 'number') {
    if (Number.isNaN(v)) return { $: 'nan' };
    if (v === Infinity) return { $: 'inf' };
    if (v === -Infinity) return { $: '-inf' };
    return v;
  }
  if (v instanceof Date) return { $: 'date', v: Number.isNaN(+v) ? null : +v };
  if (Array.isArray(v)) return v.map(enc);
  if (v && typeof v === 'object') {
    const o = {};
    for (const k of Object.keys(v)) o[k] = enc(v[k]);
    return { $: 'obj', v: o };
  }
  if (typeof v === 'function') return { $: 'fn' };
  return v;
}
export function dec(v) {
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
export const NaNv = { $: 'nan' }, U = { $: 'u' }, INF = { $: 'inf' }, NINF = { $: '-inf' };
export const D = (ms) => ({ $: 'date', v: ms });
export const O = (v) => ({ $: 'obj', v });
export const vs = vs_;

// ---- running a case ---------------------------------------------------------
export function build(c) {
  let s = vs_.scale(c.type)();
  for (const [op, arg] of c.ops) {
    const a = dec(arg);
    switch (op) {
      case 'copy': s = vs_.scaleCopy(s); break;
      case 'interpolate': s.interpolate(vs_.interpolate(a[0], a[1])); break;
      case 'nice': s.nice(a === null ? undefined : a); break;
      case 'implicit': s.unknown(vs_.scaleImplicit); break;
      case 'interpolator': s.interpolator(vs_.scheme(a)); break;
      case 'bins': s.bins = a; break;
      default: s[op](a);
    }
  }
  return s;
}
function query(s, q) {
  const [kind, arg] = q;
  const a = dec(arg);
  switch (kind) {
    case 'apply': return s(a);
    case 'invert': return s.invert(a);
    case 'invertRange': return s.invertRange(a);
    case 'invertExtent': return s.invertExtent(a);
    case 'ticks': return s.ticks(a === null ? undefined : a);
    case 'domain': return s.domain();
    case 'range': return s.range();
    case 'bandwidth': return s.bandwidth();
    case 'step': return s.step();
    case 'quantiles': return s.quantiles();
    case 'thresholds': return s.thresholds();
    case 'clamp': return s.clamp();
    case 'base': return s.base();
    case 'exponent': return s.exponent();
    case 'constant': return s.constant();
    case 'padding': return s.padding();
    case 'align': return s.align();
    case 'round': return s.round();
    case 'unknown': return s.unknown();
    case 'invertRangeVega': return s.invertRange(a);
    default: throw new Error('query ' + kind);
  }
}
export const cases = [];
export function C(name, type, ops, queries) {
  const c = { name, type, ops, queries: [] };
  let s;
  try { s = build(c); } catch (e) { c.buildThrows = true; }
  for (const q of queries) {
    let r;
    if (s) {
      try { r = enc(query(s, q)); } catch (e) { r = { $: 'throw' }; }
    } else r = { $: 'throw' };
    c.queries.push([q[0], q[1] === undefined ? null : q[1], r]);
  }
  cases.push(c);
}
export function emit() { console.log(JSON.stringify(cases)); }
