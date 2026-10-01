// The locale sweep: every number and time locale d3 ships, through the parts
// of a chart that format or parse with one.
//
// A Vega or Vega-Lite specification sets its locale in config.locale (a d3
// number locale and a d3 time locale); every other comparison uses the
// default, en-US. This renders, for each of d3-format's locale definitions,
// charts whose axis, legend and text format numbers (grouping, decimal mark,
// currency, SI prefixes, percentages, signs, the accounting form), and for
// each of d3-time-format's, charts that format and parse dates (month and day
// names, the locale's date and time patterns, AM/PM, a time axis's default
// multi-format labels, parsing a date written with the locale's month name).

import fs from 'node:fs';
import path from 'node:path';

function locales(ctx, pkg) {
  // The package's exports hide its files, so the directory is read by path.
  const dir = path.join(ctx.repo, 'testdata/oracle-node/node_modules', pkg, 'locale');
  return fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.json'))
    .sort()
    .map((f) => ({ name: f.slice(0, -'.json'.length), def: JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8')) }));
}

const NUMBERS = [-1234567.891, -0.5, 0, 0.000123, 0.25, 7, 1234.5, 98765432.1];

const NUMBER_SPECIFIERS = [',.2f', '$,.2f', '.3s', ',d', '+,.1%', '.2e', '($,.0f', '~g', ',.0f'];

// Text marks, one per (value, specifier).
function formatText(locale) {
  const rows = [];
  for (const v of NUMBERS) for (const f of NUMBER_SPECIFIERS) rows.push({ v, f });
  return {
    width: 420,
    height: rows.length * 13,
    config: { locale: { number: locale } },
    data: [{ name: 't', values: rows.map((r, i) => ({ ...r, i })) }],
    marks: [
      {
        type: 'text',
        from: { data: 't' },
        encode: { update: { x: { value: 4 }, y: { signal: 'datum.i * 13 + 11' }, text: { signal: "datum.f + ' ' + format(datum.v, datum.f)" } } },
      },
    ],
  };
}

const NUMBER_CHARTS = {
  'number-format-text': (number) => ({ lite: false, spec: formatText(number) }),
  'number-axis-format': (number) => ({
    lite: true,
    spec: {
      width: 300,
      height: 160,
      config: { locale: { number } },
      data: { values: NUMBERS.map((v, i) => ({ c: `c${i}`, v })) },
      mark: 'bar',
      encoding: { x: { field: 'c', type: 'nominal' }, y: { field: 'v', type: 'quantitative', axis: { format: '$,.2f' } } },
    },
  }),
  'number-axis-default': (number) => ({
    lite: true,
    spec: {
      width: 300,
      height: 160,
      config: { locale: { number } },
      data: { values: [{ x: 0, y: 1200000 }, { x: 1, y: 3450000.5 }, { x: 2, y: 9999999 }] },
      mark: 'line',
      encoding: { x: { field: 'x', type: 'quantitative' }, y: { field: 'y', type: 'quantitative' } },
    },
  }),
  'number-legend': (number) => ({
    lite: true,
    spec: {
      width: 200,
      height: 120,
      config: { locale: { number } },
      data: { values: NUMBERS.map((v, i) => ({ x: i, v })) },
      mark: 'point',
      encoding: { x: { field: 'x', type: 'quantitative' }, color: { field: 'v', type: 'quantitative', legend: { format: ',.1f' } } },
    },
  }),
};

const DATES = [Date.UTC(2024, 0, 1, 0, 0), Date.UTC(2024, 1, 29, 13, 5, 9), Date.UTC(2024, 6, 14, 23, 59, 59), Date.UTC(2024, 11, 31, 12, 0)];

const TIME_SPECIFIERS = ['%A %d %B %Y', '%a %b %e', '%c', '%x', '%X', '%I:%M %p', '%B', '%b %Y'];

const TIME_CHARTS = {
  'time-format-text': (time) => {
    const rows = [];
    for (const t of DATES) for (const f of TIME_SPECIFIERS) rows.push({ t, f });
    return {
      lite: false,
      spec: {
        width: 420,
        height: rows.length * 13,
        config: { locale: { time } },
        data: [{ name: 't', values: rows.map((r, i) => ({ ...r, i })) }],
        marks: [
          {
            type: 'text',
            from: { data: 't' },
            encode: { update: { x: { value: 4 }, y: { signal: 'datum.i * 13 + 11' }, text: { signal: "datum.f + ' ' + timeFormat(datum.t, datum.f)" } } },
          },
        ],
      },
    };
  },
  'time-axis-default': (time) => ({
    lite: true,
    spec: {
      width: 420,
      height: 100,
      config: { locale: { time } },
      data: { values: DATES.map((t, i) => ({ t, v: i })) },
      mark: 'line',
      encoding: { x: { field: 't', type: 'temporal' }, y: { field: 'v', type: 'quantitative' } },
    },
  }),
  'time-parse-month-names': (time) => {
    // Dates written with the locale's own month names and parsed back.
    const rows = time.months.map((m, i) => ({ s: `${i + 1} ${m} 2024`, i }));
    return {
      lite: false,
      spec: {
        width: 420,
        height: rows.length * 13,
        config: { locale: { time } },
        data: [{ name: 't', values: rows }],
        marks: [
          {
            type: 'text',
            from: { data: 't' },
            encode: { update: { x: { value: 4 }, y: { signal: 'datum.i * 13 + 11' }, text: { signal: "datum.s + ' ' + time(timeParse(datum.s, '%e %B %Y'))" } } },
          },
        ],
      },
    };
  },
  'time-month-units': (time) => ({
    lite: true,
    spec: {
      width: 420,
      height: 120,
      config: { locale: { time } },
      data: { values: DATES.map((t, i) => ({ t, v: i + 1 })) },
      mark: 'bar',
      encoding: { x: { timeUnit: 'month', field: 't', type: 'ordinal' }, y: { field: 'v', type: 'quantitative' } },
    },
  }),
};

export function generate(ctx) {
  const cases = [];
  for (const { name, def } of locales(ctx, 'd3-format')) {
    for (const [chart, make] of Object.entries(NUMBER_CHARTS)) {
      const { lite, spec } = make(def);
      cases.push({ name: `${chart}--${name}`, family: chart, property: name, lite, spec });
    }
  }
  for (const { name, def } of locales(ctx, 'd3-time-format')) {
    for (const [chart, make] of Object.entries(TIME_CHARTS)) {
      const { lite, spec } = make(def);
      cases.push({ name: `${chart}--${name}`, family: chart, property: name, lite, spec });
    }
  }
  return { cases, skips: [] };
}
