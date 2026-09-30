// Records golden vectors for the force transform by running a Vega view.
//   NODE_PATH=purego/testdata/oracle-node/node_modules node testdata/gen_force.mjs > testdata/force.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

// Deterministic pseudo-random node attributes (not related to the simulation RNG).
function mk(n, seed) {
  let s = seed;
  const r = () => (s = (s * 16807) % 2147483647) / 2147483647;
  const nodes = [];
  for (let i = 0; i < n; i++) nodes.push({ id: 'n' + i, r: 2 + Math.floor(r() * 8), w: r() * 100 });
  const edges = [];
  for (let i = 1; i < n; i++) edges.push({ source: 'n' + Math.floor(r() * i), target: 'n' + i, k: 1 + r() * 3 });
  for (let i = 0; i < n / 3; i++) edges.push({ source: 'n' + Math.floor(r() * n), target: 'n' + Math.floor(r() * n), k: 1 });
  return { nodes, edges };
}

const cases = [];
async function run(name, nodes, edges, force, extra = {}) {
  const inNodes = JSON.parse(JSON.stringify(nodes)), inEdges = JSON.parse(JSON.stringify(edges)); // Vega mutates its input tuples
  const spec = {
    $schema: 'https://vega.github.io/schema/vega/v6.json',
    data: [
      { name: 'edges', values: edges },
      { name: 'nodes', values: nodes, transform: [{ type: 'force', ...extra, forces: force }] },
    ],
  };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  await view.runAsync();
  const out = view.data('nodes').map(d => ({ x: d.x, y: d.y, vx: d.vx, vy: d.vy, index: d.index }));
  const links = view.data('edges').map(l => ({ index: l.index, source: l.source && l.source.index, target: l.target && l.target.index }));
  cases.push({ name, nodes: inNodes, edges: inEdges, spec: { forces: force, ...extra }, out, links });
}

let a = mk(40, 7);
await run('nbody_center_static', a.nodes, [], [{ force: 'center', x: 5, y: -3 }, { force: 'nbody', strength: -20, theta: 0.7, distanceMin: 2, distanceMax: 60 }], { static: true });
a = mk(40, 7);
await run('collide_xy_static', a.nodes, [], [{ force: 'collide', radius: { expr: 'datum.r' }, strength: 0.8, iterations: 2 }, { force: 'x', x: 'w', strength: 0.05 }, { force: 'y', strength: 0.2 }], { static: true, iterations: 120 });
let b = mk(25, 11);
await run('link_default', b.nodes, b.edges, [{ force: 'center', x: 0, y: 0 }, { force: 'nbody' }, { force: 'link', links: 'edges', id: 'id', distance: 40 }], { static: true });
b = mk(25, 11);
await run('link_expr', b.nodes, b.edges, [{ force: 'link', links: 'edges', id: 'id', distance: { expr: 'datum.k * 20' }, strength: { expr: '0.3' }, iterations: 3 }, { force: 'nbody', strength: { expr: '-datum.r * 3' } }], { static: true, iterations: 200, alpha: 0.7, alphaMin: 0.01, velocityDecay: 0.3 });
b = mk(25, 11);
await run('t_link_iter3', b.nodes, b.edges, [{ force: 'link', links: 'edges', id: 'id', distance: 40, iterations: 3 }], { static: true, iterations: 200 });
b = mk(25, 11);
await run('t_link_expr', b.nodes, b.edges, [{ force: 'link', links: 'edges', id: 'id', distance: { expr: 'datum.k * 20' }, strength: { expr: '0.3' } }], { static: true, iterations: 200 });
b = mk(25, 11);
await run('t_nbody_expr', b.nodes, b.edges, [{ force: 'nbody', strength: { expr: '-datum.r * 3' } }], { static: true, iterations: 200 });
b = mk(25, 11);
await run('t_params', b.nodes, b.edges, [{ force: 'nbody' }], { static: true, iterations: 200, alpha: 0.7, alphaMin: 0.01, velocityDecay: 0.3 });
for (const [k, ex] of [['alpha', { alpha: 0.7 }], ['alphaMin', { alphaMin: 0.01 }], ['vd', { velocityDecay: 0.3 }]]) {
b = mk(25, 11);
await run('t_p_' + k, b.nodes, b.edges, [{ force: 'nbody' }], { static: true, iterations: 200, ...ex });
}
const mkco = () => { const co = []; for (let i = 0; i < 12; i++) co.push({ id: 'c' + i, r: 5, x: 0, y: 0 }); return co; };
await run('coincident', mkco(), [], [{ force: 'nbody' }, { force: 'collide', radius: 6 }], { static: true, iterations: 50 });
await run('coincident_nbody', mkco(), [], [{ force: 'nbody' }], { static: true, iterations: 5 });
for (const it of [1, 10]) await run('coincident_both' + it, mkco(), [], [{ force: 'nbody' }, { force: 'collide', radius: 6 }], { static: true, iterations: it });
await run('coincident_both5', mkco(), [], [{ force: 'nbody' }, { force: 'collide', radius: 6 }], { static: true, iterations: 5 });
await run('coincident_collide', mkco(), [], [{ force: 'collide', radius: 6 }], { static: true, iterations: 5 });
const fx = mk(10, 3).nodes; fx[0].fx = 0; fx[0].fy = 0; fx[3].fx = 50; fx[5].x = 10; fx[5].y = 20;
await run('fixed', fx, [], [{ force: 'center' }, { force: 'nbody' }, { force: 'collide', radius: 4 }], { static: true, iterations: 60 });
a = mk(40, 7);
await run('nonstatic_first_tick', a.nodes, [], [{ force: 'center' }, { force: 'nbody' }, { force: 'collide', radius: 4 }]);
// Even signal-driven alpha/alphaMin/alphaTarget/velocityDecay are ignored on the
// first pass (see Params.Bound): the operator's initial value is already in place
// before the first pulse, so nothing counts as modified.
{
  const s = mk(20, 5);
  const inNodes = JSON.parse(JSON.stringify(s.nodes));
  const spec = {
    signals: [{ name: 'al', value: 0.5 }, { name: 'am', value: 0.02 }, { name: 'vd', value: 0.25 }, { name: 'at', value: 0.05 }],
    data: [{ name: 'nodes', values: s.nodes, transform: [{ type: 'force', static: true, iterations: 100, alpha: { signal: 'al' }, alphaMin: { signal: 'am' }, velocityDecay: { signal: 'vd' }, alphaTarget: { signal: 'at' }, forces: [{ force: 'center' }, { force: 'nbody' }] }] }],
  };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' });
  await view.runAsync();
  cases.push({ name: 'signal_params_ignored', nodes: inNodes, edges: [],
    spec: { forces: [{ force: 'center' }, { force: 'nbody' }], static: true, iterations: 100, alpha: 0.5, alphaMin: 0.02, velocityDecay: 0.25 },
    out: view.data('nodes').map(d => ({ x: d.x, y: d.y, vx: d.vx, vy: d.vy, index: d.index })), links: [] });
}
// Params.Bound: all four parameters applied, recorded from d3-force directly.
{
  const d3 = await import(require.resolve('d3-force'));
  const s = mk(20, 5);
  const inNodes = JSON.parse(JSON.stringify(s.nodes));
  const nodes = s.nodes;
  const sim = d3.forceSimulation(nodes).stop().force('forces0', d3.forceCenter()).force('forces1', d3.forceManyBody());
  sim.alpha(0.5).alphaMin(0.02).velocityDecay(0.25).alphaTarget(0.05);
  sim.alpha(Math.max(sim.alpha(), 0.5)).alphaDecay(1 - Math.pow(sim.alphaMin(), 1 / 100));
  for (let i = 0; i < 100; i++) sim.tick();
  cases.push({ name: 'bound_params', nodes: inNodes, edges: [], bound: { alphaTarget: 0.05 },
    spec: { forces: [{ force: 'center' }, { force: 'nbody' }], static: true, iterations: 100, alpha: 0.5, alphaMin: 0.02, velocityDecay: 0.25 },
    out: nodes.map(d => ({ x: d.x, y: d.y, vx: d.vx, vy: d.vy, index: d.index })), links: [] });
}
console.log(JSON.stringify(cases));
