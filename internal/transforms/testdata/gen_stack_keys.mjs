// Records upstream's stack grouping over group-by values that are not plain
// strings and numbers: upstream keys a stack by JSON.stringify of the
// group-by values, so objects and arrays group by their content (nested,
// integer keys first, undefined members dropped), NaN, Infinity, null and
// undefined all write null, a date writes its ISO string, and 1 and "1"
// stay apart.
//
//   NODE_PATH=testdata/oracle-node/node_modules node gen_stack_keys.mjs > stack_keys.json
import { record } from './lib.mjs';

const D = (ms) => ({ $: 'date', v: ms });
const U = { $: 'undef' };
const keys = [
  { a: 1, b: 2 }, { b: 2, a: 1 }, { a: 1, b: 2, c: U }, { a: 1 }, {},
  { 1: 'x', a: 'y' }, { a: 'y', 1: 'x' }, { 10: 1, 2: 1 }, { 2: 1, 10: 1 }, { '01': 1 }, { 1: 1 },
  [1, 2], [2, 1], [1, [2, 3]], [1, [2, 3]], [U], [null], [], [{ a: [1, { b: U }] }], [{ a: [1, {}] }],
  { $: 'NaN' }, { $: 'Inf' }, { $: '-Inf' }, null, U, 0, -0, 1, '1', 'null', '"q"', true, 'true',
  D(0), D(0), D(500), D(1000), '1970-01-01T00:00:00.000Z', { $: 'date', v: { $: 'NaN' } }, [D(0)], { d: D(0) },
];
const rows = [];
keys.forEach((k, i) => {
  rows.push({ i, k, v: i + 1 });
  rows.push({ i: i + 100, k, v: 1 });
});
// rawInput keeps the dialect's undefined members, which the echoed input
// drops.
const out = await record([
  { name: 'stack by stringify', input: rows, transform: [{ type: 'stack', field: 'v', groupby: ['k'] }] },
  { name: 'stack by two', input: rows, transform: [{ type: 'stack', field: 'v', groupby: ['k', 'v'] }] },
]);
for (const c of out) c.rawInput = rows;
console.log(JSON.stringify(out));
