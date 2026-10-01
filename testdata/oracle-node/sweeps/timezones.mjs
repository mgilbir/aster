// The time-zone sweep: local time, compared in zones chosen for what they do
// to it.
//
// Every other comparison runs in UTC, where local time and UTC agree, so
// none of them can see a time scale, a time unit, a format or a parse that
// gets local time wrong. This renders charts that read local time in nine
// zones, with node running in that zone (TZ) and the engine configured with
// WithTimezone: a daylight-saving zone each side of the Atlantic, one whose
// shift is half an hour (Lord Howe), offsets of a half and three quarters of
// an hour (Kolkata, St John's, Kathmandu, Chatham), a zone that dropped
// daylight saving (São Paulo), and UTC as the control.
//
// The data sits on the 2024 transitions: a window of hourly timestamps
// around each spring-forward and fall-back date any of the zones has, given
// as epoch milliseconds so the instant is unambiguous and only its local
// reading varies. The charts are journeys through local time: an axis over
// those hours, local time units aggregated, d3-time-format with and without
// %Z, parsing a local time that does not exist or exists twice, the
// datetime() and date-part functions, a time scale's nice domain and its
// hourly ticks, week bins, ISO strings with and without a zone, and the UTC
// variants of each, which must not move.

const ZONES = [
  'UTC',
  'America/New_York',
  'Europe/Amsterdam',
  'Australia/Lord_Howe',
  'Asia/Kolkata',
  'Asia/Kathmandu',
  'Pacific/Chatham',
  'America/St_Johns',
  'America/Sao_Paulo',
];

// The 2024 transition dates (UTC days) of the zones above: the US and
// Canada (March 10, November 3), Europe (March 31, October 27), Lord Howe
// (April 7, October 6), Chatham (April 7, September 29).
const WINDOWS = [
  { name: 'us-spring', start: Date.UTC(2024, 2, 9, 12) },
  { name: 'eu-spring', start: Date.UTC(2024, 2, 30, 12) },
  { name: 'south-autumn', start: Date.UTC(2024, 3, 6, 6) },
  { name: 'chatham-spring', start: Date.UTC(2024, 8, 28, 6) },
  { name: 'lordhowe-spring', start: Date.UTC(2024, 9, 5, 6) },
  { name: 'eu-autumn', start: Date.UTC(2024, 9, 26, 12) },
  { name: 'us-autumn', start: Date.UTC(2024, 10, 2, 12) },
];

const HOURS = 30;

function hourly(start, hours = HOURS) {
  const rows = [];
  for (let i = 0; i < hours; i++) rows.push({ t: start + i * 3600e3, v: (i * 7) % 11 });
  return rows;
}

const W = 420;

// Vega-Lite charts over one window.
const WINDOWED = {
  'axis-over-the-hours': (rows) => ({
    width: W,
    height: 120,
    data: { values: rows },
    mark: 'line',
    encoding: { x: { field: 't', type: 'temporal' }, y: { field: 'v', type: 'quantitative' } },
  }),
  'axis-over-the-hours-utc': (rows) => ({
    width: W,
    height: 120,
    data: { values: rows },
    mark: 'line',
    encoding: { x: { field: 't', type: 'temporal', scale: { type: 'utc' } }, y: { field: 'v', type: 'quantitative' } },
  }),
  'local-hours-aggregated': (rows) => ({
    width: W,
    height: 120,
    data: { values: rows },
    mark: 'bar',
    encoding: { x: { timeUnit: 'hours', field: 't', type: 'ordinal' }, y: { aggregate: 'count', type: 'quantitative' } },
  }),
  'local-dates-aggregated': (rows) => ({
    width: W,
    height: 120,
    data: { values: rows },
    mark: 'bar',
    encoding: { x: { timeUnit: 'yearmonthdate', field: 't', type: 'ordinal' }, y: { aggregate: 'sum', field: 'v', type: 'quantitative' } },
  }),
  'utc-hours-aggregated': (rows) => ({
    width: W,
    height: 120,
    data: { values: rows },
    mark: 'bar',
    encoding: { x: { timeUnit: 'utchours', field: 't', type: 'ordinal' }, y: { aggregate: 'count', type: 'quantitative' } },
  }),
  'hours-and-minutes-binned': (rows) => ({
    width: W,
    height: 120,
    data: { values: rows },
    mark: 'tick',
    encoding: { x: { timeUnit: 'hoursminutes', field: 't', type: 'temporal' } },
  }),
};

// Vega charts over one window: text marks reading local time through the
// expression language, and a time scale's own ticks and nice domain.
function vegaTextChart(rows, expr) {
  return {
    width: W,
    height: rows.length * 14,
    data: [{ name: 't', values: rows }],
    marks: [
      {
        type: 'text',
        from: { data: 't' },
        encode: { update: { x: { value: 4 }, y: { signal: 'datum.v * 0 + indexof(pluck(data("t"), "t"), datum.t) * 14 + 12' }, text: { signal: expr } } },
      },
    ],
  };
}

const VEGA_WINDOWED = {
  'time-format-local': (rows) => vegaTextChart(rows, "timeFormat(datum.t, '%Y-%m-%d %H:%M %Z %a %j %U %W')"),
  'time-format-utc': (rows) => vegaTextChart(rows, "utcFormat(datum.t, '%Y-%m-%d %H:%M %Z')"),
  'date-parts-local': (rows) => vegaTextChart(rows, "[year(datum.t), month(datum.t), date(datum.t), day(datum.t), hours(datum.t), minutes(datum.t), dayofyear(datum.t)] + ''"),
  'time-floor-to-day': (rows) => vegaTextChart(rows, "timeOffset('hour', datum.t, 0) + ' ' + time(datetime(year(datum.t), month(datum.t), date(datum.t)))"),
  'hourly-ticks-and-nice': (rows) => ({
    width: W,
    height: 60,
    data: [{ name: 't', values: rows }],
    scales: [{ name: 'x', type: 'time', domain: { data: 't', field: 't' }, range: 'width', nice: 'day' }],
    axes: [
      // 'hours', vega-time's unit name: the schema's 'hour' is not a key of
      // the interval table upstream looks it up in, and fails.
      { orient: 'bottom', scale: 'x', tickCount: { interval: 'hours', step: 6 }, format: '%H:%M' },
      { orient: 'top', scale: 'x' },
    ],
  }),
  'time-sequence': (rows) =>
    vegaTextChart([rows[0]], `timeSequence('hour', ${rows[0].t}, ${rows[0].t + 30 * 3600e3}, 3) + ''`),
};

// Charts that do not depend on a window.
const FIXED = {
  'nonexistent-and-repeated-local-times': () =>
    vegaTextChart(
      [{ t: 0, v: 0 }],
      "[time(timeParse('2024-03-10 02:30', '%Y-%m-%d %H:%M')), time(timeParse('2024-03-31 02:30', '%Y-%m-%d %H:%M')), time(timeParse('2024-11-03 01:30', '%Y-%m-%d %H:%M')), time(timeParse('2024-10-27 02:30', '%Y-%m-%d %H:%M')), time(datetime(2024, 2, 10, 2, 30)), time(datetime(2024, 9, 6, 2, 15))] + ''",
    ),
  'iso-strings-with-and-without-zone': () => ({
    width: W,
    height: 120,
    data: {
      values: [
        { t: '2024-03-10T01:30:00', v: 1 },
        { t: '2024-03-10T03:30:00', v: 2 },
        { t: '2024-03-31T02:30:00Z', v: 3 },
        { t: '2024-10-27T02:30:00+01:00', v: 4 },
        { t: '2024-11-03', v: 5 },
      ],
    },
    mark: 'point',
    encoding: { x: { field: 't', type: 'temporal' }, y: { field: 'v', type: 'quantitative' } },
  }),
  'weeks-binned': () => ({
    width: W,
    height: 120,
    data: { values: hourly(Date.UTC(2024, 2, 1), 24 * 40).filter((_, i) => i % 7 === 0) },
    mark: 'bar',
    encoding: { x: { timeUnit: 'yearweek', field: 't', type: 'ordinal' }, y: { aggregate: 'count', type: 'quantitative' } },
  }),
  'months-across-the-year': () => ({
    width: W,
    height: 120,
    data: { values: hourly(Date.UTC(2024, 0, 1), 24 * 366).filter((_, i) => i % 24 === 0) },
    mark: 'line',
    encoding: { x: { field: 't', type: 'temporal' }, y: { field: 'v', type: 'quantitative' } },
  }),
  'time-unit-default-year': () => ({
    width: W,
    height: 120,
    data: { values: [{ t: '2024-03-31 02:30', v: 1 }, { t: '2024-10-27 02:30', v: 2 }, { t: '2024-11-03 01:30', v: 3 }] },
    mark: 'point',
    encoding: { x: { timeUnit: 'monthdatehoursminutes', field: 't', type: 'temporal' }, y: { field: 'v', type: 'quantitative' } },
  }),
};

export function generate() {
  const cases = [];
  for (const zone of ZONES) {
    const z = zone.replace(/\//g, '_');
    for (const win of WINDOWS) {
      const rows = hourly(win.start);
      for (const [name, chart] of Object.entries(WINDOWED)) {
        cases.push({ name: `${name}--${win.name}--${z}`, family: name, property: zone, lite: true, zone, spec: chart(rows) });
      }
      for (const [name, chart] of Object.entries(VEGA_WINDOWED)) {
        cases.push({ name: `${name}--${win.name}--${z}`, family: name, property: zone, zone, spec: chart(rows) });
      }
    }
    for (const [name, chart] of Object.entries(FIXED)) {
      const spec = chart();
      cases.push({ name: `${name}--${z}`, family: name, property: zone, lite: !spec.marks, zone, spec });
    }
  }
  return { cases, skips: [] };
}
