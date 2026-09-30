import { record } from './lib.mjs';

const rows = [];
for (let i = 0; i < 24; i++) {
  rows.push({ g: ['a', 'b'][i % 2], t: Math.floor(i / 3) + (i % 4 === 0 ? 1 : 0), x: (i * 7) % 10 + (i % 3) / 2, y: i % 5 === 0 ? null : (i * 3) % 11, id: i });
}
rows.push({ g: 'a', t: 2, x: NaN, y: 4, id: 24 });
rows.push({ g: 'c', t: 1, x: 2, y: 'q', id: 25 });
const small = [{ v: 3, id: 0 }, { v: 3, id: 1 }, { v: 1, id: 2 }, { v: null, id: 3 }, { v: 5, id: 4 }];

const winOps = ['row_number', 'rank', 'dense_rank', 'percent_rank', 'cume_dist'];
// argmin/argmax/values are left out: in a window they store tuple references on the tuples themselves (cyclic).
const aggOps = ['count', 'valid', 'missing', 'distinct', 'sum', 'product', 'mean', 'average', 'variance', 'variancep', 'stdev', 'stdevp', 'stderr', 'median', 'q1', 'q3', 'min', 'max'];
const sort = { field: ['t'], order: ['ascending'] };
const cases = [];
const add = (name, input, transform) => cases.push({ name, input, transform });

add('ranks sorted', rows, [{ type: 'window', sort, groupby: ['g'], ops: winOps, as: winOps }]);
add('ranks sorted desc', rows, [{ type: 'window', sort: { field: ['t', 'id'], order: ['descending', 'ascending'] }, ops: winOps, as: winOps }]);
add('ranks unsorted', rows, [{ type: 'window', groupby: ['g'], ops: winOps, as: winOps }]);
add('ntile', rows, [{ type: 'window', sort, ops: ['ntile', 'ntile'], params: [3, 5], as: ['n3', 'n5'] }]);
add('lag lead', rows, [{ type: 'window', sort, groupby: ['g'], ops: ['lag', 'lag', 'lead', 'lead'], fields: ['y', 'y', 'y', 'y'], params: [null, 2, null, 3], as: ['l1', 'l2', 'e1', 'e3'] }]);
add('first last nth', rows, [{ type: 'window', sort, groupby: ['g'], frame: [-1, 1], ops: ['first_value', 'last_value', 'nth_value', 'nth_value'], fields: ['x', 'x', 'x', 'y'], params: [null, null, 2, 3], as: ['f', 'l', 'n2', 'n3'] }]);
add('prev next', small, [{ type: 'window', ops: ['prev_value', 'next_value'], fields: ['v', 'v'], as: ['p', 'n'] }]);
add('aggs default frame', rows, [{ type: 'window', sort, groupby: ['g'], ops: aggOps, fields: aggOps.map(() => 'y'), as: aggOps.map(o => 'w_' + o) }]);
add('aggs default frame x', rows, [{ type: 'window', sort, ops: aggOps, fields: aggOps.map(() => 'x'), as: aggOps.map(o => 'w_' + o) }]);
add('aggs frame -2,2', rows, [{ type: 'window', sort, groupby: ['g'], frame: [-2, 2], ops: aggOps, fields: aggOps.map(() => 'x'), as: aggOps.map(o => 'w_' + o) }]);
add('aggs frame -2,2 ignorePeers', rows, [{ type: 'window', sort, groupby: ['g'], frame: [-2, 2], ignorePeers: true, ops: aggOps, fields: aggOps.map(() => 'x'), as: aggOps.map(o => 'w_' + o) }]);
add('aggs frame null,null', rows, [{ type: 'window', sort, frame: [null, null], ops: ['sum', 'mean', 'count', 'max'], fields: ['x', 'x', null, 'y'] }]);
add('aggs frame 0,null', rows, [{ type: 'window', sort, frame: [0, null], ops: ['sum', 'mean', 'min', 'max', 'median'], fields: ['x', 'x', 'y', 'y', 'y'] }]);
add('frame -1,0', rows, [{ type: 'window', sort, frame: [-1, 0], ops: ['sum', 'mean', 'variance', 'min', 'max', 'median'], fields: ['x', 'x', 'x', 'x', 'x', 'x'] }]);
add('frame 1,3 positive', rows, [{ type: 'window', sort, frame: [1, 3], ignorePeers: true, ops: ['sum', 'mean', 'count', 'min', 'max', 'valid'], fields: ['x', 'x', null, 'x', 'x', 'x'] }]);
add('frame -3,-1', rows, [{ type: 'window', sort, frame: [-3, -1], ignorePeers: true, ops: ['sum', 'mean', 'count', 'min', 'max'], fields: ['x', 'x', null, 'x', 'x'] }]);
add('exponential', rows, [{ type: 'window', sort, frame: [-3, 0], ops: ['exponential', 'exponentialb'], fields: ['x', 'x'], aggregate_params: [0.5, 0.5] }]);
add('mixed ops', rows, [{ type: 'window', sort, groupby: ['g'], ops: ['row_number', 'sum', 'lag', 'count'], fields: [null, 'x', 'x', null] }]);
// With peers (the default when sorted) upstream throws on frames that are empty at an end of the partition.
add('empty', [], [{ type: 'window', sort, ops: ['row_number', 'sum'], fields: [null, 'x'] }]);
add('single', [{ x: 1, t: 1 }], [{ type: 'window', sort, ops: ['row_number', 'percent_rank', 'cume_dist', 'sum', 'first_value', 'last_value'], fields: [null, null, null, 'x', 'x', 'x'] }]);
add('ntile bad', rows, [{ type: 'window', ops: ['ntile'], params: [0] }]);

console.log(JSON.stringify(await record(cases)));
