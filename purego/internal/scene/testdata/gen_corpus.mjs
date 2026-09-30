// Records golden vectors for the scene and svg packages from upstream Vega.
//
// For every specification it renders a view headlessly, dumps the resulting
// scenegraph (every item property the renderer reads, plus the bounds Vega
// computed) and the SVG from toSVG(), and writes one JSON document:
//
//   {"corpus": [{"name", "view": {...}, "scene": <root mark>, "svg": "..."}]}
//
// Usage (see DESIGN.md):
//   NODE_PATH=purego/testdata/oracle-node/node_modules \
//   FIXTURES=<dir with vega spec files *.vg.json and data/> \
//   node gen_corpus.mjs [name-regexp] > corpus.json
//
// Specifications come from ./specs/*.vg.json (checked in) and, when FIXTURES is
// set, <FIXTURES>/specs/*.vg.json. Text is measured with vega's estimate
// (no canvas), which the Go tests mirror.
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const aria = await import(path.join(process.env.NODE_PATH, 'vega-scenegraph/src/util/aria.js'));

const here = path.dirname(fileURLToPath(import.meta.url));
const filter = process.argv[2] ? new RegExp(process.argv[2]) : null;

const roots = [];
const specs = [];
function addDir(dir) {
  if (!fs.existsSync(dir)) return;
  for (const f of fs.readdirSync(dir).sort()) {
    if (f.endsWith('.vg.json')) specs.push({ name: f.replace(/\.vg\.json$/, ''), file: path.join(dir, f) });
  }
}
addDir(path.join(here, 'specs'));
if (process.env.FIXTURES) addDir(path.join(process.env.FIXTURES, 'specs'));
roots.push(path.join(here, 'specs'));
if (process.env.FIXTURES) roots.push(process.env.FIXTURES, path.join(process.env.FIXTURES, 'specs'));

const loaders = roots.map((root) => vega.loader({ mode: 'file', baseURL: root }));
// Image and href URLs are sanitized with the spec's own loader options
// (usermeta.loaderOptions: baseURL, target, rel, defaultProtocol; none by
// default), as a browser or vl-convert would; only data loading resolves
// against the spec roots.
function makeLoader(urlOptions) {
  const plainLoader = vega.loader(urlOptions);
  return {
  ...loaders[0],
  sanitize: (uri, options) => (options && (options.context === 'image' || options.context === 'href')
    ? plainLoader.sanitize(uri, options)
    : loaders[0].sanitize(uri, options)),
  async load(uri, options) {
    let refusal;
    for (const l of loaders) {
      try { return await l.load(uri, options); } catch (e) { refusal = e; }
    }
    throw refusal;
  },
  };
}

// Specs whose output depends on the clock/canvas, or that upstream cannot render headlessly.
const SKIP = new Set(['contour-plot', 'density-heatmaps', 'volcano-contours', 'clock', 'pi-monte-carlo', 'platformer', 'pacman', 'watch', 'hypothetical-outcome-plots']);

// A gradient is dumped once with an id and referenced afterwards, so that the
// Go side can keep object identity (gradient ids are per object).
let gradients;

function dumpNum(v) {
  if (typeof v === 'number' && !Number.isFinite(v)) return { $num: String(v) };
  return v;
}

const SKIP_KEYS = new Set(['mark', 'group', 'bounds', 'datum', 'pathCache', 'clip_id', 'image', 'context', 'zdirty', 'zitems', 'index', 'tooltip']);

function dumpValue(v) {
  if (typeof v === 'number') return dumpNum(v);
  if (v && typeof v === 'object' && v.gradient) {
    let id = gradients.get(v);
    if (id !== undefined) return { $gref: id };
    id = gradients.size;
    gradients.set(v, id);
    return {
      $gid: id, gradient: v.gradient, id: v.id, x1: dumpNum(v.x1), y1: dumpNum(v.y1), x2: dumpNum(v.x2), y2: dumpNum(v.y2),
      r1: dumpNum(v.r1), r2: dumpNum(v.r2), stops: (v.stops || []).map((s) => ({ offset: s.offset, color: s.color })),
    };
  }
  if (Array.isArray(v)) return v.map(dumpValue);
  if (typeof v === 'function') return undefined;
  if (v instanceof Date) return { $date: v.getTime() };
  return v;
}

// recordOps runs a path generator against a recording context and returns the
// canvas calls it made, at full precision. The Go side replays them into its
// own contexts (bounds) and uses `path` (the generator's own string) for SVG.
function recordOps(call) {
  const ops = [];
  const ctx = {
    beginPath() {},
    moveTo(x, y) { ops.push(['M', x, y]); },
    lineTo(x, y) { ops.push(['L', x, y]); },
    quadraticCurveTo(a, b, c, d) { ops.push(['Q', a, b, c, d]); },
    bezierCurveTo(a, b, c, d, e, f) { ops.push(['C', a, b, c, d, e, f]); },
    arc(x, y, r, a0, a1, ccw) { ops.push(['A', x, y, r, a0, a1, ccw ? 1 : 0]); },
    rect(x, y, w, h) { ops.push(['R', x, y, w, h]); },
    closePath() { ops.push(['Z']); },
  };
  call(ctx);
  return ops.map((op) => op.map(dumpNum));
}

function dumpBounds(b) {
  return b ? [b.x1, b.y1, b.x2, b.y2].map(dumpNum) : undefined;
}

function dumpItem(item, mark) {
  const out = {};
  for (const k of Object.keys(item)) {
    if (SKIP_KEYS.has(k)) continue;
    const v = item[k];
    if (k === 'items' && mark.marktype === 'group') continue;
    if (k === 'clip' && typeof v === 'function') { out.clip = { path: v(null), ops: recordOps((c) => v(c)) }; continue; }
    if (k === 'shape' && typeof v === 'function') { out.shape = dumpShape(item.mark.shape || v, item); continue; }
    const d = dumpValue(v);
    if (d !== undefined) out[k] = d;
  }
  if (mark.marktype === 'shape' && item.mark.shape && !out.shape) {
    out.shape = dumpShape(item.mark.shape, item);
  }
  out.bounds = dumpBounds(item.bounds);
  if (mark.marktype === 'group') out.items = item.items.map(dumpMark);
  return out;
}

function dumpShape(shape, item) {
  const path = shape.context(null)(item);
  const ops = recordOps((c) => shape.context(c)(item));
  shape.context(null);
  return { path, ops };
}

function dumpMark(mark) {
  const out = {
    marktype: mark.marktype, name: mark.name, role: mark.role, interactive: mark.interactive,
    zindex: mark.zindex, aria: mark.aria, description: mark.description,
  };
  if (mark.role === 'axis' || mark.role === 'legend') {
    // Axis and legend captions are computed from the guide's scale
    // (item.context), which the Go side cannot see: record the result.
    const attrs = aria.ariaMarkAttributes(mark);
    if (attrs && attrs['aria-label'] && !mark.description && !(mark.items[0] && mark.items[0].description)) {
      out.guideCaption = attrs['aria-label'];
    }
  }
  if (typeof mark.clip === 'function') out.clip = { path: mark.clip(null), ops: recordOps((c) => mark.clip(c)) };
  else out.clip = mark.clip;
  out.bounds = dumpBounds(mark.bounds);
  out.items = mark.items.map((it) => dumpItem(it, mark));
  return out;
}

// rebound recomputes bounds from scratch with upstream's own bound functions, in
// the order of vega's Bound transform (children first, nested marks share one
// box, then the clip region). The live bounds vega records after layout can be
// stale relative to items that layout moved afterwards, so the Go tests compare
// against these ("rbounds"), which depend only on the final items.
function rebound(mark) {
  const entry = vega.Marks[mark.marktype];
  if (mark.marktype === 'group') for (const g of mark.items) for (const child of g.items) rebound(child);
  if (entry.nested) {
    if (mark.items.length) {
      const b = entry.bound(mark.bounds.clear(), mark);
      for (const it of mark.items) it.bounds.clear().union(b);
    } else {
      mark.bounds.clear();
    }
  } else {
    mark.bounds.clear();
    for (const it of mark.items) mark.bounds.union(entry.bound(it.bounds.clear(), it));
  }
  vega.boundClip(mark);
}

function attachRebound(dumped, mark) {
  dumped.rbounds = dumpBounds(mark.bounds);
  mark.items.forEach((it, i) => {
    dumped.items[i].rbounds = dumpBounds(it.bounds);
    if (mark.marktype === 'group') it.items.forEach((child, j) => attachRebound(dumped.items[i].items[j], child));
  });
}

function pin() {
  vega.setRandom(vega.randomLCG(42));
  Date.now = () => 1767225600000;
}

process.on('unhandledRejection', (e) => { process.stderr.write(`unhandled: ${e && e.message}\n`); });
const corpus = [];
for (const { name, file } of specs) {
  if (filter && !filter.test(name)) continue;
  if (SKIP.has(name)) continue;
  try {
    const spec = JSON.parse(fs.readFileSync(file, 'utf8'));
    pin();
    const urlOptions = (spec.usermeta && spec.usermeta.loaderOptions) || {};
    const view = new vega.View(vega.parse(spec), { renderer: 'none', loader: makeLoader(urlOptions) });
    view.logLevel(vega.Error);
    await view.runAsync();
    const pad = view.padding();
    const info = {
      width: Math.max(0, view._viewWidth + pad.left + pad.right),
      height: Math.max(0, view._viewHeight + pad.top + pad.bottom),
      origin: [pad.left + view._origin[0], pad.top + view._origin[1]],
      background: view.background(),
    };
    gradients = new Map();
    const root = view.scenegraph().root;
    const scene = dumpMark(root);
    vega.resetSVGDefIds();
    const svg = await view.toSVG();
    rebound(root);
    attachRebound(scene, root);
    corpus.push({ name, view: info, urlOptions, scene, svg });
    await view.finalize();
  } catch (e) {
    process.stderr.write(`skip ${name}: ${e.message}\n`);
  }
}
process.stdout.write(JSON.stringify({ corpus }));
