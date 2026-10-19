// Records golden vectors for the voronoi transform on hostile coordinates:
// non-finite values, magnitudes whose squares overflow, subnormals. Upstream
// (vega-voronoi, d3-delaunay, delaunator) answers each of them without
// hanging, so the port must give the same cells.
//
//   NODE_PATH=testdata/oracle-node/node_modules \
//     node testdata/gen_voronoi_hostile.mjs > testdata/voronoi_hostile.json
//
// A coordinate that JSON cannot hold is written as a string ("Infinity",
// "-Infinity", "NaN"); vega-voronoi stores it in a Float64Array, which
// coerces it exactly as Number() does.
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.resolve(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const { Delaunay } = await import(require.resolve('d3-delaunay'));

function rng(seed) {
  let s = seed >>> 0;
  return () => ((s = (Math.imul(s, 1664525) + 1013904223) >>> 0) / 4294967296);
}

const extremes = [1e308, -1e308, 1.7976931348623157e308, 5e-324, -5e-324, 1e154, -1e154, 1e200, 'Infinity', '-Infinity', 'NaN', 0, 1, 2];
function hostile(n, seed, pExtreme) {
  const r = rng(seed);
  const coord = () => (r() < pExtreme ? extremes[Math.floor(r() * extremes.length)] : Math.round(r() * 1000) / 10);
  return Array.from({ length: n }, () => [coord(), coord()]);
}

const cases = [];
const add = (name, points, opts = {}) => cases.push({ name, points, ...opts });

// The 2026-10-11 audit's C2 input: the seed point is never found (every
// distance from the bounding box centre overflows), which upstream reads as
// undefined.
add('audit-c2', [[97.2, 82.1], [5e-324, -1e308], [-1e308, 41.2], [1, 36.3], [95.7, 0], [19.2, -1e308], [-1e308, -1e308], [64.9, 70.8]], { size: [100, 100] });
add('all-overflow', [[1e308, 1e308], [-1e308, -1e308], [1e308, -1e308], [-1e308, 1e308], [0, 0]], { size: [100, 100] });
add('one-far', [[10, 10], [20, 30], [40, 10], [1e308, 1e308]], { size: [100, 100] });
add('infinite', [[10, 10], [20, 30], ['Infinity', 5], [40, 10], [5, '-Infinity']], { size: [100, 100] });
add('nan', [['NaN', 'NaN'], [10, 10], [20, 30], [40, 10]], { size: [100, 100] });
add('all-nan', [['NaN', 1], ['NaN', 2], ['NaN', 3]], { size: [100, 100] });
add('subnormal', [[5e-324, 0], [0, 5e-324], [-5e-324, 0], [0, -5e-324], [5e-324, 5e-324]], { size: [100, 100] });
add('huge-extent', [[0, 0], [1, 1], [2, 0]], { extent: [[-1e308, -1e308], [1e308, 1e308]] });
for (let seed = 1; seed <= 60; seed++) {
  add(`random-${seed}`, hostile(4 + (seed % 9), seed, seed % 3 === 0 ? 0.6 : 0.3), { size: [100, 100] });
}
// Larger sets found by a 3,000-seed sweep to exercise the paths small ones
// miss: infinite orientation terms, and hull links read at undefined.
for (const seed of [1166, 1237, 1392, 1461, 2357, 2420, 2446, 2563, 2748, 2774]) {
  add(`random-${seed}`, hostile(3 + (seed % 23), seed, seed % 3 === 0 ? 0.6 : 0.3), { size: [100, 100] });
}

const out = [];
for (const c of cases) {
  const values = c.points.map(([x, y], i) => ({ x, y, i }));
  const transform = { type: 'voronoi', x: 'x', y: 'y' };
  if (c.size) transform.size = c.size;
  if (c.extent) transform.extent = c.extent;
  const spec = { data: [{ name: 't', values, transform: [transform] }] };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  await view.runAsync();
  const paths = view.data('t').map((d) => d.path);
  const d = Delaunay.from(c.points);
  out.push({
    name: c.name,
    points: c.points,
    size: c.size ?? null,
    extent: c.extent ? [...c.extent[0], ...c.extent[1]] : null,
    paths,
    triangles: Array.from(d.triangles),
    halfedges: Array.from(d.halfedges),
    hull: Array.from(d.hull),
    collinear: d.collinear ? Array.from(d.collinear) : null,
  });
}
process.stdout.write(JSON.stringify({ upstream: 'vega 6.4.0 / d3-delaunay 6.0.4 / delaunator 5.0.0', cases: out }));
