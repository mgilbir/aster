// Records the builtins differential: every function of the expression
// language called with nominal arguments and with each argument replaced by an
// edge value, the order its arguments are evaluated in (each one wrapped in
// warn()), reads of the members every kind of value inherits, and targeted
// cases; plus which UTF-16 code units the parser takes as identifier
// characters. builtins_test.go replays it.
//
// Record with the pinned node (see docs/testing.md) and the oracle's modules:
//
//   NODE_PATH=testdata/oracle-node/node_modules TZ=UTC \
//     node internal/expr/testdata/gen_builtins.mjs | gzip -9n > internal/expr/testdata/builtins.json.gz
//
// Every case is evaluated alone, in a formula transform over one datum, by
// upstream's View; the warnings it logs from warn(), info() and debug() are
// recorded with its result or its error. Values are encoded in gen_expr.mjs's
// tagged JSON, with functions as {fn: 1} and d3 colours with their class.
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import {fork} from 'node:child_process';
const isMainThread = process.argv[2] !== 'child';
const require = createRequire(path.resolve(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

function enc(v) {
  if (v === null) return null;
  switch (typeof v) {
    case 'boolean': case 'string': return v;
    case 'number':
      if (Object.is(v, -0)) return {n: '-0'};
      if (!Number.isFinite(v)) return {n: String(v)};
      return v;
    case 'undefined': return {u: 1};
    case 'function': return {fn: 1};
    case 'object':
      if (v instanceof Date) return {d: Number.isNaN(v.getTime()) ? 'NaN' : v.getTime()};
      if (v instanceof RegExp) return {re: [v.source, v.flags]};
      if (v instanceof Set) return {set: [...v].map(enc)};
      if (Array.isArray(v)) return Array.from(v, enc);
      const proto = Object.getPrototypeOf(v);
      const colour = proto && proto !== Object.prototype && /^(Rgb|Hsl|Lab|Hcl)$/.test(proto.constructor.name);
      if (proto === Object.prototype || colour) {
        const o = {o: Object.keys(v).map(k => [k, enc(v[k])])};
        if (colour) o.c = proto.constructor.name;
        return o;
      }
      return {x: String(v)};
  }
  return {x: String(v)};
}

const datum = {x: 1, s: 'abc', a: [3, 1, 2], o: {k: 1}, n: null, d: new Date(Date.UTC(2020, 0, 15, 10, 30)), paren: '(', t: 'b'};

// ---- cases ----
const cases = [];
const seen = new Set();
// Results within two units in the last place pass for the functions that
// go through Math's transcendental functions, which V8 computes differently
// on arm64, where the engine's are recorded (internal/jsmath).
const inexact = /\b(sin|cos|tan|asin|acos|atan|atan2|exp|log|pow|hypot|ease\w*|cumulative\w*|density\w*|quantile\w*|sample\w*|pan(Log|Pow|Symlog)|zoom(Log|Pow|Symlog)|luminance|contrast|lab|hcl|hsl|pinchAngle)\(/;
const add = (e, opt = {}) => {
  if (seen.has(e)) return;
  seen.add(e);
  const c = {e, ...opt};
  if (inexact.test(e)) c.tol = 1;
  // The current time.
  if (/\b(datetime|now)\(\)/.test(e)) c.logOnly = 1;
  // pluck memoises its accessors by name in a module-level object, so a
  // case would see the accessor an earlier one made (pluck(x, 0) and
  // pluck(x, '0') share one): each runs in a fresh process.
  if (/\bpluck\(/.test(e)) c.fresh = 1;
  cases.push(c);
};

// Edge values, as expression source. datum.u is undefined (a bare
// `undefined` is a signal reference).
const edges = ['datum.u', 'null', 'true', 'false', '0', '-0', '1', '-1', '2.5', 'NaN', '1/0', '-1/0', "''", "'abc'",
  "'10'", "'9'", "' 2 '", "'0'", "'-1'", "'0x1f'", '[]', '[0]', '[1,2]', "['10','9']", "['b','a']", '[null,2]', '[[1]]',
  '{}', '{a: 1}', "{length: 2, 0: 'x'}", 'datetime(2020, 0, 1)', '/a/', '/,*/g', "rgb('red')", 'datum.s'];

// Nominal arguments of every function the expression language has, without a
// runtime (the scale, geo, view and data-writing functions are only checked
// for argument order). The upstream generator's own forms: clamp reorders its
// arguments, the date and case functions ignore extra ones, test/split/substring
// convert their first argument before they read the rest.
const nominal = {
  abs: ['-2.5'], acos: ['0.5'], asin: ['0.5'], atan: ['0.5'], atan2: ['1', '2'], ceil: ['2.5'], cos: ['1'], exp: ['1'],
  floor: ['2.5'], hypot: ['3', '4'], log: ['2'], max: ['1', '3', '2'], min: ['1', '3', '2'], pow: ['2', '10'],
  round: ['2.5'], sin: ['1'], sqrt: ['2'], tan: ['1'], isNaN: ['NaN'], isFinite: ['1'], clamp: ['5', '1', '3'],
  datetime: ['2020', '1', '15', '10', '30', '45', '500'], utc: ['2020', '1', '15', '10', '30', '45', '500'],
  length: ['datum.a'], parseFloat: ["'3.5px'"], parseInt: ["'ff'", '16'], upper: ["'abc'"], lower: ["'ABC'"],
  substring: ["'abcdef'", '1', '4'], split: ["'a,b,c'", "','", '2'], trim: ["' a '"], btoa: ["'abc'"], atob: ["'YWJj'"],
  encodeURIComponent: ["'a b&c'"], regexp: ["'a+'", "'g'"], test: ['/a+/', "'caat'"], if: ['true', '1', '2'],
  cumulativeNormal: ['0.5', '0', '1'], cumulativeLogNormal: ['0.5', '0', '1'], cumulativeUniform: ['0.5', '0', '1'],
  densityNormal: ['0.5', '0', '1'], densityLogNormal: ['0.5', '0', '1'], densityUniform: ['0.5', '0', '1'],
  quantileNormal: ['0.5', '0', '1'], quantileLogNormal: ['0.5', '0', '1'], quantileUniform: ['0.5', '0', '1'],
  isArray: ['1'], isBoolean: ['1'], isDate: ['1'], isDefined: ['1'], isNumber: ['1'], isObject: ['1'], isRegExp: ['1'],
  isString: ['1'], isTuple: ['datum'], isValid: ['1'], toBoolean: ["'false'"], toDate: ["'2020-01-01'"],
  toNumber: ["'1.5'"], toString: ['1.5'], indexof: ["'abcb'", "'b'", '2'], lastindexof: ["'abcb'", "'b'", '2'],
  join: ['[1,2,3]', "'-'"], replace: ["'abcb'", "'b'", "'x'"], reverse: ['[1,2,3]'], sort: ['[3,1,2]'],
  slice: ['[1,2,3,4]', '1', '3'], flush: ['[0,10]', '2', '3', "'l'", "'r'", "'c'"], lerp: ['[0,10]', '0.5'],
  interpolateLinear: ['[0,10,20]', '0.25'], merge: ['{a: 1}', '{b: 2}'], pad: ["'ab'", '5', "'*'", "'center'"],
  peek: ['[1,2,3]'], pluck: ['[{a: 1}, {a: 2}]', "'a'"], span: ['[1,5]'], inrange: ['5', '[1,10]', 'true', 'false'],
  truncate: ["'abcdef'", '4', "'center'", "'~'"], rgb: ["'red'"], lab: ["'red'"], hcl: ["'red'"], hsl: ["'red'"],
  luminance: ["'red'"], contrast: ["'red'", "'blue'"], sequence: ['1', '10', '3'], format: ['1234.5', "',.2f'"],
  utcFormat: ['datum.d', "'%Y-%m-%d'"], utcParse: ["'2020-01-15'", "'%Y-%m-%d'"], utcOffset: ["'day'", 'datum.d', '2'],
  utcSequence: ["'day'", 'datum.d', 'datetime(2020, 0, 20)', '2'], timeFormat: ['datum.d', "'%Y-%m-%d'"],
  timeParse: ["'2020-01-15'", "'%Y-%m-%d'"], timeOffset: ["'day'", 'datum.d', '2'],
  timeSequence: ["'day'", 'datum.d', 'datetime(2020, 0, 20)', '2'], timeUnitSpecifier: ["['year', 'month']"],
  monthFormat: ['3'], monthAbbrevFormat: ['3'], dayFormat: ['3'], dayAbbrevFormat: ['3'], extent: ['[3,1,2]'],
  clampRange: ['[5,10]', '0', '8'], bandspace: ['3', '0.1', '0.2'],
  pinchDistance: ['{touches: [{clientX: 0, clientY: 0}, {clientX: 3, clientY: 4}]}'],
  pinchAngle: ['{touches: [{clientX: 0, clientY: 0}, {clientX: 3, clientY: 4}]}'],
  panLinear: ['[0,10]', '0.1'], panLog: ['[1,100]', '0.1'], panPow: ['[0,10]', '0.1', '2'], panSymlog: ['[0,10]', '0.1', '1'],
  zoomLinear: ['[0,10]', '5', '2'], zoomLog: ['[1,100]', '10', '2'], zoomPow: ['[0,10]', '5', '2', '2'],
  zoomSymlog: ['[0,10]', '5', '2', '1'], lassoAppend: ['[[0,0]]', '3', '4', '1'], lassoPath: ['[[0,0],[1,1]]'],
  warn: ["'w'", '1'], info: ["'i'", '1'], debug: ["'d'", '1'],
  data: ["'tbl'"], indata: ["'tbl'", "'a'", '2'],
};
for (const name of ['date', 'day', 'year', 'month', 'hours', 'minutes', 'seconds', 'milliseconds', 'time', 'timezoneoffset',
  'utcdate', 'utcday', 'utcyear', 'utcmonth', 'utchours', 'utcminutes', 'utcseconds', 'utcmilliseconds', 'quarter',
  'utcquarter', 'week', 'utcweek', 'dayofyear', 'utcdayofyear', 'isoweek', 'utcisoweek']) nominal[name] = ['datum.d'];
const functions = JSON.parse(fs.readFileSync(new URL('./functions.json', import.meta.url))).functions;
for (const f of functions) if (f.startsWith('ease')) nominal[f] = ['0.3'];

// Functions whose result depends on the runtime or a clock: their argument
// order is checked, their result is not.
const orderOnly = {
  now: [], random: [], sampleNormal: ['0', '1'], sampleLogNormal: ['0', '1'], sampleUniform: ['0', '1'],
  scale: ["'nosuch'", '1'], invert: ["'nosuch'", '1'], domain: ["'nosuch'"], range: ["'nosuch'"],
  bandwidth: ["'nosuch'"], copy: ["'nosuch'"], gradient: ["'nosuch'", '[0,0]', '[1,0]', '3'],
  _scale: ["'nosuch'", '1'], _range: ["'nosuch'"], _bandwidth: ["'nosuch'"],
  geoArea: ["'p'", "{type: 'Point', coordinates: [0, 0]}"], geoBounds: ["'p'", "{type: 'Point', coordinates: [0, 0]}"],
  geoCentroid: ["'p'", "{type: 'Point', coordinates: [0, 0]}"], geoShape: ["'p'", '{}'], geoScale: ["'p'"],
  geoTranslate: ["'p'"], pathShape: ["'M0,0'"], containerSize: [], windowSize: [], screen: [],
  encode: ['datum', "'x'", 'true'], inScope: ['datum'], intersect: ['[[0,0],[1,1]]'], intersectLasso: ["'m'", '[[0,0]]'],
  modify: ["'tbl'", 'null', 'null'], setdata: ["'tbl'", '[]'], treeAncestors: ["'tbl'", 'datum'],
  treePath: ["'tbl'", 'datum', 'datum'], vlSelectionTest: ["'tbl'", 'datum'], vlSelectionIdTest: ["'tbl'", 'datum'],
  vlSelectionResolve: ["'tbl'"], vlSelectionTuples: ['[]', '{}'],
  view: [], item: [], group: [], xy: [], x: [], y: [],
};
// Data-dependent functions name their dataset with a literal, which is not
// wrapped.
const literalFirst = new Set(['data', 'indata', 'scale', 'invert', 'domain', 'range', 'bandwidth', 'copy', 'gradient',
  '_scale', '_range', '_bandwidth', 'geoArea', 'geoBounds', 'geoCentroid', 'geoShape', 'geoScale', 'geoTranslate',
  'modify', 'setdata', 'treeAncestors', 'treePath', 'vlSelectionTest', 'vlSelectionIdTest', 'vlSelectionResolve']);

for (const [f, args] of Object.entries(nominal)) {
  if (!functions.includes(f)) throw new Error('not a function: ' + f);
  add(`${f}(${args.join(', ')})`);
  for (let k = 0; k < args.length; k++) add(`${f}(${args.slice(0, k).join(', ')})`);
  if (f === 'if' || f === 'warn' || f === 'info' || f === 'debug') continue;
  for (let i = literalFirst.has(f) ? 1 : 0; i < args.length; i++) {
    for (const e of edges) add(`${f}(${args.map((a, j) => j === i ? e : a).join(', ')})`);
  }
}
// Argument order: every argument wrapped in warn(), and an extra one.
const order = (f, args, opt) => {
  const w = (a, i) => literalFirst.has(f) && i === 0 ? a : `warn('a${i}', ${a})`;
  add(`${f}(${args.map(w).join(', ')})`, opt);
  add(`${f}(${[...args, '0'].map(w).join(', ')})`, opt);
};
for (const [f, args] of Object.entries(nominal)) order(f, args);
for (const [f, args] of Object.entries(orderOnly)) order(f, args, {logOnly: 1});
for (const f of ['rgb', 'hsl', 'lab', 'hcl']) order(f, ['10', '20', '30', '0.5']);
order('test', ["datum.paren", "'x'"]);
order('test', ["'('", "'x'"]);
order('split', ["datum.o", "','"]);

// Operators and literals evaluate left to right.
for (const e of [
  "warn('a', 1) + warn('b', 2)", "warn('a', 'x') + warn('b', 2) + warn('c', 3)", "[warn('a', 1), warn('b', 2)]",
  "{x: warn('a', 1), 1: warn('b', 2), y: warn('c', 3)}", "warn('o', datum.o)[warn('k', 'k')]",
  "warn('c', true) ? warn('t', 1) : warn('f', 2)", "warn('c', 0) ? warn('t', 1) : warn('f', 2)",
  "warn('l', 0) || warn('r', 1)", "warn('l', 1) && warn('r', 0)", "-warn('u', 1)", "!warn('u', 1)",
  "warn('a', 'k') in warn('b', {})", "warn('a', 1) < warn('b', 2)", "warn('a', 'x') == warn('b', 'x')",
  "warn('a', 1) - warn('b', 2) * warn('c', 3)", "if(warn('c', 1), warn('t', 2), warn('f', 3))",
  "warn('a', datum.a)[warn('i', 1)]", "warn(warn('in', 1), warn('in2', 2))", "info('i', debug('d', warn('w', 1)))",
  "warn()", "warn('x')", "warn(1, 'a', null, datum.u, [1, 'b'], {k: 2})",
]) add(e);

// Arguments upstream does not generate are not compiled either.
for (const e of ["upper('a', nosuch())", "lower('A', nosuch(), 1)", "trim(' a ', nosuch())", "length([1], nosuch())",
  "year(datum.d, nosuch())", "utcmonth(datum.d, nosuch())", "time(datum.d, nosuch())", "upper('a', datum.u.x)",
  "length(datum.a, datum.u.x)", "substring('abc', 1, 2, nosuch())", "upper()", "length()"]) add(e);
for (const e of ["pluck([{a: 1}], 'a[')", "pluck([{a: 1}], ']')", "pluck([{a: 1}], ['a', 'b'])", "pluck([{'a,': 1}], ['a', 'b'])",
  "pluck([{a: {b: 2}}], 'a.b')", "pluck({a: 1}, 'a')", "pluck([{a: 1}], 1.5)", "pluck([{a: 1}], {length: 1.5, 0: 'a'})",
  "indata('tbl', 'a', '1')", "indata('tbl', 'b', ['x'])", "indata('tbl', 'a', datum.u)"]) add(e);
// Only rgb(), hsl(), lab() and hcl() make colours; an object of the same
// shape is a plain object.
for (const e of ["'' + {r: 1, g: 2, b: 3, opacity: 1}", "rgb({r: 1, g: 2, b: 3, opacity: 1})", "'brighter' in {r: 1, g: 2, b: 3, opacity: 1}",
  "{r: 1, g: 2, b: 3, opacity: 1}.brighter", "'' + rgb('red')", "'' + hsl('red')", "'' + lab('red')", "'' + hcl('red')",
  "rgb(hsl('red'))", "hsl(rgb(1, 2, 3))", "'' + merge(rgb('red'))", "merge(hsl('red'))", "merge(lab('red'), hcl('red'))",
  "luminance({r: 255, g: 0, b: 0, opacity: 1})", "contrast(rgb('red'), {r: 0, g: 0, b: 255, opacity: 1})"]) add(e);

// C38: String.prototype.split with a pattern stops at limit pieces.
for (const str of ["'abc'", "'a,b,,c'", "'2020-01-05'", "''", "'a1b22c333'", "'héllo'"]) {
  for (const re of ["regexp('')", '/,*/', '/-?/', '/(?:)/', '/(,)/', '/\\d*/', '/\\d+/', "','", "''", '/(-)?/', '/x*/y']) {
    for (const lim of ['', ', 0', ', 1', ', 2', ', 3', ', 4', ', 10', ', -1', ', 4294967297', ', NaN', ", '2'", ', datum.u']) {
      add(`split(${str}, ${re}${lim})`);
    }
  }
}
// C104: parseInt in every radix, past 2^53.
for (let r = 2; r <= 36; r++) {
  for (const str of ['1'.repeat(60), '9'.repeat(30), 'z'.repeat(20), '123456789abcdefghijklmnopqrstuvwxyz'.repeat(2), '1'.repeat(30) + '.5', '-' + '7'.repeat(40), '10000000000000000000001', '0x' + 'f'.repeat(20)]) {
    add(`parseInt('${str}', ${r})`);
  }
}
for (const e of ["parseInt('0x' + 'f'.repeat(1))", "parseInt('" + 'f'.repeat(20) + "')", "parseInt('" + '1'.repeat(400) + "', 10)", "parseInt('" + '1'.repeat(400) + "', 7)", "parseInt('1e3')", "parseInt(' 12abc', 0)", "parseInt('12', 1)", "parseInt('12', 37)", "parseInt('-0')", "parseInt('" + '9'.repeat(320) + "')"]) {
  if (!e.includes('.repeat')) add(e);
}
// C98: a string literal is never a signal reference.
for (const w of ['then', 'if', 'constructor', 'toString', 'valueOf', '__proto__', 'hasOwnProperty', 'datum', 'event', 'NaN', 'PI', 'null', 'true']) {
  add(`'${w}' + 'x'`);
  add(`'${w}'`);
  add(`["${w}"]`);
}
// C101: comparisons of the raw values where upstream compares them raw.
for (const e of [
  "flush(['10','9'], 9.1, 0.5, 'left', 'right', 'center')", "flush(['9','10'], 9.1, 0.5, 'l', 'r', 'c')",
  "flush(['a','b'], 1, 1, 'l', 'r', 'c')", "flush([datetime(2020,0,1), datetime(2020,0,2)], 0, 1e20, 'l', 'r', 'c')",
  "clampRange(['10','9'], 0, 100)", "clampRange(['1','2'], '0', '5')", "clampRange(['1','2'], '0', '1')",
  "clampRange([5, 10], '0', '3')", "clampRange(['b','a'], 0, 1)", "clampRange([datum.u, 2], 0, 10)",
  "bandspace('0')", "bandspace('0', 0, 0)", "bandspace('', 1, 1)", "bandspace([], 0, 0)", "bandspace(3, '0', '0')",
  "densityUniform(0.5, '1', '0')", "densityUniform(0.5, '10', '9')", "cumulativeUniform(0.5, '10', '9')",
  "cumulativeUniform(9.5, '10', '9')", "quantileUniform(0.5, '10', '9')", "densityUniform(0.5, 1, 0)",
  "densityUniform(9.5, '9', '10')", "cumulativeUniform(5, 'a', 'b')", "inrange('5', ['10', '9'])", "inrange(5, ['10', '9'])",
  "lerp(['1', '3'], 0.5)", "lerp(['1', '3'], 0)", "lerp(['1', '3'], 1)", "span(['1', '3'])", "extent(['10', '9', '1'])",
]) add(e);

// C156: the members every kind of value inherits.
const holders = {
  'datum.a': datum.a, 'datum.s': datum.s, 'datum.x': datum.x, 'true': true, 'datum.d': datum.d, '/a/': /a/,
  "rgb('red')": vega.expressionFunction('rgb')('red'), "hsl('red')": vega.expressionFunction('hsl')('red'),
  "lab('red')": vega.expressionFunction('lab')('red'), "hcl('red')": vega.expressionFunction('hcl')('red'),
  'datum.o': datum.o, '{}': {}, '[]': [], "''": '',
};
const namesOf = v => {
  const names = new Set();
  for (let o = Object(v); o; o = Object.getPrototypeOf(o)) for (const k of Object.getOwnPropertyNames(o)) names.add(k);
  return [...names].sort();
};
const colour = e => /^(rgb|hsl|lab|hcl)\(/.test(e);
for (const [src, v] of Object.entries(holders)) {
  for (const k of namesOf(v)) {
    // __proto__ reads the prototype object itself, which is not modelled.
    if (!/^[A-Za-z_$][\w$]*$/.test(k) || k === '__proto__') continue;
    add(`${src}.${k}`);
    add(`${src}['${k}']`);
    if (typeof v === 'object') add(`'${k}' in ${src}`);
    // A colour's methods are d3's own functions, whose text is their source.
    if (!colour(src)) add(`'' + ${src}.${k}`);
  }
  for (const k of ['nosuch', 'then', '0', '1', 'length']) {
    add(`${src}['${k}']`);
    if (typeof v === 'object') add(`'${k}' in ${src}`);
  }
}

// ---- identifier characters (C105) ----
// The parser tests each UTF-16 code unit against its own tables: a code unit
// is an identifier start (part) when it alone (after `a`) parses as one
// identifier, written raw or as a \u escape.
function idOK(src, name) {
  try {
    const ast = vega.parseExpression(src);
    return ast.type === 'Identifier' && ast.name === name;
  } catch (e) { return false; }
}
const ranges = test => {
  const out = [];
  for (let c = 0x80; c <= 0xFFFF; c++) {
    if (!test(c)) continue;
    const last = out[out.length - 1];
    if (last && last[1] === c - 1) last[1] = c; else out.push([c, c]);
  }
  return out;
};
const hex = c => c.toString(16).padStart(4, '0');
const idents = !isMainThread ? null : {
  start: ranges(c => idOK(String.fromCharCode(c), String.fromCharCode(c))),
  part: ranges(c => idOK('a' + String.fromCharCode(c), 'a' + String.fromCharCode(c))),
  startEscaped: ranges(c => idOK('\\u' + hex(c), String.fromCharCode(c))),
  partEscaped: ranges(c => idOK('a\\u' + hex(c), 'a' + String.fromCharCode(c))),
  // Characters outside the Basic Multilingual Plane: two code units, the
  // first a high surrogate.
  astral: [0x1D465, 0x10400, 0x20000, 0x1F600, 0x1E900, 0x16E40].map(c => [c, idOK(String.fromCodePoint(c), String.fromCodePoint(c)), idOK('a' + String.fromCodePoint(c), 'a' + String.fromCodePoint(c))]),
};

// ---- evaluation ----
// Cases run in a child process with a bounded heap: a case upstream cannot
// finish (pad('ab', 1/0) builds a string until the heap is exhausted) kills it, and
// is recorded as unbounded, which the engine must refuse with an error.
const tbl = [{a: 1, b: 'x'}, {a: 2, b: 'y'}];
async function evalOne(c) {
  const rec = {e: c.e};
  if (c.logOnly) rec.logOnly = 1;
  if (c.tol) rec.tol = 1;
  const log = [], errs = [];
  const capture = level => (...a) => { if (a[0] === 'EXPRESSION') log.push([level, ...a.slice(1).map(enc)]); };
  const logger = {level() { return this; }, error(...a) { errs.push(a.map(x => String(x && x.message || x)).join(' ')); return this; },
    warn: capture('warn'), info: capture('info'), debug: capture('debug')};
  let view;
  try {
    const spec = {data: [{name: 'tbl', values: tbl}, {name: 'd', values: [structuredClone(datum)], transform: [{type: 'formula', expr: c.e, as: 'r'}]}]};
    view = new vega.View(vega.parse(spec), {renderer: 'none', logger});
  } catch (err) {
    rec.error = 'parse: ' + String(err.message || err);
    return rec;
  }
  vega.setRandom(vega.randomLCG(1));
  await view.runAsync();
  if (errs.length) rec.error = errs.join(' | ');
  else if (!c.logOnly) rec.result = enc(view.data('d')[0].r); // a clock's or the runtime's: not compared, nor recorded
  if (log.length) rec.log = log;
  view.finalize();
  return rec;
}

if (!isMainThread) {
  const send = m => new Promise(resolve => process.send(m, resolve));
  for (let i = Number(process.argv[3]); i < Number(process.argv[4]); i++) {
    await send({i, start: true});
    await send({i, rec: await evalOne(cases[i])});
  }
  process.disconnect();
} else {
  const out = {datum: enc(datum), tbl: tbl.map(enc), idents, cases: new Array(cases.length)};
  let from = 0;
  while (from < cases.length) {
    let current = from;
    let to = from + 1;
    if (!cases[from].fresh) while (to < cases.length && !cases[to].fresh) to++;
    await new Promise(resolve => {
      const w = fork(new URL(import.meta.url), ['child', String(from), String(to)], {execArgv: ['--max-old-space-size=1024'], stdio: ['ignore', 'ignore', 'ignore', 'ipc']});
      let timer = null;
      const arm = () => { clearTimeout(timer); timer = setTimeout(() => w.kill('SIGKILL'), 20000); };
      w.on('message', m => {
        if (m.start) { current = m.i; arm(); if (process.env.GEN_DEBUG) process.stderr.write(cases[m.i].e + '\n'); return; }
        out.cases[m.i] = m.rec;
      });
      w.on('error', () => {});
      w.on('exit', () => {
        clearTimeout(timer);
        if (out.cases[current] === undefined) {
          out.cases[current] = {e: cases[current].e, unbounded: 1};
          from = current + 1;
        } else {
          from = to;
        }
        resolve();
      });
      arm();
    });
  }
  process.stdout.write(JSON.stringify(out) + '\n');
}
