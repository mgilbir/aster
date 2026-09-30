// Shared helpers for the golden-vector generators in this directory.
// Run a generator as:
//   NODE_PATH=testdata/oracle-node/node_modules node gen_x.mjs > x.json
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
export const vega = await import(require.resolve('vega'));

// runTransforms evaluates upstream Vega transforms over `values` and returns the
// resulting tuples of the last data set, with vega's hidden tuple ids dropped
// and undefined/NaN/Infinity/Date encoded so Go can decode them:
//   NaN -> {"$":"NaN"}, Infinity -> {"$":"Inf"}, -Infinity -> {"$":"-Inf"},
//   Date -> {"$":"date","v":ms}, undefined properties are omitted and an
//   undefined array element is {"$":"undef"}.
// `signals` optionally declares signals for the spec.
export async function runTransforms(values, transform, signals = []) {
  const spec = {
    $schema: 'https://vega.github.io/schema/vega/v6.json',
    width: 10, height: 10, signals,
    data: [{ name: 'src', values, transform }],
  };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  // Vega logs dataflow failures instead of rejecting; turn them into errors.
  const errors = [];
  const logger = { level() { return this; }, error(...a) { errors.push(a.map(String).join(' ')); return this; },
    warn() { return this; }, info() { return this; }, debug() { return this; } };
  view.logger(logger);
  await view.runAsync();
  if (errors.length) throw new Error(errors[0]);
  return view.data('src');
}

// encode converts a JS value into the JSON dialect described above.
export function encode(v) {
  if (v === undefined) return undefined;
  if (v instanceof Date) return { $: 'date', v: encode(v.getTime()) };
  if (typeof v === 'number') {
    if (Number.isNaN(v)) return { $: 'NaN' };
    if (v === Infinity) return { $: 'Inf' };
    if (v === -Infinity) return { $: '-Inf' };
    return v;
  }
  if (Array.isArray(v) || ArrayBuffer.isView(v)) return Array.from(v, (x) => (x === undefined ? { $: 'undef' } : encode(x)));
  if (v && typeof v === 'object') {
    const o = {};
    for (const k of Object.keys(v)) {
      const e = encode(v[k]);
      if (e !== undefined) o[k] = e;
    }
    return o;
  }
  return v;
}

// decodeInput turns the same dialect into JS values (for inputs containing NaN,
// dates, ...): apply to the `values` passed to runTransforms if needed.
export function decode(v) {
  if (Array.isArray(v)) return v.map(decode);
  if (v && typeof v === 'object') {
    if (v.$ === 'undef') return undefined;
    if (v.$ === 'NaN') return NaN;
    if (v.$ === 'Inf') return Infinity;
    if (v.$ === '-Inf') return -Infinity;
    if (v.$ === 'date') return new Date(decode(v.v));
    const o = {};
    for (const k of Object.keys(v)) o[k] = decode(v[k]);
    return o;
  }
  return v;
}

// Case list helper: each case is {name, input, transform, signals?}; output is
// [{name, input, transform, output}] where input is echoed in the dialect.
export async function record(cases) {
  const out = [];
  for (const c of cases) {
    let output, error;
    try {
      output = encode(await runTransforms(decode(structuredClone(c.input)), c.transform, c.signals));
    } catch (e) { error = String(e && e.message || e); }
    out.push({ name: c.name, input: encode(decode(c.input)), transform: c.transform, output, error });
  }
  return out;
}
