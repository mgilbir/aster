// Run with the local zone pinned, the Go test uses the same location:
//   TZ=America/New_York NODE_PATH=... node gen_timeunit.mjs > timeunit.json
import { record } from './lib.mjs';

const D = (y, m, d, H = 0, M = 0, S = 0, L = 0) => Date.UTC(y, m, d, H, M, S, L);
const specials = [
  D(2012, 0, 1), D(2012, 1, 29, 12, 30), D(2012, 2, 11, 6, 59), D(2012, 2, 11, 7, 1), D(2012, 10, 4, 5, 0), D(2012, 10, 4, 6, 30),
  D(2012, 11, 31, 23, 59, 59, 999), D(2013, 0, 1, 0, 0, 0, 1), D(2013, 5, 15, 13, 45, 12, 345), D(2011, 11, 31, 22),
  D(2014, 6, 4, 3, 2, 1, 0), D(2015, 2, 8, 6, 59, 59), D(2015, 2, 8, 7, 0, 0), D(2016, 1, 29), D(1999, 8, 9, 9, 9, 9, 9),
];
let s = 12345;
const rnd = () => (s = (s * 1103515245 + 12345) % 2147483648) / 2147483648;
const input = [...specials];
for (let i = 0; i < 60; i++) input.push(D(2011, 0, 1) + Math.floor(rnd() * 4 * 365 * 86400000));
const rows = input.map((t, i) => ({ t: { $: 'date', v: t }, n: t, i }));
rows.push({ t: null, n: null, i: -1 });
rows.push({ i: -2 });

const cases = [];
const add = (name, transform, data = rows) => cases.push({ name, input: data, transform });
const unitSets = [['year'], ['quarter'], ['month'], ['year', 'month'], ['year', 'quarter'], ['date'], ['year', 'month', 'date'],
  ['week'], ['year', 'week'], ['day'], ['week', 'day'], ['year', 'week', 'day'], ['dayofyear'], ['year', 'dayofyear'],
  ['hours'], ['year', 'month', 'date', 'hours'], ['minutes'], ['seconds'], ['milliseconds'], ['month', 'date', 'hours', 'minutes']];
for (const tz of ['local', 'utc']) {
  for (const units of unitSets) add(`${units.join('+')} ${tz}`, [{ type: 'timeunit', field: 't', units, timezone: tz }]);
}
for (const tz of ['local', 'utc']) {
  add(`steps ${tz} month3`, [{ type: 'timeunit', field: 't', units: ['year', 'month'], step: 3, timezone: tz }]);
  add(`steps ${tz} hours6`, [{ type: 'timeunit', field: 't', units: ['year', 'month', 'date', 'hours'], step: 6, timezone: tz }]);
  add(`steps ${tz} minutes15`, [{ type: 'timeunit', field: 't', units: ['hours', 'minutes'], step: 15, timezone: tz }]);
  add(`steps ${tz} year2`, [{ type: 'timeunit', field: 't', units: ['year'], step: 2, timezone: tz }]);
  add(`steps ${tz} date5`, [{ type: 'timeunit', field: 't', units: ['year', 'month', 'date'], step: 5, timezone: tz }]);
  add(`no interval ${tz}`, [{ type: 'timeunit', field: 't', units: ['year', 'month'], interval: false, timezone: tz }]);
  add(`as ${tz}`, [{ type: 'timeunit', field: 't', units: ['month'], as: ['a', 'b'], timezone: tz }]);
  add(`numeric field ${tz}`, [{ type: 'timeunit', field: 'n', units: ['year', 'month', 'date'], timezone: tz }]);
  for (const maxbins of [5, 10, 40, 200]) add(`auto maxbins ${maxbins} ${tz}`, [{ type: 'timeunit', field: 't', maxbins, timezone: tz }]);
  add(`auto extent ${tz}`, [{ type: 'timeunit', field: 't', extent: [D(2012, 0, 1), D(2012, 0, 2)], maxbins: 24, timezone: tz }]);
  add(`auto extent wide ${tz}`, [{ type: 'timeunit', field: 't', extent: [D(1900, 0, 1), D(2100, 0, 2)], maxbins: 10, timezone: tz }]);
  add(`auto extent tiny ${tz}`, [{ type: 'timeunit', field: 't', extent: [D(2012, 0, 1), D(2012, 0, 1, 0, 0, 0, 500)], maxbins: 20, timezone: tz }]);
  add(`auto narrow data ${tz}`, [{ type: 'timeunit', field: 't', maxbins: 12, timezone: tz }], rows.filter((r) => r.i >= 0 && r.i < 6));
}
const monthStarts = [0, 1, 2, 3, 4, 5].map((m) => ({ t: { $: 'date', v: D(2012, m, 1) } }));
const dayStarts = [0, 1, 2, 3].map((d) => ({ t: { $: 'date', v: D(2012, 2, 5 + d) } }));
const mondays = [0, 7, 14, 21].map((d) => ({ t: { $: 'date', v: D(2012, 2, 5 + d) } }));
const years = [2000, 2010, 2020].map((y) => ({ t: { $: 'date', v: D(y, 0, 1) } }));
const hourly = [0, 1, 2, 5].map((h) => ({ t: { $: 'date', v: D(2012, 2, 5, h) } }));
const fives = [0, 5, 35, 40].map((m) => ({ t: { $: 'date', v: D(2012, 2, 5, 3, m) } }));
for (const [name, data] of [['month starts', monthStarts], ['day starts', dayStarts], ['mondays', mondays], ['years', years], ['hourly', hourly], ['fives', fives], ['mixed', rows.slice(0, 20).filter((r) => r.i >= 0)]]) {
  add(`infer ${name} utc`, [{ type: 'timeunit', field: 't', inferUnits: true, timezone: 'utc' }], data);
  add(`infer ${name} local`, [{ type: 'timeunit', field: 't', inferUnits: true, timezone: 'local' }], data);
}
add('empty units', [{ type: 'timeunit', field: 't', units: ['year'] }], []);
add('empty auto', [{ type: 'timeunit', field: 't' }], []);
add('bad unit', [{ type: 'timeunit', field: 't', units: ['fortnight'] }]);
add('incompatible', [{ type: 'timeunit', field: 't', units: ['week', 'month'] }]);
add('invalid date infer', [{ type: 'timeunit', field: 't', inferUnits: true }], [{ t: { $: 'date', v: { $: 'NaN' } } }]);

console.log(JSON.stringify(await record(cases)));
