// Records time/utc scales. The output depends on the host time zone, so the
// generator is run with TZ=America/New_York (the test installs the same zone):
// TZ=America/New_York NODE_PATH=<node_modules> node gen_time.mjs | gzip -9 > time.json.gz
import { C, emit, NaNv, U, INF, D } from './gen_common.mjs';

const H = 3600e3, DAY = 24 * H;
const t0 = Date.UTC(2020, 0, 1);
const doms = [
  ['year', [D(t0), D(Date.UTC(2020, 11, 31))]],
  ['year-rev', [D(Date.UTC(2020, 11, 31)), D(t0)]],
  ['decade', [D(Date.UTC(2000, 0, 1)), D(Date.UTC(2031, 5, 15))]],
  ['century', [D(Date.UTC(1900, 0, 1)), D(Date.UTC(2100, 0, 1))]],
  ['month', [D(Date.UTC(2020, 1, 3, 5)), D(Date.UTC(2020, 2, 17, 7))]],
  ['dst-spring', [D(Date.UTC(2020, 2, 7, 12)), D(Date.UTC(2020, 2, 10, 12))]],
  ['dst-fall', [D(Date.UTC(2020, 10, 1, 0)), D(Date.UTC(2020, 10, 2, 12))]],
  ['week', [D(Date.UTC(2020, 5, 1)), D(Date.UTC(2020, 5, 8))]],
  ['day', [D(t0), D(t0 + DAY)]],
  ['hours', [D(t0 + 3 * H + 17 * 60e3), D(t0 + 9 * H + 41 * 60e3)]],
  ['minutes', [D(t0 + 5 * 60e3 + 7000), D(t0 + 27 * 60e3 + 21000)]],
  ['seconds', [D(t0 + 1234), D(t0 + 47 * 1000 + 999)]],
  ['millis', [D(t0 + 12), D(t0 + 480)]],
  ['equal', [D(t0), D(t0)]],
  ['numbers', [t0, t0 + 30 * DAY]],
  ['nan', [D(NaN), D(t0)]],
  ['three', [D(t0), D(t0 + 10 * DAY), D(t0 + 100 * DAY)]],
  ['strings', ['2020-01-01', '2020-06-01']],
  ['big', [D(-8.64e15), D(8.64e15)]],
];
const XS = (d) => {
  const lo = d[0]?.$ === 'date' ? d[0].v : d[0], hi = d[d.length - 1]?.$ === 'date' ? d[d.length - 1].v : d[d.length - 1];
  const span = (typeof lo === 'number' && typeof hi === 'number') ? hi - lo : DAY;
  const base = typeof lo === 'number' ? lo : t0;
  return [D(base), D(base + span * 0.25 || 0), D(base + span / 2 || 0), D(base + span || 0), D(base - DAY), D(base + span + DAY), base + span / 3, NaNv, null, U, D(NaN), '2020-03-01'];
};
const TC = [null, 1, 2, 3, 5, 8, 10, 12, 20, 50, 100, 0, NaNv];
for (const type of ['time', 'utc']) for (const [name, d] of doms) {
  C(`${type} ${name}`, type, [['domain', d], ['range', [0, 100]]],
    [['domain'], ...XS(d).map((x) => ['apply', x]), ...TC.map((c) => ['ticks', c]),
      ...[-10, 0, 25, 50, 100, 110, NaNv, null].map((y) => ['invert', y]), ['invertRange', [10, 60]], ['invertRange', [60, 10]]]);
  C(`${type} ${name} clamp`, type, [['domain', d], ['range', [0, 100]], ['clamp', true]], [...XS(d).map((x) => ['apply', x]), ['invert', 150]]);
  for (const n of [null, 1, 3, 5, 10, 20, 100]) {
    C(`${type} ${name} nice ${n}`, type, [['domain', d], ['nice', n]], [['domain'], ['ticks', 5], ['ticks', null]]);
  }
}
C('time defaults', 'time', [], [['domain'], ['range'], ['apply', D(Date.UTC(2000, 0, 1, 12))], ['ticks', 5]]);
C('utc defaults', 'utc', [], [['domain'], ['range'], ['apply', D(Date.UTC(2000, 0, 1, 12))], ['ticks', 5]]);
C('utc color', 'utc', [['domain', [D(t0), D(t0 + 100 * DAY)]], ['range', ['red', 'blue']]], [['apply', D(t0 + 50 * DAY)], ['apply', D(t0)]]);
C('utc unknown', 'utc', [['domain', [D(t0), D(t0 + DAY)]], ['unknown', 'u']], [['apply', NaNv], ['apply', null], ['apply', D(t0)]]);
C('utc copy', 'utc', [['domain', [D(t0), D(t0 + DAY)]], ['copy'], ['domain', [D(t0), D(t0 + 5 * DAY)]]], [['domain'], ['ticks', 5], ['apply', D(t0 + DAY)]]);
emit();
