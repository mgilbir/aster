// Records d3-time, d3-time-format and vega-time at instants that test two
// classes of the 2026-10-18 audit:
//   C119: every entry point makes a Date of its argument (new Date(+v),
//         setTime), which TimeClips it: -0.5 is 1970, 1.5 is 1;
//   C120/C161: day and week numbers are interval counts (minute-truncated
//         offsets), and vega-time's UTC helpers read years 0-99 as 19xx.
// Run once per zone:
//   TZ=America/New_York NODE_PATH=testdata/oracle-node/node_modules node testdata/gen_calendar.mjs
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const tf = await import(require.resolve('d3-time-format'));
const d3 = await import(require.resolve('d3-time'));
const vt = await import(require.resolve('vega-time'));

const tz = process.env.TZ;
const local = (...a) => new Date(...a).getTime();
const instants = [
  -0.5, 0.5, 1.5, -1.5, -1000.5, 999.5, -86400000.5, 86399999.5, -0.25e-9, 8.64e15 + 0.5, 8.64e15 - 0.5, -8.64e15 - 0.5,
  // seconds-offset years (a zone leaving local mean time) and their neighbours
  local(1972, 5, 1), local(1972, 0, 7, 12), local(1972, 11, 31), local(1883, 11, 1), local(1883, 10, 18, 12),
  local(1906, 5, 1), local(1914, 5, 1), local(1935, 5, 1),
  // years 0-99: vega-time's UTC helpers use Date.UTC(y, 0, 1)
  -59861460820772, Date.UTC(73, 0, 1) - 62167219200000 + 1e3, new Date(Date.UTC(2000, 0, 1)).setUTCFullYear(5),
  new Date(Date.UTC(2000, 6, 4)).setUTCFullYear(99), new Date(Date.UTC(2000, 11, 31)).setUTCFullYear(0),
  Date.UTC(2021, 0, 3, 12), Date.UTC(2020, 11, 31, 23, 59, 59, 999),
];
const fmts = ['%Y-%m-%d %H:%M:%S.%L', '%Q', '%s', '%j %U %W %V %g %G %u %w', '%a %b %e %p %I %q %Z'];
const floors = [['year', 'month', 'date', 'hours', 'minutes', 'seconds'], ['year', 'week'], ['year', 'isoweek'],
  ['isoweek', 'day'], ['year', 'week', 'day'], ['dayofyear'], ['year', 'isoweek', 'day'], ['week'], ['day'], ['year', 'day'],
  ['year', 'quarter'], ['milliseconds']];
const n = (d) => (d === null || d === undefined ? null : Number.isNaN(+d) ? 'NaN' : +d);
const rows = instants.map((t) => {
  const r = { t: String(t) };
  r.fmt = fmts.map((f) => tf.timeFormat(f)(t));
  r.utcFmt = fmts.map((f) => tf.utcFormat(f)(t));
  const iv = [];
  for (const [name, I, span] of [['second', d3.timeSecond, 5e3], ['day', d3.timeDay, 3 * 864e5], ['week', d3.timeWeek, 3 * 6048e5],
    ['month', d3.timeMonth, 1e11], ['year', d3.timeYear, 1e11], ['utcSecond', d3.utcSecond, 5e3], ['utcDay', d3.utcDay, 3 * 864e5],
    ['utcMonday', d3.utcMonday, 3 * 6048e5]]) {
    iv.push([name, n(I.floor(t)), n(I.ceil(t)), n(I.round(t)), n(I.offset(t, 2)), n(I.count(t, t + 86400000.7)),
      (I.range(t, t + span).length)]);
  }
  // A filtered interval's offset loops forever upstream once the date is
  // invalid: the edges of the range are left to the unfiltered ones.
  const edge = !(Math.abs(t) < 8.6e15);
  if (!edge) for (const [name, I, span] of [['ms10', d3.utcMillisecond.every(10), 100], ['month3', d3.utcMonth.every(3), 1e11],
    ['minute15', d3.timeMinute.every(15), 864e5]]) {
    iv.push([name, n(I.floor(t)), n(I.ceil(t)), n(I.round(t)), n(I.offset(t, 2)), null, I.range(t, t + span).length]);
  }
  r.iv = iv;
  r.vt = [vt.dayofyear(t), vt.week(t), vt.isoweek(t), vt.utcdayofyear(t), vt.utcweek(t), vt.utcisoweek(t)].map(n);
  r.floor = floors.map((u) => n(vt.timeFloor(u)(t)));
  r.utcFloor = floors.map((u) => n(vt.utcFloor(u)(t)));
  r.offset = [n(vt.timeOffset('day', t, 1)), n(vt.utcOffset('second', t, -1)), edge ? null : n(vt.utcOffset('quarter', t, 1))];
  return r;
});
process.stdout.write(JSON.stringify({ node: process.version, tz, fmts, floors, rows }) + '\n');
