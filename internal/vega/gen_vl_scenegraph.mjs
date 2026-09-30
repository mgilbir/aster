// Records, for Vega-Lite specifications, the Vega specification upstream's
// Vega-Lite compiler produces and the scenegraph upstream Vega renders from it,
// so the runtime can be compared on real compiled charts independently of the
// Go Vega-Lite compiler.
//
// usage: NODE_PATH=... VEGA_DATA_DIRS=... node gen_vl_scenegraph.mjs outdir spec.vl.json...
process.env.TZ = 'UTC';
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const vl = await import(require.resolve('vega-lite'));

const FIXED_NOW = 1700000000000;
const RealDate = Date;
globalThis.Date = class extends RealDate {
  constructor(...a) { if (a.length) super(...a); else super(FIXED_NOW); }
  static now() { return FIXED_NOW; }
};

const dataDirs = (process.env.VEGA_DATA_DIRS || '').split(':').filter(Boolean);
const loader = {
  async load(uri) {
    const rel = uri.replace(/^data\//, '');
    for (const d of dataDirs) {
      const f = path.join(d, rel);
      if (fs.existsSync(f)) return fs.readFileSync(f, 'utf8');
    }
    throw new Error('not found: ' + uri);
  },
  async sanitize(uri) { return { href: uri }; },
  async http(uri) { return this.load(uri); },
  async file(uri) { return this.load(uri); },
};

const SKIP = new Set(['mark', 'datum', 'bounds', 'context', 'items', 'group', 'source', 'exit', 'zdirty', 'pathCache']);
function num(v) {
  if (typeof v === 'number') {
    if (Number.isNaN(v)) return 'NaN';
    if (v === Infinity) return 'Infinity';
    if (v === -Infinity) return '-Infinity';
  }
  return v;
}
function conv(v, depth = 0) {
  if (v === undefined || typeof v === 'function') return undefined;
  if (v === null) return null;
  if (typeof v === 'number') return num(v);
  if (v instanceof Date) return { $date: v.getTime() };
  if (Array.isArray(v)) return v.map(x => conv(x, depth + 1) ?? null);
  if (typeof v === 'object') {
    if (depth > 6) return null;
    const o = {};
    for (const k of Object.keys(v)) { const c = conv(v[k], depth + 1); if (c !== undefined) o[k] = c; }
    return o;
  }
  return v;
}
const bounds = b => [num(b.x1), num(b.y1), num(b.x2), num(b.y2)];
function item(it, group) {
  const o = {};
  for (const k of Object.keys(it)) {
    if (SKIP.has(k) || k[0] === '_') continue;
    const c = conv(it[k]);
    if (c !== undefined) o[k] = c;
  }
  o.bounds = bounds(it.bounds);
  if (group) o.items = it.items.map(mark);
  return o;
}
function mark(m) {
  const g = m.marktype === 'group';
  const o = { marktype: m.marktype, interactive: m.interactive, clip: !!m.clip, zindex: m.zindex || 0,
    bounds: bounds(m.bounds), items: (m.items || []).map(i => item(i, g)) };
  if (m.name) o.name = m.name;
  if (m.role) o.role = m.role;
  if (m.aria != null) o.aria = m.aria;
  if (m.description) o.description = m.description;
  return o;
}

process.on('uncaughtException', e => { console.error('uncaught', e && e.message); });
const [outdir, ...files] = process.argv.slice(2);
fs.mkdirSync(outdir, { recursive: true });
for (const f of files) {
  const name = path.basename(f).replace(/\.json$/, '').replace(/\.vl$/, '');
  const out = {};
  vega.setRandom(vega.randomLCG(12345));
  try {
    const spec = JSON.parse(fs.readFileSync(f, 'utf8'));
    const vg = vl.compile(spec).spec;
    out.vega = vg;
    const view = new vega.View(vega.parse(vg), { loader, renderer: 'none', logLevel: vega.None });
    await view.runAsync();
    await view.runAsync();
    const pad = view.padding();
    out.width = Math.max(0, view._viewWidth + pad.left + pad.right);
    out.height = Math.max(0, view._viewHeight + pad.top + pad.bottom);
    out.origin = [pad.left + view._origin[0], pad.top + view._origin[1]];
    out.background = view.background() ?? null;
    out.scene = mark(view.scenegraph().root);
  } catch (e) {
    out.error = String(e && e.message || e);
  }
  fs.writeFileSync(path.join(outdir, name + '.json'), JSON.stringify(out));
}
