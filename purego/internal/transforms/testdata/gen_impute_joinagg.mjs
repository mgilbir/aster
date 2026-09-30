import { record } from './lib.mjs';

const ts = [
  { s: 'a', k: 1, v: 10 }, { s: 'a', k: 3, v: 30 }, { s: 'b', k: 2, v: 5 }, { s: 'b', k: 3, v: null },
  { s: 'c', k: 1, v: 7 }, { s: 'c', k: 4, v: 1 }, { s: 'c', k: '4', v: 9 }, { s: null, k: 2, v: 2 }, { k: 5, v: 8 },
];
const strk = [{ g: 1, k: 'x', v: 1 }, { g: 1, k: 'y', v: 4 }, { g: 2, k: 'z', v: 9 }, { g: 2, k: 'x', v: 'a' }];
const cases = [];
const add = (name, input, transform) => cases.push({ name, input, transform });

for (const method of ['value', 'mean', 'median', 'min', 'max']) {
  add(`impute ${method} groupby`, ts, [{ type: 'impute', field: 'v', key: 'k', groupby: ['s'], method, value: -1 }]);
  add(`impute ${method} nogroup`, ts, [{ type: 'impute', field: 'v', key: 'k', method }]);
  add(`impute ${method} keyvals`, ts, [{ type: 'impute', field: 'v', key: 'k', groupby: ['s'], keyvals: [0, 1, 2, 9], method }]);
}
add('impute default', ts, [{ type: 'impute', field: 'v', key: 'k', groupby: ['s'] }]);
add('impute value string', strk, [{ type: 'impute', field: 'v', key: 'k', groupby: ['g'], value: 'n/a' }]);
add('impute strings mean', strk, [{ type: 'impute', field: 'v', key: 'k', groupby: ['g'], method: 'mean' }]);
add('impute strings min', strk, [{ type: 'impute', field: 'v', key: 'k', groupby: ['g'], method: 'min' }]);
add('impute two groupby', [{ a: 1, b: 2, k: 1, v: 1 }, { a: 1, b: 3, k: 2, v: 2 }, { a: 1, b: 2, k: 2, v: 4 }], [{ type: 'impute', field: 'v', key: 'k', groupby: ['a', 'b'], method: 'mean' }]);
add('impute empty', [], [{ type: 'impute', field: 'v', key: 'k', keyvals: [1, 2] }]);
add('impute empty groupby', [], [{ type: 'impute', field: 'v', key: 'k', groupby: ['s'], keyvals: [1, 2] }]);
add('impute dup key', [{ k: 1, v: 1 }, { k: 1, v: 5 }, { k: 2, v: 3 }, { k: 3 }], [{ type: 'impute', field: 'v', key: 'k', keyvals: [4], method: 'mean' }]);

const allOps = ['count', 'valid', 'missing', 'distinct', 'sum', 'product', 'mean', 'average', 'variance', 'variancep',
  'stdev', 'stdevp', 'stderr', 'median', 'q1', 'q3', 'min', 'max'];
const nums = [];
for (let i = 0; i < 30; i++) nums.push({ g: ['a', 'b', 'c'][i % 3], h: i % 2 ? 'x' : 'y', x: (i * 37) % 11 + (i % 5) / 4, y: i % 7 === 0 ? null : (i * 13) % 17 });
nums.push({ g: 'a', h: 'x', x: NaN, y: '' }, { g: 'b' });
add('joinaggregate count', nums, [{ type: 'joinaggregate' }]);
add('joinaggregate groupby all ops', nums, [{ type: 'joinaggregate', groupby: ['g'], fields: allOps.map(() => 'y'), ops: allOps }]);
add('joinaggregate two groupby', nums, [{ type: 'joinaggregate', groupby: ['g', 'h'], fields: ['x', 'x', 'y'], ops: ['mean', 'max', 'sum'], as: ['m', null, 'total'] }]);
add('joinaggregate nogroup', nums, [{ type: 'joinaggregate', fields: ['x', 'x'], ops: ['sum', 'max'] }]);
add('joinaggregate existing field', nums, [{ type: 'joinaggregate', groupby: ['g'], fields: ['x'], ops: ['sum'], as: ['y'] }]);
add('joinaggregate exponential', nums, [{ type: 'joinaggregate', groupby: ['h'], fields: ['x'], ops: ['exponential'], aggregate_params: [0.5] }]);
add('joinaggregate empty', [], [{ type: 'joinaggregate', groupby: ['g'], fields: ['x'], ops: ['sum'] }]);
console.log(JSON.stringify(await record(cases)));
