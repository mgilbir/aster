// Records vega-util's ascending() and compare() over a mixed-type value list.
import { createRequire } from 'node:module';
import path from 'node:path';
import { encode } from './lib.mjs';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

const values = [null, undefined, NaN, new Date(NaN), new Date(0), new Date(86400000), 0, -1, 2.5, 10, 9, Infinity,
  -Infinity, true, false, '', 'a', 'B', 'b', '10', '9', ' 5', '\u{1F600}', '～', 'abc'];
const asc = values.map((u) => values.map((v) => vega.ascending(u, v)));

const rows = [
  { a: 1, b: 'x' }, { a: 1, b: 'a' }, { a: null, b: 'c' }, { a: NaN, b: 'q' }, { a: 3, b: null }, { a: 2, b: 'b' },
  { a: undefined, b: 'z' }, { a: 1, b: 'x' }, { a: '2', b: 'k' }, { a: new Date(5), b: 'd' },
];
const specs = [
  { fields: ['a'], orders: ['ascending'] },
  { fields: ['a'], orders: ['descending'] },
  { fields: ['a', 'b'], orders: ['ascending', 'descending'] },
  { fields: ['b', 'a'], orders: ['descending', 'descending'] },
  { fields: ['b', 'a'], orders: [] },
];
const compares = specs.map((s) => {
  const cmp = vega.compare(s.fields, s.orders);
  return { ...s, result: rows.map((x) => rows.map((y) => Math.sign(cmp(x, y)))) };
});
console.log(JSON.stringify({ values: encode(values), ascending: asc, rows: encode(rows), compares }));
