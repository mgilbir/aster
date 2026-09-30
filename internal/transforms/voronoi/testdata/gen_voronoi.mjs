// Records golden vectors for the voronoi transform from upstream vega 6.x
// (vega-voronoi + d3-delaunay + delaunator).
//
//   NODE_PATH=testdata/oracle-node/node_modules \
//     node testdata/gen_voronoi.mjs > testdata/voronoi.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const { Delaunay } = await import(require.resolve('d3-delaunay'));

// Deterministic point generator (LCG) so the cases are reproducible.
function rng(seed) {
  let s = seed >>> 0;
  return () => ((s = (Math.imul(s, 1664525) + 1013904223) >>> 0) / 4294967296);
}
function randomPoints(n, seed, w = 400, h = 300, round = false) {
  const r = rng(seed);
  return Array.from({ length: n }, () => {
    const x = r() * w, y = r() * h;
    return round ? [Math.round(x), Math.round(y)] : [x, y];
  });
}

const cases = [];
const add = (name, points, opts = {}) => cases.push({ name, points, ...opts });

add('random5', randomPoints(5, 1), { size: [400, 300] });
add('random50', randomPoints(50, 2), { size: [400, 300] });
add('random300', randomPoints(300, 3), { size: [400, 300] });
add('random50-default-extent', randomPoints(50, 4));
add('random50-extent', randomPoints(50, 5), { extent: [[20, 30], [380, 270]] });
add('random50-extent-inside', randomPoints(50, 6), { extent: [[100, 100], [250, 200]] });
add('points-outside', randomPoints(40, 7, 800, 600), { size: [400, 300] });
add('integers-dups', randomPoints(200, 8, 40, 30, true), { size: [400, 300] });
add('grid', Array.from({ length: 64 }, (_, i) => [(i % 8) * 50, Math.floor(i / 8) * 40]), { size: [400, 320] });
add('grid-offset', Array.from({ length: 49 }, (_, i) => [10 + (i % 7) * 33.3, 7 + Math.floor(i / 7) * 21.7]), { size: [400, 300] });
add('circle', Array.from({ length: 24 }, (_, i) => [200 + 100 * Math.cos(i * Math.PI / 12), 150 + 100 * Math.sin(i * Math.PI / 12)]), { size: [400, 300] });
add('one', [[10, 20]], { size: [400, 300] });
add('two', [[10, 20], [300, 200]], { size: [400, 300] });
add('two-same', [[10, 20], [10, 20]], { size: [400, 300] });
add('three-same', [[5, 5], [5, 5], [5, 5]], { size: [400, 300] });
add('three-collinear', [[10, 10], [100, 100], [200, 200]], { size: [400, 300] });
add('collinear-horizontal', Array.from({ length: 7 }, (_, i) => [20 + i * 50, 100]), { size: [400, 300] });
add('collinear-vertical', Array.from({ length: 6 }, (_, i) => [100, 10 + i * 40]), { size: [400, 300] });
add('collinear-shuffled', [[300, 300], [0, 0], [200, 200], [100, 100], [150, 150]], { size: [400, 400] });
add('near-collinear', [[0, 0], [1, 1e-12], [2, 0], [3, 1e-13], [1.5, 1e-9], [100, 100]], { size: [400, 300] });
add('near-dups', [[10, 10], [10, 10 + 1e-17], [50, 60], [200, 100], [90, 250], [10, 10]], { size: [400, 300] });
add('one-empty-size', randomPoints(10, 9), { size: [0, 0] });
add('degenerate-extent-line', randomPoints(10, 10), { extent: [[50, 50], [50, 200]] });
add('negative-coords', randomPoints(30, 11, 200, 200).map(([x, y]) => [x - 100, y - 100]), { extent: [[-100, -100], [100, 100]] });

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
