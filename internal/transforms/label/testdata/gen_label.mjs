// Records golden vectors for the label package by running upstream vega-label
// (through the vega dataflow) with:
//   - a deterministic text width: textMetrics.width = 0.6 * fontSize * length
//   - a stand-in `canvas` module (canvas_hooks.mjs) so marks can be rasterized
// Usage:
//   NODE_PATH=<node_modules> node gen_label.mjs > label.json
import { register, createRequire } from 'node:module';
import path from 'node:path';
register('./canvas_hooks.mjs', import.meta.url);
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const {label: Label} = await import(require.resolve('vega-label'));
const {Bounds, Marks} = vega;

vega.textMetrics.width = (item, text) => 0.6 * (item.fontSize ?? 11) * String(text).length;

const box = (x1, y1, x2, y2) => ({x1, y1, x2, y2});

// ---- scenario construction ----
// Base items are plain scenegraph-shaped objects; `spec` mirrors what the Go
// side needs to rebuild them.
function rectBase(x, y, w, h, extra = {}) {
  const item = {x, y, width: w, height: h, fill: '#4c78a8', strokeWidth: 1,
    bounds: box(x, y, x + w, y + h), ...extra};
  item.mark = {marktype: 'rect', items: [item]};
  return item;
}
function pointBase(marktype, x, y) {
  const item = {x, y, bounds: box(x - 3, y - 3, x + 3, y + 3), fill: '#000'};
  item.mark = {marktype, items: [item]};
  return item;
}
function groupBase(subtype, pts, stroke) {
  const items = pts.map(p => ({...p, fill: '#888', stroke: stroke ? '#000' : undefined, strokeWidth: 1, interpolate: 'linear'}));
  const mark = {marktype: subtype, items};
  items.forEach(i => i.mark = mark);
  const group = {items: [mark], bounds: box(0, 0, 100, 100), mark: {marktype: 'group'}};
  mark.group = group;
  return group;
}
const textItem = (base, text, fontSize, x, y) =>
  ({datum: base, text, fontSize, x, y, mark: {marktype: 'text'}});

// ---- rendering shapes for the Go test rasterizer ----
function recordItems(items, interior, out) {
  if (!items.length) return;
  const type = items[0].mark.marktype;
  if (type === 'group') {
    items.forEach(g => g.items.forEach(m => recordItems(m.items, interior, out)));
    return;
  }
  const list = interior ? items.map(prepare) : items;
  const dump = subs => subs.map(s => ({pts: s.pts.map(p => p.slice()), closed: s.closed}));
  const ctx = {
    subs: [], lineWidth: 1,
    beginPath() { this.subs = []; },
    moveTo(x, y) { this.subs.push({pts: [[x, y]], closed: false}); },
    lineTo(x, y) { if (!this.subs.length) return this.moveTo(x, y); this.subs.at(-1).pts.push([x, y]); },
    closePath() { if (this.subs.length) this.subs.at(-1).closed = true; },
    rect(x, y, w, h) { this.subs.push({pts: [[x, y], [x + w, y], [x + w, y + h], [x, y + h]], closed: true}); },
    fill() { out.push({fill: true, subs: dump(this.subs)}); },
    stroke() { out.push({stroke: true, lw: this.lineWidth, subs: dump(this.subs)}); },
    setLineDash() {},
  };
  Marks[type].draw(ctx, {items: list});
}
function prepare(source) {
  const item = {...source};
  if ((item.stroke && item.strokeOpacity !== 0) || (item.fill && item.fillOpacity !== 0)) {
    return {...item, strokeOpacity: 1, stroke: '#000', fillOpacity: 0};
  }
  return item;
}
function shapes(items) {
  const normal = [], outline = [];
  recordItems(items, false, normal);
  recordItems(items, true, outline);
  return {normal, outline};
}

function baseSpec(b) {
  const mt = b.mark.marktype;
  const s = {marktype: mt, x: b.x, y: b.y, bounds: b.bounds};
  if (mt === 'group') s.marks = b.items.map(m => ({marktype: m.marktype,
    points: m.items.map(p => ({x: p.x, y: p.y, x2: p.x2, y2: p.y2}))}));
  return s;
}

function run(sc) {
  const df = new vega.Dataflow();
  const src = df.add(new vega.transforms.collect());
  const params = {size: sc.size, pulse: src};
  for (const k of ['anchor', 'offset', 'padding', 'lineAnchor', 'markIndex', 'avoidBaseMark', 'method', 'sortKey'])
    if (sc[k] !== undefined && k !== 'sortKey') params[k] = sc[k];
  if (sc.sortKey) {
    const cmp = (a, b) => a.sortv - b.sortv; cmp.fields = [];
    params.sort = cmp;
  }
  if (sc.avoid) params.avoidMarks = sc.avoid.map(a => a.items);
  const lbl = df.add(new Label(params));
  df.pulse(src, df.changeset().insert(sc.texts)).run();
  const out = lbl.pulse ? lbl.pulse.source : sc.texts;
  return out;
}

const scenarios = [];
const add = sc => scenarios.push(sc);
const P = (marktype, x, y) => pointBase(marktype, x, y);

// 1. points, no base mark bitmap (marktype undefined), avoid data points
{
  const texts = [];
  const pts = [[30, 40], [35, 42], [120, 90], [60, 20], [5, 5], [190, 110], [100, 60]];
  pts.forEach(([x, y], i) => texts.push({...textItem(undefined, 'lbl' + i, 10, x, y), sortv: i}));
  add({name: 'bare-points', size: [200, 120], texts});
}
// 2. rect base marks with defaults
{
  const texts = [];
  [[10, 10, 40, 30], [30, 25, 40, 30], [120, 60, 50, 40], [0, 0, 20, 20], [170, 90, 40, 40], [300, 50, 10, 10]]
    .forEach(([x, y, w, h], i) => {
      const b = rectBase(x, y, w, h);
      texts.push({...textItem(b, 'bar' + i, 11, x, y), sortv: -i});
    });
  add({name: 'rects-default', size: [200, 120], texts});
  add({name: 'rects-inside', size: [200, 120], anchor: ['middle'], offset: [-1], texts: texts.map(t => ({...t}))});
  add({name: 'rects-avoid-base-false', size: [200, 120], avoidBaseMark: false, offset: [2, 4], anchor: ['top', 'bottom', 'right'], texts: texts.map(t => ({...t}))});
  add({name: 'rects-sorted-padding', size: [200, 120], padding: 20, sortKey: true, offset: [3], texts: texts.map(t => ({...t}))});
  add({name: 'rects-null-padding', size: [200, 120], padding: null, texts: texts.map(t => ({...t}))});
}
// 3. symbol marks, avoid another mark
{
  const texts = [];
  const avoid = rectBase(40, 40, 80, 30);
  [[50, 55], [100, 20], [150, 80], [45, 100]].forEach(([x, y], i) => {
    const b = rectBase(x - 3, y - 3, 6, 6);
    texts.push({...textItem(b, 'sym' + i, 12, x, y), sortv: i});
  });
  add({name: 'symbols-avoid', size: [200, 120], avoid: [{items: [avoid]}], texts});
}
// 4. group line: start/end anchors
{
  const line = groupBase('line', [{x: 10, y: 20}, {x: 90, y: 50}, {x: 180, y: 30}], true);
  const line2 = groupBase('line', [{x: 10, y: 90}, {x: 90, y: 80}, {x: 190, y: 100}], true);
  for (const la of ['start', 'end']) {
    const texts = [line, line2].map((g, i) => ({...textItem(g, 'series' + i, 11, 0, 0), sortv: i}));
    add({name: 'group-line-' + la, size: [200, 120], lineAnchor: la, texts, groups: true});
  }
}
// 5. group area with each method
{
  for (const method of ['naive', 'reduced-search', 'floodfill']) for (const avoidBase of [true, false]) {
    const a1 = groupBase('area', [{x: 10, y: 20, y2: 60}, {x: 60, y: 30, y2: 70}, {x: 110, y: 40, y2: 100}, {x: 160, y: 20, y2: 60}]);
    const a2 = groupBase('area', [{x: 10, y: 60, y2: 80}, {x: 60, y: 70, y2: 95}, {x: 110, y: 100, y2: 115}, {x: 160, y: 60, y2: 80}]);
    const texts = [a1, a2].map((g, i) => ({...textItem(g, 'area' + i, 11, 0, 0), sortv: i}));
    add({name: `group-area-${method}-${avoidBase ? 'avoid' : 'overlap'}`, size: [180, 120], method,
      avoidBaseMark: avoidBase, texts, groups: true});
  }
}

const result = scenarios.map(sc => {
  // rasterization handles: index -> shapes, for base items and avoid marks
  const handles = {};
  const bases = sc.texts.map(t => t.datum);
  const inputs = sc.texts.map(t => ({text: t.text, fontSize: t.fontSize, x: t.x, y: t.y, sortv: t.sortv,
    base: t.datum ? baseSpec(t.datum) : null}));
  const baseShapes = bases.map(b => b ? shapes([b]) : null);
  const avoidShapes = (sc.avoid || []).map(a => shapes(a.items));
  const out = run(sc);
  return {
    name: sc.name, size: sc.size, anchor: sc.anchor, offset: sc.offset,
    padding: sc.padding === undefined ? 0 : sc.padding, // null => unbounded
    paddingNull: sc.padding === null,
    lineAnchor: sc.lineAnchor, markIndex: sc.markIndex || 0, avoidBaseMark: sc.avoidBaseMark,
    method: sc.method, sorted: !!sc.sortKey, inputs, baseShapes, avoidShapes,
    output: sc.texts.map(t => ({x: t.x, y: t.y, opacity: t.opacity, align: t.align, baseline: t.baseline,
      // outputs overwrite x/y, so recompute placed flag via opacity
    })),
  };
});
console.log(JSON.stringify(result, (k, v) => (v === undefined ? null : v), 1));
