// Records how d3-format and d3-time-format read a locale definition whose
// members are not the types their documentation names: JavaScript reads them
// with duck typing (Array.prototype.map.call over any array-like, s[0] of a
// string, a pattern's length and charCodeAt), so a string grouping groups,
// a string currency is indexed, and a time locale without its %c/%x/%X
// patterns fails only when one is used.
//   NODE_PATH=testdata/oracle-node/node_modules node testdata/gen_duck.mjs | gzip -n -9 > testdata/duck.json.gz
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const d3f = await import(require.resolve('d3-format'));
const d3t = await import(require.resolve('d3-time-format'));

const numberLocales = [
  '{"decimal":".","thousands":",","grouping":"3","currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":"32","currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":"3.5","currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":3,"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":true,"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":{"length":2,"0":3,"1":2},"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":{"length":"2","0":"3","1":[2]},"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":{"0":3},"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":{"length":1e300,"0":3,"1":1},"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[2.5],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[1.5,3],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[3.9999],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[1e300],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[1e20],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[3,0,0.5],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[3,"x",2],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[[2],"1"],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[],"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":null,"currency":["$",""]}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":"€"}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":""}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":"😀x"}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":5}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":{"0":"a","1":"b"}}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["€"]}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":[["a","b"],null]}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":null}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":"0123456789"}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":"٠١٢٣٤٥٦٧٨٩"}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":"0123"}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":"𝟎𝟏"}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":{"length":10,"0":"a","1":"b","9":"j"}}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":7}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":[0,1,2,3,4,5,6,7,8,9]}',
  '{"decimal":".","thousands":",","grouping":[3],"currency":["$",""],"numerals":null}',
  '{"decimal":[1,2],"thousands":5,"grouping":[3],"currency":["$",""]}',
  '{"decimal":null,"thousands":null,"grouping":[3],"currency":["$",""],"percent":["%%"],"minus":0,"nan":false}',
  '{"decimal":".","thousands":"😀","grouping":[3],"currency":["$",""],"nan":"😀NaNé"}',
];
const numberSpecs = ['$,.2f', ',d', '012,d', '020,.1f', ',.2%', ',', '$,', ',.3s', ',c'];
const numberValues = [1234567.891, -1234567.891, 1234567, 12, 0, NaN, 1e21];

const out = { node: process.version, numberSpecs, numberValues: numberValues.map(String), number: [], time: [] };
for (const src of numberLocales) {
  let loc;
  try {
    loc = d3f.formatLocale(JSON.parse(src));
  } catch (e) {
    out.number.push({ locale: src, error: String(e) });
    continue;
  }
  const rows = [];
  for (const spec of numberSpecs) {
    const f = loc.format(spec);
    rows.push({ spec, out: numberValues.map((v) => f(v)) });
  }
  out.number.push({ locale: src, rows });
}

const names = '"periods":["AM","PM"],"days":["Sunday","Monday","Tuesday","Wednesday","Thursday","Friday","Saturday"],"shortDays":["Sun","Mon","Tue","Wed","Thu","Fri","Sat"],"months":["January","February","March","April","May","June","July","August","September","October","November","December"],"shortMonths":["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"]';
const timeLocales = [
  `{${names}}`,
  `{"dateTime":"%x %X","date":"%d/%m/%Y","time":"%H:%M",${names}}`,
  `{"dateTime":5,"date":true,"time":{},${names}}`,
  `{"dateTime":[],"date":["%Y"],"time":null,${names}}`,
  `{"dateTime":{"length":1},"date":{"length":0},"time":{"length":"2"},${names}}`,
  `{"dateTime":"","date":"%Y","time":"",${names}}`,
  `{"dateTime":"%c","date":"%x","time":"%X",${names}}`,
  `{"dateTime":"%x","date":"%c","time":"%H",${names}}`,
  `{"dateTime":"%x, %X",${names.replace('"periods":["AM","PM"]', '"periods":"AP"')}}`,
  `{"dateTime":"%x, %X","date":"%Y","time":"%H",${names.replace('"periods":["AM","PM"]', '"periods":[1,2]')}}`,
  `{"dateTime":"%x, %X","date":"%Y","time":"%H",${names.replace(',"shortMonths":["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"]', '')}}`,
];
const timeSpecs = ['%B %Y', '%a %d %b %p', '%c', '%x', '%X', '%Y %c', '%-c', ''];
const parseCases = [
  ['%B %Y', 'March 2020'], ['%c', ''], ['%c', '2020'], ['%c%Y', '2020'], ['%Y%c', '2020'], ['%Y%c', '2020x'],
  ['%x', '05/03/2020'], ['%X', '10:30'], ['%c', '05/03/2020 10:30'], ['%Y%x', '2020'], ['%Y%X', '2020'],
  ['%Y %X', '2020'], ['x%c', 'y'],
];
const t = Date.UTC(2020, 2, 5, 14, 30, 15);
out.timeSpecs = timeSpecs;
out.t = t;
for (const src of timeLocales) {
  let loc;
  try {
    loc = d3t.timeFormatLocale(JSON.parse(src));
  } catch (e) {
    out.time.push({ locale: src, error: String(e) });
    continue;
  }
  const fmt = timeSpecs.map((spec) => {
    try {
      return { spec, out: loc.utcFormat(spec)(t) };
    } catch (e) {
      return { spec, error: String(e) };
    }
  });
  const parse = parseCases.map(([spec, s]) => {
    try {
      const d = loc.utcParse(spec)(s);
      return { spec, s, out: d === null ? null : +d };
    } catch (e) {
      return { spec, s, error: String(e) };
    }
  });
  out.time.push({ locale: src, fmt, parse });
}
process.stdout.write(JSON.stringify(out));
