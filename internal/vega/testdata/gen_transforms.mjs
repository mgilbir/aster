// Records upstream Vega's answer for every transform with each of its
// optional parameters absent and null, and for the hand-written cases of
// transform semantics below, into transforms.node.json, which
// TestTransformsMatchNode (transforms_node_test.go) replays against the
// engine.
//
// Why the sweep: vega-parser never fills a definition's defaults (an absent
// parameter is undefined; parseParameter returns nothing for it), so what an
// absent parameter does is whatever the transform's own code does with
// undefined (`_.x || d`, `_.x !== false`, `_.x == null ? …`), and what null
// does is the same code with null, or the TypeError of `null.map` where the
// definition says the parameter is an array. The engine's glue must follow
// the code, not the definition; this sweep holds every parameter of every
// transform to it.
//
// Regenerate through the vector manifest, which fingerprints this generator,
// the box canvas it imports and the output, so that none can drift:
//
//   go run ./internal/cmd/vectors -write -run internal/vega/testdata/transforms.node.json
//
// For each case it records the first error upstream logs or throws, the
// distinct warnings in the order first logged, and the named data sets as
// JSON.stringify writes them (a mark's items reduced to the fields named).
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { boxContext } from '../../../testdata/oracle-node/wordcloud-box.mjs';

if (process.env.TZ !== 'UTC') {
  console.error('run with TZ=UTC');
  process.exit(2);
}
const self = fileURLToPath(import.meta.url);
const repo = path.resolve(path.dirname(self), '../../..');
const require = createRequire(path.join(path.resolve(process.env.NODE_PATH), 'x.js'));

// Text is measured as the oracle measures it (testdata/oracle-node/oracle.mjs):
// fontconfig sees only the repository's fonts, and every family resolves to
// DejaVu Sans. The engine's side measures with the same faces.
{
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aster-gen-fonts-'));
  const conf = path.join(dir, 'fonts.conf');
  fs.writeFileSync(
    conf,
    `<?xml version="1.0"?>\n<!DOCTYPE fontconfig SYSTEM "fonts.dtd">\n<fontconfig>\n` +
      `  <dir>${path.join(repo, 'internal/fonts/dejavu')}</dir>\n` +
      `  <cachedir>${path.join(dir, 'cache')}</cachedir>\n</fontconfig>\n`,
  );
  process.env.FONTCONFIG_FILE = conf;
  process.on('exit', () => fs.rmSync(dir, { recursive: true, force: true }));
}
const canvas = require('canvas');
for (const [file, weight, style] of [['', 'normal', 'normal'], ['-Bold', 'bold', 'normal'], ['-Oblique', 'normal', 'italic'], ['-BoldOblique', 'bold', 'italic']]) {
  canvas.registerFont(path.join(repo, `internal/fonts/dejavu/DejaVuSans${file}.ttf`), { family: 'DejaVu Sans', weight, style });
}
{
  const proto = canvas.CanvasRenderingContext2D.prototype;
  const fontProp = Object.getOwnPropertyDescriptor(proto, 'font');
  Object.defineProperty(proto, 'font', {
    ...fontProp,
    set(v) {
      const m = /^(.*?\d[\d.]*(?:px|pt|em|rem|%)(?:\s*\/\s*\S+)?\s+)(.+)$/.exec(String(v));
      fontProp.set.call(this, m ? m[1] + '"DejaVu Sans"' : String(v));
    },
  });
}
// The box canvas: while a case asks for it (`canvas: 'box'`), the canvas the
// wordcloud layout draws its sprites on (the only one wider than a pixel a
// render makes: text is measured on vega-scenegraph's one-pixel context) is
// a deterministic one: text measures 0.6 em per code point and every word
// paints a solid (rotated) box 0.8 em above and 0.2 em below its baseline,
// grown by half the line width when stroked. The engine's
// wordcloud.BoxRenderer is the same model, so a box case compares the
// layout (its spiral, collisions, sprite cutting and the transform's
// parameters) without the platform's glyph rasterization. It is the oracle's
// (testdata/oracle-node/wordcloud-box.mjs).
let boxWordcloud = false;
{
  const getContext = canvas.Canvas.prototype.getContext;
  canvas.Canvas.prototype.getContext = function (...args) {
    return boxWordcloud && this.width > 1 ? boxContext(this.width, this.height) : getContext.apply(this, args);
  };
}
const vega = await import(require.resolve('vega'));

// -- data ----------------------------------------------------------------------

const rows = [
  { id: 1, a: 1, b: 2.5, c: 'x', k: 'p', t: 'the quick brown fox', when: '2020-01-05' },
  { id: 2, a: 3, b: 0.5, c: 'y', k: 'q', t: 'jumps over the lazy dog', when: '2020-03-17' },
  { id: 3, a: 2, b: 4, c: 'x', k: 'q', t: 'the dog barks', when: '2021-07-01' },
  { id: 4, a: 5, b: 1.5, c: 'y', k: 'p', t: 'a fox', when: '2021-11-30' },
  { id: 5, a: 4, b: 3, c: 'x', k: 'r', t: 'quick quick', when: '2022-02-14' },
  { id: 6, a: 2, b: 2, c: 'z', k: 'p', t: 'lazy', when: '2022-06-21' },
];
const tree = [
  { id: 'r', parent: null, v: 0 },
  { id: 'a', parent: 'r', v: 3 },
  { id: 'b', parent: 'r', v: 5 },
  { id: 'c', parent: 'a', v: 2 },
  { id: 'd', parent: 'a', v: 4 },
  { id: 'e', parent: 'b', v: 1 },
];
const dates = { parse: { when: 'date' } };

// -- the sweep -------------------------------------------------------------------

// base gives, for each transform, a data pipeline using it with every
// parameter set that the sweep varies, the index of the transform in the
// pipeline, the data sets to record and, where a field's value depends on
// text measurement or animation, the fields kept.
const bases = {
  aggregate: { tx: { groupby: ['c'], ops: ['sum', 'argmax', 'exponential'], fields: ['a', 'b', 'a'], as: ['s', 'm', 'e'], aggregate_params: [null, null, 0.5], drop: false, cross: true, key: 'c' } },
  bin: { tx: { field: 'a', interval: true, anchor: 0.5, maxbins: 4, base: 10, divide: [5, 2], extent: [0, 6], span: 6, step: 2, steps: [1, 2], minstep: 0.5, nice: true, name: 'bb', as: ['lo', 'hi'] } },
  collect: { tx: { sort: { field: 'a', order: 'descending' } } },
  contour: {
    tx: { size: [20, 15], x: { expr: 'datum.a * 3' }, y: { expr: 'datum.b * 3' }, weight: 'a', cellSize: 4, bandwidth: 4, count: 3, nice: true, thresholds: [0.004, 0.008], smooth: false },
  },
  countpattern: { tx: { field: 't', case: 'upper', pattern: '[a-z]+', stopwords: 'the|a', as: ['w', 'n'] } },
  cross: { tx: { filter: 'datum.a.a < datum.b.a', as: ['l', 'r'] }, keep: ['l', 'r'] },
  density: { pre: [], tx: { extent: [0, 6], steps: 4, minsteps: 3, maxsteps: 8, method: 'cdf', distribution: { function: 'normal', mean: 2, stdev: 1.5 }, as: ['x', 'y'] }, source: false },
  dotbin: { tx: { field: 'b', groupby: ['c'], step: 1, smooth: true, as: 'dot' } },
  extent: { tx: { field: 'a' }, signal: true },
  filter: { tx: { expr: 'datum.a > 2' } },
  flatten: { values: [{ id: 1, xs: [1, 2], ys: ['a'] }, { id: 2, xs: [], ys: ['b', 'c'] }], tx: { fields: ['xs', 'ys'], index: 'i', as: ['x', 'y'] } },
  fold: { tx: { fields: ['a', 'b'], as: ['key0', 'val0'] } },
  formula: { tx: { expr: 'datum.a * 2', as: 'aa', initonly: true } },
  geojson: { tx: { fields: ['a', 'b'], geojson: null }, signal: true },
  geopath: { tx: { projection: 'proj', field: 'g', pointRadius: 3, as: 'p' }, values: 'geo' },
  geopoint: { tx: { projection: 'proj', fields: ['a', 'b'], as: ['px', 'py'] } },
  // a shape is a function, which JSON leaves out
  geoshape: { tx: { projection: 'proj', field: 'g', pointRadius: 3, as: 'sh' }, values: 'geo', drop: ['sh', 'shape'] },
  graticule: { tx: { extent: [[-30, -20], [30, 20]], extentMajor: [[-30, -20], [30, 20]], extentMinor: [[-20, -10], [20, 10]], step: [10, 10], stepMajor: [30, 30], stepMinor: [10, 10], precision: 5 }, values: 'none' },
  identifier: { tx: { as: 'uid' } },
  impute: { values: [{ x: 0, y: 1, c: 'a' }, { x: 1, y: 2, c: 'a' }, { x: 0, y: 3, c: 'b' }, { x: 2, y: 4, c: 'b' }], tx: { field: 'y', key: 'x', keyvals: [5], groupby: ['c'], method: 'mean', value: 9 } },
  joinaggregate: { tx: { groupby: ['c'], fields: ['a', 'b'], ops: ['sum', 'max'], as: ['s', 'm'], key: 'c' } },
  kde: { tx: { groupby: ['c'], field: 'a', cumulative: true, counts: true, bandwidth: 0.5, extent: [0, 6], resolve: 'shared', steps: 4, minsteps: 3, maxsteps: 6, as: ['x', 'y'] } },
  kde2d: { tx: { size: [30, 20], x: { expr: 'datum.a * 5' }, y: { expr: 'datum.b * 5' }, weight: 'a', groupby: ['c'], cellSize: 4, bandwidth: [6, 6], counts: true, as: 'grid' }, drop: ['grid'] },
  linkpath: {
    values: [{ source: { x: 1, y: 2 }, target: { x: 5, y: 7 }, sx: 0, sy: 1, tx: 9, ty: 4 }],
    tx: { sourceX: 'sx', sourceY: 'sy', targetX: 'tx', targetY: 'ty', orient: 'horizontal', shape: 'curve', require: null, as: 'pth' },
  },
  loess: { tx: { x: 'a', y: 'b', groupby: ['c'], bandwidth: 0.8, as: ['lx', 'ly'] } },
  lookup: { tx: { from: 'other', key: 'c', fields: ['c'], values: ['label'], as: ['lab'], default: 'none' } },
  pie: { tx: { field: 'a', startAngle: 1, endAngle: 4, sort: true, as: ['s0', 's1'] } },
  pivot: { tx: { groupby: ['k'], field: 'c', value: 'a', op: 'max', limit: 2, key: 'k' } },
  project: { tx: { fields: ['a', 'c'], as: ['aa', 'cc'] } },
  quantile: { tx: { groupby: ['c'], field: 'a', probs: [0.25, 0.5], step: 0.5, as: ['p', 'q'] } },
  regression: { tx: { x: 'a', y: 'b', groupby: ['c'], method: 'poly', order: 2, extent: [0, 6], params: true, as: ['rx', 'ry'] } },
  sample: { tx: { size: 3 } },
  sequence: { tx: { start: 1, stop: 9, step: 3, as: 'n' }, values: 'none' },
  stack: { tx: { field: 'a', groupby: ['c'], sort: { field: 'b' }, offset: 'normalize', as: ['lo', 'hi'] } },
  timeunit: {
    format: dates,
    tx: { field: 'when', interval: true, units: ['year', 'month'], step: 2, maxbins: 10, extent: [0, 1700000000000], inferUnits: false, timezone: 'utc', as: ['u0', 'u1'] },
  },
  window: { tx: { sort: { field: 'a' }, groupby: ['c'], ops: ['rank', 'lag', 'sum'], params: [null, 2, null], aggregate_params: [null, null, null], fields: [null, 'a', 'b'], as: ['r', 'l', 's'], frame: [-1, 1], ignorePeers: true } },
  voronoi: { tx: { x: 'a', y: 'b', size: [10, 10], extent: [[0, 0], [8, 8]], as: 'cell' } },
  force: {
    tx: {
      static: true,
      restart: false,
      iterations: 20,
      alpha: 0.8,
      alphaMin: 0.01,
      alphaTarget: 0.1,
      velocityDecay: 0.3,
      forces: [
        { force: 'center', x: 50, y: 40 },
        { force: 'collide', radius: 3, strength: 0.5, iterations: 2 },
        { force: 'nbody', strength: -10, theta: 0.8, distanceMin: 2, distanceMax: 100 },
        { force: 'link', links: 'links', id: 'id', distance: 15, strength: 0.4, iterations: 2 },
        { force: 'x', strength: 0.2, x: 'a' },
        { force: 'y', strength: 0.2, y: 'b' },
      ],
      as: ['fx', 'fy', 'fvx', 'fvy'],
    },
  },
  wordcloud: {
    tx: { size: [400, 300], font: 'sans-serif', fontStyle: 'italic', fontWeight: 'bold', fontSize: { field: 'a' }, fontSizeRange: [8, 30], rotate: 0, text: { field: 'c' }, spiral: 'rectangular', padding: 2, as: ['wx', 'wy', 'wf', 'ws', 'wst', 'ww', 'wa'] },
    // every variant is recorded twice: with node-canvas, whose glyph
    // rasterization depends on the platform's Pango and Cairo (see
    // transforms.node.divergences.txt), and with the box canvas
    boxToo: true,
  },
  // hierarchies
  // a generated node's values are its children's data, and with no keys the
  // root's are the data set, itself included
  nest: { tx: { keys: ['c'], generate: true }, after: [{ type: 'tree', size: [10, 10] }], drop: ['values'] },
  stratify: { values: 'tree', tx: { key: 'id', parentKey: 'parent' }, after: [{ type: 'tree', size: [10, 10] }] },
  tree: { values: 'tree', pre: [{ type: 'stratify', key: 'id', parentKey: 'parent' }], tx: { field: 'v', sort: { field: 'v' }, method: 'cluster', size: [100, 80], nodeSize: [5, 5], separation: false, as: ['tx', 'ty', 'td', 'tc'] } },
  pack: { values: 'tree', pre: [{ type: 'stratify', key: 'id', parentKey: 'parent' }], tx: { field: 'v', sort: { field: 'v' }, padding: 2, radius: 'v', size: [100, 80], as: ['px', 'py', 'pr', 'pd', 'pc'] } },
  partition: { values: 'tree', pre: [{ type: 'stratify', key: 'id', parentKey: 'parent' }], tx: { field: 'v', sort: { field: 'v' }, padding: 1, round: true, size: [100, 80], as: ['a0', 'b0', 'a1', 'b1', 'dd', 'cc'] } },
  treemap: {
    values: 'tree',
    pre: [{ type: 'stratify', key: 'id', parentKey: 'parent' }],
    tx: { field: 'v', sort: { field: 'v' }, method: 'binary', padding: 1, paddingInner: 2, paddingOuter: 1, paddingTop: 3, paddingRight: 1, paddingBottom: 1, paddingLeft: 1, ratio: 2, round: true, size: [100, 80], as: ['a0', 'b0', 'a1', 'b1', 'dd', 'cc'] },
  },
  treelinks: { values: 'tree', pre: [{ type: 'stratify', key: 'id', parentKey: 'parent' }, { type: 'tree', size: [10, 10] }], tx: {} },
  heatmap: {
    pre: [{ type: 'kde2d', size: [20, 20], x: { expr: 'datum.a * 3' }, y: { expr: 'datum.b * 3' }, as: 'grid' }],
    tx: { field: 'grid', color: 'red', opacity: 0.5, resolve: 'shared', as: 'img' },
    drop: ['grid', 'img', 'image'],
  },
  isocontour: {
    pre: [{ type: 'kde2d', size: [20, 20], x: { expr: 'datum.a * 3' }, y: { expr: 'datum.b * 3' }, as: 'grid' }],
    tx: { field: 'grid', thresholds: [0.001], levels: 3, nice: true, resolve: 'shared', zero: false, smooth: false, scale: 2, translate: [1, 2], as: 'iso' },
    drop: ['grid'],
  },
};

// label is a transform of a mark's items: its base is a text mark labelling
// a symbol mark, whose items (x, y, opacity, align, baseline) are recorded.
bases.label = {
  mark: true,
  tx: { size: [100, 80], sort: { field: 'datum.datum.a' }, anchor: ['top', 'bottom', 'right'], offset: [2, 3], padding: 1, lineAnchor: 'start', markIndex: 0, avoidBaseMark: false, avoidMarks: ['other'], method: 'reduced-search', as: ['lx', 'ly', 'lo', 'la', 'lb'] },
  keep: ['x', 'y', 'opacity', 'align', 'baseline', 'lx', 'ly', 'lo', 'la', 'lb'],
};

// The parameters a transform reads that are no member of its definition.
const skipParams = {
  lookup: ['index'], // `from` and `key` are the index; both required
};

function markSpec(type, base, tx) {
  return {
    width: 100,
    height: 80,
    data: [{ name: 'src', values: rows }],
    marks: [
      { type: 'symbol', name: 'base', from: { data: 'src' }, encode: { enter: { x: { field: 'a', mult: 15 }, y: { field: 'b', mult: 15 }, size: { value: 40 } } } },
      { type: 'rect', name: 'other', encode: { enter: { x: { value: 40 }, y: { value: 30 }, width: { value: 20 }, height: { value: 10 } } } },
      { type: 'text', name: 'd', from: { data: 'base' }, encode: { enter: { text: { field: 'datum.c' }, fontSize: { value: 9 } } }, transform: [{ type, ...tx }] },
    ],
  };
}

function pipelineSpec(type, base, tx) {
  if (base.mark) return markSpec(type, base, tx);
  let values = rows;
  if (base.values === 'tree') values = tree;
  else if (base.values === 'geo')
    values = [
      { id: 1, g: { type: 'Point', coordinates: [10, 20] } },
      { id: 2, g: { type: 'LineString', coordinates: [[0, 0], [20, 10]] } },
    ];
  else if (Array.isArray(base.values)) values = base.values;
  const d = { name: 'd', transform: [...(base.pre || []), { type, ...tx }, ...(base.after || [])] };
  if (base.values !== 'none') d.values = values;
  if (base.format) d.format = base.format;
  const spec = {
    width: 100,
    height: 80,
    projections: [{ name: 'proj', type: 'mercator', scale: 100, translate: [50, 40] }],
    data: [
      { name: 'other', values: [{ c: 'x', label: 'ex' }, { c: 'y', label: 'why' }] },
      { name: 'links', values: [{ source: 1, target: 2 }, { source: 2, target: 3 }, { source: 4, target: 6 }] },
      d,
    ],
  };
  if (base.signal) spec.data[2].transform[(base.pre || []).length].signal = 'out';
  return spec;
}

// falsy lists the falsy values a parameter of type t is also tried with:
// where upstream writes `_.x || d`, 0 and "" are the default, where it
// writes `_.x != null ? _.x : d` or `_.x !== false`, they are not.
function falsy(p) {
  if (p.array) return [];
  switch (p.type) {
    case 'number':
      return [['0', 0]];
    case 'string':
    case 'enum':
      return [['""', '']];
    case 'boolean':
      return [['false', false], ['true', true]];
  }
  return [];
}

// variants yields [name, params] for the base parameters and each optional
// parameter absent, null and falsy, the sub-parameters of a `param`
// parameter (density's distribution, force's forces) included.
function* variants(type, base) {
  const def = vega.definition(type);
  const tx = base.tx;
  yield ['base', tx];
  const skip = skipParams[type] || [];
  for (const p of def.params) {
    if (p.required || skip.includes(p.name)) continue;
    if (p.name in tx) {
      const { [p.name]: _, ...rest } = tx;
      yield [`${p.name}=absent`, rest];
    }
    yield [`${p.name}=null`, { ...tx, [p.name]: null }];
    for (const [tag, v] of falsy(p)) yield [`${p.name}=${tag}`, { ...tx, [p.name]: v }];
    if (p.type === 'param' && tx[p.name] != null) {
      const list = p.array ? tx[p.name] : [tx[p.name]];
      for (let i = 0; i < list.length; i++) {
        const sub = list[i];
        const sdef = p.params.find((s) => Object.entries(s.key).every(([k, v]) => sub[k] === v));
        for (const sp of sdef.params) {
          if (sp.required) continue;
          const put = (val) => {
            const copy = list.map((x) => ({ ...x }));
            if (val === undefined) delete copy[i][sp.name];
            else copy[i][sp.name] = val;
            return { ...tx, [p.name]: p.array ? copy : copy[0] };
          };
          const tag = `${p.name}[${Object.values(sdef.key).join()}].${sp.name}`;
          if (sp.name in sub) yield [`${tag}=absent`, put(undefined)];
          yield [`${tag}=null`, put(null)];
          for (const [ftag, v] of falsy(sp)) yield [`${tag}=${ftag}`, put(v)];
        }
      }
    }
  }
}

// -- hand-written cases ------------------------------------------------------------

// Each is a whole specification and the data sets it records. They cover the
// classes of the 2026-10-18 audit's transform findings: memo state shared by
// aggregate ops over a field (every ordering of ops over shared fields),
// d3-hierarchy's summing order, ties broken by tuple id after a sort upstream,
// nest leaves with a `values` member, pivot counts and limits, impute medians,
// exponential decay over strings, V8's sort of NaN comparisons, unmatched
// aggregate fields, warnings upstream logs, bin bases, plain-object keys.
const cases = {};
function addCase(name, spec, data) {
  cases[name] = { spec, data };
}
// a case's keep, like a sweep's, lists the only fields recorded
function oneData(values, transform, extra = {}) {
  return { data: [{ name: 'd', values, transform, ...extra }] };
}

// Every ordered pair and triple of ops over two fields that share a measure
// group (C29): the memoised extent and sorted values of one field must never
// serve another's op.
{
  const vals = [{ a: 0, b: 9 }, { a: 5, b: 1 }, { a: 3, b: 4 }, { a: 3, b: null }, { a: 'x', b: 7 }];
  const memoOps = ['min', 'max', 'argmin', 'argmax', 'median', 'q1', 'q3', 'ci0', 'ci1', 'distinct', 'values', 'valid', 'sum', 'mean'];
  let n = 0;
  for (const o1 of memoOps)
    for (const o2 of memoOps)
      for (const o3 of ['argmax', 'min', 'median', 'q3', 'values']) {
        if (n++ % 3 !== 0 && o3 !== 'argmax') continue; // a third of the triples, every pair
        addCase(`aggregate-ops-${o1}-${o2}-${o3}`, oneData(vals, [{ type: 'aggregate', fields: ['a', 'b', 'b', 'a'], ops: [o1, o2, 'distinct', o3], as: ['w', 'x', 'y', 'z'] }]), ['d']);
      }
  addCase('aggregate-c29', oneData([{ a: 0, b: 9 }, { a: 5, b: 1 }, { a: 3, b: 4 }], [{ type: 'aggregate', fields: ['a', 'b', 'b', 'b'], ops: ['argmin', 'median', 'distinct', 'argmax'], as: ['amin', 'bmed', 'bdist', 'bmax'] }]), ['d']);
  addCase('joinaggregate-memo', oneData(vals, [{ type: 'joinaggregate', fields: ['a', 'b', 'b', 'b'], ops: ['argmin', 'median', 'distinct', 'argmax'], as: ['w', 'x', 'y', 'z'] }]), ['d']);
  addCase('window-memo', oneData(vals, [{ type: 'window', fields: ['a', 'b', 'b', 'b'], ops: ['argmin', 'median', 'distinct', 'argmax'], as: ['w', 'x', 'y', 'z'] }]), ['d']);
}
// Aggregate parameter shapes (C130): unmatched fields and ops, absent ones.
{
  const vals = [{ a: 1, b: 2 }, { a: 3, b: 4 }];
  addCase('aggregate-unmatched', oneData(vals, [{ type: 'aggregate', fields: ['a', 'b'], ops: ['sum'] }]), ['d']);
  addCase('aggregate-unmatched-ops', oneData(vals, [{ type: 'aggregate', fields: ['a'], ops: ['sum', 'max'] }]), ['d']);
  addCase('aggregate-fields-only', oneData(vals, [{ type: 'aggregate', fields: ['a'] }]), ['d']);
  addCase('aggregate-fields-two', oneData(vals, [{ type: 'aggregate', fields: ['a', 'b'] }]), ['d']);
  addCase('aggregate-ops-only', oneData(vals, [{ type: 'aggregate', ops: ['count'] }]), ['d']);
  addCase('aggregate-ops-two', oneData(vals, [{ type: 'aggregate', ops: ['count', 'count'] }]), ['d']);
  addCase('joinaggregate-unmatched', oneData(vals, [{ type: 'joinaggregate', fields: ['a', 'b'], ops: ['sum'] }]), ['d']);
  addCase('window-unmatched', oneData(vals, [{ type: 'window', fields: ['a', 'b'], ops: ['sum'] }]), ['d']);
}
// Exponential over strings and with a later null parameter (C128).
{
  const vals = [{ v: '1' }, { v: '2' }, { v: '4' }];
  addCase('exponential-strings', oneData(vals, [{ type: 'aggregate', ops: ['exponential', 'exponentialb'], fields: ['v', 'v'], aggregate_params: [0.5, 0.5], as: ['e', 'eb'] }]), ['d']);
  addCase('exponential-later-null', oneData([{ v: 1 }, { v: 2 }, { v: 4 }], [{ type: 'aggregate', fields: ['v', 'v'], ops: ['exponential', 'exponential'], aggregate_params: [0.5, 0], as: ['e1', 'e2'] }]), ['d']);
  addCase('exponential-earlier-null', oneData([{ v: 1 }, { v: 2 }, { v: 4 }], [{ type: 'aggregate', fields: ['v', 'v'], ops: ['exponential', 'exponential'], aggregate_params: [0, 0.5], as: ['e1', 'e2'] }]), ['d']);
  addCase('exponential-mixed', oneData([{ v: 1 }, { v: '2' }, { v: null }, { v: 4 }], [{ type: 'window', fields: ['v'], ops: ['exponential'], aggregate_params: [0.3], as: ['e'] }]), ['d']);
}
// d3-hierarchy sums children last to first (C37).
{
  const vals = [{ k: 'a', v: 0.1 }, { k: 'a', v: 0.2 }, { k: 'a', v: 0.3 }, { k: 'b', v: 0.7 }];
  for (const m of ['treemap', 'partition', 'pack']) {
    const tx = m === 'treemap' ? { type: m, field: 'v', method: 'slice', size: [100, 100] } : { type: m, field: 'v', size: [100, 100] };
    addCase(`hierarchy-sum-${m}`, oneData(vals, [{ type: 'nest', keys: ['k'] }, tx]), ['d']);
  }
  addCase('hierarchy-sum-tree', oneData(vals, [{ type: 'nest', keys: ['k'] }, { type: 'tree', field: 'v', sort: { field: 'value' }, size: [100, 100] }]), ['d']);
}
// Ties broken by tuple id after a sort upstream of the transform (C36).
{
  const vals = [{ v: 1, c: 'a' }, { v: 2, c: 'a' }, { v: 3, c: 'a' }, { v: 4, c: 'b' }];
  addCase('stack-ties', oneData(vals, [{ type: 'collect', sort: { field: 'v', order: 'descending' } }, { type: 'stack', field: 'v', sort: { field: 'c' } }]), ['d']);
  addCase('stack-ties-groups', oneData(vals, [{ type: 'collect', sort: { field: 'v', order: 'descending' } }, { type: 'stack', field: 'v', groupby: ['c'], sort: { field: 'c' } }]), ['d']);
  const t = [{ id: 'r' }, { id: 'a', p: 'r', s: 1 }, { id: 'b', p: 'r', s: 1 }, { id: 'c', p: 'r', s: 1 }];
  for (const m of ['tidy', 'cluster']) {
    addCase(`hierarchy-ties-${m}`, oneData(t, [{ type: 'collect', sort: { field: 'id', order: 'descending' } }, { type: 'stratify', key: 'id', parentKey: 'p' }, { type: 'tree', method: m, sort: { field: 's' }, size: [100, 100] }]), ['d']);
  }
  for (const m of ['treemap', 'partition', 'pack']) {
    addCase(`hierarchy-ties-${m}`, oneData(t.map((x, i) => ({ ...x, s: 1, w: i + 1 })), [{ type: 'collect', sort: { field: 'id', order: 'descending' } }, { type: 'stratify', key: 'id', parentKey: 'p' }, { type: m, field: 'w', sort: { field: 's' }, size: [100, 100] }]), ['d']);
  }
  addCase('hierarchy-ties-nest', oneData([{ k: 'b', s: 1, w: 1 }, { k: 'a', s: 1, w: 2 }, { k: 'b', s: 1, w: 3 }], [{ type: 'collect', sort: { field: 'w', order: 'descending' } }, { type: 'nest', keys: ['k'] }, { type: 'treemap', field: 'w', sort: { field: 's' }, size: [100, 100] }]), ['d']);
}
// Nest leaves with a `values` member are children too (C125).
{
  addCase('nest-values-array', oneData([{ k: 'a', values: [{ q: 1 }, { q: 2 }] }, { k: 'a' }, { k: 'b' }], [{ type: 'nest', keys: ['k'] }, { type: 'partition', size: [100, 100] }]), ['d']);
  addCase('nest-values-string', oneData([{ k: 'a', values: 'xy' }, { k: 'b' }], [{ type: 'nest', keys: ['k'] }, { type: 'partition', size: [100, 100] }]), ['d']);
  addCase('nest-values-empty', oneData([{ k: 'a', values: [] }, { k: 'b' }], [{ type: 'nest', keys: ['k'] }, { type: 'partition', size: [100, 100] }]), ['d']);
  addCase('nest-values-nokeys', oneData([{ values: [{ q: 1 }] }, { k: 'b' }], [{ type: 'nest' }, { type: 'tree', size: [100, 100] }]), ['d']);
  addCase('nest-values-object', oneData([{ k: 'a', values: { length: 2, 0: { q: 1 } } }, { k: 'b' }], [{ type: 'nest', keys: ['k'] }, { type: 'partition', size: [100, 100] }]), ['d']);
}
// Pivot counts and limits (C126).
{
  const vals = [{ g: 1, k: 'x', v: 'abc' }, { g: 1, k: 'x', v: 2 }, { g: 1, k: 'y', v: 3 }, { g: 2, k: 'z', v: null }];
  addCase('pivot-count-nan', oneData(vals, [{ type: 'pivot', groupby: ['g'], field: 'k', value: 'v', op: 'count' }], { format: { parse: { v: 'number' } } }), ['d']);
  addCase('pivot-count-raw', oneData(vals, [{ type: 'pivot', groupby: ['g'], field: 'k', value: 'v', op: 'count' }]), ['d']);
  addCase('pivot-valid', oneData(vals, [{ type: 'pivot', groupby: ['g'], field: 'k', value: 'v', op: 'valid' }]), ['d']);
  for (const limit of [-1, -2, 1, 0.5, 2.5, '1', true]) {
    addCase(`pivot-limit-${limit}`, oneData(vals, [{ type: 'pivot', groupby: ['g'], field: 'k', value: 'v', limit }]), ['d']);
  }
}
// Impute medians follow d3-array (C127).
{
  for (const method of ['median', 'mean', 'min', 'max']) {
    addCase(`impute-${method}-strings`, oneData([{ x: 0, y: '', c: 'a' }, { x: 1, y: 10, c: 'a' }, { x: 2, y: 20, c: 'a' }, { x: 3, y: 'q', c: 'a' }, { x: 4, y: true, c: 'a' }, { x: 0, y: 1, c: 'b' }, { x: 5, y: 2, c: 'b' }], [{ type: 'impute', field: 'y', key: 'x', groupby: ['c'], method }]), ['d']);
  }
}
// Pie sorted over 64 and more tuples with missing values: V8's TimSort (C129).
{
  const vals = [];
  for (let i = 0; i < 80; i++) vals.push(i % 7 === 0 ? { i } : { i, v: (i * 37) % 23 });
  addCase('pie-sort-nan-80', oneData(vals, [{ type: 'pie', field: 'v', sort: true }]), ['d']);
  addCase('pie-sort-nan-20', oneData(vals.slice(0, 20), [{ type: 'pie', field: 'v', sort: true }]), ['d']);
  addCase('pie-start-nan', oneData([{ v: 1 }, { v: 2 }], [{ type: 'pie', field: 'v', startAngle: { signal: 'nan' } }]), ['d']);
  cases['pie-start-nan'].spec.signals = [{ name: 'nan', update: 'NaN' }];
}
// Warnings upstream logs (C131).
{
  addCase('warn-regression-params', oneData([{ x: 1, y: 2 }, { x: 2, y: 3 }], [{ type: 'regression', x: 'x', y: 'y', method: 'poly', order: 4 }]), ['d']);
  addCase('warn-regression-log-extent', oneData([{ x: 1, y: 2 }, { x: 2, y: 3 }, { x: 4, y: 3 }], [{ type: 'regression', x: 'x', y: 'y', method: 'log', extent: [-1, 4] }]), ['d']);
  addCase('warn-timeunit-infer', oneData([{ t: 0 }, { t: 1e10 }], [{ type: 'timeunit', field: 't', inferUnits: true, units: ['year'] }]), ['d']);
  addCase('warn-bin-infinite', oneData([{ x: 1 }], [{ type: 'bin', field: 'x', extent: [0, { signal: 'inf' }] }]), ['d']);
  cases['warn-bin-infinite'].spec.signals = [{ name: 'inf', update: '1/0' }];
}
// Bin bases upstream bins with (C132).
{
  for (const base of [0.5, 1, 2, { signal: 'inf' }, 0, -2, 1.5, '3']) {
    addCase(`bin-base-${JSON.stringify(base)}`, { signals: [{ name: 'inf', update: '1/0' }], ...oneData([{ x: 1 }, { x: 7 }], [{ type: 'bin', field: 'x', extent: [0, 10], base }]) }, ['d']);
  }
}
// Lookup with no values (C136).
{
  for (const values of [[], null]) {
    addCase(`lookup-values-${JSON.stringify(values)}`, {
      data: [
        { name: 'o', values: [{ k: 1, w: 'one' }] },
        { name: 'd', values: [{ k: 1 }, { k: 2 }], transform: [{ type: 'lookup', from: 'o', key: 'k', fields: ['k'], values }] },
      ],
    }, ['d']);
  }
}
// Plain-object keys upstream reads through Object.prototype (C135).
{
  const words = [{ t: 'constructor toString __proto__ hasOwnProperty valueOf constructor plain' }];
  addCase('countpattern-proto', oneData(words, [{ type: 'countpattern', field: 't' }]), ['d']);
  const vals = [{ g: 1, k: 'constructor', v: 1 }, { g: 1, k: 'toString', v: 2 }, { g: 1, k: 'x', v: 3 }, { g: 2, k: '__proto__', v: 4 }];
  addCase('pivot-proto', oneData(vals, [{ type: 'pivot', groupby: ['g'], field: 'k', value: 'v' }]), ['d']);
  for (const g of ['toString', 'constructor', 'valueOf', '__proto__', 'hasOwnProperty']) {
    const gv = [{ g, v: 1 }, { g, v: 3 }, { g: 'x', v: 2 }];
    addCase(`quantile-proto-${g}`, oneData(gv, [{ type: 'quantile', field: 'v', groupby: ['g'], probs: [0.5] }]), ['d']);
    addCase(`kde-proto-${g}`, oneData(gv, [{ type: 'kde', field: 'v', groupby: ['g'], steps: 3, extent: [0, 4] }]), ['d']);
    addCase(`regression-proto-${g}`, oneData(gv.map((x, i) => ({ ...x, x: i })), [{ type: 'regression', x: 'x', y: 'v', groupby: ['g'] }]), ['d']);
    addCase(`loess-proto-${g}`, oneData(gv.map((x, i) => ({ ...x, x: i })), [{ type: 'loess', x: 'x', y: 'v', groupby: ['g'] }]), ['d']);
    addCase(`impute-proto-key-${g}`, oneData([{ x: g, y: 1, c: 'a' }, { x: 'q', y: 2, c: 'b' }], [{ type: 'impute', field: 'y', key: 'x', groupby: ['c'] }]), ['d']);
    addCase(`impute-proto-group-${g}`, oneData([{ x: 1, y: 1, c: g }, { x: 2, y: 2, c: 'b' }], [{ type: 'impute', field: 'y', key: 'x', groupby: ['c'] }]), ['d']);
    addCase(`impute-proto-keyvals-${g}`, oneData([{ x: 1, y: 1, c: 'a' }], [{ type: 'impute', field: 'y', key: 'x', keyvals: [g], groupby: ['c'] }]), ['d']);
  }
}
// More of the plain-object maps upstream keys by data (the class of C135).
{
  for (const g of ['toString', '__proto__', 'constructor']) {
    addCase(`aggregate-cross-proto-${g}`, oneData([{ a: g, b: 1 }, { a: 'x', b: 2 }, { a: 'y', b: 1 }], [{ type: 'aggregate', groupby: ['a', 'b'], cross: true }]), ['d']);
    addCase(`distinct-proto-${g}`, oneData([{ v: g }, { v: g }, { v: 'x' }], [{ type: 'aggregate', fields: ['v'], ops: ['distinct'] }]), ['d']);
    addCase(`window-proto-${g}`, oneData([{ g, v: 1 }, { g: 'x', v: 2 }], [{ type: 'window', groupby: ['g'], ops: ['sum'], fields: ['v'] }]), ['d']);
    addCase(`kde2d-proto-${g}`, oneData([{ g, x: 1, y: 2 }, { g: 'q', x: 3, y: 4 }], [{ type: 'kde2d', groupby: ['g'], x: 'x', y: 'y', size: [10, 10] }]), ['d']);
    addCase(`nest-proto-${g}`, oneData([{ k: g }, { k: 'x' }], [{ type: 'nest', keys: ['k'] }, { type: 'tree', size: [10, 10] }]), ['d']);
    addCase(`dotbin-proto-${g}`, oneData([{ g, v: 1 }, { g: 'x', v: 2 }], [{ type: 'dotbin', field: 'v', groupby: ['g'] }]), ['d']);
    addCase(`lookup-proto-${g}`, {
      data: [
        { name: 'o', values: [{ k: g, w: 'one' }, { k: 'x', w: 'two' }] },
        { name: 'd', values: [{ k: g }, { k: 'x' }], transform: [{ type: 'lookup', from: 'o', key: 'k', fields: ['k'], values: ['w'] }] },
      ],
    }, ['d']);
  }
  addCase('aggregate-cross-order', oneData([{ a: 'b', b: 1 }, { a: '2', b: 2 }, { a: 'a', b: 1 }, { a: '1', b: 3 }], [{ type: 'aggregate', groupby: ['a', 'b'], cross: true }]), ['d']);
  addCase('pivot-count-op', oneData([{ g: 1, v: 1 }, { g: 1, v: null }, { g: 1, v: 'NaN' }, { g: 2, v: '' }], [{ type: 'aggregate', groupby: ['g'], fields: ['v', 'v'], ops: ['__count__', 'count'] }]), ['d']);
}
// Nest leaves whose values are children, with generate (C125).
{
  addCase('nest-values-generate', oneData([{ k: 'a', values: [{ q: 1 }, { q: 2, values: [{ r: 1 }] }] }, { k: 'b' }], [{ type: 'nest', keys: ['k'], generate: true }, { type: 'partition', size: [100, 100] }]), ['d']);
  addCase('nest-values-number', oneData([{ k: 'a', values: 5 }, { k: 'b', values: true }], [{ type: 'nest', keys: ['k'] }, { type: 'partition', size: [100, 100] }]), ['d']);
  addCase('nest-values-nested-null', oneData([{ k: 'a', values: [null] }], [{ type: 'nest', keys: ['k'] }, { type: 'partition', size: [100, 100] }]), ['d']);
  addCase('nest-values-field', oneData([{ k: 'a', v: 2, values: [{ v: 3 }, { v: 'x' }] }, { k: 'b', v: 4 }], [{ type: 'nest', keys: ['k'] }, { type: 'treemap', field: 'v', size: [100, 100] }]), ['d']);
}
// V8's sort of NaN differences, over the audit's data and seeded others (C129).
{
  const audit = [16, 38, 35, null, 9, 24, 39, 31, 41, 38, null, 5, 39, 1, 31, 17, 36, null, 15, 13, 46, 31, 35, 36, null, 31, 26, 41, 10];
  let seed = 7;
  const rnd = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
  for (const n of [64, 65, 80, 200, 1000]) {
    const vals = [];
    for (let i = 0; i < n; i++) {
      const r = rnd();
      vals.push(i < audit.length && n === 80 ? (audit[i] == null ? { i } : { i, v: audit[i] }) : r < 0.12 ? { i } : { i, v: Math.floor(rnd() * 50) });
    }
    addCase(`pie-sort-v8-${n}`, oneData(vals, [{ type: 'pie', field: 'v', sort: true }, { type: 'project', fields: ['i', 'startAngle'] }]), ['d']);
  }
  addCase('pie-start-string', oneData([{ v: 1 }, { v: 2 }, { v: 3 }], [{ type: 'pie', field: 'v', startAngle: '1' }]), ['d']);
  addCase('pie-start-zero-string', oneData([{ v: 1 }, { v: 2 }], [{ type: 'pie', field: 'v', startAngle: '0' }]), ['d']);
}
// Exponential concatenation of what reads as text (C128).
{
  addCase('exponential-dates', oneData([{ v: 1 }, { v: 2 }], [{ type: 'formula', expr: 'datetime(2020, 0, datum.v)', as: 'v' }, { type: 'aggregate', fields: ['v'], ops: ['exponential'], aggregate_params: [0.5] }]), ['d']);
  addCase('exponential-objects', oneData([{ v: { a: 1 } }, { v: [2] }, { v: true }], [{ type: 'aggregate', fields: ['v'], ops: ['exponential', 'exponentialb'], aggregate_params: [0.5, 0.5] }]), ['d']);
  addCase('exponentialb-alone', oneData([{ v: 1 }, { v: 2 }], [{ type: 'aggregate', fields: ['v'], ops: ['exponentialb'], aggregate_params: [0.5] }]), ['d']);
}
// Quantiles undefined where there is no value, NaN where d3 computes it (C167).
{
  addCase('quantile-empty-group', oneData([{ g: 'a', v: 'x' }, { g: 'b', v: 1 }], [{ type: 'quantile', field: 'v', groupby: ['g'], probs: [0.5] }]), ['d']);
  addCase('aggregate-median-none', oneData([{ v: null }, { v: 'x' }], [{ type: 'aggregate', fields: ['v', 'v', 'v'], ops: ['median', 'q1', 'q3'] }]), ['d']);
}
// fontSizeRange as upstream's `if (range)` reads it (C35).
{
  for (const fontSizeRange of [[], [20], [5, 40, 9], [0, 0]]) {
    addCase(`wordcloud-range-${JSON.stringify(fontSizeRange)}`, oneData(rows, [{ type: 'wordcloud', size: [500, 400], fontSize: { field: 'a' }, fontSizeRange, text: { field: 'c' } }]), ['d']);
    cases[`wordcloud-range-${JSON.stringify(fontSizeRange)}`].keep = ['c', 'fontSize', 'x', 'y'];
  }
  addCase('wordcloud-size-strings', oneData(rows.map((r) => ({ ...r, a: String(r.a * 3) })), [{ type: 'wordcloud', size: [500, 400], fontSize: { field: 'a' }, fontSizeRange: [10, 40], text: { field: 'c' } }]), ['d']);
  cases['wordcloud-size-strings'].keep = ['c', 'a', 'fontSize', 'x', 'y'];
  addCase('wordcloud-size-constant', oneData(rows, [{ type: 'wordcloud', size: [500, 400], fontSize: 20, fontSizeRange: [1, 2], text: { field: 'c' } }]), ['d']);
  cases['wordcloud-size-constant'].keep = ['c', 'fontSize', 'x', 'y'];
  for (const name of Object.keys(cases).filter((n) => n.startsWith('wordcloud-'))) {
    cases[`${name}-box`] = { ...cases[name], box: true };
  }
}
// Window operations read a null field where they run (the sweep's class).
{
  for (const op of ['lag', 'lead', 'first_value', 'last_value', 'nth_value', 'prev_value', 'next_value']) {
    addCase(`window-null-field-${op}`, oneData([{ v: 1 }, { v: 2 }], [{ type: 'window', ops: [op], params: [1] }]), ['d']);
    addCase(`window-null-field-${op}-one`, oneData([{ v: 1 }], [{ type: 'window', ops: [op], params: [1] }]), ['d']);
  }
  addCase('window-ntile-zero', oneData([{ v: 1 }], [{ type: 'window', ops: ['ntile'], params: [0] }]), ['d']);
  addCase('window-order-of-errors', oneData([{ v: 1 }], [{ type: 'window', ops: ['sum', 'ntile'], fields: [null, 'v'], params: [null, 0] }]), ['d']);
}

// Curves over a reversed extent, which make more points than maxsteps
// (C134): density, kde and regression sample them the same way.
{
  addCase('density-reversed', { data: [{ name: 'd', transform: [{ type: 'density', extent: [10, -10], steps: undefined, minsteps: 25, maxsteps: 40, distribution: { function: 'normal' } }] }] }, ['d']);
  addCase('density-reversed-mixture', {
    data: [{ name: 'd', transform: [{ type: 'density', extent: [10, -10], minsteps: 25, maxsteps: 40, distribution: { function: 'mixture', distributions: [-8, -6, -4, -2, 0, 2, 4, 6, 8, 9].map((mean) => ({ function: 'normal', mean, stdev: 0.05 })) } }] }],
  }, ['d']);
  addCase('kde-reversed', oneData(rows, [{ type: 'kde', field: 'a', extent: [6, -2], minsteps: 25, maxsteps: 40 }]), ['d']);
  addCase('regression-reversed', oneData(rows, [{ type: 'regression', x: 'a', y: 'b', method: 'quad', extent: [6, -2] }]), ['d']);
  addCase('regression-reversed-exp', oneData(rows, [{ type: 'regression', x: 'a', y: 'b', method: 'exp', extent: [9, -9] }]), ['d']);
}

// Small parameter coercions (C167).
{
  for (const interval of [0, null, '', false, 'no']) {
    addCase(`bin-interval-${JSON.stringify(interval)}`, oneData([{ x: 1 }], [{ type: 'bin', field: 'x', extent: [0, 10], interval }]), ['d']);
    addCase(`timeunit-interval-${JSON.stringify(interval)}`, oneData([{ t: 0 }], [{ type: 'timeunit', field: 't', units: ['year'], interval }]), ['d']);
  }
  addCase('treemap-ratio-null', oneData(tree, [{ type: 'stratify', key: 'id', parentKey: 'parent' }, { type: 'treemap', field: 'v', ratio: null, size: [100, 80] }]), ['d']);
  addCase('quantile-infinities', oneData([{ v: Infinity }, { v: Infinity }].map((x) => x), [{ type: 'formula', expr: '1/0', as: 'v' }, { type: 'quantile', field: 'v', probs: [0.5] }]), ['d']);
  addCase('aggregate-median-infinities', oneData([{ v: 1 }, { v: 2 }], [{ type: 'formula', expr: '1/0', as: 'v' }, { type: 'aggregate', fields: ['v', 'v', 'v'], ops: ['median', 'q1', 'ci0'] }]), ['d']);
  addCase('sample-default', oneData([{ a: 1 }, { a: 2 }, { a: 3 }], [{ type: 'sample' }]), ['d']);
}

// -- running -----------------------------------------------------------------------

// A case can write onto a built-in prototype through a plain object keyed
// by its data ("__proto__", "constructor"), as upstream does. Each case
// starts from the built-ins as they were, as in a fresh process: what a case
// added or changed is undone after it.
const builtins = [Object.prototype, Array.prototype, Function.prototype, String.prototype, Number.prototype, Object, Array];
const pristine = builtins.map((o) => Object.getOwnPropertyDescriptors(o));
function restoreBuiltins() {
  builtins.forEach((o, i) => {
    const was = pristine[i];
    for (const k of Reflect.ownKeys(o)) {
      if (!Object.hasOwn(was, k)) delete o[k];
    }
    for (const k of Reflect.ownKeys(was)) {
      const d = Object.getOwnPropertyDescriptor(o, k);
      const w = was[k];
      if (!d || d.value !== w.value || d.get !== w.get || d.set !== w.set) Object.defineProperty(o, k, w);
    }
  });
}

// Errors and warnings as the engine reports them: a TypeError's text without
// its name, as vega's logger prints an Error's message.
async function run(spec, data, keep, drop, box) {
  let res;
  boxWordcloud = !!box;
  try {
    res = await run1(spec, data, keep, drop);
  } finally {
    boxWordcloud = false;
    restoreBuiltins();
  }
  if (res.view) res.view.finalize(); // stops a force simulation's timer
  return res.rec;
}

async function run1(spec, data, keep, drop) {
  if (process.env.GEN_TRACE) console.error(JSON.stringify(spec).slice(-300));
  const errors = [];
  const warnings = [];
  const logger = {
    _level: 2,
    level(l) {
      if (l === undefined) return this._level;
      this._level = l;
      return this;
    },
    error(...a) {
      errors.push(a.map((x) => (x instanceof Error ? x.message : String(x))).join(' '));
      return this;
    },
    warn(...a) {
      const s = a.map(String).join(' ');
      if (!warnings.includes(s)) warnings.push(s);
      return this;
    },
    info() {
      return this;
    },
    debug() {
      return this;
    },
  };
  vega.setRandom(vega.randomLCG(12345));
  let view;
  try {
    // a fresh copy: ingest adopts inline objects, and transforms write into them
    view = new vega.View(vega.parse(structuredClone(spec)), { renderer: 'none', logger, logLevel: vega.Warn });
    await view.runAsync();
  } catch (e) {
    return { rec: { error: e.message, warnings } };
  }
  const out = { warnings, data: {} };
  if (errors.length) out.error = errors[0];
  for (const d of data) {
    let v;
    try {
      v = view.data(d);
    } catch (e) {
      out.data[d] = 'ERR ' + e.message;
      continue;
    }
    if (Array.isArray(v) && (keep || drop)) v = v.map((t) => project(t, keep, drop));
    out.data[d] = JSON.stringify(acyclic(v, []));
  }
  let sig;
  try {
    sig = view.signal('out');
  } catch (e) {
    sig = undefined;
  }
  if (sig !== undefined) out.signal = JSON.stringify(acyclic(sig, []));
  return { rec: out, view };
}

// project keeps a tuple's fields named in keep, or drops those in drop,
// for a field whose value is a canvas or depends on what the engine does not
// reproduce exactly (an image's pixels).
// Kept fields are written in keep's order: a mark item's fields have an
// order of their own in the engine's view of an item.
function project(t, keep, drop) {
  const o = {};
  for (const k of keep || Object.keys(t)) {
    if (!Object.hasOwn(t, k) || (drop && drop.includes(k))) continue;
    o[k] = t[k];
  }
  return o;
}

// acyclic copies v for JSON.stringify with every reference to an object
// that contains it (an argmin of a joinaggregate is the tuple it is written
// into) replaced by the string "[Circular]".
function acyclic(v, stack) {
  if (v === null || typeof v !== 'object' || v instanceof Date) return v;
  if (stack.includes(v)) return '[Circular]';
  stack.push(v);
  let o;
  if (Array.isArray(v)) o = v.map((x) => acyclic(x, stack));
  else {
    o = {};
    for (const k of Object.keys(v)) o[k] = acyclic(v[k], stack);
  }
  stack.pop();
  return o;
}

const out = {
  vega: vega.version,
  // what node-canvas rasterizes glyphs with, which keys the cases whose
  // positions depend on it (transforms.node.divergences.txt)
  canvas: `${process.platform}-${process.arch} pango ${canvas.pangoVersion} cairo ${canvas.cairoVersion}`,
  cases: {},
};
for (const type of Object.keys(bases)) {
  const base = bases[type];
  for (const [name, tx] of variants(type, base)) {
    const spec = pipelineSpec(type, base, tx);
    const rec = await run(spec, ['d'], base.keep, base.drop);
    rec.spec = spec;
    if (base.keep) rec.keep = base.keep;
    if (base.drop) rec.drop = base.drop;
    out.cases[`${type}/${name}`] = rec;
    if (base.boxToo) {
      const brec = await run(spec, ['d'], base.keep, base.drop, true);
      brec.spec = spec;
      brec.canvas = 'box';
      out.cases[`${type}-box/${name}`] = brec;
    }
  }
}
for (const [name, c] of Object.entries(cases)) {
  const rec = await run(c.spec, c.data, c.keep, undefined, c.box);
  rec.spec = c.spec;
  if (c.keep) rec.keep = c.keep;
  if (c.box) rec.canvas = 'box';
  out.cases[`case/${name}`] = rec;
}
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
