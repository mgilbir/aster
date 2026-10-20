// Regenerate dates.json with: TZ=UTC node testdata/gen_dates.mjs > testdata/dates.json
// (node 24.21.0). How JavaScript writes a Date as text inside a value:
// String, join, + and the relational operators use Date.prototype.toString;
// JSON.stringify and vega-util's stringValue use toJSON.
import { stringValue } from '../../../testdata/oracle-node/node_modules/vega-util/build/index.js';
const times = [0, -1, 1e12, 1.5e12 + 0.5, 8.64e15, -8.64e15, -62198755200000, 253402300800000, -62135596800001, NaN];
const out = times.map((t) => {
  const d = new Date(t);
  return {
    t: Number.isNaN(t) ? 'NaN' : t,
    string: String([d, 1, null, [d]]),
    join: [d, 'x', undefined].join('|'),
    plus: '' + [d],
    json: JSON.stringify([d, { d }]),
    jsonTop: JSON.stringify(d),
    stringValue: stringValue([d, [d]]),
    stringValueTop: stringValue(d),
    less: [d] < ['T'],
  };
});
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
