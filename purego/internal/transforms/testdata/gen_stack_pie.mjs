import { record } from './lib.mjs';

const rows = [];
for (let i = 0; i < 24; i++) rows.push({ c: ['a', 'b', 'c'][i % 3], g: i % 2 ? 'x' : 'y', v: ((i * 7) % 9) - 2, w: (i * 5) % 6 });
rows.push({ c: 'a', g: 'x', v: null, w: NaN });
rows.push({ c: 'b', g: 1, v: '3', w: 2 });
rows.push({ c: 'b', g: '1', v: 'q', w: 2 });
rows.push({ c: 'c', v: 2, w: 0 });
const pos = rows.map((r) => ({ ...r, v: Math.abs(r.v) || 1 })).filter((r) => typeof r.v === 'number');
const cases = [];
const add = (name, input, transform) => cases.push({ name, input, transform });

for (const offset of ['zero', 'center', 'normalize']) {
  add(`stack ${offset} groupby`, rows, [{ type: 'stack', field: 'v', groupby: ['c'], offset }]);
  add(`stack ${offset} groupby sort`, rows, [{ type: 'stack', field: 'w', groupby: ['c', 'g'], sort: { field: ['w', 'v'], order: ['descending', 'ascending'] }, offset }]);
  add(`stack ${offset} nogroup`, pos, [{ type: 'stack', field: 'v', offset }]);
  add(`stack ${offset} as`, pos, [{ type: 'stack', field: 'v', groupby: ['g'], offset, as: ['lo', 'hi'] }]);
  add(`stack ${offset} nofield`, pos, [{ type: 'stack', groupby: ['c'], offset }]);
  add(`stack ${offset} empty`, [], [{ type: 'stack', field: 'v', groupby: ['c'], offset }]);
}
add('stack default offset', rows, [{ type: 'stack', field: 'v', groupby: ['c'] }]);
add('stack sort string', rows, [{ type: 'stack', field: 'v', groupby: ['g'], sort: { field: 'c' } }]);

const pv = [{ v: 3 }, { v: 1 }, { v: 2 }, { v: 4 }, { v: 0 }];
add('pie basic', pv, [{ type: 'pie', field: 'v' }]);
add('pie sort', pv, [{ type: 'pie', field: 'v', sort: true }]);
add('pie angles', pv, [{ type: 'pie', field: 'v', startAngle: 1, endAngle: 4 }]);
add('pie endAngle 0', pv, [{ type: 'pie', field: 'v', startAngle: -1, endAngle: 0 }]);
add('pie as', pv, [{ type: 'pie', field: 'v', as: ['s', 'e'] }]);
add('pie nofield', pv, [{ type: 'pie' }]);
add('pie messy', [{ v: 3 }, { v: null }, { v: '2' }, { v: NaN }, {}, { v: 1 }], [{ type: 'pie', field: 'v' }]);
add('pie messy sort', [{ v: 3 }, { v: null }, { v: '2' }, { v: NaN }, {}, { v: 1 }], [{ type: 'pie', field: 'v', sort: true }]);
add('pie empty', [], [{ type: 'pie', field: 'v' }]);
add('pie zeros', [{ v: 0 }, { v: 0 }], [{ type: 'pie', field: 'v' }]);
console.log(JSON.stringify(await record(cases)));
