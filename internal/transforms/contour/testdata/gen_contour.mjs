// Records golden vectors for the contour package from upstream vega 6.4.0.
// Run: NODE_PATH=<node_modules with vega> node testdata/gen_contour.mjs > testdata/contour.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

// Deterministic pseudo-random points (LCG) so the data is reproducible.
function lcg(seed) {
  let s = seed >>> 0;
  return () => (s = (Math.imul(s, 1664525) + 1013904223) >>> 0) / 4294967296;
}
function points(n, seed, w, h) {
  const r = lcg(seed), out = [];
  for (let i = 0; i < n; ++i) {
    // two clusters plus uniform noise
    const c = r() < 0.5 ? [w * 0.3, h * 0.35] : [w * 0.7, h * 0.65];
    const gx = (r() + r() + r() - 1.5) * w * 0.25, gy = (r() + r() + r() - 1.5) * h * 0.25;
    out.push({ x: r() < 0.1 ? r() * w : c[0] + gx, y: r() < 0.1 ? r() * h : c[1] + gy,
               w: 1 + Math.floor(r() * 3), g: r() < 0.6 ? 'a' : 'b' });
  }
  return out;
}
function surface(w, h) {
  const v = [];
  for (let y = 0; y < h; ++y) for (let x = 0; x < w; ++x) {
    const a = Math.exp(-((x - w * 0.3) ** 2 + (y - h * 0.4) ** 2) / 12);
    const b = Math.exp(-((x - w * 0.7) ** 2 + (y - h * 0.6) ** 2) / 8);
    v.push(a + 0.8 * b + 0.05 * Math.sin(x * 0.9) * Math.cos(y * 0.7));
  }
  return v;
}
const ser = v => JSON.parse(JSON.stringify(v, (k, x) =>
  ArrayBuffer.isView(x) ? Array.from(x) : x));

async function run(name, data, transforms, outs) {
  const spec = { $schema: 'https://vega.github.io/schema/vega/v6.json', width: 10, height: 10,
    data: [{ name: 'input', values: data }, ...transforms] };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  await view.runAsync();
  const result = { name, data, transforms: transforms.flatMap(t => t.transform), outputs: {} };
  for (const o of outs) result.outputs[o] = ser(view.data(o));
  // Isocontour copies the source grid into every output tuple; drop it to keep
  // the recording small (the Go test skips that field too).
  if (name.startsWith('iso_')) for (const t of result.outputs.iso) delete t.grid;
  return result;
}

const cases = [];
const S1 = surface(24, 18), S2 = surface(3, 3), S3 = surface(40, 30);
const pts = points(300, 7, 200, 150), small = points(40, 3, 60, 40);
const C = (p, src = 'input') => [{ name: 'out', source: src, transform: [{ type: 'contour', ...p }] }];

for (const [n, p] of Object.entries({
  grid_count5: { size: [24, 18], values: S1, count: 5 },
  grid_nice: { size: [24, 18], values: S1, count: 6, nice: true },
  grid_thresholds: { size: [24, 18], values: S1, thresholds: [0.2, 0.5, 0.9] },
  grid_nosmooth: { size: [24, 18], values: S1, count: 4, smooth: false },
  grid_tiny: { size: [3, 3], values: S2, thresholds: [0.3, 0.6] },
  grid_big: { size: [40, 30], values: S3, count: 12 },
  grid_short: { size: [24, 18], values: S1.slice(0, 300), count: 3 },
}))
  cases.push(await run('contour_' + n, [{}], C(p), ['out']));

for (const [n, p] of Object.entries({
  kde_default: { size: [200, 150], x: 'x', y: 'y' },
  kde_bw: { size: [200, 150], x: 'x', y: 'y', cellSize: 2, bandwidth: 10, count: 7 },
  kde_nice: { size: [200, 150], x: 'x', y: 'y', nice: true, count: 8 },
  kde_weight: { size: [200, 150], x: 'x', y: 'y', weight: 'w', cellSize: 8, bandwidth: 30 },
  kde_thresh: { size: [200, 150], x: 'x', y: 'y', thresholds: [0.005, 0.02] },
}))
  cases.push(await run('contour_' + n, pts, C(p), ['out']));
cases.push(await run('contour_kde_cell1', small, C({ size: [60, 40], x: 'x', y: 'y', cellSize: 1, bandwidth: 5, count: 5 }), ['out']));
cases.push(await run('contour_kde_empty', [], C({ size: [100, 50], x: 'x', y: 'y' }), ['out']));

const K = (p) => [{ name: 'kde', source: 'input', transform: [{ type: 'kde2d', ...p }] }];
for (const [n, p] of Object.entries({
  plain: { size: [200, 150], x: 'x', y: 'y' },
  counts: { size: [200, 150], x: 'x', y: 'y', counts: true, cellSize: 4 },
  group: { size: [200, 150], x: 'x', y: 'y', groupby: ['g'], bandwidth: [25, 15], as: 'dens' },
  weight_c2: { size: [200, 150], x: 'x', y: 'y', weight: 'w', cellSize: 2, bandwidth: [10] },
  rx_only: { size: [200, 150], x: 'x', y: 'y', cellSize: 4, bandwidth: [30, 1] },
}))
  cases.push(await run('kde2d_' + n, pts, K(p), ['kde']));

const I = (p) => [...K({ size: [200, 150], x: 'x', y: 'y', groupby: ['g'], cellSize: 4, bandwidth: [20, 20] }),
  { name: 'iso', source: 'kde', transform: [{ type: 'isocontour', field: 'grid', ...p }] }];
for (const [n, p] of Object.entries({
  levels5: { levels: 5 },
  nice: { levels: 6, nice: true },
  shared: { levels: 4, resolve: 'shared' },
  nozero: { levels: 4, zero: false },
  thresholds: { thresholds: [0.0005, 0.001] },
  scale2: { levels: 3, scale: 2, translate: [10, 20] },
  flip: { levels: 3, scale: [1, -1], translate: [0, 150] },
  asnull: { levels: 3, as: null },
  asfield: { levels: 3, as: 'c' },
  nosmooth: { levels: 3, smooth: false },
}))
  cases.push(await run('iso_' + n, points(200, 11, 200, 150), I(p), ['iso']));

// Isocontour over a hand-made grid tuple (no scale: coordinates stay raster).
cases.push(await run('iso_rawgrid',
  [{ id: 1, grid: { values: S1, width: 24, height: 18 } }],
  [{ name: 'iso', source: 'input', transform: [{ type: 'isocontour', field: 'grid', levels: 4 }] }], ['iso']));

process.stdout.write(JSON.stringify(cases));
