// Records d3-format golden vectors.
//   NODE_PATH=purego/testdata/oracle-node/node_modules node testdata/gen_number.mjs | gzip -9 > testdata/number.json.gz
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const d3 = await import(require.resolve('d3-format'));

const enc = (v) => (Object.is(v, -0) ? '-0' : String(v));
const values = [0, -0, 1, -1, 0.5, -0.5, 1.5, 2.5, -2.5, 0.05, 0.005, 123.456, -123.456, 1234.5678,
  1e6, 1234567.891, -1234567.891, 1e9, 1e12, 1e21, 1e-7, 1e-10, 0.000123456, 0.1 + 0.2, 1 / 3, NaN,
  Infinity, -Infinity, 5e-324, 1.7976931348623157e308, 999.5, 999999.5, 0.99999, 12345678901234567890,
  42, 100, 0.045, 1e-24, 1.5e-25, 1e-30, 1e24, 1e27, 3e-9, 0.0001234, 123456789, 65536, 255.5, -255.5,
  4.35, 1.005, 2.675, 8.345, 1e100, 1.2345678901234567e25, 2 ** 80, -0.0004, 0.0004, 9.995, 99.995,
  0.9995, 1e-6, 12345.6789, 999999, 1000, 1024, -1e21, 7, -7, 0.1, 15, 25, 35, 0.15, 0.25, 0.35, 1e20,
  4503599627370497.5, 9007199254740993, 1234567890123456789012];
const specs = ['', ',', 'd', ',d', '.0f', '.1f', '.2f', '.3f', ',.2f', '+.2f', '(.2f', ' .2f', '-.2f', '$,.2f', '$.0f',
  '$,.0f', '$', '#', '08.2f', '010,.2f', '030,.2f', '*>10.2f', '<10.2f', '^10.2f', '>10.2f', '=10.2f',
  '0=10.2f', '_<10', '^^11,.1f', '(,.2f', '+(.2f', '.2%', '.0%', '+.1%', '.3~%', '%', '.2e', '.0e', 'e', '.3~e',
  '.10e', '.3g', 'g', '.6g', '.21g', '.12~g', '.1g', 'r', '.3r', '.5r', '~r', '.10r', 's', '.3s', '.2s', '~s',
  '.0s', '.1s', '$.2s', ',s', '.6s', '.21s', '08s', 'p', '.2p', '.0p', '~p', '.4p', 'b', 'o', 'x', 'X', '#x',
  '#b', '#o', '#X', '08b', '010x', ',x', 'c', '10c', 'n', '.3n', ',n', '~f', '.3~f', '.2~f', '~e', '~', '.2~',
  '~%', 'z', 'E', 'F', '.3E', '10', '10,', '05', '0', '020,d', '+', '-', '( ', '>', '<', '^', '=', '_>10,d',
  '$010,.2f', '(010,.2f', '+010.2f', ',.0f', ',.3f', '+,.0f', '.20f', '.25f', '.100f', '.0d', '.3d', '20', '.2', '012',
  '+$,.2f', '($,.2f', ' $,.0f', '#s', '#.3s', '$~s', '~,f', '.1~f', '0,', '05,', '04,d', '011,d', '013,.1f', '015,d',
  '>8,d', '<8,d', '^8,d'];
const invalid = ['.', 'abc', '%%', '..2f', '2.f', ',,', '#$', '.f.', 'f.2', '~~', '10.2.f', '\n<', ' ff', '$$'];

const locales = {
  default: null,
  de: { decimal: ',', thousands: '.', grouping: [3], currency: ['', ' €'] },
  fr: { decimal: ',', thousands: ' ', grouping: [3], currency: ['', ' €'], percent: ' %' },
  in: { decimal: '.', thousands: ',', grouping: [3, 2], currency: ['₹', ''] },
  ar: { decimal: '٫', thousands: '٬', grouping: [3], currency: ['', ' د.إ'], numerals: ['٠', '١', '٢', '٣', '٤', '٥', '٦', '٧', '٨', '٩'], minus: '−', nan: 'ليس رقم' },
  ascii: { decimal: '.', thousands: ',', grouping: [3], currency: ['$', ''], minus: '-', nan: 'n/a' },
  mixed: { decimal: '.', thousands: "'", grouping: [1, 2, 3], currency: ['CHF ', ''] },
  bare: { decimal: ',' },
  nogroup: { thousands: ',' },
  emptygroup: { thousands: ',', grouping: [] },
  shortnum: { numerals: ['a', 'b'], thousands: ',', grouping: [3] },
};
const mk = (name) => (locales[name] ? d3.formatLocale(locales[name]) : { format: d3.format, formatPrefix: d3.formatPrefix });
const run = (fn, v) => { try { return fn(v); } catch (e) { return null; } };

const out = { values: values.map(enc), locales, cases: [], invalid: {}, prefix: [], precision: [] };
for (const [name] of Object.entries(locales)) {
  const L = mk(name);
  const list = name === 'default' ? specs : specs.filter((_, i) => i % 3 === 0 || ['', ',', ',d', '$,.2f', '.2s', '(,.2f', '010,.2f', '+.1%', '.3g', 'n'].includes(specs[i]));
  for (const spec of list) {
    let f;
    try { f = L.format(spec); } catch (e) { out.invalid[name + '|' + spec] = true; continue; }
    out.cases.push({ locale: name, spec, out: values.map((v) => f(v)) });
  }
}
for (const spec of invalid) {
  try { d3.format(spec); out.invalid['default|' + spec] = false; } catch (e) { out.invalid['default|' + spec] = true; }
}
const pvals = [0, 1, 1234, 0.00123, 5e6, 1e-9, 2.5e10, 1e30, NaN, 1e24, 1e-25, 999.9, -4321, 1e-30, 8e21];
const pin = [0, 1, 1.5, 12345, 0.000123, 2.5e6, -3.2e-7, 4e10, 1e-9, 123456789, NaN, 6e27];
for (const name of ['default', 'de', 'ar']) {
  const L = mk(name);
  for (const spec of ['.2', '.0', ',.1', '$.2', '', '+.3', '08', '~', '.3~']) {
    for (const rv of pvals) {
      let f; try { f = L.formatPrefix(spec, rv); } catch (e) { continue; }
      out.prefix.push({ locale: name, spec, ref: enc(rv), out: pin.map((v) => f(v)) });
    }
  }
}
const steps = [0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10, 25, 100, 1000, 1e6, 1e-9, 0, NaN, -0.2, 3e-5, 1e30];
const maxes = [0, 1, 5, 12, 100, 1234, 1e5, 3e6, 1e9, 0.01, 1e-4, 1e-10, 1e12, 5e30];
for (const s of steps) {
  out.precision.push({ fn: 'fixed', a: enc(s), out: d3.precisionFixed(s) });
  for (const m of maxes) {
    out.precision.push({ fn: 'prefix', a: enc(s), b: enc(m), out: d3.precisionPrefix(s, m) });
    out.precision.push({ fn: 'round', a: enc(s), b: enc(m), out: d3.precisionRound(s, m) });
  }
}
out.precision.forEach((p) => { if (typeof p.out === 'number' && !Number.isFinite(p.out)) p.out = String(p.out); });
console.log(JSON.stringify(out));
