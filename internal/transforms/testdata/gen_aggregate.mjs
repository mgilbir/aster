import { record } from './lib.mjs';

const nums = [];
for (let i = 0; i < 40; i++) {
  nums.push({ g: ['a', 'b', 'c'][i % 3], h: i % 2 ? 'x' : 'y', x: (i * 37) % 11 + (i % 5) / 4, y: i % 7 === 0 ? null : (i * 13) % 17, s: String(i % 4) });
}
nums.push({ g: 'a', h: 'x', x: NaN, y: '', s: 'z' });
nums.push({ g: 'b', h: 'y' });
nums.push({ g: null, h: 'x', x: 3, y: 4 });
nums.push({ g: 1, h: 'y', x: 5, y: 6 });
nums.push({ g: '1', h: 'y', x: 5, y: 6 });
nums.push({ g: 10, h: 'z', x: -2, y: 6 });
nums.push({ g: 2, h: 'z', x: -3, y: 6 });

const strs = [{ k: 'b' }, { k: 'a' }, { k: 'c' }, { k: 'a' }, { k: null }, { k: 'B' }];
const nested = [{ a: { b: 1 }, g: 'p' }, { a: { b: 5 }, g: 'p' }, { a: { b: 2 }, g: 'q' }, { a: {}, g: 'q' }];

const allOps = ['count', 'valid', 'missing', 'distinct', 'sum', 'product', 'mean', 'average', 'variance', 'variancep',
  'stdev', 'stdevp', 'stderr', 'median', 'q1', 'q3', 'min', 'max', 'argmin', 'argmax'];
const cases = [];
const add = (name, input, transform) => cases.push({ name, input, transform });

add('count only', nums, [{ type: 'aggregate' }]);
add('count groupby', nums, [{ type: 'aggregate', groupby: ['g'] }]);
add('all ops y groupby g', nums, [{ type: 'aggregate', groupby: ['g'], fields: allOps.map(() => 'y'), ops: allOps }]);
add('all ops x groupby g,h', nums, [{ type: 'aggregate', groupby: ['g', 'h'], fields: allOps.map(() => 'x'), ops: allOps }]);
add('all ops no groupby', nums, [{ type: 'aggregate', fields: allOps.map(() => 'x'), ops: allOps }]);
add('as names', nums, [{ type: 'aggregate', groupby: ['h'], fields: ['x', 'x', 'y'], ops: ['sum', 'mean', 'max'], as: ['s1', null, 'mx'] }]);
add('strings min max', strs, [{ type: 'aggregate', fields: ['k', 'k', 'k', 'k'], ops: ['min', 'max', 'distinct', 'valid'] }]);
add('string-numbers', nums, [{ type: 'aggregate', groupby: ['h'], fields: ['s', 's', 's', 's'], ops: ['sum', 'mean', 'min', 'max'] }]);
add('nested', nested, [{ type: 'aggregate', groupby: ['g'], fields: ['a.b', 'a.b'], ops: ['sum', 'max'] }]);
add('exponential', nums, [{ type: 'aggregate', groupby: ['h'], fields: ['x', 'x', 'y'], ops: ['exponential', 'exponentialb', 'exponential'], aggregate_params: [0.5, 0.5, 0.9] }]);
add('exponentialb alone', nums, [{ type: 'aggregate', fields: ['x'], ops: ['exponentialb'], aggregate_params: [0.5] }]);
add('cross', nums, [{ type: 'aggregate', groupby: ['g', 'h'], cross: true, fields: ['x'], ops: ['sum'] }]);
add('cross 3', [{ a: 1, b: 'x', c: 'p' }, { a: 2, b: 'y', c: 'q' }, { a: 1, b: 'y', c: 'p' }], [{ type: 'aggregate', groupby: ['a', 'b', 'c'], cross: true }]);
add('empty', [], [{ type: 'aggregate', groupby: ['g'], fields: ['x'], ops: ['sum'] }]);
add('empty nogroup', [], [{ type: 'aggregate', fields: ['x'], ops: ['sum'] }]);
add('values', nested, [{ type: 'aggregate', groupby: ['g'], fields: ['a.b'], ops: ['values'], as: ['vals'] }]);
add('dup field ops order', nums, [{ type: 'aggregate', groupby: ['h'], fields: ['x', 'y', 'x', 'y'], ops: ['max', 'min', 'variance', 'sum'] }]);
// median memoises before argmin asks for the extent of the same tuple store
// (upstream TupleStore quirk): the second field reuses the first field's extent.
add('memo quirk', nums, [{ type: 'aggregate', groupby: ['h'], fields: ['x', 'y', 'x', 'y', 'x', 'y'], ops: ['median', 'median', 'argmin', 'argmin', 'argmax', 'argmax'] }]);
add('key', nums, [{ type: 'aggregate', groupby: ['h'], key: 'h', fields: ['x'], ops: ['sum'] }]);
add('dates', [{ d: { $: 'date', v: 1000 } }, { d: { $: 'date', v: 500 } }, { d: { $: 'date', v: 2000 } }], [{ type: 'aggregate', fields: ['d', 'd', 'd'], ops: ['min', 'max', 'sum'] }]);

console.log(JSON.stringify(await record(cases)));
