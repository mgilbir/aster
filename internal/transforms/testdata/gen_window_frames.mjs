// Records upstream's window transform over frames that are not plain small
// integers: infinite, huge, NaN, fractional, inverted (start past end), and
// over the `values` op, whose array upstream shares between the rows of a
// frame that only grew (TupleStore.values() returns its own array until a
// removal makes it build a new one, and later rows push into it).
//
//   NODE_PATH=testdata/oracle-node/node_modules node gen_window_frames.mjs > window_frames.json
//
// Each case records, per output row, the plain outputs, and for `values` the
// ids of the tuples in the array and the first row holding the same array.
import { runTransforms, encode } from './lib.mjs';

const rows = [];
for (let i = 0; i < 9; i++) rows.push({ id: i, t: Math.floor(i / 2), x: (i * 7) % 5 + (i === 4 ? 0.5 : 0), g: i % 3 === 0 ? 'a' : 'b' });

// A frame bound is null, a number, or an expression (for what JSON cannot
// hold); num is what it evaluates to.
const bounds = [
  [null, null], [0, 0], [-1, 1], [-2, 0], [0, 2], [1, 3], [-3, -1],
  [2, -2], [1, 0], [3, -3], [0, -1], [5, 6], [-9, -7],
  [-0.5, 0], [0, 1.5], [0.5, 0.5],
  [{ e: '1/0', num: Infinity }, 0], [0, { e: '1/0', num: Infinity }], [{ e: '-1/0', num: -Infinity }, { e: '1/0', num: Infinity }],
  [{ e: '1/0', num: Infinity }, { e: '-1/0', num: -Infinity }],
  [-1e300, 1e300], [1e300, 1e300], [-1e300, -1e300],
  [{ e: '0/0', num: NaN }, 0], [0, { e: '0/0', num: NaN }], [{ e: '0/0', num: NaN }, { e: '0/0', num: NaN }],
  [null, 1], [1, null], [null, -2], [2, null],
];
const opSets = [
  { ops: ['sum', 'count', 'values'], fields: ['x', null, 'x'] },
  { ops: ['count'], fields: [null] },
  { ops: ['mean', 'min', 'max', 'median', 'valid', 'missing'], fields: ['x', 'x', 'x', 'x', 'x', 'x'] },
  { ops: ['values', 'distinct', 'argmin'], fields: ['x', 'x', 'x'] },
  { ops: ['row_number', 'first_value', 'last_value', 'nth_value'], fields: [null, 'x', 'x', 'x'], params: [null, null, null, 2] },
];
const sorts = [
  { name: 'unsorted' },
  { name: 'sorted', sort: { field: ['t'], order: ['ascending'] } },
  { name: 'sorted-ignorePeers', sort: { field: ['t'], order: ['ascending'] }, ignorePeers: true },
  { name: 'grouped', sort: { field: ['id'], order: ['descending'] }, groupby: ['g'] },
];

const out = [];
for (const b of bounds) {
  const frame = b.map((v) => (v && typeof v === 'object' ? { signal: v.e } : v));
  const frameNum = b.map((v) => encode(v && typeof v === 'object' ? v.num : v));
  for (const s of sorts) {
    for (const o of opSets) {
      const tr = { type: 'window', frame, ops: o.ops, fields: o.fields, as: o.ops.map((op, i) => op + i) };
      if (o.params) tr.params = o.params;
      if (s.sort) tr.sort = s.sort;
      if (s.ignorePeers) tr.ignorePeers = true;
      if (s.groupby) tr.groupby = s.groupby;
      let output, error;
      try {
        const res = await runTransforms(structuredClone(rows), [tr]);
        const arrays = [];
        output = res.map((t) => {
          const r = { id: t.id };
          o.ops.forEach((op, i) => {
            const v = t[op + i];
            if (op === 'values') {
              let k = arrays.indexOf(v);
              if (k < 0) { arrays.push(v); k = arrays.length - 1; }
              r[op + i] = { ids: Array.from(v, (x) => (x === undefined ? null : x.id)), shared: k };
            } else if (op === 'argmin') {
              r[op + i] = v && v.id !== undefined ? v.id : null;
            } else {
              r[op + i] = encode(v);
            }
          });
          return r;
        });
      } catch (e) { error = String(e && e.message || e); }
      out.push({ name: `${JSON.stringify(b.map((v) => (v && typeof v === 'object' ? v.e : v)))} ${s.name} ${o.ops.join(',')}`, frame: frameNum, transform: tr, output, error });
    }
  }
}
// lag and lead with offsets past the partition: null, not a smaller offset.
for (const params of [[1e300, 1e300, 3, 2], [{ e: '1/0' }, { e: '1/0' }, 0, null]]) {
  const tr = { type: 'window', ops: ['lag', 'lead', 'lag', 'lead'], fields: ['x', 'x', 'x', 'x'], as: ['lag0', 'lead1', 'lag2', 'lead3'],
    params: params.map((v) => (v && typeof v === 'object' ? { signal: v.e } : v)) };
  const res = await runTransforms(structuredClone(rows), [tr]);
  out.push({ name: `lag/lead ${JSON.stringify(params)}`, frame: [null, 0], transform: tr, paramNum: params.map((v) => encode(v && typeof v === 'object' ? Infinity : v)),
    output: res.map((t) => ({ id: t.id, lag0: encode(t.lag0), lead1: encode(t.lead1), lag2: encode(t.lag2), lead3: encode(t.lead3) })) });
}
out.unshift({ rows });
console.log(JSON.stringify(out));
