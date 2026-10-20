// Records d3-geo with coordinates that are not numbers ("x", null, true),
// which upstream reads as NaN or 0: a test of JavaScript's truthiness where
// d3 writes `!x` or `x || y` on a number that may be NaN (C122 of the
// 2026-10-18 audit: circle clipping's `!determinant`, the rectangle clip's
// `!dx`, the adder's `if (lo)`, polygonContains' `arc[0] || arc[1]`).
//   NODE_PATH=../../testdata/oracle-node/node_modules node testdata/gen_nan.mjs > testdata/nan.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const g = await import(require.resolve('d3-geo'));
const geoms = [
  { type: 'LineString', coordinates: [[0, 0], [10, 'x'], [120, 0], [30, 10]] },
  { type: 'Polygon', coordinates: [[[0, 0], [10, 'x'], [120, 0], [30, 10], [0, 0]]] },
  { type: 'LineString', coordinates: [[0, 0], [10, null], [120, 0]] },
  { type: 'LineString', coordinates: [['x', 5], [20, 5], [30, 'x'], [-170, 60]] },
  { type: 'Polygon', coordinates: [[[-10, -10], [10, -10], ['x', 10], [-10, 10], [-10, -10]]] },
  { type: 'Polygon', coordinates: [[[100, 10], [170, 10], [170, 'x'], [100, 60], [100, 10]]] },
  { type: 'MultiPoint', coordinates: [['a', 3], [3, 4]] },
  { type: 'LineString', coordinates: [[-200, 0], [200, 'x'], [0, 89]] },
  { type: 'LineString', coordinates: [[-170, 5], [10, 'x'], [20, 5]] },
  { type: 'LineString', coordinates: [[0, 80], ['x', 0], [20, 5]] },
  { type: 'LineString', coordinates: [[0, 80], [5, 'x'], [20, 5], [0, -80], [6, 'x'], [1, 1]] },
  { type: 'LineString', coordinates: [[-170, 5], ['x', 5], [20, 5], [170, 5], ['x', 6], [0, 5]] },
  { type: 'LineString', coordinates: [[170, -5], [10, 'x'], [0, -80], ['x', 3], [5, 5]] },
  { type: 'Polygon', coordinates: [[[-170, 5], [10, 'x'], [20, 5], [-170, 5]]] },
];
const projs = [
  ['orthographic', {}], ['stereographic', {}], ['gnomonic', {}], ['mercator', {}],
  ['mercator', { clipExtent: [[100, 100], [700, 400]] }], ['equirectangular', { clipExtent: [[300, 150], [600, 300]] }],
  ['orthographic', { clipExtent: [[300, 150], [600, 300]] }], ['azimuthalEqualArea', { clipAngle: 90 }],
];
const ctor = (n) => g['geo' + n[0].toUpperCase() + n.slice(1)]();
const paths = [];
for (const geom of geoms) {
  for (const [name, opts] of projs) {
    const p = ctor(name);
    if (opts.clipExtent) p.clipExtent(opts.clipExtent);
    if (opts.clipAngle) p.clipAngle(opts.clipAngle);
    let r;
    try { r = g.geoPath(p)(geom); } catch (e) { r = 'ERR ' + e.message; }
    paths.push({ geom, name, opts, path: r });
  }
}
const n = (x) => (Number.isNaN(x) ? 'NaN' : x);
// d3-array's Adder (d3-geo's) with infinities: a NaN partial is dropped
// (`if (lo)`), so Infinity + 1 stays Infinity.
const { Adder } = await import(require.resolve('d3-array'));
const sums = [[Infinity, 1], [1, Infinity], [-Infinity, 1, 2], [1e308, 1e308, -1e308], [Infinity, -Infinity, 5], [1, NaN, 2],
  [Infinity, 1e-300, -1e308]].map((xs) => {
  const a = new Adder();
  for (const x of xs) a.add(x);
  return { xs: xs.map(String), sum: String(+a) };
});
const measures = geoms.map((geom) => ({ geom, area: n(g.geoArea(geom)), contains: [[0, 0], [5, 5], [130, 30], [180, 0]].map((pt) => g.geoContains(geom, pt)) }));
process.stdout.write(JSON.stringify({ paths, measures, sums }, null, 0) + '\n');
