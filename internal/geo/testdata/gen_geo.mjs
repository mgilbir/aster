// Records golden vectors for the geo package from upstream d3-geo 3.1.1,
// d3-geo-projection 4.0.0, vega-projection 2.1.2, topojson-client 3.1.0 and
// vega-geo 5.1.3 (as shipped with vega 6.4.0).
//
//   NODE_PATH=/path/to/node_modules node testdata/gen_geo.mjs
//
// It writes points.json.gz, paths.json.gz, fit.json.gz, measures.json, graticule.json.gz,
// topo.json.gz, digits.json, angle.json and geoms.json.gz next to this file (gen_pipeline.mjs and gen_contour.mjs record the vega-geo transforms).
//
// Record on macOS/arm64, which jsmath follows, except for the conicConformal entries of
// points, fit and random: they use Math.pow, which V8 leaves to the C library, and jsmath.Pow
// is glibc's, so those entries are taken from a run in a Linux container (node 24.21.0).
import { createRequire } from 'node:module';
import crypto from 'node:crypto';
import fs from 'node:fs';
import zlib from 'node:zlib';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const d3 = await import(require.resolve('d3-geo'));
const vp = await import(require.resolve('vega-projection'));
const topo = await import(require.resolve('topojson-client'));
const vega = await import(require.resolve('vega'));

const here = path.dirname(fileURLToPath(import.meta.url));
const dataDir = path.join(here, '../../../testdata/vega-datasets/data');
const world = JSON.parse(fs.readFileSync(path.join(dataDir, 'world-110m.json'), 'utf8'));
const us = JSON.parse(fs.readFileSync(path.join(dataDir, 'us-10m.json'), 'utf8'));

// JSON with non-finite numbers and negative zero spelled out.
function enc(v) {
  return JSON.stringify(v, (k, x) => {
    if (typeof x === 'number') {
      if (Number.isNaN(x)) return 'NaN';
      if (x === Infinity) return 'Infinity';
      if (x === -Infinity) return '-Infinity';
      if (Object.is(x, -0)) return '-0';
    }
    if (x === undefined) return 'undefined';
    return x;
  });
}
const write = (name, v) => {
  const text = enc(v);
  if (name.endsWith('.gz')) fs.writeFileSync(path.join(here, name), zlib.gzipSync(text, { level: 9 }));
  else fs.writeFileSync(path.join(here, name), text);
};
const sha = (s) => crypto.createHash('sha256').update(s).digest('hex').slice(0, 16);

let seed = 12345;
function rnd() { // mulberry32
  seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
}

const TYPES = ['albers', 'albersusa', 'azimuthalequalarea', 'azimuthalequidistant', 'conicconformal',
  'conicequalarea', 'conicequidistant', 'equalearth', 'equirectangular', 'gnomonic', 'identity',
  'mercator', 'mollweide', 'naturalearth1', 'orthographic', 'stereographic', 'transversemercator'];

// Apply properties the way vega-geo's Projection operator does.
function make(cfg) {
  const p = vp.projection(cfg.type)();
  for (const prop of vp.projectionProperties) {
    if (cfg.props && cfg.props[prop] != null && typeof p[prop] === 'function') p[prop](cfg.props[prop]);
  }
  if (cfg.props && cfg.props.pointRadius != null) p.path.pointRadius(cfg.props.pointRadius);
  return p;
}

const configs = [];
const add = (type, props, tag) => configs.push({ id: `${type}${tag ? ':' + tag : ''}`, type, props: props || {} });
for (const t of TYPES) {
  add(t, {}, '');
  add(t, { scale: 180, translate: [400, 260], center: [12, 30], rotate: [20, -15] }, 'A');
  add(t, { rotate: [-100, -30, 25], precision: 0.1, reflectX: true, translate: [300, 200] }, 'B');
  add(t, { precision: 0, reflectY: true, translate: [100, 100], scale: 120 }, 'C');
}
add('orthographic', { clipAngle: 45, rotate: [30, -20] }, 'clip45');
add('orthographic', { rotate: [-170, 50], scale: 300 }, 'rotAM');
add('orthographic', { rotate: [0, -90], scale: 300 }, 'southpole');
add('azimuthalequalarea', { clipAngle: 60, rotate: [-100, -40] }, 'clip60');
add('azimuthalequidistant', { clipAngle: 120, rotate: [10, 60, 20] }, 'clip120');
add('stereographic', { rotate: [0, -90], clipAngle: 100 }, 'south');
add('stereographic', { rotate: [100, 30], clipAngle: 20 }, 'clip20');
add('gnomonic', { rotate: [-90, -30], clipAngle: 30 }, 'clip30');
add('mercator', { clipExtent: [[50, 60], [350, 300]] }, 'clip');
add('mercator', { rotate: [-170, 0], scale: 100, translate: [480, 250] }, 'rotAM');
add('equirectangular', { clipExtent: [[100, 100], [300, 300]] }, 'clip');
add('equirectangular', { rotate: [160, 0], clipExtent: [[-50, 20], [800, 400]] }, 'clipAM');
add('naturalearth1', { clipExtent: [[80, 40], [520, 300]], rotate: [-30, 0] }, 'clip');
add('mollweide', { clipExtent: [[0, 0], [900, 250]] }, 'clip');
add('identity', { clipExtent: [[10, 10], [200, 100]], scale: 1.5 }, 'clip');
add('identity', { scale: 2, translate: [10, 20], reflectY: true }, 'flip');
add('albers', { parallels: [20, 40] }, 'par');
add('albers', { parallels: [-30, 30], rotate: [0, 0] }, 'parsym');
add('conicequalarea', { parallels: [-30, 30] }, 'sym');
add('conicconformal', { parallels: [20, 60], center: [0, 40] }, 'par');
add('conicconformal', { parallels: [0, 0] }, 'zero');
add('conicconformal', { parallels: [-40, -10], rotate: [-100, 0] }, 'south');
add('conicequidistant', { parallels: [10, 50] }, 'par');
add('conicequidistant', { parallels: [0, 0] }, 'zero');
add('conicequidistant', { parallels: [25, 25] }, 'same');
add('transversemercator', { center: [10, 20], rotate: [5, 10] }, 'cr');
add('transversemercator', { rotate: [-20, 0, 5], scale: 200 }, 'rot3');
add('albersusa', { scale: 700, translate: [400, 250] }, 'scaled');
add('albersusa', { precision: 0 }, 'p0');

const lons = [-180, -179.9999, -170, -120, -90, -45, -10, -0.5, 0, 5, 30, 90, 135, 170, 179.9999, 180];
const lats = [-90, -89, -60, -30, -0.1, 0, 15, 45, 80, 89, 90];
const pts = [];
for (const lo of lons) for (const la of lats) pts.push([lo, la]);
pts.push([-100, 40], [-122.4, 37.8], [-74, 40.7], [-157.8, 21.3], [-149.9, 61.2], [2.35, 48.86], [139.7, 35.7]);
for (let i = 0; i < 60; i++) pts.push([+(rnd() * 360 - 180).toFixed(4), +(rnd() * 180 - 90).toFixed(4)]);
const xys = [];
for (let x = -100; x <= 1100; x += 100) for (let y = -100; y <= 700; y += 100) xys.push([x, y]);
xys.push([480, 250], [300.5, 200.25], [0, 0]);
for (let i = 0; i < 40; i++) xys.push([+(rnd() * 1000).toFixed(3), +(rnd() * 500).toFixed(3)]);

{
  const out = [];
  for (const cfg of configs) {
    const p = make(cfg);
    const fwd = pts.map((pt) => { const r = p(pt); return r == null ? null : [r[0], r[1]]; });
    const inv = xys.map((xy) => { const r = p.invert ? p.invert(xy) : undefined; return r === undefined ? 'undefined' : (r == null ? null : [r[0], r[1]]); });
    out.push({ id: cfg.id, type: cfg.type, props: cfg.props, scale: p.scale(), translate: p.translate(),
      forward: fwd, invert: inv });
  }
  write('points.json.gz', { points: pts, xys, cases: out });
}

// ---- geometry corpus ------------------------------------------------------
const countries = topo.feature(world, world.objects.countries);
const land = topo.feature(world, world.objects.land);
const byId = (id) => countries.features.find((f) => f.id === id || f.id === String(id));
const named = {
  fiji: byId(242) || byId('242'), russia: byId(643), antarctica: byId(10), greenland: byId(304),
  usa: byId(840), chile: byId(152), indonesia: byId(360), canada: byId(124), norway: byId(578),
};
const states = topo.feature(us, us.objects.states);
const nation = topo.feature(us, us.objects.land);
const counties = topo.feature(us, us.objects.counties);
const line = (coords) => ({ type: 'LineString', coordinates: coords });
const poly = (...rings) => ({ type: 'Polygon', coordinates: rings });
const synth = {
  sphere: { type: 'Sphere' },
  point: { type: 'Point', coordinates: [-100, 40] },
  multipoint: { type: 'MultiPoint', coordinates: [[-100, 40], [10, 20], [170, -30], [-170, 60], [0, 90]] },
  point3: { type: 'Point', coordinates: [10, 20, 500] },
  lineAM: line([[170, 10], [-170, 20], [-160, 25]]),
  lineAM2: line([[-175, -10], [175, 5], [160, 30], [-160, 40]]),
  linePole: line([[-90, 80], [90, 80]]),
  lineLong: line([[-180, 0], [-90, 45], [0, 0], [90, -45], [180, 0]]),
  lineMerid: line([[0, -90], [0, 90]]),
  polyAM: poly([[170, 10], [-170, 10], [-170, 30], [170, 30], [170, 10]]),
  polyAMwide: poly([[100, -20], [-100, -20], [-100, 40], [100, 40], [100, -20]]),
  polyPole: poly([[-180, 70], [-90, 70], [0, 70], [90, 70], [180, 70], [-180, 70]]),
  polyPoleCW: poly([[-180, -60], [180, -60], [90, -60], [0, -60], [-90, -60], [-180, -60]]),
  polyHole: poly([[-60, -30], [60, -30], [60, 50], [-60, 50], [-60, -30]], [[-20, 0], [-20, 30], [20, 30], [20, 0], [-20, 0]]),
  polySmall: poly([[10, 10], [10.5, 10], [10.5, 10.5], [10, 10.5], [10, 10]]),
  polyHemi: poly([[-90, 0], [0, 60], [90, 0], [0, -60], [-90, 0]]),
  polyBig: poly([[-179, -80], [179, -80], [179, 80], [-179, 80], [-179, -80]]),
  polyDeg2: poly([[0, 0], [10, 10], [0, 0]]),
  polyEmpty: poly([]),
  polyRingEmpty: poly([], [[0, 0], [5, 0], [5, 5], [0, 0]]),
  multipoly: { type: 'MultiPolygon', coordinates: [[[[170, 10], [-170, 10], [-170, 30], [170, 30], [170, 10]]], [[[0, 0], [20, 0], [20, 20], [0, 20], [0, 0]]]] },
  gc: { type: 'GeometryCollection', geometries: [{ type: 'Point', coordinates: [1, 2] }, line([[0, 0], [30, 30]]), poly([[0, 0], [10, 0], [10, 10], [0, 0]])] },
  feature: { type: 'Feature', properties: {}, geometry: line([[0, 0], [50, 50]]) },
  fc: { type: 'FeatureCollection', features: [{ type: 'Feature', geometry: { type: 'Point', coordinates: [5, 5] } }, { type: 'Feature', geometry: null }] },
  nullGeom: { type: 'Feature', geometry: null },
  circle0: d3.geoCircle().center([0, 0]).radius(90)(),
  circle1: d3.geoCircle().center([-100, 40]).radius(25).precision(3)(),
  circleAM: d3.geoCircle().center([179, 10]).radius(20)(),
  circlePole: d3.geoCircle().center([0, 88]).radius(10)(),
  circleBig: d3.geoCircle().center([30, -20]).radius(120)(),
  graticuleLines: d3.geoGraticule()(),
  graticuleOutline: d3.geoGraticule().outline(),
  graticule30: d3.geoGraticule().step([30, 30])(),
};
const big = { land, countries, states, nation, ...named, nyc: null };
delete big.nyc;

// Long strings are recorded as length + hash, short ones in full.
function summarize(s) {
  if (s == null || typeof s !== 'string') return s === undefined ? 'undefined' : s;
  return s.length > 400 ? { len: s.length, sha: sha(s) } : s;
}

write('geoms.json.gz', synth);

function pathOf(p, obj, digits) {
  const gp = d3.geoPath(p);
  if (digits !== undefined) gp.digits(digits);
  return gp(obj);
}

// ---- paths ----------------------------------------------------------------
{
  const out = [];
  for (const cfg of configs) {
    const p = make(cfg);
    const entry = { id: cfg.id, small: {}, big: {} };
    for (const [k, g] of Object.entries(synth)) {
      let s;
      try { s = pathOf(p, g); } catch (e) { s = 'ERR'; }
      entry.small[k] = summarize(s);
    }
    for (const [k, g] of Object.entries(big)) {
      if (!g) continue;
      let s;
      try { s = pathOf(p, g); } catch (e) { s = 'ERR'; }
      entry.big[k] = summarize(s);
    }
    out.push(entry);
  }
  write('paths.json.gz', { configs: configs.map((c) => ({ id: c.id, type: c.type, props: c.props })), cases: out });
}

// ---- fit ------------------------------------------------------------------
{
  const out = [];
  const targets = { land, usStates: states, russia: named.russia, fiji: named.fiji, antarctica: named.antarctica, nation, polyAM: synth.polyAM, multipoint: synth.multipoint };
  const fitCfgs = ['mercator', 'equirectangular', 'albers', 'albersusa', 'orthographic', 'naturalearth1', 'conicconformal', 'identity', 'transversemercator', 'stereographic', 'azimuthalequalarea', 'mollweide', 'equalearth', 'gnomonic']
    .map((t) => ({ type: t, props: {} }));
  fitCfgs.push({ type: 'mercator', props: { clipExtent: [[10, 10], [300, 200]] } });
  fitCfgs.push({ type: 'albers', props: { rotate: [90, 0], center: [0, 40] } });
  fitCfgs.push({ type: 'orthographic', props: { rotate: [-100, -40] } });
  fitCfgs.push({ type: 'identity', props: { reflectY: true } });
  fitCfgs.push({ type: 'mercator', props: { reflectX: true, precision: 0.1 } });
  for (const cfg of fitCfgs) {
    for (const [tk, t] of Object.entries(targets)) {
      for (const mode of ['extent', 'size', 'width', 'height']) {
        const p = make(cfg);
        const before = p.clipExtent ? p.clipExtent() : undefined;
        let ok = true;
        try {
          if (mode === 'extent') p.fitExtent([[12, 8], [512, 308]], t);
          else if (mode === 'size') p.fitSize([640, 360], t);
          else if (mode === 'width') p.fitWidth(500, t);
          else p.fitHeight(300, t);
        } catch (e) { ok = false; }
        const probe = [[0, 0], [-100, 40], [10, 50], [170, -20]].map((pt) => { const r = p(pt); return r == null ? null : [r[0], r[1]]; });
        const s = pathOf(p, t);
        out.push({ cfg: { type: cfg.type, props: cfg.props }, target: tk, mode, ok, scale: p.scale(), translate: p.translate(),
          clip: p.clipExtent ? p.clipExtent() : 'undefined', probe, path: s == null ? null : { len: s.length, sha: sha(s) } });
      }
    }
  }
  write('fit.json.gz', out);
}

// ---- measures ---------------------------------------------------------------
{
  const geoms = { ...synth, land, russia: named.russia, fiji: named.fiji, antarctica: named.antarctica,
    greenland: named.greenland, usa: named.usa, chile: named.chile, canada: named.canada, indonesia: named.indonesia,
    norway: named.norway, countries, states, nation };
  // A ring without points makes upstream's clipping throw, and its module-level
  // measure state then leaks into later calls: keep those degenerate polygons out.
  delete geoms.polyEmpty; delete geoms.polyRingEmpty;
  const spherical = {};
  for (const [k, g] of Object.entries(geoms)) {
    let e = {};
    try { e.area = d3.geoArea(g); } catch (x) { console.error('area threw', k); e.area = 'ERR'; }
    try { e.bounds = d3.geoBounds(g); } catch (x) { console.error('bounds threw', k); e.bounds = 'ERR'; }
    try { e.centroid = d3.geoCentroid(g); } catch (x) { console.error('centroid threw', k); e.centroid = 'ERR'; }
    try { e.length = d3.geoLength(g); } catch (x) { console.error('length threw', k); e.length = 'ERR'; }
    const probes = [[0, 0], [-100, 40], [10, 55], [170, -18], [-179.9, -15], [0, 90], [100, 62], [-3, 60]];
    try { e.contains = probes.map((pt) => d3.geoContains(g, pt)); } catch (x) { e.contains = 'ERR'; }
    spherical[k] = e;
  }
  const dist = [];
  for (let i = 0; i < 40; i++) {
    const a = [rnd() * 360 - 180, rnd() * 180 - 90], b = [rnd() * 360 - 180, rnd() * 180 - 90];
    const it = d3.geoInterpolate(a, b);
    dist.push({ a, b, d: d3.geoDistance(a, b), i: [0, 0.25, 0.5, 1].map((t) => it(t)), idist: it.distance });
  }
  dist.push({ a: [10, 10], b: [10, 10], d: d3.geoDistance([10, 10], [10, 10]), i: [0, 0.5].map((t) => d3.geoInterpolate([10, 10], [10, 10])(t)) });
  const rot = [];
  for (const r of [[0, 0], [30, 0], [0, 40], [20, -30, 10], [-170, 80, -20], [370, 0], [0, 0, 45]]) {
    const f = d3.geoRotation(r);
    rot.push({ r, f: [[0, 0], [10, 20], [-170, 80], [179, -45]].map((pt) => f(pt)), i: [[0, 0], [10, 20], [-170, 80]].map((pt) => f.invert(pt)) });
  }
  const circles = [];
  for (const [c, r, pr] of [[[0, 0], 90, 6], [[-100, 40], 25, 2], [[179, 10], 20, 5], [[0, 88], 10, 3], [[30, -20], 120, 10], [[0, 0], 5, 0.5]]) {
    circles.push({ c, r, pr, out: d3.geoCircle().center(c).radius(r).precision(pr)() });
  }

  // planar measures of projected geometry
  const planar = [];
  for (const cfg of [{ type: 'mercator' }, { type: 'albers' }, { type: 'orthographic' }, { type: 'equirectangular', props: { clipExtent: [[100, 100], [700, 350]] } }, { type: 'identity' }, { type: 'albersusa' }, { type: 'naturalearth1', props: { rotate: [-160, 0] } }]) {
    const p = make(cfg);
    const gp = d3.geoPath(p);
    for (const k of ['land', 'russia', 'fiji', 'usa', 'polyAM', 'polyPole', 'lineAM', 'multipoint', 'point', 'polyHole', 'gc', 'states', 'sphere', 'circle1']) {
      if (k === 'sphere' && cfg.type === 'identity') continue; // a projection-less path cannot stream a sphere
      const g = geoms[k];
      const e = { cfg: { type: cfg.type, props: cfg.props || {} }, k };
      // Upstream's measure streams keep module-level state that an exception
      // (a sphere streamed into a projection-less path) leaves behind, so cases
      // that throw are skipped and, to be safe, the corpus avoids them.
      try { e.area = gp.area(g); } catch (x) { console.error('planar area threw', cfg.type, k, x.message); e.area = 'ERR'; }
      try { e.measure = gp.measure(g); } catch (x) { console.error('planar measure threw', cfg.type, k); e.measure = 'ERR'; }
      try { e.bounds = gp.bounds(g); } catch (x) { console.error('planar bounds threw', cfg.type, k); e.bounds = 'ERR'; }
      try { e.centroid = gp.centroid(g); } catch (x) { console.error('planar centroid threw', cfg.type, k); e.centroid = 'ERR'; }
      planar.push(e);
    }
  }
  write('measures.json', { spherical, dist, rot, circles, planar });
}

// ---- graticule --------------------------------------------------------------
{
  const cfgs = [
    {},
    { step: [30, 30] },
    { extent: [[-90, -45], [90, 45]] },
    { extentMajor: [[-180, -90], [180, 90]], stepMajor: [45, 90] },
    { extentMinor: [[-60, -30], [60, 30]], stepMinor: [5, 5] },
    { precision: 10 },
    { precision: 0.5, step: [20, 20] },
    { stepMajor: [90, 360], stepMinor: [15, 15], extent: [[0, 0], [90, 60]] },
    { extent: [[90, 60], [0, 0]] },
    { step: [7, 11], stepMajor: [70, 110] },
    { precision: 1e9 },
  ];
  const out = cfgs.map((c) => {
    const g = d3.geoGraticule();
    for (const k of ['extent', 'extentMajor', 'extentMinor', 'step', 'stepMajor', 'stepMinor', 'precision']) if (c[k] !== undefined) g[k](c[k]);
    return { cfg: c, lines: g(), outline: g.outline() };
  });
  write('graticule.json.gz', out);
}

// ---- topojson ----------------------------------------------------------------
{
  const out = {};
  const h = (o) => { const s = JSON.stringify(o); return { len: s.length, sha: sha(s) }; };
  for (const [name, t] of [['world', world], ['us', us]]) {
    const res = {};
    for (const key of Object.keys(t.objects)) {
      const o = t.objects[key];
      res[key] = { feature: h(topo.feature(t, o)), mesh: h(topo.mesh(t, o)),
        interior: h(topo.mesh(t, o, (a, b) => a !== b)), exterior: h(topo.mesh(t, o, (a, b) => a === b)) };
    }
    out[name] = res;
  }
  // full copies of a small case
  out.small = topo.feature(world, world.objects.land);
  write('topo.json.gz', out);
}
// ---- random configurations -----------------------------------------------------
// Seeded random property sets (a projection type each), to reach branches the
// hand-written configurations miss. Recorded with the geometries that exercise
// clipping most: the world, countries crossing the antimeridian, poles, holes.
{
  seed = 20240607;
  const U = (a, b) => a + (b - a) * rnd();
  const pickOne = (arr) => arr[Math.floor(rnd() * arr.length)];
  const geomKeys = ['land', 'russia', 'fiji', 'antarctica', 'usa', 'polyAMwide', 'polyPole', 'polyHole', 'lineAM2', 'lineLong', 'circle1', 'circleBig', 'graticuleLines', 'sphere', 'multipoint'];
  const geomSet = { land, russia: named.russia, fiji: named.fiji, antarctica: named.antarctica, usa: named.usa, ...synth };
  const out = [];
  for (let i = 0; i < 260; i++) {
    const type = pickOne(TYPES);
    const props = {};
    if (rnd() < 0.7) props.scale = +U(40, 600).toFixed(3);
    if (rnd() < 0.7) props.translate = [+U(0, 900).toFixed(2), +U(0, 600).toFixed(2)];
    if (rnd() < 0.5) props.center = [+U(-180, 180).toFixed(3), +U(-80, 80).toFixed(3)];
    if (rnd() < 0.6) props.rotate = rnd() < 0.5 ? [+U(-180, 180).toFixed(2), +U(-90, 90).toFixed(2)] : [+U(-180, 180).toFixed(2), +U(-90, 90).toFixed(2), +U(-180, 180).toFixed(2)];
    if (rnd() < 0.35) props.clipAngle = +U(0, 175).toFixed(2);
    if (rnd() < 0.25) { const x0 = U(0, 300), y0 = U(0, 200); props.clipExtent = [[+x0.toFixed(1), +y0.toFixed(1)], [+(x0 + U(50, 700)).toFixed(1), +(y0 + U(50, 500)).toFixed(1)]]; }
    if (rnd() < 0.5) props.precision = pickOne([0, 0.1, 0.5, 1, 3, 10]);
    if (rnd() < 0.2) props.reflectX = true;
    if (rnd() < 0.2) props.reflectY = true;
    if (rnd() < 0.6) props.parallels = [+U(-80, 80).toFixed(2), +U(-80, 80).toFixed(2)];
    const p = make({ type, props });
    const e = { type, props, out: {} };
    for (const k of geomKeys) {
      let s;
      try { s = pathOf(p, geomSet[k]); } catch (x) { s = 'ERR'; }
      e.out[k] = summarize(s);
    }
    e.probe = pts.slice(0, 40).map((pt) => { const r = p(pt); return r == null ? null : [r[0], r[1]]; });
    out.push(e);
  }
  write('random.json.gz', out);
}

// ---- angle (a d3 setter Vega never calls) -----------------------------------
{
  const out = [];
  const subset = [[-100, 40], [10, 50], [0, 0], [170, -30], [-120, 20], [30, 60]];
  const xy = [[300, 200], [480, 250], [700, 100]];
  for (const [name, ctor] of [['mercator', d3.geoMercator], ['albers', d3.geoAlbers], ['orthographic', d3.geoOrthographic], ['equirectangular', d3.geoEquirectangular], ['identity', d3.geoIdentity]]) {
    for (const angle of [0, 25, -70, 400]) {
      const p = ctor().angle(angle);
      if (name !== 'identity') p.rotate([10, -20]);
      const fwd = subset.map((pt) => { const r = p(pt); return [r[0], r[1]]; });
      const inv = xy.map((q) => { const r = p.invert(q); return r ? [r[0], r[1]] : null; });
      const g = name === 'identity' ? synth.polyHole : synth.lineAM;
      out.push({ name, angle, fwd, inv, path: d3.geoPath(p)(g), got: p.angle() });
    }
  }
  write('angle.json', { subset, xy, out });
}

// ---- digits, point radius -----------------------------------------------
// d3-geo's PathString keeps module-level caches (the rounding function, the
// circle fragment of the last point radius) that a path drawn with rounding
// turned off leaves stale for the next one. Vega never changes digits, so the
// recorded cases avoid that: point radii are recorded first, at the default
// digits, and the digits sweep draws no isolated points. This block must stay
// last.
{
  const p = make({ type: 'mercator', props: {} });
  const radii = [];
  for (const r of [0, 1, 4.5, 10.25, 0.0004, 1234.5678, -5]) {
    const gp = d3.geoPath(p).pointRadius(r);
    radii.push({ r, point: gp(synth.point), multipoint: gp(synth.multipoint) });
  }
  const gpf = d3.geoPath(p).pointRadius((f) => (f && f.coordinates ? f.coordinates[0] / 20 : 3));
  radii.push({ r: 'fn', point: gpf(synth.point), multipoint: gpf(synth.multipoint) });
  const out = [];
  const objs = { land, lineAM: synth.lineAM, polyAM: synth.polyAM, polyHole: synth.polyHole };
  for (const digits of [0, 1, 2, 3, 4, 6, 15, 16, null]) {
    const e = { digits, out: {} };
    for (const [k, g] of Object.entries(objs)) {
      const gp = d3.geoPath(p); gp.digits(digits);
      const s = gp(g);
      e.out[k] = s == null ? null : (s.length > 2000 ? { len: s.length, sha: sha(s) } : s);
    }
    out.push(e);
  }
  write('digits.json', { digits: out, radii });
}
console.error('done');
