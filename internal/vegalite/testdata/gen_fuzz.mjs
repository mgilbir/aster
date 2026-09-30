// Records upstream's output for randomly mutated corpus specifications, for
// TestDifferentialFuzz. Each line: {"name":"..","spec":{..},"vega":{..}} or
// {"name":"..","spec":{..},"error":".."}.
//
// Usage: NODE_PATH=<node_modules with the vega-lite to record> TZ=Europe/Amsterdam \
//        node gen_fuzz.mjs [count] [seed] > fuzz.jsonl
// Run the Go side with VEGALITE_FUZZ=fuzz.jsonl (VEGALITE_VERSION=5.8 for a
// vega-lite 5.8 recording).
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vl = await import(require.resolve('vega-lite'));
const here = path.dirname(fileURLToPath(import.meta.url));
const count = Number(process.argv[2] || 2000);
let seed = Number(process.argv[3] || 1) >>> 0;
const rnd = () => {
  // mulberry32
  seed = (seed + 0x6d2b79f5) >>> 0;
  let t = seed;
  t = Math.imul(t ^ (t >>> 15), t | 1);
  t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (a) => a[Math.floor(rnd() * a.length)];
const chance = (p) => rnd() < p;

const specsDir = path.join(here, 'specs');
const corpus = [];
for (const group of fs.readdirSync(specsDir).sort()) {
  for (const f of fs.readdirSync(path.join(specsDir, group)).filter((f) => f.endsWith('.json')).sort()) {
    corpus.push({ name: `${group}/${f.replace(/\.json$/, '')}`, file: path.join(specsDir, group, f) });
  }
}

const MARKS = ['bar', 'line', 'area', 'point', 'tick', 'rect', 'text', 'circle', 'square', 'rule', 'trail', 'arc', 'boxplot', 'errorbar', 'errorband'];
const TIMEUNITS = ['year', 'yearmonth', 'month', 'utcyear', 'utcyearmonth', 'week', 'dayofyear', 'quarter', 'yearmonthdate', 'hours', 'day', 'binnedyearmonth', { unit: 'yearmonth', step: 3 }, { unit: 'year', utc: true }, { unit: 'month', binned: true }];
const AGGS = ['sum', 'count', 'mean', 'median', 'max', 'min', 'distinct', 'exponential', 'exponentialb', 'q1', { argmax: 'x' }];
const TYPES = ['quantitative', 'nominal', 'ordinal', 'temporal'];
const SCALES = [{ type: 'log' }, { type: 'sqrt' }, { type: 'band' }, { type: 'point' }, { type: 'ordinal' }, { type: 'linear', zero: false }, { zero: true }, { domain: [-1, 5] }, { domain: ['-1', '3'] }, { domainRaw: [1, 2] }, { nice: true }, { padding: 0.2 }, { type: 'time' }, { type: 'utc' }];
const CHANNELS = ['x', 'y', 'x2', 'y2', 'xOffset', 'yOffset', 'color', 'fill', 'stroke', 'size', 'opacity', 'detail', 'order', 'tooltip', 'text', 'shape', 'theta', 'radius', 'time', 'description', 'strokeWidth', 'row', 'column'];

function walk(node, visit, pathStr = '') {
  if (Array.isArray(node)) node.forEach((v, i) => walk(v, visit, `${pathStr}/${i}`));
  else if (node && typeof node === 'object') {
    visit(node, pathStr);
    for (const k of Object.keys(node)) walk(node[k], visit, `${pathStr}/${k}`);
  }
}

function fieldsOf(spec) {
  const fields = new Set();
  walk(spec, (o) => {
    if (typeof o.field === 'string') fields.add(o.field);
  });
  return [...fields];
}

function mutate(spec) {
  const units = [];
  walk(spec, (o) => {
    if (o.mark !== undefined || o.encoding) units.push(o);
  });
  const fields = fieldsOf(spec);
  if (units.length === 0) {
    spec.config = { ...(spec.config || {}), mark: { invalid: pick(['filter', 'hide', null, 'show']) } };
    return;
  }
  const n = 1 + Math.floor(rnd() * 3);
  for (let i = 0; i < n; i++) {
    const u = pick(units);
    u.encoding ??= {};
    const enc = u.encoding;
    const chs = Object.keys(enc);
    const f = () => (fields.length ? pick(fields) : 'x');
    switch (Math.floor(rnd() * 16)) {
      case 0:
        if (u.mark !== undefined) u.mark = typeof u.mark === 'object' ? { ...u.mark, type: pick(MARKS) } : pick(MARKS);
        break;
      case 1:
        enc[pick(CHANNELS)] = { field: f(), type: pick(TYPES) };
        break;
      case 2:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].timeUnit = pick(TIMEUNITS);
        }
        break;
      case 3:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].aggregate = pick(AGGS);
        }
        break;
      case 4:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].bin = pick([true, { maxbins: 5 }, 'binned', { binned: true, step: 2 }, { step: 10 }]);
        }
        break;
      case 5:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].stack = pick([true, false, null, 'zero', 'normalize', 'center']);
        }
        break;
      case 6:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].scale = pick(SCALES);
        }
        break;
      case 7:
        spec.config = { ...(spec.config || {}), mark: { ...(spec.config?.mark || {}), invalid: pick(['filter', 'hide', null, 'show', 'break-paths-filter-domains']) } };
        break;
      case 8:
        if (u.mark !== undefined) u.mark = { ...(typeof u.mark === 'object' ? u.mark : { type: u.mark }), ...pick([{ size: 0 }, { size: 10 }, { orient: 'horizontal' }, { opacity: 0.5 }, { tooltip: true }, { invalid: 'filter' }, { clip: true }, { cursor: 'pointer' }, { fillOpacity: 0.3 }, { width: { band: 0.5 } }, { minBandSize: 3 }, { thickness: 3 }, { binSpacing: 2 }, { timeUnitBandPosition: 0.2 }]) };
        break;
      case 9:
        u.width = pick([0, 100, 'container', { step: 12 }]);
        u.height = pick([0, 100, 'container', { step: 12 }]);
        break;
      case 10:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].axis = pick([{ tickMinStep: 2 }, { title: null }, { grid: true }, { format: 'd' }, { labelAngle: 45 }, null]);
        }
        break;
      case 11:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].legend = pick([{ orient: 'top' }, null, { title: 'T' }, { symbolType: 'square' }]);
        }
        break;
      case 12:
        u.params = [...(u.params || []), pick([
          { name: 'pt', select: { type: 'point', on: 'mouseover', nearest: true } },
          { name: 'br', select: { type: 'interval', encodings: ['x'] } },
          { name: 'lg', select: { type: 'point', fields: [f()] }, bind: 'legend' },
          { name: 'sl', select: { type: 'interval', clear: false }, bind: 'scales' },
          { name: 'tm', select: { type: 'point', on: 'timer' } },
        ])];
        break;
      case 13:
        u.transform = [...(u.transform || []), pick([
          { filter: { field: f(), timeUnit: pick(['year', 'utcmonth']), range: [1, 5] } },
          { density: f(), groupby: [f()] },
          { timeUnit: pick(TIMEUNITS), field: f(), as: 'tu' },
          { aggregate: [{ op: 'mean', field: f(), as: 'm' }, { op: 'mean', field: f(), as: 'm2' }], groupby: [f()] },
          { extent: f(), param: 'ext' },
          { stack: f(), groupby: [f()], as: ['a', 'b'] },
          { loess: f(), on: f() },
        ])];
        break;
      case 14:
        spec.config = { ...(spec.config || {}), ...pick([{ view: { continuousWidth: 150 } }, { scale: { invalid: { color: { value: 'red' } } } }, { tooltipFormat: { numberFormat: '.2f' } }, { scale: { minSize: 20 } }, { bar: { minBandSize: 5 } }, { tick: { bandSize: 10 } }, { aria: false }, { legend: { orient: 'top' } }]) };
        break;
      default:
        if (chs.length) {
          const c = pick(chs);
          if (enc[c] && typeof enc[c] === 'object' && !Array.isArray(enc[c])) enc[c].bandPosition = pick([0, 0.2, 0.5, 1]);
        }
    }
  }
}

console.warn = () => {};
console.log = () => {};
const out = [];
for (let i = 0; i < count; i++) {
  const base = pick(corpus);
  let spec;
  try {
    spec = JSON.parse(fs.readFileSync(base.file, 'utf8'));
  } catch {
    continue;
  }
  mutate(spec);
  const name = `${base.name}#${i}`;
  try {
    const { spec: vg } = vl.compile(structuredClone(spec));
    out.push(JSON.stringify({ name, spec, vega: vg }));
  } catch (e) {
    out.push(JSON.stringify({ name, spec, error: String((e && e.message) || e) }));
  }
}
process.stdout.write(out.join('\n') + '\n');
