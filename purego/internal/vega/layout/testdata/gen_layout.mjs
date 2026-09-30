// Records the inputs and outputs of vega-view-transforms' ViewLayout and
// Overlap operators for a corpus of specifications.
//
//   NODE_PATH=<node_modules> [CORPUS=<dir of *.vg.json>] [DATA=<loader base dir>] \
//     node gen_layout.mjs | gzip -9 > layout.json.gz
//
// Every ViewLayout run is stored as the scenegraph subtree before and after
// the transform (item properties and bounds; only guide groups keep their
// children), the transform parameters, the view state it read, and the
// _resizeView calls it made. Text is measured with vega's width estimate.
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const vt = await import(require.resolve('vega-view-transforms'));
vega.textMetrics.canvas(false); // deterministic width estimate

const here = path.dirname(fileURLToPath(import.meta.url));
const corpusDir = process.env.CORPUS || path.join(here, '../../../../testdata/corpus/vega');
const dataDir = process.env.DATA || path.join(here, '../../../../testdata/data');
const loader = vega.loader({ mode: 'file', baseURL: dataDir });

const num = v => (typeof v === 'number' && !Number.isFinite(v) ? { $num: String(v) } : v);
const dumpBounds = b => (b ? [b.x1, b.y1, b.x2, b.y2].map(num) : undefined);
const GUIDE = new Set(['axis', 'legend', 'title']);
const SKIP = new Set(['mark', 'group', 'bounds', 'datum', 'pathCache', 'clip_id', 'image', 'context', 'zdirty', 'zitems', 'index', 'tooltip', 'items', 'shape', 'path', 'fill', 'stroke', 'strokeDash', '_bounds']);

function dumpDatum(d) {
  if (!d || typeof d !== 'object') return undefined;
  const o = {};
  for (const k of Object.keys(d)) {
    const v = d[k];
    if (typeof v === 'number') o[k] = num(v);
    else if (typeof v === 'string' || typeof v === 'boolean' || v === null) o[k] = v;
  }
  return o;
}

function dumpItem(item, mark, inGuide) {
  const out = {};
  for (const k of Object.keys(item)) {
    if (SKIP.has(k)) continue;
    const v = item[k];
    if (typeof v === 'function') continue;
    if (k === 'clip') { out.clip = typeof v === 'function' ? { path: v(null) } : v; continue; }
    if (typeof v === 'number') out[k] = num(v);
    else if (typeof v === 'string' || typeof v === 'boolean' || v === null) out[k] = v;
    else if (Array.isArray(v) && v.every(x => typeof x === 'string' || typeof x === 'number')) out[k] = v;
    else if (v instanceof Date) out[k] = { $date: v.getTime() };
  }
  // boundStroke only tests the truthiness of stroke and fill
  if (item.stroke) out.stroke = typeof item.stroke === 'string' ? item.stroke : '#000';
  if (item.fill) out.fill = typeof item.fill === 'string' ? item.fill : '#000';
  if (typeof item.clip === 'function') out.clip = { path: item.clip(null) };
  if (inGuide) out.datum = dumpDatum(item.datum);
  out.bounds = dumpBounds(item.bounds);
  if (mark.marktype === 'group') out.items = item.items.map(m => dumpMark(m, inGuide));
  return out;
}

function dumpMark(mark, inGuide) {
  inGuide = inGuide || GUIDE.has(mark.role);
  const out = {
    marktype: mark.marktype, name: mark.name, role: mark.role, interactive: mark.interactive,
    bounds: dumpBounds(mark.bounds),
  };
  if (typeof mark.clip === 'function') out.clip = { path: mark.clip(null) };
  else out.clip = mark.clip;
  if (inGuide || mark.marktype === 'group') out.items = mark.items.map(it => dumpItem(it, mark, inGuide));
  else out.items = [];
  return out;
}

const records = new Map(); // mark -> latest ViewLayout record
const overlaps = [];
let current = null;

const origLayout = vt.viewlayout.prototype.transform;
vt.viewlayout.prototype.transform = function (_, pulse) {
  const view = pulse.dataflow;
  const rec = {
    pre: dumpMark(_.mark, false),
    params: {
      layout: _.layout, legends: _.legends,
      autosize: _.autosize ? { type: _.autosize.type, contains: _.autosize.contains, resize: _.autosize.resize } : undefined,
    },
    view: { width: view._width, height: view._height, autosize: view._autosize, padding: view.padding() },
    sizes: [],
  };
  const orig = view._resizeView;
  view._resizeView = (...args) => { rec.sizes.push(args.map(a => Array.isArray(a) ? a.slice() : a)); };
  let rv;
  try {
    rv = origLayout.call(this, _, pulse);
  } finally {
    delete view._resizeView;
  }
  rec.post = dumpMark(_.mark, false);
  records.set(_.mark, rec);
  return rv;
};

const origOverlap = vt.overlap.prototype.transform;
vt.overlap.prototype.transform = function (_, pulse) {
  const source = pulse.materialize(pulse.SOURCE).source;
  const rec = {
    params: {
      method: _.method === true ? true : _.method, separation: _.separation,
      order: _.sort && _.sort.fields ? _.sort.fields[0] : undefined, hasSort: !!_.sort,
      bound: _.boundScale ? { range: _.boundScale.range(), orient: _.boundOrient, tolerance: num(_.boundTolerance) } : undefined,
    },
    pre: source && source.map(it => ({ bounds: dumpBounds(it.bounds), opacity: it.opacity, index: it.datum && it.datum.index })),
    markBoundsPre: source && source.length ? dumpBounds(source[0].mark.bounds) : undefined,
  };
  const rv = origOverlap.call(this, _, pulse);
  if (source && source.length) {
    rec.post = source.map(it => it.opacity);
    rec.markBounds = dumpBounds(source[0].mark.bounds);
    overlaps.push(rec);
  }
  return rv;
};

const files = [];
for (const f of fs.readdirSync(corpusDir).sort()) if (f.endsWith('.vg.json')) files.push({ name: f.replace(/\.vg\.json$/, ''), file: path.join(corpusDir, f) });
for (const f of fs.readdirSync(path.join(here, 'specs')).sort()) if (f.endsWith('.vg.json')) files.push({ name: 'x-' + f.replace(/\.vg\.json$/, ''), file: path.join(here, 'specs', f) });

const filter = process.argv[2] ? new RegExp(process.argv[2]) : null;
const SKIPSPEC = new Set(['contour-plot', 'density-heatmaps', 'volcano-contours', 'clock', 'pi-monte-carlo', 'platformer', 'pacman', 'watch', 'hypothetical-outcome-plots']);
const cases = [];
const MAX_MARKS = 16;
for (const { name, file } of files) {
  if (filter && !filter.test(name)) continue;
  if (SKIPSPEC.has(name)) continue;
  const spec = JSON.parse(fs.readFileSync(file, 'utf8'));
  if (!/"axes"|"legends"|"title"|"layout"|"autosize"|"role"|"overlap"|"labelOverlap"/.test(JSON.stringify(spec))) continue;
  records.clear();
  overlaps.length = 0;
  try {
    vega.setRandom(vega.randomLCG(42));
    const view = new vega.View(vega.parse(spec), { renderer: 'none', loader });
    view.logLevel(vega.Error);
    await view.runAsync();
    await view.finalize();
  } catch (e) {
    process.stderr.write(`skip ${name}: ${e.message}\n`);
    continue;
  }
  const recs = [...records.values()];
  // keep the records with the most content first: root, then guide-bearing groups
  const kept = recs.slice(-MAX_MARKS);
  cases.push({ name, layouts: kept, overlaps: overlaps.slice(0, MAX_MARKS) });
}
process.stdout.write(JSON.stringify(cases));
