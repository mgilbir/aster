// Records end-to-end vega runs of the geo transforms (projection operator,
// geopoint, geopath, geojson, graticule) from vega 6.4.0:
//
//   NODE_PATH=/path/to/node_modules node testdata/gen_pipeline.mjs
//
// Each case is a small description that the Go test replays with the geo
// package; the expected output comes from a real vega.View.
import { createRequire } from 'node:module';
import crypto from 'node:crypto';
import fs from 'node:fs';
import zlib from 'node:zlib';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const topo = await import(require.resolve('topojson-client'));

const here = path.dirname(fileURLToPath(import.meta.url));
const dataDir = path.join(here, '../../../testdata/vega-datasets/data');
const world = JSON.parse(fs.readFileSync(path.join(dataDir, 'world-110m.json'), 'utf8'));
const us = JSON.parse(fs.readFileSync(path.join(dataDir, 'us-10m.json'), 'utf8'));
const sha = (s) => crypto.createHash('sha256').update(s).digest('hex').slice(0, 16);

const countries = topo.feature(world, world.objects.countries).features;
const pick = (ids) => ids.map((id) => countries.find((f) => f.id === id));
const some = pick([242, 643, 10, 304, 840, 152, 360, 124, 578, 76, 356, 36]);
const states = topo.feature(us, us.objects.states).features;
const usSome = states.filter((s, i) => i % 5 === 0);

const points = [
  { lon: -100, lat: 40, name: 'a' }, { lon: 10, lat: 50 }, { lon: '5', lat: '60' }, { lon: null, lat: 0 },
  { lon: 'abc', lat: 10 }, { lat: 20 }, { lon: 179.9, lat: -85 }, { lon: -157.8, lat: 21.3 }, { lon: -149.9, lat: 61.2 },
  { lon: 0, lat: 90 }, { lon: 180, lat: 0 }, { lon: -74, lat: 40.7 }, { lon: 139.7, lat: 35.7 }, { lon: 500, lat: 200 },
];
const pointFeatures = points.filter((p) => typeof p.lon === 'number' && typeof p.lat === 'number')
  .map((p, i) => ({ type: 'Feature', id: i, properties: {}, geometry: { type: 'Point', coordinates: [p.lon, p.lat] } }));

const datasets = { points, some, usSome, pointFeatures };
const cases = [];

// ---- geopoint ---------------------------------------------------------------
for (const [type, props] of [['mercator', {}], ['albersusa', {}], ['orthographic', { rotate: [20, -30] }], ['equirectangular', { scale: 100, translate: [200, 100] }],
  ['naturalEarth1', {}], ['stereographic', { clipAngle: 90 }], ['identity', { scale: 2, translate: [5, 6] }], ['albers', { parallels: [20, 50], rotate: [90, 0] }]]) {
  cases.push({ name: `geopoint ${type}`, kind: 'geopoint', dataset: 'points', projection: { type, props }, params: { fields: ['lon', 'lat'] } });
}
cases.push({ name: 'geopoint as', kind: 'geopoint', dataset: 'points', projection: { type: 'mercator', props: {} }, params: { fields: ['lon', 'lat'], as: ['px', 'py'] } });
cases.push({ name: 'geopoint fit', kind: 'geopoint', dataset: 'points', projection: { type: 'mercator', props: {}, fit: 'some', size: [500, 300] }, params: { fields: ['lon', 'lat'] } });

// ---- geopath with fitted projections ---------------------------------------
for (const type of ['mercator', 'equirectangular', 'naturalEarth1', 'equalEarth', 'orthographic', 'albers', 'conicConformal', 'azimuthalEqualArea', 'transverseMercator', 'mollweide', 'stereographic', 'gnomonic', 'identity']) {
  cases.push({ name: `geopath fit size ${type}`, kind: 'geopath', dataset: 'some', projection: { type, props: {}, fit: 'some', size: [640, 380] }, params: {} });
}
cases.push({ name: 'geopath fit extent', kind: 'geopath', dataset: 'some', projection: { type: 'mercator', props: {}, fit: 'some', extent: [[20, 30], [600, 350]] }, params: {} });
cases.push({ name: 'geopath albersusa fit', kind: 'geopath', dataset: 'usSome', projection: { type: 'albersUsa', props: {}, fit: 'usSome', size: [800, 500] }, params: {} });
cases.push({ name: 'geopath albersusa default', kind: 'geopath', dataset: 'usSome', projection: { type: 'albersUsa', props: {} }, params: {} });
cases.push({ name: 'geopath props', kind: 'geopath', dataset: 'some', projection: { type: 'orthographic', props: { scale: 200, translate: [300, 200], rotate: [-20, -40, 10], clipAngle: 60, precision: 0.5 } }, params: { as: 'd' } });
cases.push({ name: 'geopath clipExtent', kind: 'geopath', dataset: 'some', projection: { type: 'mercator', props: { clipExtent: [[50, 50], [400, 300]], scale: 100 } }, params: {} });
cases.push({ name: 'geopath reflect', kind: 'geopath', dataset: 'some', projection: { type: 'equirectangular', props: { reflectX: true, reflectY: true, center: [30, 10] } }, params: {} });
cases.push({ name: 'geopath no projection point radius const', kind: 'geopath', dataset: 'pointFeatures', projection: null, params: { pointRadius: 7 } });
cases.push({ name: 'geopath point radius const', kind: 'geopath', dataset: 'pointFeatures', projection: { type: 'mercator', props: {} }, params: { pointRadius: 3.25 } });
cases.push({ name: 'geopath point radius projection', kind: 'geopath', dataset: 'pointFeatures', projection: { type: 'mercator', props: { pointRadius: 9 } }, params: {} });
cases.push({ name: 'geopath point radius expr', kind: 'geopath', dataset: 'pointFeatures', projection: { type: 'mercator', props: {} }, params: { pointRadiusExpr: 'datum.id + 1' } });

// ---- geojson ---------------------------------------------------------------
cases.push({ name: 'geojson fields', kind: 'geojson', dataset: 'points', params: { fields: ['lon', 'lat'] } });
cases.push({ name: 'geojson geojson', kind: 'geojson', dataset: 'some', params: { geojson: 'geometry' } });
cases.push({ name: 'geojson identity', kind: 'geojson', dataset: 'some', params: {} });
cases.push({ name: 'geojson both', kind: 'geojson', dataset: 'points', params: { fields: ['lon', 'lat'], geojson: 'nothing' } });

// ---- graticule -------------------------------------------------------------
for (const params of [{}, { step: [30, 30] }, { extent: [[-90, -45], [90, 45]], precision: 5 }, { stepMinor: [5, 5], stepMajor: [45, 90] }, { step: [15, 15], stepMajor: [60, 180] }, { extentMinor: [[-60, -30], [60, 30]], extentMajor: [[-90, -60], [90, 60]] }]) {
  cases.push({ name: `graticule ${JSON.stringify(params)}`, kind: 'graticule', params });
}

// ---- run ---------------------------------------------------------------------
const clone = (x) => JSON.parse(JSON.stringify(x));
async function run(c) {
  const spec = { $schema: 'https://vega.github.io/schema/vega/v6.json', width: 640, height: 380, signals: [], data: [], projections: [] };
  for (const [name, values] of Object.entries(datasets)) spec.data.push({ name, values: clone(values) });
  let proj = null;
  if (c.projection) {
    proj = { name: 'proj', type: c.projection.type, ...c.projection.props };
    if (c.projection.fit) proj.fit = { signal: `data('${c.projection.fit}')` };
    if (c.projection.size) proj.size = c.projection.size;
    if (c.projection.extent) proj.extent = c.projection.extent;
    spec.projections.push(proj);
  }
  const t = { type: c.kind };
  if (c.kind === 'geopoint') {
    t.projection = 'proj'; t.fields = c.params.fields; if (c.params.as) t.as = c.params.as;
  } else if (c.kind === 'geopath') {
    if (c.projection) t.projection = 'proj';
    if (c.params.pointRadius != null) t.pointRadius = c.params.pointRadius;
    if (c.params.pointRadiusExpr) t.pointRadius = { expr: c.params.pointRadiusExpr };
    if (c.params.as) t.as = c.params.as;
  } else if (c.kind === 'geojson') {
    if (c.params.fields) t.fields = c.params.fields;
    if (c.params.geojson) t.geojson = c.params.geojson;
    t.signal = 'gj';
  } else if (c.kind === 'graticule') {
    Object.assign(t, c.params);
  }
  if (c.kind === 'graticule') spec.data.push({ name: 'out', transform: [t] });
  else spec.data.push({ name: 'out', source: c.dataset, transform: [t] });
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  await view.runAsync();
  const out = view.data('out');
  const result = { name: c.name };
  if (c.kind === 'geojson') result.value = clone(view.signal('gj'));
  else if (c.kind === 'graticule') result.value = clone(out[0]);
  else if (c.kind === 'geopath') {
    const as = c.params.as || 'path';
    result.paths = out.map((d) => (d[as] == null ? null : d[as].length > 400 ? { len: d[as].length, sha: sha(d[as]) } : d[as]));
  } else {
    const [xf, yf] = c.params.as || ['x', 'y'];
    result.xy = out.map((d) => [d[xf], d[yf]]);
  }
  return result;
}

const out = [];
for (const c of cases) {
  try { out.push({ case: c, result: await run(c) }); }
  catch (e) { console.error('case failed', c.name, e.message); out.push({ case: c, error: String(e.message) }); }
}
function enc(v) {
  return JSON.stringify(v, (k, x) => {
    if (typeof x === 'number') { if (Number.isNaN(x)) return 'NaN'; if (x === Infinity) return 'Infinity'; if (x === -Infinity) return '-Infinity'; }
    if (x === undefined) return 'undefined';
    return x;
  });
}
fs.writeFileSync(path.join(here, 'pipeline.json.gz'), zlib.gzipSync(enc({ datasets, cases: out }), { level: 9 }));
console.error('cases:', out.length, 'errors:', out.filter((o) => o.error).length);
