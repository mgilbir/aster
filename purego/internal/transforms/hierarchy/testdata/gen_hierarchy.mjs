// Records golden vectors for the hierarchy transforms from upstream vega.
//   NODE_PATH=purego/testdata/oracle-node/node_modules node gen_hierarchy.mjs > hierarchy.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

// Deterministic pseudo-random data (own LCG; independent of upstream RNGs).
let seed = 12345;
const rnd = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;

function makeTree(n, withBranchBias) {
  const rows = [{ id: 'n0', parent: null, size: 0, name: 'root', cat: 'c0', yr: 2000 }];
  for (let i = 1; i < n; i++) {
    const p = withBranchBias ? Math.floor(rnd() * Math.min(i, 8)) : Math.floor(rnd() * i);
    rows.push({
      id: 'n' + i, parent: 'n' + p,
      size: Math.floor(rnd() * 40) + 1,
      name: 'node' + i, cat: 'c' + Math.floor(rnd() * 4), yr: 2000 + Math.floor(rnd() * 5) * 3,
    });
  }
  return rows;
}
// leaf sizes only matter; give internal nodes a size of 0 so sums are the leaf sums
const treeA = makeTree(60, false);
const treeB = makeTree(150, true);
const flat = makeTree(40, false).map(({ id, size, cat, yr }) => ({ id, size, cat, yr, sub: 's' + (size % 3) }));

const cases = [];
const datasets = { treeA, treeB, flat, treeC: makeTree(600, false) };
function tc(name, values, transforms, extra = {}) {
  const dataset = Object.keys(datasets).find(k => datasets[k] === values);
  cases.push({ name, dataset, transforms, ...extra });
}

const strat = { type: 'stratify', key: 'id', parentKey: 'parent' };
// vega applies sort comparators to hierarchy nodes, not tuples: 'value' is the
// summed node value and tuple fields live under 'data'.
const cmpSize = { field: 'value', order: 'descending' };
const cmpName = { field: 'data.name' };

tc('tidy', treeA, [strat, { type: 'tree', method: 'tidy' }]);
tc('tidy_size', treeA, [strat, { type: 'tree', method: 'tidy', size: [300, 200] }]);
tc('tidy_nodesize', treeB, [strat, { type: 'tree', method: 'tidy', nodeSize: [10, 30] }]);
tc('tidy_nosep', treeB, [strat, { type: 'tree', method: 'tidy', size: [500, 100], separation: false }]);
tc('tidy_as', treeA, [strat, { type: 'tree', as: ['px', 'py', 'd', 'kids'], size: [1, 1] }]);
tc('cluster', treeA, [strat, { type: 'tree', method: 'cluster', size: [400, 300] }]);
tc('cluster_nodesize', treeB, [strat, { type: 'tree', method: 'cluster', nodeSize: [5, 20], separation: false }]);
tc('cluster_default', treeB, [strat, { type: 'tree', method: 'cluster' }]);

tc('pack', treeA, [strat, { type: 'pack', field: 'size', size: [400, 400] }]);
tc('pack_padding', treeB, [strat, { type: 'pack', field: 'size', size: [600, 400], padding: 3 }]);
tc('pack_count', treeB, [strat, { type: 'pack', size: [300, 300] }]);
tc('pack_sort', treeB, [strat, { type: 'pack', field: 'size', sort: cmpSize, size: [500, 500], padding: 2 }]);
tc('pack_radius', treeA, [strat, { type: 'pack', radius: { field: 'data.size' }, size: [500, 500], padding: 1 }]);
tc('pack_big', datasets.treeC, [strat, { type: 'pack', field: 'size', size: [800, 800], padding: 1 }]);

for (const method of ['squarify', 'resquarify', 'binary', 'dice', 'slice', 'slicedice']) {
  tc('treemap_' + method, treeB, [strat, { type: 'treemap', field: 'size', method, size: [600, 400], sort: cmpSize }]);
}
// Second run on the same tree with a new size: resquarify keeps the row
// arrangement of the first run, squarify recomputes it.
for (const method of ['resquarify', 'squarify']) {
  tc('treemap_rerun_' + method, treeB, [strat, { type: 'treemap', field: 'size', method, size: [{ signal: 'w' }, 400] }],
    { rerun: { signal: 'w', first: 600, second: 250, values2: true } });
}
tc('treemap_ratio', treeB, [strat, { type: 'treemap', field: 'size', ratio: 1, size: [600, 400], sort: cmpSize }]);
tc('treemap_ratio3', treeB, [strat, { type: 'treemap', field: 'size', method: 'squarify', ratio: 3, size: [300, 700] }]);
tc('treemap_padding', treeB, [strat, { type: 'treemap', field: 'size', padding: 4, size: [600, 400] }]);
tc('treemap_padding_inner_outer', treeA, [strat, { type: 'treemap', field: 'size', paddingInner: 3, paddingOuter: 6, round: true, size: [500, 500] }]);
tc('treemap_sides', treeA, [strat, { type: 'treemap', field: 'size', paddingTop: 20, paddingRight: 2, paddingBottom: 5, paddingLeft: 7, paddingInner: 1, size: [500, 500] }]);
tc('treemap_round', treeB, [strat, { type: 'treemap', field: 'size', round: true, size: [333, 217], method: 'binary' }]);
tc('treemap_count', treeB, [strat, { type: 'treemap', size: [100, 100], as: ['a0', 'b0', 'a1', 'b1', 'dd', 'kk'] }]);
tc('treemap_bigpad', treeA, [strat, { type: 'treemap', field: 'size', padding: 60, size: [200, 200] }]);

tc('partition', treeB, [strat, { type: 'partition', field: 'size', size: [600, 300] }]);
tc('partition_padding', treeA, [strat, { type: 'partition', field: 'size', size: [600, 300], padding: 2, round: true }]);
tc('partition_sort', treeA, [strat, { type: 'partition', field: 'size', size: [1, 1], sort: cmpName }]);

tc('nest_treemap', flat, [{ type: 'nest', keys: ['cat', 'yr'], generate: true },
  { type: 'treemap', field: 'size', size: [500, 400], padding: 2 }], { fields: ['id', 'key', 'size'] });
tc('nest_nogen', flat, [{ type: 'nest', keys: ['sub'] }, { type: 'tree', method: 'tidy' }], { fields: ['id', 'size'] });
tc('nest_pack', flat, [{ type: 'nest', keys: ['yr', 'cat', 'sub'], generate: true }, { type: 'pack', field: 'size', size: [300, 300] }], { fields: ['id', 'key', 'size'] });

tc('links', treeA, [strat, { type: 'tree' }], { links: true });

function scalarRow(t, fields) {
  const o = {};
  for (const k of Object.keys(t)) {
    const v = t[k];
    if (typeof v === 'number' || typeof v === 'string' || typeof v === 'boolean' || v === null) o[k] = v;
  }
  if (fields) for (const k of Object.keys(o)) if (!fields.includes(k) && !isLayout(k, o)) delete o[k];
  return o;
}
const layoutKeys = new Set(['x','y','r','x0','y0','x1','y1','depth','children','px','py','d','kids','a0','b0','a1','b1','dd','kk']);
function isLayout(k) { return layoutKeys.has(k); }

for (const c of cases) {
  const spec = {
    signals: [{ name: 'w', value: c.rerun ? c.rerun.first : 0 }],
    data: [
      { name: 'tree', values: JSON.parse(JSON.stringify(datasets[c.dataset])), transform: c.transforms },
      ...(c.links ? [{ name: 'links', source: 'tree', transform: [{ type: 'treelinks' }] }] : []),
    ],
  };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  await view.runAsync();
  c.output = view.data('tree').map(t => scalarRow(t, c.fields));
  if (c.rerun) {
    view.signal('w', c.rerun.second);
    await view.runAsync();
    c.output2 = view.data('tree').map(t => scalarRow(t, c.fields));
  }
  if (c.links) c.linkOutput = view.data('links').map(l => [l.source.id, l.target.id]);
}
process.stdout.write(JSON.stringify({ datasets, cases }));
