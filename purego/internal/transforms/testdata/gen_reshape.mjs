// Golden vectors for collect, filter, formula, identifier, project, flatten,
// fold, pivot, lookup, cross, countpattern, extent, sequence and sample.
import { vega, encode, decode } from './lib.mjs';

async function run({ input, transform, other, signal, sequence }) {
  const data = [];
  if (other) data.push({ name: 'other', values: decode(structuredClone(other)) });
  const src = { name: 'src', transform };
  if (!sequence) src.values = decode(structuredClone(input));
  data.push(src);
  const spec = { $schema: 'https://vega.github.io/schema/vega/v6.json', width: 10, height: 10, signals: [], data };
  const view = new vega.View(vega.parse(spec), { renderer: 'none' }).logLevel(0);
  await view.runAsync();
  return { output: encode(view.data('src')), signal: signal ? encode(view.signal(signal)) : undefined };
}

const cases = [];
const add = (name, input, transform, extra = {}) => cases.push({ name, input, transform, ...extra });

const rows = [
  { id: 1, g: 'a', x: 5, y: 'p', s: 'B' }, { id: 2, g: 'b', x: 2, y: 'q', s: 'a' },
  { id: 3, g: 'a', x: 2, y: 'p', s: 'c' }, { id: 4, g: 'b', x: null, y: 'q', s: 'A' },
  { id: 5, g: 'c', x: 9, y: 'r', s: null }, { id: 6, g: null, x: NaN, y: 'p' }, { id: 7, g: 'a', x: 2, y: 'r', s: 'C' },
];

// collect
add('collect/single', rows, [{ type: 'collect', sort: { field: 'x' } }]);
add('collect/desc', rows, [{ type: 'collect', sort: { field: 'x', order: 'descending' } }]);
add('collect/multi', rows, [{ type: 'collect', sort: { field: ['g', 'x'], order: ['ascending', 'descending'] } }]);
add('collect/strings', rows, [{ type: 'collect', sort: { field: 's' } }]);
add('collect/none', rows, [{ type: 'collect' }]);

// filter / formula
add('filter/gt', rows, [{ type: 'filter', expr: 'datum.x > 2' }]);
add('filter/none', rows, [{ type: 'filter', expr: 'false' }]);
add('formula/double', rows, [{ type: 'formula', expr: 'datum.x * 2', as: 'x2' }]);
add('formula/overwrite', rows, [{ type: 'formula', expr: 'datum.id + 1', as: 'id' }]);
add('formula/undef', rows, [{ type: 'formula', expr: 'datum.s', as: 'z' }]);

// identifier
add('identifier/basic', rows, [{ type: 'identifier', as: 'uid' }]);
add('identifier/existing', [{ a: 1, uid: 7 }, { a: 2 }, { a: 3, uid: 0 }, { a: 4 }], [{ type: 'identifier', as: 'uid' }]);

// project
add('project/fields', rows, [{ type: 'project', fields: ['x', 'g'] }]);
add('project/as', rows, [{ type: 'project', fields: ['x', 'g', 'id'], as: ['xx', null, 'k'] }]);
add('project/nested', [{ a: { b: 1 }, c: 2 }, { a: {}, c: 3 }], [{ type: 'project', fields: ['a.b', 'c'] }]);
add('project/all', rows, [{ type: 'project' }]);
add('project/empty', rows, [{ type: 'project', fields: [] }]);

// flatten
const arrs = [{ k: 1, a: [1, 2, 3], b: ['x', 'y'] }, { k: 2, a: [], b: [] }, { k: 3, a: [null, 5], b: ['z'] }, { k: 4, a: 7, b: [1] }, { k: 5, a: [9], b: [] }];
add('flatten/one', arrs.filter(r => r.k !== 4), [{ type: 'flatten', fields: ['a'] }]);
add('flatten/two', arrs.filter(r => r.k !== 4), [{ type: 'flatten', fields: ['a', 'b'] }]);
add('flatten/as-index', arrs.filter(r => r.k !== 4), [{ type: 'flatten', fields: ['a', 'b'], as: ['aa', 'bb'], index: 'i' }]);
add('flatten/non-array', arrs, [{ type: 'flatten', fields: ['a'] }]);
add('flatten/string', [{ s: 'abc' }], [{ type: 'flatten', fields: ['s'], as: ['c'] }]);

// fold
add('fold/default', rows, [{ type: 'fold', fields: ['x', 'g'] }]);
add('fold/as', rows, [{ type: 'fold', fields: ['x', 'id'], as: ['k', 'v'] }]);
add('fold/nested', [{ a: { b: 1 }, c: 2 }], [{ type: 'fold', fields: ['a.b', 'c'] }]);

// pivot
const pv = [
  { g: 'a', k: 'x', v: 1 }, { g: 'a', k: 'y', v: 2 }, { g: 'b', k: 'x', v: 3 }, { g: 'a', k: 'x', v: 4 },
  { g: 'b', k: 'z', v: null }, { g: 'c', k: 'y', v: 5 }, { g: 'c', k: 'x' },
];
add('pivot/sum', pv, [{ type: 'pivot', field: 'k', value: 'v', groupby: ['g'] }]);
add('pivot/count', pv, [{ type: 'pivot', field: 'k', value: 'v', groupby: ['g'], op: 'count' }]);
add('pivot/max', pv, [{ type: 'pivot', field: 'k', value: 'v', groupby: ['g'], op: 'max' }]);
add('pivot/mean', pv, [{ type: 'pivot', field: 'k', value: 'v', groupby: ['g'], op: 'mean' }]);
add('pivot/limit', pv, [{ type: 'pivot', field: 'k', value: 'v', groupby: ['g'], limit: 2 }]);
add('pivot/nogroup', pv, [{ type: 'pivot', field: 'k', value: 'v' }]);
add('pivot/numeric keys', [{ k: 3, v: 1 }, { k: 1, v: 2 }, { k: 3, v: 5 }], [{ type: 'pivot', field: 'k', value: 'v' }]);
add('pivot/empty', [], [{ type: 'pivot', field: 'k', value: 'v', groupby: ['g'] }]);
add('pivot/mixed keys', [{ k: 1, v: 1 }, { k: '1', v: 2 }, { k: 'a', v: 3 }], [{ type: 'pivot', field: 'k', value: 'v' }]);

// lookup
const other = [{ id: 'a', p: 10, q: 'A' }, { id: 'b', p: 20, q: 'B' }, { id: 3, p: 30 }, { id: 'a', p: 11, q: 'A2' }];
const prim = [{ k: 'a', j: 'b' }, { k: 'b', j: 'zz' }, { k: 'c', j: 3 }, { k: null, j: undefined }, { k: 3 }];
add('lookup/whole', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k'], as: ['m'] }], { other });
add('lookup/values', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k'], values: ['p', 'q'] }], { other });
add('lookup/values-as-default', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k'], values: ['p'], as: ['pp'], default: -1 }], { other });
add('lookup/multi', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k', 'j'], values: ['p', 'q'], as: ['kp', 'kq', 'jp', 'jq'], default: 'none' }], { other });
add('lookup/multi-whole', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k', 'j'], as: ['km', 'jm'] }], { other });
add('lookup/err-multi-as', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k', 'j'], values: ['p'] }], { other });
add('lookup/err-no-as', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k'] }], { other });
add('lookup/err-short-as', prim, [{ type: 'lookup', from: 'other', key: 'id', fields: ['k'], values: ['p', 'q'], as: ['z'] }], { other });

// cross
const cr = [{ x: 1 }, { x: 2 }, { x: 3 }];
add('cross/all', cr, [{ type: 'cross' }]);
add('cross/filter', cr, [{ type: 'cross', filter: 'datum.a.x < datum.b.x' }]);
add('cross/as', cr, [{ type: 'cross', as: ['l', 'r'], filter: 'datum.l.x != datum.r.x' }]);
add('cross/empty', [], [{ type: 'cross' }]);

// countpattern
const txt = [{ t: 'The quick brown fox. The lazy dog, the end! 2 2 10 "quoted"' }, { t: "It's a fox's tale: 10 apples" }, { t: '' }];
add('countpattern/default', txt, [{ type: 'countpattern', field: 't' }]);
add('countpattern/lower', txt, [{ type: 'countpattern', field: 't', case: 'lower' }]);
add('countpattern/upper', txt, [{ type: 'countpattern', field: 't', case: 'upper', as: ['w', 'n'] }]);
add('countpattern/pattern', txt, [{ type: 'countpattern', field: 't', pattern: "[\\w']+", case: 'lower' }]);
add('countpattern/stop', txt, [{ type: 'countpattern', field: 't', case: 'lower', stopwords: 'the|a|\\d+' }]);
add('countpattern/uni', [{ t: 'straße naïve ÉCOLE' }], [{ type: 'countpattern', field: 't', case: 'upper', pattern: '\\S+' }]);

// extent (recorded from the signal)
const ex = (name, input, field) => add(name, input, [{ type: 'extent', field, signal: 'ext' }], { signal: 'ext' });
ex('extent/basic', rows, 'x');
ex('extent/strings', [{ v: '3' }, { v: '10' }, { v: '' }, { v: null }], 'v');
ex('extent/empty', [], 'x');
ex('extent/allnull', [{ v: null }, { v: 'a' }], 'v');
ex('extent/inf', [{ v: 1 }, { v: Infinity }], 'v');
ex('extent/dates', [{ v: { $: 'date', v: 100 } }, { v: { $: 'date', v: 50 } }], 'v');
ex('extent/bool', [{ v: true }, { v: false }], 'v');

// sequence
const sq = (name, t) => add(name, [], [t], { sequence: true });
sq('sequence/basic', { type: 'sequence', start: 0, stop: 5 });
sq('sequence/step', { type: 'sequence', start: 1, stop: 2, step: 0.25, as: 'v' });
sq('sequence/neg', { type: 'sequence', start: 5, stop: 0, step: -2 });
sq('sequence/wrongdir', { type: 'sequence', start: 5, stop: 0 });
sq('sequence/float', { type: 'sequence', start: 0, stop: 1, step: 0.1 });
sq('sequence/zero-step', { type: 'sequence', start: 0, stop: 3, step: 0 });
sq('sequence/empty', { type: 'sequence', start: 3, stop: 3 });

// sample (seeded through vega's setRandom)
const many = Array.from({ length: 50 }, (_, i) => ({ i }));
for (const [name, n, size] of [['sample/under', 5, 10], ['sample/exact', 10, 10], ['sample/over', 50, 10], ['sample/one', 50, 1], ['sample/zero', 5, 0]]) {
  add(name, many.slice(0, n), [{ type: 'sample', size }], { seed: 42 });
}

const out = [];
for (const c of cases) {
  let r = {}, error;
  try {
    if (c.seed !== undefined) vega.setRandom(vega.randomLCG(c.seed));
    r = await run(c);
  } catch (e) { error = String(e && e.message || e); }
  out.push({ name: c.name, input: encode(decode(c.input)), other: c.other && encode(decode(c.other)), transform: c.transform,
    seed: c.seed, output: r.output, signal: r.signal, error });
}
console.log(JSON.stringify(out));
