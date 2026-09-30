// Records d3-time, d3-time-format, vega-time and vega-format golden vectors for
// the process time zone (set TZ). Regenerate every file with testdata/gen.sh.
//   TZ=UTC NODE_PATH=testdata/oracle-node/node_modules \
//     node testdata/gen_time.mjs | gzip -9 > testdata/time_UTC.json.gz
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const d3 = await import(require.resolve('d3-time'));
const tf = await import(require.resolve('d3-time-format'));
const vt = await import(require.resolve('vega-time'));
const vf = await import(require.resolve('vega-format'));
const { tickStep } = await import(require.resolve('d3-array'));

const tz = process.env.TZ || 'UTC';
const FULL = tz === 'UTC';
const num = (v) => { const n = +v; return Number.isNaN(n) ? null : n; };
const U = Date.UTC;

// ---- instants ----
let seed = 12345;
const rnd = () => (seed = (seed * 1664525 + 1013904223) >>> 0) / 4294967296;
const base = [
  U(2000, 0, 1), U(2000, 1, 29, 12, 34, 56, 789), U(2001, 0, 1), U(1999, 11, 31, 23, 59, 59, 999),
  U(2024, 1, 29), U(2023, 11, 31, 23, 59, 59, 999), U(2021, 0, 1), U(2021, 0, 3, 12), U(2020, 11, 31), U(2020, 11, 28),
  U(2015, 11, 29), U(2016, 0, 3), U(2016, 0, 4), U(2018, 11, 31), U(2019, 0, 1), U(2026, 0, 1), U(2027, 0, 1),
  U(1970, 0, 1), -1, 1, -86400000 * 400 - 12345, U(1969, 11, 31, 23, 59, 59, 999), U(1900, 0, 1), U(1900, 5, 15, 7, 8, 9, 10),
  U(2100, 11, 31, 23, 59, 59, 999), U(2000, 5, 15, 12), U(2012, 6, 4, 6, 30, 15, 500), U(1985, 9, 26, 1, 21),
  // DST transitions: New York, Amsterdam, Sao Paulo, Lord Howe, and Kolkata (no DST)
  U(2021, 2, 14, 6, 59, 59, 999), U(2021, 2, 14, 7), U(2021, 2, 14, 7, 30), U(2021, 10, 7, 4, 59, 59, 999), U(2021, 10, 7, 5, 30),
  U(2021, 10, 7, 6), U(2021, 10, 7, 6, 30), U(2021, 10, 7, 7), U(2021, 2, 28, 0, 59, 59, 999), U(2021, 2, 28, 1), U(2021, 2, 28, 2),
  U(2021, 9, 31, 0, 30), U(2021, 9, 31, 1), U(2021, 9, 31, 1, 30), U(2021, 9, 31, 2), U(2018, 10, 4, 2, 59), U(2018, 10, 4, 3, 0),
  U(2018, 10, 4, 12), U(2019, 1, 17, 1, 59), U(2019, 1, 17, 2, 30), U(2021, 3, 3, 14, 30), U(2021, 9, 2, 15, 30),
  U(2021, 3, 4, 12), U(2021, 6, 1, 12), U(2021, 5, 30, 23, 59, 59, 999),
];
const instants = [...base];
for (let i = 0; i < (FULL ? 60 : 12); i++) instants.push(Math.floor(U(1950, 0, 1) + rnd() * (U(2050, 0, 1) - U(1950, 0, 1))));
instants.push(NaN);
const iv = instants.map(num);

// ---- d3-time intervals ----
const bases = {
  millisecond: [d3.timeMillisecond, d3.utcMillisecond, 1], second: [d3.timeSecond, d3.utcSecond, 1e3],
  minute: [d3.timeMinute, d3.utcMinute, 6e4], hour: [d3.timeHour, d3.utcHour, 36e5],
  day: [d3.timeDay, d3.utcDay, 864e5], unixDay: [d3.timeDay, d3.unixDay, 864e5],
  week0: [d3.timeSunday, d3.utcSunday, 6048e5], week1: [d3.timeMonday, d3.utcMonday, 6048e5],
  week2: [d3.timeTuesday, d3.utcTuesday, 6048e5], week3: [d3.timeWednesday, d3.utcWednesday, 6048e5],
  week4: [d3.timeThursday, d3.utcThursday, 6048e5], week5: [d3.timeFriday, d3.utcFriday, 6048e5],
  week6: [d3.timeSaturday, d3.utcSaturday, 6048e5], month: [d3.timeMonth, d3.utcMonth, 2592e6],
  year: [d3.timeYear, d3.utcYear, 31536e6],
};
const names = ['millisecond', 'second', 'minute', 'hour', 'day', 'unixDay', 'week0', 'week1', 'week2', 'week3', 'week4', 'week5', 'week6',
  'month', 'year', 'millisecond.every(100)', 'millisecond.every(7)', 'millisecond.every(1)', 'second.every(5)', 'second.every(30)',
  'second.every(1)', 'minute.every(5)', 'minute.every(15)', 'hour.every(3)', 'hour.every(12)', 'day.every(2)', 'day.every(5)',
  'day.every(40)', 'unixDay.every(2)', 'unixDay.every(3)', 'week0.every(2)', 'week0.every(3)', 'week1.every(4)', 'month.every(3)',
  'month.every(5)', 'month.every(1)', 'year.every(5)', 'year.every(10)', 'year.every(1)', 'year.every(100)', 'millisecond.every(100).every(3)',
  'hour.every(1)', 'month.every(24)', 'millisecond.every(0)', 'day.every(2).every(2)'];
function make(expr, utc) {
  const parts = expr.split('.every(');
  let it = bases[parts[0]][utc ? 1 : 0];
  for (const p of parts.slice(1)) {
    if (typeof it.every !== 'function') return null; // upstream throws a TypeError
    it = it.every(parseFloat(p));
    if (!it) return null;
  }
  return it;
}
const safe = (f) => { try { return num(f()); } catch (e) { return 'ERR'; } };
const intervals = [];
const pivot = U(1999, 5, 15, 3);
for (const name of names) {
  for (const utc of [true, false]) {
    if (name.startsWith('unixDay') && !utc) continue;
    const it = make(name, utc);
    if (!it) { intervals.push({ name, utc, null: true }); continue; }
    const rec = { name, utc, hasCount: typeof it.count === 'function', hasEvery: typeof it.every === 'function' };
    const ops = { floor: (t) => it.floor(t), ceil: (t) => it.ceil(t), round: (t) => it.round(t), off1: (t) => it.offset(t, 1),
      offm1: (t) => it.offset(t, -1), off5: (t) => it.offset(t, 5), offm13: (t) => it.offset(t, -13), off0: (t) => it.offset(t, 0) };
    for (const [k, f] of Object.entries(ops)) rec[k] = instants.map((t) => safe(() => f(t)));
    if (rec.hasCount) rec.count = instants.map((t) => safe(() => it.count(pivot, t)));
    const unit = bases[name.split('.')[0]][2];
    rec.ranges = [];
    for (const start of [U(2000, 0, 1), U(2021, 2, 13, 12, 34, 56, 789), U(2018, 9, 30, 20)]) {
      for (const step of [1, 2, 7]) {
        const stop = start + unit * (name.includes('every') ? 60 : 40) * step;
        const r = it.range(new Date(start), new Date(stop), step);
        rec.ranges.push({ start, stop, step, out: r.map(Number) });
      }
    }
    intervals.push(rec);
  }
}

// ---- tick intervals ----
const tickCases = [];
for (const [a, b, c] of [[U(2000, 0, 1), U(2000, 0, 2), 10], [U(2000, 0, 1), U(2000, 0, 1, 0, 0, 10), 5], [U(2000, 0, 1), U(2001, 0, 1), 12],
  [U(2000, 0, 1), U(2010, 0, 1), 10], [U(1900, 0, 1), U(2100, 0, 1), 10], [U(2000, 0, 1), U(2000, 0, 1, 0, 0, 0, 500), 10],
  [U(2021, 2, 1), U(2021, 3, 1), 8], [U(2021, 2, 1), U(2021, 2, 10), 10], [U(2000, 0, 1), U(2003, 0, 1), 4], [U(2000, 0, 1), U(2000, 0, 1), 10],
  [U(2020, 0, 1), U(1999, 0, 1), 6], [U(2000, 0, 1), U(2000, 6, 1), 6], [U(2000, 0, 1), U(2000, 0, 15), 7], [U(2000, 0, 1), U(2000, 0, 1, 12), 6]]) {
  for (const utc of [true, false]) {
    const f = utc ? d3.utcTicks : d3.timeTicks;
    tickCases.push({ start: a, stop: b, count: c, utc, out: f(new Date(a), new Date(b), c).map(Number) });
  }
}
const tickStepCases = [];
for (const [a, b, c] of [[0, 1, 10], [0, 100, 5], [0.1, 0.35, 7], [-5, 5, 10], [1e-9, 1e-7, 4], [0, 1e9, 3], [5, 1, 4], [3, 3, 10], [0, 1, 0], [0, 0.5, 1], [0, 10, 1.5], [1e21, 5e21, 6], [-1234, 5678, 9], [0, NaN, 3]])
  tickStepCases.push({ a: num(a), b: num(b), c, out: String(tickStep(a, b, c)) });

// ---- d3-time-format ----
const de = { dateTime: '%A, der %e. %B %Y, %X', date: '%d.%m.%Y', time: '%H:%M:%S', periods: ['AM', 'PM'],
  days: ['Sonntag', 'Montag', 'Dienstag', 'Mittwoch', 'Donnerstag', 'Freitag', 'Samstag'], shortDays: ['So', 'Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa'],
  months: ['Januar', 'Februar', 'März', 'April', 'Mai', 'Juni', 'Juli', 'August', 'September', 'Oktober', 'November', 'Dezember'],
  shortMonths: ['Jan', 'Feb', 'Mär', 'Apr', 'Mai', 'Jun', 'Jul', 'Aug', 'Sep', 'Okt', 'Nov', 'Dez'] };
const odd = { dateTime: '%x %X %%', date: '%Y-%m-%d', time: '%H:%M', periods: ['a.m.', 'p.m.'], days: de.days, shortDays: ['So', 'Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa'],
  months: de.months, shortMonths: ['Ja', 'Fe', 'Mä', 'Ap', 'Ma', 'Ju', 'Jl', 'Au', 'Se', 'Ok', 'No', 'De'] };
const shortLoc = { dateTime: '%x|%X', date: '%d', time: '%H', periods: ['AM'], days: ['Su', 'Mo'], shortDays: ['S', 'M'], months: ['a', 'b', 'c'], shortMonths: ['a', 'A', 'b'] };
const locales = { default: null, de, odd, short: shortLoc };
const tfl = (name) => (locales[name] ? tf.timeFormatLocale(locales[name]) : { format: tf.timeFormat, utcFormat: tf.utcFormat, parse: tf.timeParse, utcParse: tf.utcParse });

const dirs = 'aAbBcdefgGHIjLmMpqQsSuUVwWxXyYZ%'.split('');
const fspecs = [];
for (const d of dirs) { fspecs.push('%' + d); if ('dejHILmMSUVWyYgGf'.includes(d)) for (const m of ['-', '_', '0']) fspecs.push('%' + m + d); }
fspecs.push('%Y-%m-%dT%H:%M:%S.%LZ', '%b %d, %Y', '%A, %B %e, %Y', '%I:%M:%S %p', '%-I:%M %p', 'Q%q %Y', 'W%U', 'W%W', '%G-W%V-%u', '%x %X', '%c',
  '%Y%m%d', '%H:%M', '%y%j', 'abc', '', '%', '%%', '%-', '%Q %s', '%k %E %J', '%-%d', '%_e|%-e|%0e', '%é', '%-é', '100%%', '%d/%m/%Y %H:%M:%S %Z',
  '%f', '%-f', '%_H', '%-Y', '%_m', '%-j', '%_j', '%0d', '%__d', '%-_d');
const fmtInstants = instants;
const formats = [];
for (const name of Object.keys(locales)) {
  const L = tfl(name);
  for (const utc of [true, false]) {
    const list = (name === 'default' || FULL) ? fspecs : fspecs.filter((_, i) => i % 3 === 0);
    for (const spec of list) {
      const f = utc ? L.utcFormat(spec) : L.format(spec);
      formats.push({ locale: name, utc, spec, out: fmtInstants.map((t) => f(new Date(t))) });
    }
  }
}
// ISO week edge cases and every day of two years.
const days = [];
for (const y of [2015, 2016, 2020, 2021]) for (let d = 0; d < 366; d += 1) days.push(U(y, 0, 1 + d, 12, 30, 45, 123));
const dayFormats = [];
for (const spec of ['%j', '%U', '%W', '%V', '%g', '%G', '%u', '%w', '%a %d %b']) for (const utc of [true, false]) {
  const f = utc ? tf.utcFormat(spec) : tf.timeFormat(spec);
  dayFormats.push({ spec, utc, out: days.map((t) => f(new Date(t))) });
}

const rt = ['%Y-%m-%d', '%Y-%m-%d %H:%M:%S', '%Y-%m-%dT%H:%M:%S.%LZ', '%b %d, %Y', '%B %e %Y', '%A %d %B %Y', '%d/%m/%y', '%I:%M %p', '%H:%M:%S.%f', '%Y%m%d%H%M%S',
  '%Q', '%s', '%s.%L', '%Y-%m-%d %H:%M:%S %Z', '%Y-%m-%dT%H:%M:%S%Z', '%G-W%V-%u', '%Y-W%U-%w', '%Y-W%W-%u', '%Y-%j', '%Y Q%q', '%x %X', '%c',
  '%y', '%-d/%-m/%Y', '%_d.%_m.%Y', '%e', '%m', '%p', '%a', '%Z', '%%%Y', '%q', '%u', '%V', '%U', '%W', '%G', '%g', '%H', '%I %p', '%f'];
const strs = ['2000-01-01', '2021-03-14 02:30:00', '2021-03-14T06:59:59.123Z', 'Jan 5, 2021', 'March 7 2021', 'Sunday 07 March 2021', '05/03/21', '05/03/68', '05/03/69',
  '11:45 PM', '12:00 AM', '12:00 pm', '0:00 am', '13:00 PM', '23:59:59.999999', '20210307123456', '946684800000', '946684800', '946684800.5', '2021-03-07 12:00:00 +0100',
  '2021-03-07 12:00:00 -0530', '2021-03-07 12:00:00 Z', '2021-03-07T12:00:00+01:00', '2021-03-07T12:00:00+0100', '2021-03-07T12:00:00-05', '2021-W09-7', '2021-W53-1', '2021-W00-1', '2015-W53-4',
  '2021-W09', '2021-09-0', '2021-09-1', '2021-09-7', '2021-10-3', '2021-065', '2021-366', '2021-000', '2021 Q3', '2021 Q4', '1/2/2021 3:04:05 PM', '2021-03-07 12:00:00abc', 'abc',
  '', ' 5', '5 ', '  2021-03-07', '2021-3-7', '2021-13-01', '2021-02-30', '2021-00-10', '99', '68', '69', '00', '  7', '7', '12', '31', '32', '1', '4', '0', '5', '%2021', '2021',
  'sun', 'SUNDAY', 'MON', 'monday', 'Tuesday', 'jan', 'JANUARY', 'Mär', 'März', 'mar', 'AM', 'pm', 'a.m.', 'p.m.', 'ok', '12', '12 PM', '12 AM', '1 PM', '00 PM', '2021-03-07 24:00:00', '2021-03-07 23:60:00',
  '2021-03-07 12:00:60', '+0100', 'Z', '-0800', '+05:30', '+05', '+5', 'x+0530', '1234567890123', '0', '-5', '253402300800000', '99999999999999999999999', '8.64e15', '8640000000000000',
  '2021-03-07 12:00:00.123456', '2021-03-07 12:00:00.1', '31/12/99', '01/01/00', '1/1/1', '12/31/9999', '0000-01-01', '0050-06-15', '0099-12-31', '0100-01-01', '  12:30', '9', '99999', '2021-3-7 9:5:3'];
const parses = [];
for (const name of Object.keys(locales)) {
  const L = tfl(name);
  for (const utc of [true, false]) {
    const list = (name === 'default' || FULL) ? rt : rt.filter((_, i) => i % 2 === 0);
    for (const spec of list) {
      const p = utc ? L.utcParse(spec) : L.parse(spec);
      const cases = strs.map((s) => { const r = p(s); return [s, r === null ? 'null' : Number.isNaN(+r) ? 'NaN' : String(+r)]; });
      // Round trips of formatted instants.
      const f = utc ? L.utcFormat(spec) : L.format(spec);
      for (const t of instants.slice(0, 40)) {
        if (Number.isNaN(t)) continue;
        const s = f(new Date(t));
        const r = p(s);
        cases.push([s, r === null ? 'null' : Number.isNaN(+r) ? 'NaN' : String(+r)]);
      }
      parses.push({ locale: name, utc, spec, cases });
    }
  }
}

// ---- Date.parse ----
const dateStrings = ['Jan 1', '1/2', 'January', 'Jan', '2000', '99', '1', '12', '13', '32', 'Jan 1 2000', '1 Jan', '1 Jan 2000', '12/25/99', '12/25/1999', '2000/01/02', 'Jan 2000', '2000 Jan', 'Jan 2000 5',
  '5 2000 Jan', 'Thu Jan 01 2000', 'Monday, January 1, 2000', '1/2/3/4', '2000-01-01 10:00', '2000-01-01 10:00:00.5', '2000-01-01T10', '2000-1-1', '2000-01-01T10:00Z', '2000-01-01T10:00:00+0530',
  '2000-01-01T10:00:00+05', '2000-01-01T24:00', '2000-01-01T24:01', '2000-02-30', '2000-13-01', '+002000-01-01', '-000001-01-01', '-000000-01-01', 'Jan 1 2000 10:00 PM', 'Jan 1 2000 12:00 AM',
  'Jan 1 2000 13:00 PM', '10:00', '2000-01-01T10:00:00.123456Z', 'Sat, 01 Jan 2000 00:00:00 GMT', 'Sat, 01 Jan 2000 00:00:00 GMT+0100', 'Sat Jan 01 2000 00:00:00 GMT-0800 (PST)',
  '1 January 2000', '2000-01-01T10:00:00 +01:00', 'x', '', '   ', '2000 01 01', '1.2.2000', '2000.01.02', 'Jan-01-2000', '01-Jan-2000', '2000-Jan-01', 'Jan 1, 2000 EST', 'Jan 1, 2000 10:00 PST',
  '20000101', '2000-01', '2000-01-01T10:00:00.', '2000-01-01t10:00', '2000-01-01 10:00 UTC', '2000-01-01Z', '1 2', '1 2 3', '2000-01-01 x', 'Jan 1 x', 'x Jan 1', 'Jan 1 2000 (comment) 10:00',
  'Jan 1 2000 10:00:00.5 PM', '12/31/2000 23:59:59', 'Feb 29 2001', 'Feb 30 2000', '2000-01-01T10:00:00.1234567890123', '275760-09-13', '+275760-09-13T00:00:00Z', '+275760-09-13T00:00:00.001Z',
  '2000/1/2 3:4:5', '3:4:5 2000/1/2', '1/2/00', '1/2/49', '1/2/50', '1/2/100', '1/2/999', 'Tue Mar 01 2011 00:00:00 GMT+0000 (Coordinated Universal Time)', '2000-01-01T10:00:00+05:30',
  '2000-01-01T10:00:00-0800', 'Jan 1 2000 GMT+5', 'Jan 1 2000 GMT+530', 'Jan 1 2000 UTC+05:30', 'Jan 1 2000 Z', '2021-03-14T02:30', '2021-03-14 02:30', '2021-11-07T01:30', '2018-11-04T00:30',
  '2021-03-28T02:30:00', '1970-01-01', '1970-01-01T00:00:00', '1969-12-31T23:59:59.999Z', '0000-01-01', '0001-01-01T00:00:00', '0050-01-01', '9999-12-31T23:59:59.999Z', '10000-01-01',
  '2000-01-01T10:00:00.5', '2000-01-01T10:00:00,5', '2000-01-01 10:00:00.123', 'January 1, 2000', 'Sept 1 2000', 'Sep 1 2000', 'Augu 1 2000', '2000 1 1', '00/01/01', '2000-1', '2000-01-1', '2000-01-01T1:00',
  '2000-01-01T10:0', 'Jan 1 2000 10', 'Jan 1 2000 10:', 'Jan 1 2000 10:00:', 'Jan 1 2000 10::', 'Jan 1 2000 10:00 PM PST', 'Jan 1 2000 10:00 pm', 'Jan 1 2000 10:00pm', 'Jan 1 2000 EDT', 'Jan 1 2000 CDT',
  'Jan 1 2000 MST', 'Jan 1 2000 PDT', '12 Jan 2000', '31 Dec 1999 23:59:59 GMT', 'Fri, 31 Dec 1999 23:59:59 +0000', 'Fri, 31 Dec 1999 23:59:59 -0500', 'Fri Dec 31 1999 23:59:59 GMT+0100 (CET)',
  '2000-01-01T00:00:00.000+00:00', '2000-01-01T00:00:00.000-00:00', '2000-01-01 00:00:00+0100', '2000-01-01 00:00:00 +0100', '2000-01-01 00:00:00 -01', '2000-01-01T00:00:00Z ', ' 2000-01-01T00:00:00Z',
  '2000-01-01\t10:00', ' 2000-01-01', '2000 Jan 1', '١٢/٠١/٢٠٠٠', 'Jan 1 2000 é', 'é Jan 1 2000', '1(x)/2/2000', '(x) 1/2/2000', '1/2/2000 (', '1/2/2000 )', ')1/2/2000', '1/2/2000-', '-1/2/2000',
  '1/2/2000+', '1-2-2000', '2000-01-01T10:00:00+05:3', '2000-01-01T10:00:00+05:300', '2000-01-01T10:00:00+0530x', 'tuesday', 'T', '2000-01-01T', '2000-01-01T10:00:00Zz', '99999999999', '0', '-1', '+1', '1e3',
  '2000-06-15', '2000-06-15T12:00', '2000-06-15 12:00', 'Jun 15 2000 12:00', '06/15/2000 12:00', '2038-01-19T03:14:08Z', '1901-12-13T20:45:52Z', '1800-06-15', '1800-06-15T12:00', 'Jun 15 1800', 'Jun 15 1800 12:00',
  '2000-01-01T10:00:00.99999999999999999999Z', '2000-01-01T10:00:00.000000001Z', '2000-01-01T10:00:00.1Z', '2000-01-01T10:00:00.12Z', '2000-01-01T10:00:00.1234Z', '2000-01-01T10:00:60Z', '2000-01-01T10:60Z',
  '2000-01-01T23:59:59.999+23:59', '2000-01-01T23:59:59.999+24:00', '2000-01-01T23:59:59.999-23:59', '2000-01-32', '2000-00-01', '2000-01-00', '1/32/2000', '0/1/2000', '13/1/2000', '1/0/2000', '2/29/2000', '2/29/1900',
  '2/29/2100', '12/31/275760', '9/13/275760', '1/1/0', '1/1/00', '1/1/01', '1/1/1', '0/0', '31 31 31', '1 1 1 1', 'Mon Jan 1', 'Jan 1 Mon', 'Mon', 'Jan 1 2000 10:00:00.5', 'Jan 1 2000 10:00.5', 'Jan 1 2000 10.5'];
const dateParse = dateStrings.map((s) => [s, num(new Date(s).getTime())]);

// ---- vega-time ----
const units = [['year'], ['quarter'], ['month'], ['week'], ['date'], ['day'], ['dayofyear'], ['hours'], ['minutes'], ['seconds'], ['milliseconds'],
  ['year', 'quarter'], ['year', 'month'], ['year', 'week'], ['year', 'week', 'day'], ['year', 'month', 'date'], ['year', 'dayofyear'], ['month', 'date'], ['quarter', 'day'],
  ['hours', 'minutes'], ['hours', 'minutes', 'seconds'], ['minutes', 'seconds', 'milliseconds'], ['week', 'day'], ['month'], ['year', 'month', 'date', 'hours'],
  ['year', 'month', 'date', 'hours', 'minutes'], ['year', 'month', 'date', 'hours', 'minutes', 'seconds'], ['year', 'month', 'date', 'hours', 'minutes', 'seconds', 'milliseconds'],
  ['day', 'hours'], ['week', 'hours'], ['quarter', 'date'], ['year', 'quarter', 'month'], ['dayofyear', 'hours']];
const steps = [1, 2, 3, 5, 7, 10, 15, 0.5, 0, null, 100, 12];
const floorInstants = FULL ? instants : instants.slice(0, 50);
const floors = [];
for (const utc of [true, false]) {
  for (const u of units) {
    for (const step of steps) {
      if (!FULL && steps.indexOf(step) % 3 !== 0) continue;
      const f = (utc ? vt.utcFloor : vt.timeFloor)(u, step === null ? undefined : step);
      floors.push({ utc, units: u, step, out: floorInstants.map((t) => safe(() => f(t))) });
    }
  }
}
// Years 0..99 (the Date constructor's 1900 quirk).
const early = [U(50, 5, 15), -61000000000000, -60000000000000, -62000000000000].map((t) => { const d = new Date(t); d.setUTCFullYear(50); return +d; });
const earlyFloors = [];
for (const utc of [true, false]) for (const u of [['year'], ['year', 'month'], ['year', 'month', 'date'], ['year', 'week'], ['year', 'dayofyear'], ['week'], ['day']]) {
  const f = (utc ? vt.utcFloor : vt.timeFloor)(u, 1);
  earlyFloors.push({ utc, units: u, out: early.map((t) => safe(() => f(t))), ins: early });
}
const unitOf = ['year', 'quarter', 'month', 'week', 'date', 'day', 'dayofyear', 'hours', 'minutes', 'seconds', 'milliseconds', 'bogus'];
const offsets = [];
for (const utc of [true, false]) for (const u of unitOf) for (const step of [1, -1, 3, -7, 0, 2.5]) {
  const f = utc ? vt.utcOffset : vt.timeOffset;
  offsets.push({ utc, unit: u, step, out: instants.map((t) => { const r = f(u, new Date(t), step); return r === undefined ? 'undef' : num(r); }) });
}
const sequences = [];
for (const utc of [true, false]) for (const u of unitOf) {
  const f = utc ? vt.utcSequence : vt.timeSequence;
  for (const [a, b, s] of [[U(2020, 0, 1), U(2021, 0, 1), 1], [U(2021, 2, 10), U(2021, 2, 20), 1], [U(2021, 2, 13, 12), U(2021, 2, 14, 12), 3], [U(2000, 0, 1), U(2000, 0, 1, 0, 0, 1), 250]]) {
    const span = b - a;
    const unitMs = { year: 31536e6, quarter: 7776e6, month: 2592e6, week: 6048e5, date: 864e5, day: 864e5, dayofyear: 864e5, hours: 36e5, minutes: 6e4, seconds: 1e3, milliseconds: 1 }[u] || 1;
    if (span / unitMs > 5000) continue;
    const r = f(u, new Date(a), new Date(b), s);
    sequences.push({ utc, unit: u, start: a, stop: b, step: s, out: r === undefined ? 'undef' : r.map(Number) });
  }
}
const specs = [];
const spUnits = [...units, ['year', 'month', 'date', 'hours', 'minutes', 'seconds'], ['date', 'hours'], ['quarter', 'month'], ['seconds', 'milliseconds'], ['minutes'], ['year', 'day'], ['bogus'], [], ['year', 'year']];
for (const u of spUnits) for (const over of [undefined, { year: '%y', 'year-month': '%b %Y', hours: '%H', date: '' }, { 'year-month-date': '%x', 'hours-minutes': '' }]) {
  let out; try { out = vt.timeUnitSpecifier(u, over); } catch (e) { out = { err: String(e.message) }; }
  specs.push({ units: u, over: over === undefined ? null : over, out });
}
const bins = [];
const spans = [0, 1, 500, 5000, 90000, 1800000, 18000000, 259200000, 3456000000, 17280000000, 94608000000, 3153600000000, 3e14, -5000, -3e10, 1e15];
for (const sp of spans) for (const maxbins of [1, 2, 5, 10, 20, 40, 100, undefined, 0]) for (const start of [U(2000, 0, 1), U(1970, 0, 1), 12345678]) {
  const r = vt.timeBin({ extent: [start, start + sp], maxbins });
  bins.push({ start, stop: start + sp, maxbins: maxbins === undefined ? null : maxbins, units: r.units, step: num(r.step) });
}
bins.push({ start: null, stop: 5, maxbins: 10, ...(() => { const r = vt.timeBin({ extent: [NaN, 5], maxbins: 10 }); return { units: r.units, step: num(r.step) }; })() });
const doy = instants.map((t) => ({ t: num(t), doy: num(vt.dayofyear(t)), week: num(vt.week(t)), udoy: num(vt.utcdayofyear(t)), uweek: num(vt.utcweek(t)) }));
const early2 = [-61000000000000, -60000000000000, -62135596800000, U(0, 0, 1), U(99, 5, 5), U(50, 0, 1)];
const doyEarly = early2.map((t) => ({ t, doy: num(vt.dayofyear(t)), week: num(vt.week(t)), udoy: num(vt.utcdayofyear(t)), uweek: num(vt.utcweek(t)) }));
const detect = [];
const mkd = (a) => a.map((t) => ({ x: t }));
for (const set of [[U(2000, 0, 1), U(2001, 0, 1)], [U(2000, 0, 1), U(2000, 1, 1), U(2000, 2, 1)], [U(2000, 0, 1), U(2000, 3, 1), U(2000, 6, 1)], [U(2000, 0, 3), U(2000, 0, 10), U(2000, 0, 17)],
  [U(2000, 0, 1, 1), U(2000, 0, 1, 2)], [U(2000, 0, 1, 1, 5), U(2000, 0, 1, 2, 10)], [U(2000, 0, 1, 1, 5), U(2000, 0, 1, 2, 15)], [U(2000, 0, 1, 1, 5, 7), U(2000, 0, 1, 2, 10, 8)],
  [U(2000, 0, 1, 1, 5, 7, 8)], [U(2000, 0, 2), U(2000, 0, 3)], [U(2000, 0, 1), U(2010, 0, 1), U(2020, 0, 1)], [U(2001, 0, 1), U(2010, 0, 1)], [U(2000, 5, 5)], [], [U(2000, 0, 1, 0, 30)],
  [U(2000, 0, 1, 0, 0, 30)], [U(2000, 0, 1, 12)], [U(2000, 0, 5), U(2000, 0, 12)], [U(2000, 0, 5), U(2000, 0, 12, 1)]]) {
  for (const utc of [true, false]) {
    let out; try { const r = vt.detectTimeUnits(mkd(set), (d) => d.x, utc); out = { units: r.units, step: r.step }; } catch (e) { out = { err: String(e.message) }; }
    detect.push({ set, utc, out });
  }
}
try { vt.detectTimeUnits(mkd([NaN]), (d) => d.x, true); } catch (e) { detect.push({ set: [null], utc: true, out: { err: String(e.message) } }); }

// ---- vega-format ----
const mfSpecs = [undefined, {}, { milliseconds: '%L ms', seconds: '%S s', minutes: '%H:%M', hours: '%H h', date: '%d.', week: 'wk %U', month: '%m', quarter: 'Q%q', year: "'%y" }, { day: '%A', quarter: '%b' }];
const mfInstants = [];
for (const t of [U(2021, 0, 1), U(2021, 1, 1), U(2021, 3, 1), U(2021, 0, 3), U(2021, 0, 4), U(2021, 0, 5, 6), U(2021, 0, 5, 6, 7), U(2021, 0, 5, 6, 7, 8), U(2021, 0, 5, 6, 7, 8, 9), U(2021, 5, 15, 12), U(2021, 2, 14, 7),
  U(2021, 2, 14, 6), U(2021, 10, 7, 5), U(2021, 10, 7, 6), U(2021, 2, 28, 0), U(2021, 2, 28, 1), U(2021, 9, 31, 0), U(2021, 9, 31, 1), U(2000, 0, 1), U(1999, 11, 31, 23), NaN, U(2018, 10, 4, 3), U(2018, 10, 4, 2, 0, 0, 1)]) {
  mfInstants.push(t);
}
const multi = [];
const loc = vf.locale();
for (const utc of [true, false]) for (const spec of mfSpecs) {
  const f = utc ? loc.utcFormat(spec) : loc.timeFormat(spec);
  multi.push({ utc, spec: spec === undefined ? null : spec, out: mfInstants.map((t) => f(new Date(t))) });
}
const ffVals = [0, 1, -1, 0.1, 1234.5678, 1e-7, 123456789012, 1e21, 0.1 + 0.2, 1 / 3, 12345678.9, NaN, Infinity, 0.000123, 1e-20, 100, 2.5, -0.5, 5e-324, 1e15, 123456789.123456789];
const ffSpecs = ['', ',', 's', '%', 'e', '$,', ',d', '~s', '.3', '.2f', ',.3g', '+', '(', 'r', 'p', '.0%', 'n', 'f', 'g', '$', 'x', '0>10', '.2e'];
const formatFloat = ffSpecs.map((spec) => { const f = loc.formatFloat(spec); return { spec, out: ffVals.map((v) => f(v)) }; });
const spans2 = [[0, 1, 10], [0, 100, 5], [0, 1000000, 10], [0.001, 0.009, 8], [-5, 5, 10], [1e9, 5e9, 4], [1e-6, 2e-6, 5], [0, 12345, 7], [100, 100, 10], [0, 0.5, 3], [0, 1e21, 5], [3, 1, 4]];
const spanSpecs = [undefined, ',f', 's', '%', 'e', 'g', 'r', 'p', '$,f', '~s', '.1f', '', 'f', '.2s', '+,f', '$s'];
const formatSpan = [];
for (const [a, b, c] of spans2) for (const spec of spanSpecs) {
  let out; try { const f = loc.formatSpan(a, b, c, spec); out = ffVals.map((v) => f(v)); } catch (e) { out = { err: String(e.message) }; }
  formatSpan.push({ a, b, c, spec: spec === undefined ? null : spec, out });
}
const isoFmt = instants.map((t) => { try { return tf.isoFormat(new Date(t)); } catch (e) { return 'ERR'; } });
const isoParse = ['2000-01-01', '2000-01-01T10:00:00Z', 'bogus', '', '2000-01-01T10:00:00.123+01:00'].map((s) => { const d = tf.isoParse(s); return [s, d === null ? 'null' : String(+d)]; });
const extremes = [8.64e15, -8.64e15, 8.64e15 + 1, 253402300799999, -62167219200000, -62198755200000, 1e15, 2 ** 53].map((t) => ({ t, iso: (() => { try { return tf.isoFormat(new Date(t)); } catch (e) { return 'ERR'; } })(), y: num(new Date(t).getUTCFullYear()) }));

const datetime = [];
for (const args of [[2000, 0, 1], [99, 0, 1], [0, 0, 1], [100, 0], [2000, 12, 32], [2000, -1, 1], [2000, 0, 0], [2000, 1, 30, 25, 61, 61, 1001], [1e9, 0], [275760, 8, 13], [275760, 8, 14], [-1, 0], [2000.7, 0.9, 1.9], [NaN, 0], [2000, Infinity], [50, 5, 5, 5, 5, 5, 5], [2021, 2, 14, 2, 30], [2021, 10, 7, 1, 30], [2021, 2, 28, 2, 30], [2018, 10, 4, 0, 30], [2018, 10, 4, 0, 0, 0, 0], [2000, 0, 1, 0, 0, 0, 0.9]]) {
  datetime.push({ args, local: num(new Date(...args)), utc: num(Date.UTC(...args)) });
}

console.log(JSON.stringify({ tz, instants: iv, intervals, tickCases, tickStepCases, locales, fmtInstants: iv, formats, days, dayFormats, parses, dateParse, floorInstants: floorInstants.map(num), floors, early, earlyFloors, offsets, sequences, specs, bins, doy, doyEarly, detect, multi, mfInstants: mfInstants.map(num), ffVals: ffVals.map(String), formatFloat, formatSpan, isoFmt, isoParse, extremes, datetime }));
