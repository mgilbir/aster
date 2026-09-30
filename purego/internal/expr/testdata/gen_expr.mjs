// Records golden vectors for the expression package from upstream's real
// expression evaluator (vega-parser + vega-functions code generation, run
// through a View).
//
//   NODE_PATH=purego/testdata/oracle-node/node_modules TZ=UTC \
//     node testdata/gen_expr.mjs > testdata/expr_utc.json
//   NODE_PATH=... TZ=America/New_York node testdata/gen_expr.mjs dates > testdata/expr_ny.json
//
// Every case is an expression evaluated in a formula transform over a fixed set
// of datums (with fixed signals), so that datum, signal and constant
// expressions all go through the generated function unchanged. Results are
// encoded with a tagged JSON form (see enc) that keeps NaN, undefined, -0,
// dates and regular expressions.
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));

const mode = process.argv[2] || 'all';

// ---- tagged JSON ----
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
      if (Object.getPrototypeOf(v) === Object.prototype || v.constructor && /^(Rgb|Hsl|Lab|Hcl)$/.test(v.constructor.name)) {
        return {o: Object.keys(v).map(k => [k, enc(v[k])]), c: v.constructor.name === 'Object' ? undefined : v.constructor.name};
      }
      return {x: String(v)};
  }
  return {x: String(v)};
}

const D = (...a) => new Date(Date.UTC(...a));
const datums = [
  {_vgsid_: 1, x: 1, y: 2, s: 'abc', a: [1, 2, 3], o: {a: {b: 5}}, d: D(2020, 0, 15, 10, 30, 45, 123), n: null, nan: NaN, u: undefined, b: true, f: 2.5},
  {_vgsid_: 3, x: -5, y: 0, s: '', a: [], o: {}, d: D(1999, 11, 31, 23, 59, 59, 999), n: null, nan: 3, b: false, f: -0.5},
  {_vgsid_: 4, x: 0, y: -0, s: ' 12 ', a: [5], o: {a: null}, d: new Date(NaN), n: 0, nan: NaN, b: 0, f: 1e21},
  {_vgsid_: 5, x: '7', y: '3.5', s: 'Hello, World', a: ['x', 'y', null, undefined], o: {a: {b: 'z'}}, d: D(2021, 2, 14, 1, 59, 59), n: '', nan: undefined, b: 'false', f: 1e-7},
  {_vgsid_: 'a', x: 1e10, y: NaN, s: 'héllo\u{1F600}', a: [3, 1, 2, 10, -1], o: {a: {b: [1]}}, d: D(2024, 1, 29, 12), n: null, nan: 1, b: 'true', f: 0.1},
];
const tbl = [{a: 1, b: 'x', unit: 'u1'}, {a: 2, b: 'y', unit: 'u1'}, {a: 3, b: 'x', unit: 'u2'}];
const E = (field, type = 'E') => ({field, channel: field, type});
const stores = {
  sel: [{unit: 'u1', fields: [E('x')], values: [1]}, {unit: 'u1', fields: [E('x')], values: [-5]}, {unit: 'u2', fields: [E('s'), E('y')], values: ['abc', 2]}],
  selR: [{unit: 'u1', fields: [E('x', 'R')], values: [[-10, 0]]}, {unit: 'u2', fields: [E('y', 'R-E'), E('f', 'R-LE')], values: [[0, 3], [0, 3]]}, {unit: 'u2', fields: [E('f', 'R-RE')], values: [[0, 10]]}],
  selP: [{unit: 'u1', fields: [E('x', 'E-LT'), E('y', 'E-GTE')], values: [5, 0]}, {unit: 'u1', fields: [E('nan', 'E-VALID')], values: [0]}, {unit: 'u1', fields: [E('x', 'E-ONE')], values: [[1, -5, '7']]}, {unit: 'u2', fields: [E('x', 'E-GT'), E('y', 'E-LTE')], values: [-6, 3]}],
  selD: [{unit: 'u1', fields: [E('d')], values: [D(2020, 0, 15, 10, 30, 45, 123)]}, {unit: 'u2', fields: [E('d', 'R')], values: [[D(1999, 0, 1), D(2000, 0, 1)]]}, {unit: 'u2', fields: [E('d')], values: [[D(2021, 2, 14, 1, 59, 59)]]}],
  selID: [{unit: 'u1', _vgsid_: 1}, {unit: 'u1', _vgsid_: 3}, {unit: 'u2', _vgsid_: 3}, {unit: 'u2', _vgsid_: 5}],
  selID1: [{unit: 'u1', _vgsid_: 1}, {unit: 'u1', _vgsid_: 3}],
  selMix: [{unit: 'u1', fields: [E('x'), E('y', 'R')], values: [1, [0, 5]]}, {unit: 'u1', fields: [E('x'), E('y', 'R')], values: [-5, [3, 1]]}, {unit: 'u2', fields: [E('x'), E('y', 'R')], values: [1, [1, 9]]}],
  selOne: [{unit: 'u1', fields: [E('x')], values: [[1, -5]]}],
};
const signals = {n: 7, str: 'Hello', arr: [3, 1, 2], obj: {a: 1, b: {c: 2}}, nul: null, flag: true, zero: 0, empty: ''};

// ---- cases ----
const cases = [];
// Vega rejects identifiers that are not declared signals ("Unrecognized signal
// name"), so `undefined` and `Infinity` cannot appear in a real specification.
// Outside the syntax sections, spell undefined as an absent datum field.
let rawMode = true;
const U = e => e.replace(/(?<![\w."'$])undefined(?![\w"'$])/g, 'datum.u');
const add = (e, opt = {}) => cases.push({e: rawMode ? e : U(e), ...opt});
const addAll = (list, opt) => list.forEach(e => add(e, opt));

// literals
addAll(['0x1F', '0XfF', '1e3', '1E-2', '.5', '5.', '1.5e+3', '0', '00', '017', '09', '0b11', '1_000', '1a', '3in datum.o', '9007199254740993',
  '1e400', '-1e400', '0.1+0.2', '123456789012345678901234567890', '1e21', '1e-7', '0.000001', '4.35*100', '1.005*1000',
  "'abc'", '"abc"', "'it\\'s'", "'a\\nb'", "'\\x41\\u0042\\u{43}'", "'\\u{1F600}'.length", "'\\uD83D\\uDE00'.length", "'\\101'", "'\\0'", "'a\\\nb'", "'abc", "'\\xZ'", "'\\u12'",
  'true', 'false', 'null', 'undefined', 'NaN', 'Infinity', '-Infinity', 'PI', 'E', 'LN2', 'LN10', 'LOG2E', 'LOG10E', 'SQRT1_2', 'SQRT2', 'MIN_VALUE', 'MAX_VALUE',
  '/ab+c/i', '/a\\/b/', '/[/]/.source', '/a/gi.flags', '/a/g.global', '/(?:)/.source', '/a/x', '/(/', '/a', '//', '/=/.source', '/a/.lastIndex', 'test(/a/g, "aa")',
  '[1, 2, 3]', '[]', '[1,]', '[,]', '[1,,2]', '[[1],[2,[3]]]', '{}', '{a: 1}', "{'a b': 1, 2: 3, c: [1]}", '{a: 1, a: 2}', "{a: 1, 'a': 2}", '{1: 1, "1": 2}', '{if: 1, in: 2, true: 3, null: 4}',
  '{__proto__: 1}', '{"__proto__": 1}', '{constructor: 1}', '{toString: 1}', '{valueOf: 1}', '{hasOwnProperty: 1}', '{a: 1,}', '{,}', '{a}', '{a: 1 b: 2}',
  '({a: 1}).a', '({a: 1})["a"]', '[1,2,3][1]', '(1)', '((1))', '()', '(1,2)', '1,2', '', ' ', '1 2', '(1', '1)', '[1', '{a:1', '}', '1 +', '+', '?', ':', '.', '..', '1.toString',
]);

// forbidden and unsupported syntax
addAll(['this', 'typeof x', 'typeof datum.x', 'void 0', 'delete datum.x', 'x = 1', 'datum.x = 1', 'x += 1', 'x++', '++x', 'x--', '--x', 'a, b', 'function(){}', 'function f(){}', 'x => x', '(x) => x',
  'new Date', 'new Date()', 'new Date(0)', '1 ?? 2', 'datum?.x', 'datum?.[0]', '2 ** 3', '2 ** -1', '_', '_.x', 'datum._', '`x`', '1 // 2', '1 /* c */ + 2', '/* c */ 1', '# 1', '@x', '\\u0061bc', 'a\\u0062c', '\\u0031', '$x', '_x', 'ünï', 'x​y',
  'var x', 'let x', 'if', 'if(1)', 'if(1,2)', 'if(1,2,3,4)', 'if(1,2,3)', 'else', 'while', 'in', '1 in [1]', '"a" in datum.o', '1 in datum.a', '"length" in datum.a', '"a" in "abc"', '"a" in 1', '"a" in null',
  '1 instanceof 2', 'datum instanceof datum', 'yield', 'await', 'class', 'super', 'return 1', 'throw 1', 'debugger', 'import', 'enum', 'let', 'static', 'interface', 'null.x', 'true.x', 'datum.if', 'datum.in', 'datum.new', 'datum.true', 'datum.null', 'datum.class',
  'datum.', 'datum[', 'datum[]', 'datum[1,2]', 'datum["x"', 'datum . x', 'datum\n.x', 'a ? b', 'a ? b :', '1 ? 2 : 3 ? 4 : 5', '1 ? 2 ? 3 : 4 : 5', '0 ? 1 : 0 ? 2 : 3',
  'a.b', 'datum.x.y.z', 'eval("1")', 'Math.abs(1)', 'Date.now()', 'String(1)', 'Number("1")', 'datum.x()', 'datum["x"]()', '(abs)(1)', 'abs(1)(2)', 'abs.x', 'Math', 'window', 'globalThis', 'constructor', '__proto__', 'hasOwnProperty', 'toString',
  'constructor(1)', 'hasOwnProperty(1)', 'toString(1)', 'valueOf(1)', 'isPrototypeOf(1)', 'unknownFn(1)', 'Number(1)', 'parseFloat', 'if (1) 2', 'x y', '1 2 3', ';', '1;', '1; 2', '"use strict"',
]);

rawMode = false;
// numeric and value operator matrix
const vals = ['0', '1', '-1', '2.5', '1/0', 'NaN', "''", "'0'", "' 1 '", "'a'", "'10'", 'null', 'true', '[]', '[5]', '[1,2]', '{}', 'datum.d', 'undefined', '-0', '2147483648'];
const uvals = [...vals, '-1/0', "'abc'", "'9'", 'false', '[[]]', "'0x10'", "'1e3'", '4294967296', '1e21'];
const binops = ['+', '-', '*', '/', '%', '==', '!=', '===', '!==', '<', '>', '<=', '>=', '&', '|', '^', '<<', '>>', '>>>', '&&', '||'];
for (const op of binops) for (const a of vals) for (const b of vals) add(`${a} ${op} ${b}`, {ops: 1});
for (const op of ['-', '+', '!', '~']) for (const a of uvals) add(`${op}(${a})`);
addAll(['- -1', '-+1', '!-1', '~-1', '~~2.7', '~~-2.7', '!!datum.s', '!!datum.a', '+datum.x', '-datum.x', '- - datum.x', '+datum.d', '+"  42  "', '+"1,5"', '+[]', '+{}', '+null', '+undefined', '+true', '+"0b101"', '+"0o17"', '+"-0x1"', '+"Infinity"', '+"-Infinity"', '+"infinity"', '+"1e1000"', '+".5"', '+"5."', '+"."', '+"+.5e-1"']);
addAll(['1+2*3', '(1+2)*3', '2*3%4', '2+3%2', '1<2==true', '1|2&3^4', '1|2^3&4', '5&3|4^1', 'true?1:2', '1?2:3?4:5', '0?1:2?3:4', '-2 - -2', '1 - -1', '1<<2+1', '10>>1>>1', '8>>>1>>1', '1<2<3', '3>2>1', '1==1==1', '2==2==2', '1+1+"1"', '"1"+1+1', '1+"1"-1', '"3"*"4"', '"3"+"4"*2', '1/3*3', '0.1*3', '1e21+1', '5%3', '-5%3', '5%-3', '-5%-3', '5.5%2', '5%0', '0%5', '-0%5', '1/-0', '-0===0', '0===-0', 'NaN===NaN', 'NaN==NaN', 'NaN!=NaN', 'NaN!==NaN', 'null==0', 'null>=0', 'null<=0', 'null>0', 'undefined==null', 'undefined==0', 'undefined===undefined', '"a"<"b"', '"a"<"B"', '"10"<"9"', '10<"9"', '"10"<9', '"a"<1', '"a">1', '"a"<=1', '[]<1', '[]==false', '[]==""', '[0]==false', '[1]==1', '[1,2]=="1,2"', '[]==[]', '{}=={}', '{}==="[object Object]"', '{}=="[object Object]"', 'datum.a==datum.a', 'datum.d==datum.d', 'datum.d===datum.d', 'datum.d<datum.d', 'datum.d<=datum.d', 'datum.d>=datum.d', 'datum.d-datum.d', 'datum.d+1', 'datum.d*1', '1+datum.d', '"x"+datum.d', 'datum.d==datum.d.valueOf', 'datum.d-1', 'datum.d>0', 'datum.d==0', 'datum.d=="x"', 'datum.d==toDate(datum.d)', 'true+true', 'true+"1"', 'true==1', 'true=="1"', 'true==="1"', 'false==""', 'false=="0"', 'false==null', '"1"==1', '"1"===1', '" 1 "==1', '"1e0"==1', '"0x1"==1', '""==0', '"  "==0', '"a"==0', 'null+1', 'undefined+1', 'null+"a"', 'undefined+"a"', '[]+[]', '[]+{}', '[1]+[2]', '{}+[]', '[1,[2,3]]+""', '[null]+""', '[undefined]+""', '[null,undefined]+""', '"a"*2', '"a"-"b"', '2147483647+1|0', '2147483648|0', '4294967295|0', '4294967296|0', '-2147483649|0', '1.9|0', '-1.9|0', 'NaN|0', '1/0|0', '1<<31', '1<<32', '1<<33', '1<<-1', '-1>>>0', '-1>>>31', '-1>>>32', '-1>>1', '2147483648>>0', '2147483648>>>0', '~0', '~-1', '~2147483647', '~NaN', '~null', '~"5"', '~[]', '~{}', '5^"3"', '5&"3"', '5|"3"', '"5"<<"1"', '1e10>>0', '1e10|0', '2**31|0', '~~1e10', '~~-1e10', '~~NaN', '~~1e21', '~~Infinity']);

// UTF-16 ordering: supplementary characters sort by their surrogates
addAll(['"\u{1F600}" < "\uFFFF"', '"\uFFFF" < "\u{1F600}"', '"\u{1F600}" > "\uE000"', '"a\u{1F600}" < "a\uFF5E"', '"\u{10000}" < "\u{10001}"', '"\u{10000}" == "\u{10000}"', '"\u{1F600}" <= "\u{1F600}"', '"\u00e9" > "e"', '"\u00e9" < "f"', '["\uFFFF", "\u{1F600}", "a", "\u00e9"].length', 'sort(["\uFFFF", "\u{1F600}", "a", "\u00e9", "\u{10000}", "\uD7FF"])', 'extent(["\uFFFF", "\u{1F600}", "\uE000"])', 'inrange("\u{1F600}", ["\\uD800", "\\uE000"])',
  'length("\u{1F600}\u{1F601}")', 'slice("\u{1F600}\u{1F601}", 1, 3)', 'indexof("\u{1F600}x\u{1F601}", "\u{1F601}")', 'lastindexof("\u{1F600}x\u{1F601}x", "x")', 'substring("a\u{1F600}b", 1, 3)', 'upper("stra\u00dfe")', 'lower("\u0130")', 'upper("\u{10428}")', 'lower("\u03a3")', 'lower("A\u03a3")', 'lower("A\u03a3B")', 'upper("\u0149")', 'upper("\ufb01")', 'trim("\u00a0\ufeff x \u2028")', 'pad("\u{1F600}", 3, "\u{1F601}")', 'truncate("\u{1F600}\u{1F601}\u{1F602}", 3)', 'truncate("\u{1F600}\u{1F601}\u{1F602}", 3, "left")', 'split("a\u{1F600}b", "\u{1F600}")', 'replace("a\u{1F600}b", "\u{1F600}", "-")', 'test(/^.$/u, "\u{1F600}")', 'encodeURIComponent("\u{1F600} \u00e9/?")', 'btoa("\u00e9")', 'atob("6Q==")', 'atob("6Q")', 'atob(" 6 Q = = ")', 'btoa("")', 'atob("")']);

// member access
addAll(['datum.x', 'datum["x"]', 'datum.s', 'datum.a', 'datum.a[1]', 'datum.a[-1]', 'datum.a[1.5]', 'datum.a["1"]', 'datum.a["01"]', 'datum.a[" 1"]', 'datum.a["length"]', 'datum.a.length', 'datum.s.length', 'datum.s[0]', 'datum.s[1]', 'datum.s[100]', 'datum.s["length"]', 'datum.o.a', 'datum.o.a.b', 'datum.o["a"]["b"]', 'datum.o.a.b.c', 'datum.o.b', 'datum.o.b.c', 'datum.missing', 'datum.missing.foo', 'datum.n.foo', 'datum.u', 'datum.u.foo', 'datum.x.foo', 'datum.x.length', 'datum.b.length', 'datum.d.foo', 'datum.d.length', 'datum.nan', 'datum[datum.s]', 'datum[datum.x]', 'datum[0]', 'datum[1]', 'datum[null]', 'datum[undefined]', 'datum[[]]', 'datum[{}]', 'datum["s"]', "datum['s']", 'datum["a b"]', 'datum.length',
  '"abc"[1]', '"abc"[-1]', '"abc"[3]', '"abc".length', '"abc".foo', '"héllo\u{1F600}".length', '"héllo\u{1F600}"[1]', '"héllo\u{1F600}"[5]', '"\u{1F600}"[0].length', '[1,2,3][1]', '[1,2,3].length', '[1,2,3][3]', '[1,2,3][-1]', '[1,2,3]["1"]', '[[1,2],[3]][0][1]', '[][0]', '[][0].x', '({}).x', '({}).x.y', '(null).x', '(undefined).x', 'null[0]', 'undefined["x"]', '(1).x', '(true).x', '(1)[0]', '/a/.source', '/a/i.flags', '/a/.foo', '/a/g.sticky', '/a/s.dotAll', '/a/m.multiline', '/a/i.ignoreCase', '/a/u.unicode',
  'n', 'str', 'arr', 'obj', 'obj.a', 'obj.b.c', 'obj.zz', 'nul', 'nul.x', 'flag', 'zero', 'empty', 'arr[0]', 'arr.length', 'str.length', 'str[1]', 'undefinedSignal', 'undefinedSignal.x', 'n+1', 'str+n', 'n>5', 'n==7', 'n==="7"', 'arr[1]+arr[2]', 'obj["a"]', 'obj.b["c"]', 'str[n]', 'arr[n-6]', 'PI*n', 'n*PI', 'event.x', 'item.x']);
addAll(['{a: datum.x, b: [datum.y, datum.s]}', '[datum.x, datum.y, datum.x+datum.y]', '[datum.d]', '{d: datum.d}', '[undefined]', '{u: undefined}', '[NaN, -0, 1/0]', '{a: NaN}', '{a: -0}']);

// if / conditionals / logic
addAll(['if(datum.x, "t", "f")', 'if(0, 1, 2)', 'if("", 1, 2)', 'if("0", 1, 2)', 'if(NaN, 1, 2)', 'if(null, 1, 2)', 'if([], 1, 2)', 'if({}, 1, 2)', 'if(datum.d, 1, 2)', 'if(true)', 'if(1, 2)', 'if(1,2,3,4)', 'if(1, datum.x.foo, 2)', 'if(0, datum.x.foo, 2)',
  'datum.x && datum.y', 'datum.x || datum.y', 'datum.x ? datum.s : datum.a', '0 && datum.x.foo', '1 || datum.x.foo', '1 && 0 || 2', '0 || 0 && 1', 'null || "d"', '"" || 0', '0 || ""', '"a" && "b"', 'NaN || undefined', 'undefined || null', 'null && 1', '[] && 1', '{} || 1', 'datum.d && 1', '!datum.d', '!datum.x', '!datum.s', '!datum.a', '!datum.o', '!datum.nan', '!datum.n', '!"0"', '!"false"', '!0', '!NaN', '![]', '!{}', '!!""']);

// math
const mathFns1 = ['abs', 'acos', 'asin', 'atan', 'ceil', 'cos', 'exp', 'floor', 'log', 'round', 'sin', 'sqrt', 'tan'];
const mathArgs = ['0', '-0', '1', '-1', '0.5', '-0.5', '2.5', '-2.5', '1.5', '-1.5', '0.49999999999999994', '-0.49999999999999994', '4503599627370495.5', '1e21', '-1e21', '1/0', '-1/0', 'NaN', "'4'", "''", "'a'", 'null', 'undefined', 'true', '[]', '[9]', '[1,2]', '{}', 'datum.d', 'datum.x', '10', '100', '3.14159', '-3.7', '0.1', '2', '7'];
for (const f of mathFns1) for (const a of mathArgs) add(`${f}(${a})`, {tol: 1});
addAll(['abs()', 'abs(1,2)', 'floor()', 'sqrt(-1)', 'sqrt(2)', 'log(0)', 'log(-1)', 'log(E)', 'exp(1)', 'exp(710)', 'exp(-746)', 'round(-0.5)', 'round(0.5)', 'round(1.4999999999999999)', 'round(-1.5)', 'round(2.5)', 'round(-2.5)', 'round(5e-324)', 'ceil(-0.5)', 'floor(-0)', 'ceil(-0)']);
const mathArgs2 = ['0', '-0', '1', '-1', '2', '0.5', '-0.5', '3', '1/0', '-1/0', 'NaN', "'2'", "'a'", 'null', 'undefined', 'true', '[]', '10', '-8', '1e308', '1e-308'];
for (const f of ['atan2', 'pow']) for (const a of mathArgs2) for (const b of mathArgs2) add(`${f}(${a}, ${b})`, {tol: 1});
addAll(['pow()', 'pow(2)', 'pow(2,3,4)', 'atan2(1)', 'pow(2, 0.5)', 'pow(10, -2)', 'pow(-8, 1/3)', 'pow(2, 1023)', 'pow(2, 1024)', 'pow(2, -1074)', 'pow(2, -1075)', 'pow(1.0000001, 1e9)', 'pow(0.1, 0.1)', 'pow(7, 7)', 'pow(3, 40)']);
const mm = ['1', '2', '-1', '0', '-0', 'NaN', "'3'", "'a'", 'null', 'undefined', 'true', '[]', '[5]', '[1,2]', '1/0', '-1/0', 'datum.d', 'datum.x', 'datum.y'];
for (const f of ['max', 'min', 'hypot']) {
  add(`${f}()`, {tol: 1});
  for (const a of mm) { add(`${f}(${a})`, {tol: 1}); for (const b of mm) add(`${f}(${a}, ${b})`, {tol: 1}); }
}
addAll(['max(1,2,3,4,5)', 'min(5,4,3,2,1)', 'max(-0, 0)', 'max(0, -0)', 'min(-0, 0)', 'min(0, -0)', 'hypot(3,4)', 'hypot(1,1,1)', 'hypot(3,4,12)', 'hypot(1e200, 1e200)', 'hypot(1e-200, 1e-200)', 'hypot(NaN, 1/0)', 'hypot(0,0)', 'hypot(-3)', 'hypot(1,2,3,4,5,6,7,8,9,10)', 'max(datum.x, datum.y)', 'min(datum.a)', 'max.apply', 'max(...[1])']);
addAll(['clamp(5, 0, 10)', 'clamp(-5, 0, 10)', 'clamp(15, 0, 10)', 'clamp(5, 10, 0)', 'clamp(NaN, 0, 10)', 'clamp(5, NaN, 10)', 'clamp(5, 0, NaN)', 'clamp("5", 0, 10)', 'clamp(null, 1, 10)', 'clamp(5, 0)', 'clamp(5,0,10,1)', 'clamp()', 'clamp(0, -0, 0)', 'clamp(datum.x, 0, 3)', 'clamp(datum.d, 0, 1)']);
addAll(['isNaN(NaN)', 'isNaN(1)', 'isNaN("a")', 'isNaN("")', 'isNaN(undefined)', 'isNaN(null)', 'isNaN()', 'isNaN(datum.d)', 'isNaN([])', 'isNaN(0/0)', 'isFinite(1)', 'isFinite(1/0)', 'isFinite(NaN)', 'isFinite("1")', 'isFinite(null)', 'isFinite()', 'isFinite(-1/0)', 'isFinite(datum.d)', 'isFinite(1e308*10)', 'isFinite(MAX_VALUE)',
  'isNaN(datum.x)', 'isNaN(datum.nan)', 'isFinite(datum.f)']);

// type checks & coercion
const tv = ['0', '1', "''", "'a'", "'0'", "'false'", "' '", 'null', 'undefined', 'true', 'false', 'NaN', '1/0', '[]', '[0]', '[1,2]', '{}', '{a:1}', 'datum.d', '/a/', 'datum.x', 'datum.s', 'datum.nan', 'datum.n', 'datum.u', 'datum.a', 'datum.o', 'datum.b', 'str', 'nul', 'undefinedSignal', '"2020-01-01"', '"12/25/2020"', '"abc"', '"1e3"', '" 5 "', '"0x10"', '-0', '2.5', '"Infinity"'];
for (const f of ['isArray', 'isBoolean', 'isDate', 'isDefined', 'isNumber', 'isObject', 'isRegExp', 'isString', 'isValid', 'isTuple', 'toBoolean', 'toNumber', 'toString']) {
  for (const a of tv) add(`${f}(${a})`);
  add(`${f}()`);
}
for (const a of tv) add(`toDate(${a})`, {date: 1});
addAll(['toDate()', 'toDate(0)', 'toDate(1e12)', 'toDate("2020-01-01T00:00:00Z")', 'toDate("2020-01-01T00:00:00")', 'toDate("2020-01-01T00:00")', 'toDate("2020-01-01 00:00")', 'toDate("2020-01")', 'toDate("2020")', 'toDate("2020-1-1")', 'toDate("2020/01/02")', 'toDate("1/2/2020")', 'toDate("01/02/20")', 'toDate("Jan 5, 2020")', 'toDate("January 5, 2020 10:30")', 'toDate("5 Jan 2020")', 'toDate("Jan 5 2020 10:30:15 PM")', 'toDate("Tue Mar 01 2022 10:00:00 GMT+0100")', 'toDate("Tue, 01 Mar 2022 10:00:00 GMT")', 'toDate("2020-01-01T00:00:00+02:00")', 'toDate("2020-01-01T00:00:00.5Z")', 'toDate("2020-01-01T00:00:00.123456Z")', 'toDate("2020-13-01")', 'toDate("2020-02-30")', 'toDate("2020-02-29")', 'toDate("2019-02-29")', 'toDate("2020-01-01T24:00:00Z")', 'toDate("2020-01-01T25:00:00Z")', 'toDate("+002020-01-01T00:00:00Z")', 'toDate("-000001-01-01T00:00:00Z")', 'toDate("  2020-01-01  ")', 'toDate("2020-01-01T10:00:00z")', 'toDate("20200101")', 'toDate("Mar 2020")', 'toDate("12:30")', 'toDate("not a date")', 'toDate("8.64e15")', 'toDate(8.64e15)', 'toDate(8.64e15+1)', 'toDate(-8.64e15)', 'toDate(datum.d)', 'toDate([])', 'toDate(true)', 'toDate("2020-06-15T12:00:00")', 'toDate("2020-03-08T02:30:00")', 'toDate("2020-11-01T01:30:00")', 'toDate("2020-06-15")', 'toDate(datum.s)', 'toDate("1970-01-01T00:00:00.000Z")', 'toDate("1969-12-31T23:59:59.999Z")', 'toDate("0000-01-01T00:00:00Z")', 'toDate("0099-01-01T00:00:00Z")', 'toDate("99-01-01")', 'toDate("1/1/99")', 'toDate("1/1/49")', 'toDate("1/1/50")', 'toDate("2020-01-01T00:00:00 GMT")', 'toDate("2020-01-01 10:00 PM")', 'toDate("Sat, 15 Feb 2020 00:00:00 +0530")'], {date: 1});
addAll(['toBoolean(1) === true', 'toBoolean("no")', 'toBoolean("FALSE")', 'toBoolean(" ")', 'toBoolean([])', 'toBoolean({})', 'toBoolean(datum.d)', 'toNumber("  ")', 'toNumber("1,2")', 'toNumber(datum.d)', 'toNumber(true)', 'toNumber([])', 'toNumber([7])', 'toNumber({})', 'toNumber("0x1f")', 'toNumber("1e3")', 'toNumber("١")', 'toString(1)', 'toString(1.5)', 'toString(-0)', 'toString(1e21)', 'toString(1e-7)', 'toString(123456789.123456789)', 'toString(0.1+0.2)', 'toString(true)', 'toString([1,2])', 'toString([1,[2,3]])', 'toString({})', 'toString(datum.d)', 'toString(/a/g)', 'toString(NaN)', 'toString(1/0)', 'toString(-1/0)', 'toString([null])', 'toString([undefined,1])', 'toString(2**53)', 'toString(255)', 'toString(100)', 'toString(1e100)', 'toString(1.7976931348623157e308)', 'toString(5e-324)', 'toString(123e-20)', 'toString(0.000001)', 'toString(0.0000001)', 'toString(12345678901234567890)', 'toString(1e20)', 'toString(4.35)', 'toString(0.5)', 'toString(100.0)', 'toString(1.10)', 'toString(-1e-7)', 'toString(1e21+1)', 'toString(999999999999999900000)', 'toString(0.30000000000000004)', 'toString(2.5e-5)', 'toString(12e20)'], {date: 1});

// strings
const sv = ["'abc'", "''", "'Hello, World'", "' padded '", "'héllo\u{1F600}x'", "'ß'", "'İ'", "'ΣΑΣ'", "'ABC'", 'null', 'undefined', '123', 'true', '[1,2]', '{}', 'datum.d', 'datum.s', 'NaN', '-0', "'a\\tb\\n'", "'\\u00a0x\\ufeff'", "'\\u2028y'"];
for (const f of ['length', 'upper', 'lower', 'trim']) for (const a of sv) add(`${f}(${a})`, {date: 1});
addAll(['length()', 'upper()', 'lower()', 'trim()', 'length(1,2)', 'length(datum.a)', 'length(datum.o)', 'length(datum.nan)', 'length(datum.n)', 'length(datum.u)', 'length(datum.b)', 'length(datum.x)', 'length(datum.d)', 'length([])', 'length([1,2,3])', 'length({length: 5})', 'length(1)', 'length(true)', 'length(/a/)', 'length(null)', 'length(undefined)']);
const ss = ["'abcdef'", "'héllo\u{1F600}x'", "''", 'null', '123456'];
const si = ['0', '1', '2', '-1', '-2', '100', '2.7', '-2.7', 'NaN', 'undefined', 'null', "'1'", "'a'", '1/0', '-1/0', 'true', '[]'];
for (const str of ss) for (const a of si) { add(`substring(${str}, ${a})`); for (const b of ['0', '3', '-1', 'NaN', 'undefined', '100', '1', '2.5']) add(`substring(${str}, ${a}, ${b})`); }
addAll(['substring()', 'substring("abc")', 'substring(datum.s, 1, 2)', 'substring(datum.d, 0, 3)', 'substring(datum.x, 0, 1)', 'substring("héllo\u{1F600}x", 5, 6)', 'substring("héllo\u{1F600}x", 6, 7)', 'length(substring("\u{1F600}", 0, 1))', 'substring("abc", 1, 2, 3)']);
const sp = ["'a,b,c'", "'abc'", "''", "'a, b,c '", "'héllo\u{1F600}'", 'null', 'undefined', '123', '[1,2]', 'datum.s', "'a1b22c'", "'A-B_C'"];
const sepv = ["','", "''", "', '", "'x'", 'undefined', 'null', '1', "/[0-9]+/", "/,/", "/(,)/", "/(?:)/", "/\\s*,\\s*/", "/z/", "/b/i", "/./", "regexp(',')", 'true'];
// /./ splits a surrogate pair into halves without the u flag, which Go strings cannot represent
for (const a of sp) for (const b of sepv) if (!(b === '/./' && (a.includes('\u{1F600}') || a === 'datum.s'))) add(`split(${a}, ${b})`);
for (const b of ["','", "''", '/,/']) for (const l of ['0', '1', '2', '10', 'undefined', 'NaN', '-1', '4294967296', "'2'"]) add(`split('a,b,c', ${b}, ${l})`);
addAll(['split()', 'split("abc")', 'length(split("a,b", ","))', 'split("a,b", ",")[1]', 'split("\u{1F600}", "")', 'length(split("\u{1F600}", ""))', 'split("", "")', 'split("", ",")', 'split("", /,/)', 'split("", /(?:)/)', 'split("ab", /(?:)/)', 'split("aXbxc", /x/i)', 'split("test", /(t)/)']);
addAll(['parseFloat("1.5")', 'parseFloat("  1.5abc")', 'parseFloat("abc")', 'parseFloat("")', 'parseFloat(".5")', 'parseFloat("5.")', 'parseFloat("-.5e2x")', 'parseFloat("+1e")', 'parseFloat("1e+")', 'parseFloat("1e3")', 'parseFloat("Infinity")', 'parseFloat("-Infinityx")', 'parseFloat("infinity")', 'parseFloat("0x10")', 'parseFloat("1_0")', 'parseFloat("1.2.3")', 'parseFloat(null)', 'parseFloat(undefined)', 'parseFloat(true)', 'parseFloat([1.5,2])', 'parseFloat(datum.s)', 'parseFloat(datum.x)', 'parseFloat()', 'parseFloat(1e21)', 'parseFloat(1e-7)', 'parseFloat("1e400")', 'parseFloat("\\u00a01")', 'parseFloat("-")', 'parseFloat("-0")', 'parseFloat("00012")', 'parseFloat("1e-400")', 'parseFloat({})',
  'parseInt("42")', 'parseInt("  42px")', 'parseInt("-42")', 'parseInt("+42")', 'parseInt("4.9")', 'parseInt("0x1f")', 'parseInt("0X1F")', 'parseInt("0x1f", 16)', 'parseInt("0x1f", 10)', 'parseInt("1f", 16)', 'parseInt("z", 36)', 'parseInt("10", 2)', 'parseInt("12", 2)', 'parseInt("10", 1)', 'parseInt("10", 37)', 'parseInt("10", 0)', 'parseInt("10", NaN)', 'parseInt("10", "8")', 'parseInt("10", 8.9)', 'parseInt("10", -1)', 'parseInt("10", 4294967298)', 'parseInt("")', 'parseInt("abc")', 'parseInt("-")', 'parseInt("0")', 'parseInt("-0")', 'parseInt("007")', 'parseInt("9007199254740993")', 'parseInt("123456789012345678901234567890")', 'parseInt("1e3")', 'parseInt(1e21)', 'parseInt(0.0000005)', 'parseInt(null)', 'parseInt(undefined)', 'parseInt(true)', 'parseInt([12,3])', 'parseInt(datum.s)', 'parseInt(datum.y)', 'parseInt()', 'parseInt("ff", 16)', 'parseInt("FF", 16)', 'parseInt("fffffffffffffffff", 16)', 'parseInt("7", 8)', 'parseInt("8", 8)', 'parseInt("  -0x10  ")', 'parseInt("Infinity")']);
addAll(['pad("5", 3)', 'pad("5", 3, "0")', 'pad("5", 3, "0", "left")', 'pad("5", 3, "0", "right")', 'pad("5", 3, "0", "center")', 'pad("5", 4, "0", "center")', 'pad("5", 6, "ab", "center")', 'pad("5", 6, "ab", "left")', 'pad("5", 6, "ab")', 'pad(5, 3, 0, "left")', 'pad(5, 3, "", "left")', 'pad("abc", 2)', 'pad("abc", 3)', 'pad("abc", NaN)', 'pad("abc", "6")', 'pad("abc", 5.5, "-")', 'pad("abc", 5.5, "-", "left")', 'pad("abc", 5.5, "-", "center")', 'pad("abc", 6.9, "-", "center")', 'pad(null, 6, "-")', 'pad(undefined, 12, "-")', 'pad(datum.d, 40, "-")', 'pad("\u{1F600}", 4, "-")', 'pad("hé", 5, "\u{1F600}")', 'pad()', 'pad("a")', 'pad("a", 3, true, "left")', 'pad("a", 3, 12, "left")', 'pad("a", 3, "-", "LEFT")', 'pad("a", 3, "-", 1)', 'pad([1,2], 6, ".", "left")', 'pad("a", -5)', 'pad("a", 3, null)', 'pad("a", 3, undefined, "left")', 'pad("a", 3, NaN, "left")', 'pad("a", 3, 0, "left")',
  'truncate("abcdefghij", 5)', 'truncate("abcdefghij", 5, "left")', 'truncate("abcdefghij", 5, "center")', 'truncate("abcdefghij", 5, "right")', 'truncate("abcdefghij", 5, "left", "..")', 'truncate("abcdefghij", 5, "center", "..")', 'truncate("abcdefghij", 5, undefined, "..")', 'truncate("abcdefghij", 5, "left", "")', 'truncate("abcdefghij", 5, "center", "")', 'truncate("abcdefghij", 5, undefined, "")', 'truncate("abcdefghij", 5, "left", null)', 'truncate("abcdefghij", 10)', 'truncate("abcdefghij", 11)', 'truncate("abcdefghij", 0)', 'truncate("abcdefghij", 1)', 'truncate("abcdefghij", 1, "left")', 'truncate("abcdefghij", 1, "center")', 'truncate("abcdefghij", 2, "center")', 'truncate("abcdefghij", 3, "center")', 'truncate("abcdefghij", 4, "center")', 'truncate("abcdefghij", 6, "center")', 'truncate("abcdefghij", 7, "center")', 'truncate("abcdefghij", -1)', 'truncate("abcdefghij", NaN)', 'truncate("abcdefghij", NaN, "left")', 'truncate("abcdefghij", "5")', 'truncate("abcdefghij", 5.5)', 'truncate("abcdefghij", 5.5, "center")', 'truncate("abcdefghij", 5.5, "left")', 'truncate(12345678, 5)', 'truncate(null, 2)', 'truncate(undefined, 5)', 'truncate("héllo\u{1F600}worlds", 8)', 'truncate("héllo\u{1F600}worlds", 8, "left")', 'truncate("héllo\u{1F600}worlds", 8, "center")', 'length(truncate("\u{1F600}\u{1F600}\u{1F600}", 4))', 'truncate("abcdef", 4, "left", "\u{1F600}")', 'truncate()', 'truncate("abc")', 'truncate("abc", 1/0)', 'truncate("abcdef", 3, "right", "1234567")', 'truncate("abcdef", 3, "center", "1234567")', 'truncate(datum.s, 3)', 'truncate(datum.d, 10)', 'truncate("abcdefghij", 5, "left", 5)']);
addAll(['replace("abc", "b", "X")', 'replace("abcabc", "b", "X")', 'replace("abcabc", /b/, "X")', 'replace("abcabc", /b/g, "X")', 'replace("abcABC", /b/gi, "X")', 'replace("abc", "", "X")', 'replace("abc", /(?:)/g, "-")', 'replace("abc", /(b)/, "[$1]")', 'replace("abc", /(?<x>b)/, "[$<x>]")', 'replace("abc", /b/, "[$&]")', 'replace("abc", /b/, "[$`]")', 'replace("abc", /b/, "[$\'\']")', 'replace("abc", /b/, "[$$]")', 'replace("abc", "b", "[$&]")', 'replace("abc", "b", "[$`]")', 'replace("abc", "b", "[$$]")', 'replace("abc", "b", "[$1]")', 'replace("abc", /b/, "$0")', 'replace("abc", /b/, "$")', 'replace("abc", "b", "$")', 'replace("abc", "b", "$&$&")', 'replace("abc", "z", "X")', 'replace("abc", /z/, "X")', 'replace(123, "2", "X")', 'replace(null, "l", "X")', 'replace(undefined, "d", "X")', 'replace("abc", "b")', 'replace("abc", "b", undefined)', 'replace("abc", "b", null)', 'replace("abc", "b", 5)', 'replace("abc", "b", [1,2])', 'replace("abc", 1, "X")', 'replace("abc", null, "X")', 'replace("abc", undefined, "X")', 'replace("abc", {}, "X")', 'replace("abc", ["b"], "X")', 'replace()', 'replace("a")', 'replace(datum.s, "l", "L")', 'replace(datum.s, /l/g, "L")', 'replace(datum.s, /[aeiou]/g, "*")', 'replace("héllo\u{1F600}", /./g, "-")', 'replace("héllo\u{1F600}", /./gu, "-")', 'replace("a.b.c", ".", "-")', 'replace("a.b.c", /\\./g, "-")', 'replace("aaa", /a/y, "b")', 'replace("aaa", /a/gy, "b")', 'replace("abc", /^/, "X")', 'replace("abc", /$/, "X")', 'replace("a\\nb", /^b/m, "X")', 'replace("abc", /(a)(b)(c)/, "$3$2$1")', 'replace("abc", /(a)(b)(c)/, "$10")', 'replace("abc", /(a)/, "$01")', 'replace("abc", /(a)/, "$2")', 'replace("abc", /(?:a)/, "$1")', 'replace(datum.d, /:/g, "_")', 'replace("2020-01-15", /(\\d+)-(\\d+)-(\\d+)/, "$3/$2/$1")']);
addAll(['regexp("a+")', 'regexp("a+", "gi")', 'regexp("a+", "x")', 'regexp("(")', 'regexp()', 'regexp(undefined)', 'regexp(null)', 'regexp(1)', 'regexp("/")', 'regexp("a/b")', 'regexp("\\\\d+")', 'regexp(/a/g)', 'regexp(/a/g, "i")', 'regexp(/a/g, undefined)', 'regexp("a", undefined)', 'regexp("a", "")', 'regexp("a", null)', 'regexp("a", "gg")', 'regexp("a", "u")', 'regexp("[", "u")', 'regexp("a", "yisd")', 'regexp("\\\\u{1F600}", "u")', 'regexp("(?<n>a)")', 'regexp("a{2}")', 'regexp("a{2,1}")', 'regexp("*")', 'regexp("a**")', 'regexp("(?=a)")', 'regexp("(?<=a)b")', 'regexp("\\\\p{L}", "u")', 'regexp("\\\\p{L}")', 'regexp("^$", "m")', 'regexp(".", "s")',
  'test("a+", "caat")', 'test("a+", "cbt")', 'test(/a+/, "caat")', 'test(/A/i, "a")', 'test(/A/, "a")', 'test(regexp("a", "g"), "aa")', 'test(/a/g, "aa")', 'test("a", "b")', 'test("", "")', 'test("^$", "")', 'test("a", undefined)', 'test("undefined", undefined)', 'test("a", null)', 'test("null", null)', 'test("1", 1)', 'test(1, 1)', 'test(undefined, "x")', 'test(null, "null")', 'test()', 'test("a")', 'test("(", "x")', 'test("[a-z]+", datum.s)', 'test(/^\\d+$/, datum.x)', 'test(/^\\d+$/, datum.y)', 'test("^h", datum.s)', 'test(/\\u{1F600}/u, datum.s)', 'test(/./u, "\u{1F600}")', 'test(/^.$/u, "\u{1F600}")', 'test(/^.$/, "\u{1F600}")', 'test(/^..$/, "\u{1F600}")', 'test(/\\bworld\\b/i, "Hello, World")', 'test(/(a+)+b/, "aaaaaaaaaaaaaaaaaaaaaaac")', 'test(/^(?:a|b)*$/, "abab")', 'test(/(?<!a)b/, "cb")', 'test(/(?<year>\\d{4})/, "in 2020")', 'test(/a/, [1,"a"])', 'test(/a/, {})', 'test(/1/, 10)', 'test(/x/y, "x")', 'test(/x/gy, "xx")', 'test(datum.s, datum.s)', 'test(datum.a, datum.s)']);

// arrays
const av = ['[1,2,3]', '[]', '[3,1,2,10,-1]', "['b','a','C','B']", '[1,"a",null,undefined,NaN,true]', '[null,undefined,3,NaN,1]', "['10','9','1']", '[datum.d, datum.d]', '[[1,2],[3]]', 'datum.a', 'arr', '"abc"', '"héllo\u{1F600}"', 'null', 'undefined', '5', '{}', '{length: 2}', '[1,2,3,4,5,6]', '[undefined, 3, undefined, 1]', '[0, -0, NaN, 1/0, -1/0]'];
for (const f of ['reverse', 'sort', 'peek', 'span', 'extent', 'flush']) for (const a of av) add(`${f}(${a})`);
for (const a of av) { add(`join(${a})`); add(`join(${a}, "-")`); add(`join(${a}, "")`); add(`join(${a}, undefined)`); add(`join(${a}, null)`); add(`join(${a}, 1)`); }
for (const a of av) for (const x of ['2', '"a"', 'NaN', 'undefined', 'null', '1', '"1"', '[]', '"bc"', '""', 'datum.d']) { add(`indexof(${a}, ${x})`); add(`lastindexof(${a}, ${x})`); }
const idxv = ['0', '1', '2', '-1', '-2', '100', '-100', '1.5', 'NaN', 'undefined', 'null', '"1"', '1/0', '-1/0', 'true'];
for (const a of ['[1,2,3,2,1]', '"abcabc"', '"héllo\u{1F600}llo"', '[NaN, 1, NaN]']) for (const x of ['1', '2', '"a"', '"b"', '"l"', 'NaN', '"llo"', '""']) for (const i of idxv) { add(`indexof(${a}, ${x}, ${i})`); add(`lastindexof(${a}, ${x}, ${i})`); }
for (const a of ['[1,2,3,4,5]', '"abcdef"', '"héllo\u{1F600}x"', '[]', '""']) for (const i of idxv) { add(`slice(${a}, ${i})`); for (const j of ['0', '2', '-1', '-2', '100', 'undefined', 'NaN', '1.5', 'null', '1/0']) add(`slice(${a}, ${i}, ${j})`); }
addAll(['slice()', 'slice(null)', 'slice(5)', 'slice({}, 1)', 'slice(datum.a, 1)', 'slice(datum.s, 1, 3)', 'slice(arr, -2)', 'slice(datum.d, 1)', 'indexof(5, 1)', 'indexof({}, 1)', 'indexof()', 'lastindexof()', 'indexof(null, 1)', 'join()', 'join(5)', 'join("abc")', 'join(null, ",")', 'join({}, ",")', 'reverse("abc")', 'reverse(5)', 'sort("abc")', 'sort(null)', 'sort()', 'peek()', 'peek([])', 'peek("abc")', 'peek(null)', 'peek(5)', 'peek({})', 'peek({length: 2, 1: 5})', 'span()', 'span(0)', 'span(null)', 'span([1])', 'span([])', 'span([5, 1])', 'span("abc")', 'span(["a","b"])', 'span([1,NaN])', 'span([datum.d, datum.d])', 'span([D, datum.d])', 'span({length: 2})', 'extent()', 'extent(5)', 'extent({})', 'extent("cab")', 'extent(["b", "a", "c"])', 'extent([datum.d, datum.d])', 'extent([3, "1", 2])', 'extent([true, false])', 'extent([[2], [1]])', 'extent([{}, {}])', 'extent([1,2],3)', 'extent({length: 2, 0: 5, 1: 3})', 'flush([0, 10], 1, 2, "l", "r", "c")', 'flush([0, 10], 9, 2, "l", "r", "c")', 'flush([0, 10], 5, 2, "l", "r", "c")', 'flush([10, 0], 1, 2, "l", "r", "c")', 'flush([0, 10], 1, 0, "l", "r", "c")', 'flush([0, 10], 0, 0, "l", "r", "c")', 'flush([0, 10], 10, 0, "l", "r", "c")', 'flush([0, 10], 1, null, "l", "r", "c")', 'flush([0, 10], 1, undefined, "l", "r", "c")', 'flush([0, 10], 1, NaN, "l", "r", "c")', 'flush([0, 10], 1, "2", "l", "r", "c")', 'flush([0, 10], 1, "", "l", "r", "c")', 'flush([], 1, 2, "l", "r", "c")', 'flush([5], 5, 2, "l", "r", "c")', 'flush([5], 6, 1, "l", "r", "c")', 'flush(null, 1, 2, "l", "r", "c")', 'flush([0,10], 5, 5, "l", "r", "c")', 'flush([0,10], 4.999, 5, "l", "r", "c")', 'flush([0,10],"a",1,"l","r","c")']);
addAll(['lerp([0, 10], 0.5)', 'lerp([0, 10], 0)', 'lerp([0, 10], 1)', 'lerp([0, 10], 2)', 'lerp([0, 10], -1)', 'lerp([0, 10], "0.25")', 'lerp([0, 10], NaN)', 'lerp([0, 10], null)', 'lerp([0, 10], undefined)', 'lerp([0, 10])', 'lerp([5], 0.5)', 'lerp([5], 0)', 'lerp([], 0.5)', 'lerp([0, 5, 10], 0.5)', 'lerp([10, 0], 0.3)', 'lerp(["a","b"], 0.5)', 'lerp([1, 3], true)', 'lerp([1, 3], "1")', 'lerp([1, 3], [0.5])', 'lerp([datum.d, datum.d], 0.5)', 'lerp(null, 0.5)', 'lerp(5, 0.5)', 'lerp("abc", 0.5)', 'lerp()', 'lerp([0.1, 0.2], 0.3)', 'lerp([1e300, -1e300], 0.5)', 'lerp([0, 1], 1e-17)',
  'inrange(5, [0, 10])', 'inrange(0, [0, 10])', 'inrange(10, [0, 10])', 'inrange(0, [0, 10], false)', 'inrange(10, [0, 10], true, false)', 'inrange(0, [0, 10], false, false)', 'inrange(10, [0, 10], false, true)', 'inrange(5, [10, 0])', 'inrange(10, [10, 0], true, false)', 'inrange(11, [0, 10])', 'inrange(-1, [0, 10])', 'inrange(5, [0])', 'inrange(0, [0])', 'inrange(1, [0])', 'inrange(5, [])', 'inrange(5, [0, 5, 10])', 'inrange(5, [0, 3, 10])', 'inrange(NaN, [0, 10])', 'inrange("5", [0, 10])', 'inrange(5, ["0", "10"])', 'inrange("b", ["a", "c"])', 'inrange("5", ["0", "10"])', 'inrange(datum.d, [datum.d, datum.d])', 'inrange(datum.d, [0, 1e15])', 'inrange(5, [0, 10], null)', 'inrange(5, [0, 10], 0)', 'inrange(0, [0, 10], 0)', 'inrange(0, [0, 10], "")', 'inrange(0, [0, 10], undefined, 0)', 'inrange(10, [0, 10], 1, 0)', 'inrange(10, [0, 10], 1, null)', 'inrange(10, [0, 10], 1, undefined)', 'inrange(null, [0, 10])', 'inrange(undefined, [0, 10])', 'inrange(5, null)', 'inrange(5, 5)', 'inrange(5)', 'inrange()', 'inrange(datum.x, [-10, 10])', 'inrange(datum.s, ["a", "b"])', 'inrange(true, [0, 2])',
  'clampRange([2, 4], 0, 10)', 'clampRange([-2, 2], 0, 10)', 'clampRange([8, 12], 0, 10)', 'clampRange([4, 2], 0, 10)', 'clampRange([0, 20], 0, 10)', 'clampRange([0, 10], 0, 10)', 'clampRange([5, 5], 0, 10)', 'clampRange([12, 12], 0, 10)', 'clampRange([-3, -3], 0, 10)', 'clampRange([2, 4], 10, 0)', 'clampRange([2, 4], 5, 5)', 'clampRange([NaN, 4], 0, 10)', 'clampRange([2, NaN], 0, 10)', 'clampRange([2, 4], NaN, 10)', 'clampRange([2, 4], 0, NaN)', 'clampRange(["2", "4"], 0, 10)', 'clampRange([2, 4], "1", "10")', 'clampRange([2], 0, 10)', 'clampRange([], 0, 10)', 'clampRange(null, 0, 10)', 'clampRange([1,2,3], 0, 10)', 'clampRange([9, 11], 0, 10)', 'clampRange([-1, 1], 0, 10)', 'clampRange([0.1, 0.2], 0.15, 0.25)', 'clampRange([2, 4])', 'clampRange()', 'clampRange([null, 4], 0, 10)', 'clampRange([datum.d, datum.d], 0, 1e15)',
  'span(extent([3,1,2]))', 'peek(sort([3,1,2]))', 'join(reverse([1,2,3]), "")', 'length(sequence(5))']);
addAll(['sequence(5)', 'sequence(0)', 'sequence(-3)', 'sequence(2.5)', 'sequence(1, 5)', 'sequence(5, 1)', 'sequence(0, 1, 0.25)', 'sequence(0, 10, 3)', 'sequence(10, 0, -3)', 'sequence(10, 0, 3)', 'sequence(0, 1, 0.1)', 'sequence(0, 1, 0)', 'sequence(0, 1, -1)', 'sequence(1, 1)', 'sequence(1, 2, NaN)', 'sequence(NaN)', 'sequence(undefined)', 'sequence(null)', 'sequence("3")', 'sequence("1", "4")', 'sequence(true)', 'sequence([3])', 'sequence({})', 'sequence()', 'sequence(1/0)', 'sequence(-1/0)', 'sequence(0, 1/0)', 'sequence(0, 1e10, 1e9)', 'sequence(0.5, 3)', 'sequence(1, 4, 1.5)', 'sequence(-2, 2)', 'sequence(0, 1, 0.3)', 'sequence(datum.a.length)', 'sequence(3, 0, -1)', 'sequence(0, 5, 2, 9)', 'length(sequence(1000))', 'peek(sequence(1000))', 'sequence(0, 0.3, 0.1)', 'sequence(1e15, 1e15+3)', 'sequence(datum.d, datum.d + 3)']);
addAll(['pluck([{a:1},{a:2}], "a")', 'pluck([{a:1},{b:2}], "a")', 'pluck([{a:{b:1}},{a:{b:2}}], "a.b")', 'pluck([{a:{b:1}}], "a[\'b\']")', 'pluck([{a:[5,6]}], "a[1]")', 'pluck([{a:1}], "")', 'pluck([{a:1}])', 'pluck([{a:1}], undefined)', 'pluck({a:5}, "a")', 'pluck({a:{b:5}}, "a.b")', 'pluck({a:5}, "z")', 'pluck({a:5}, "z.y")', 'pluck(null, "a")', 'pluck(undefined, "a")', 'pluck(5, "a")', 'pluck("abc", "length")', 'pluck([], "a")', 'pluck([null], "a")', 'pluck([1], "a")', 'pluck(datum.a, "length")', 'pluck([datum.o], "a.b")', 'pluck([datum], "x")', 'pluck([{"a b": 1}], "a b")', 'pluck([{"a.b": 1}], "a\\\\.b")', 'pluck([{a:1}], "a.b")', 'pluck([{a:null}], "a.b")', 'pluck([{a:{}}], "a.b.c")', 'pluck()', 'pluck([{a:1}], "a", "b")', 'pluck([{a:1},{a:undefined}], "a")',
  'merge({a:1},{b:2})', 'merge({a:1},{a:2})', 'merge({a:1},null,{b:2})', 'merge({a:1},undefined)', 'merge({a:1},5)', 'merge({a:1},"xy")', 'merge({a:1},[7,8])', 'merge([1],[2,3])', 'merge()', 'merge({})', 'merge(null)', 'merge({a:{b:1}},{a:{c:2}})', 'merge(datum.o, {z: 1})', 'merge({a:1},{a:undefined})', 'merge({a:1},true)', 'merge(datum.d)', 'merge({b:1, a:2},{c:3, a:4})', 'merge({a:1}, /x/)', 'merge("a", "bc")', 'merge({2:1, b:2, 1:3})']);

// dates
const dfn = ['date', 'day', 'year', 'month', 'hours', 'minutes', 'seconds', 'milliseconds', 'time', 'timezoneoffset', 'utcdate', 'utcday', 'utcyear', 'utcmonth', 'utchours', 'utcminutes', 'utcseconds', 'utcmilliseconds', 'quarter', 'utcquarter', 'week', 'utcweek', 'dayofyear', 'utcdayofyear'];
const dargs = ['datum.d', '0', '-1', '1e12', '1.5', '-1.5', '8.64e15', '8.64e15+1', 'NaN', 'null', 'undefined', 'true', '"2020-03-15"', '"2020-03-15T12:30:45.678"', '"2020-03-15T12:30:45.678Z"', '"2020-12-31"', '"2021-01-01"', '"2019-12-29"', '"2020-01-05"', '"2016-01-01"', '"2017-01-01"', '"2022-01-01"', '"2024-12-31"', '"not a date"', '""', '[]', '[5]', '{}', 'datetime(2020, 1, 1)', 'datetime(2020, 5, 5, 5, 5, 5, 5)', 'datetime(2021, 2, 28, 23, 59, 59, 999)', 'datetime(2000, 0, 1)', 'datetime(1969, 11, 31, 23, 0)', 'datetime(2020, 2, 8, 2, 30)', 'datetime(2020, 10, 1, 1, 30)', 'datetime(2020, 2, 8, 3, 0)', 'datetime(1900, 0, 1)', 'datetime(1800, 5, 5)', 'datetime(1, 0, 1)', 'datetime(275760, 8, 13)', 'datetime(-1, 0, 1)', 'datetime(99, 11, 31)', 'datetime(100, 0, 1)', '-62135596800000', '-62198755200000', '253402300799999'];
for (const f of dfn) for (const a of dargs) add(`${f}(${a})`, {date: 1});
addAll(['date()', 'year()', 'time()', 'quarter()', 'week()', 'dayofyear()', 'utcweek()', 'date(1,2)', 'now() > 0', 'now() === now() || now() !== now()', 'isNumber(now())', 'isDate(now())', 'isDate(datetime())', 'isDate(datetime(0))', 'datetime(0)', 'datetime(1e12)', 'datetime()  > 0', 'datetime(NaN)', 'datetime("2020-01-01")', 'datetime("2020-01-01T00:00:00")', 'datetime("x")', 'datetime(null)', 'datetime(undefined)', 'datetime(true)', 'datetime([])', 'datetime([5])', 'datetime({})', 'datetime(datum.d)', 'datetime(2020)', 'datetime(2020, 0)', 'datetime(2020, 0, 1)', 'datetime(2020, 0, 1, 12)', 'datetime(2020, 0, 1, 12, 30)', 'datetime(2020, 0, 1, 12, 30, 45)', 'datetime(2020, 0, 1, 12, 30, 45, 500)', 'datetime(2020, 12, 1)', 'datetime(2020, -1, 1)', 'datetime(2020, 0, 0)', 'datetime(2020, 0, 32)', 'datetime(2020, 0, 1, 25)', 'datetime(2020, 0, 1, -1)', 'datetime(2020, 0, 1, 0, 60)', 'datetime(2020, 0, 1, 0, 0, 60)', 'datetime(2020, 0, 1, 0, 0, 0, 1000)', 'datetime(2020.9, 0.9, 1.9)', 'datetime(2020, 1, 30)', 'datetime(2019, 1, 29)', 'datetime(NaN, 0)', 'datetime(2020, NaN)', 'datetime(2020, "1", "2")', 'datetime("2020", "1")', 'datetime(2020, null)', 'datetime(2020, undefined)', 'datetime(2020, 0, 1, undefined)', 'datetime(0, 0)', 'datetime(50, 0)', 'datetime(99, 0)', 'datetime(100, 0)', 'datetime(-1, 0)', 'datetime(1e6, 0)', 'datetime(2020, 1e10)', 'datetime(2020, 0, 1e12)', 'datetime(2020, 0, 1, 1e12)', 'datetime(2020, 0, 1, 0, 0, 0, 1e15)', 'datetime(2020, 0, 1, 0, 0, 0, 8.64e15)', 'datetime(1970, 0, 1, 0, 0, 0, -1)', 'datetime(2020, 5, 15, 12) - datetime(2020, 5, 15, 11)', 'datetime(2020, 5, 15) < datetime(2020, 5, 16)', 'datetime(2020, 5, 15) + 1',
  'utc()', 'utc(2020)', 'utc(2020, 0)', 'utc(2020, 0, 1)', 'utc(2020, 0, 1, 12, 30, 45, 500)', 'utc(2020, 5, 15)', 'utc(0, 0)', 'utc(99, 0)', 'utc(100, 0)', 'utc(-1, 0)', 'utc(NaN)', 'utc(2020, NaN)', 'utc("2020", "1")', 'utc(2020, 12)', 'utc(2020, -1)', 'utc(2020, 0, 0)', 'utc(2020, 0, 1, 24)', 'utc(275760, 8, 13)', 'utc(275760, 8, 14)', 'utc(2020.5, 0.5)', 'utc(null)', 'utc(undefined)', 'utc(2020, null)', 'utc(2020, undefined)', 'utc(true)', 'utc([2020])', 'utc(1970, 0, 1, 0, 0, 0, 0)', 'utc(1e10)', 'utc(2020, 1e10)', 'isNumber(utc(2020))', 'isDate(utc(2020))',
  'datetime(utc(2020, 0, 1, 12))', 'year(utc(2020, 11, 31, 23, 59, 59))', 'utcyear(utc(2020, 11, 31, 23, 59, 59))', 'hours(utc(2020, 5, 15, 12))', 'utchours(utc(2020, 5, 15, 12))', 'timezoneoffset(utc(2020, 0, 15))', 'timezoneoffset(utc(2020, 6, 15))', 'timezoneoffset(utc(1800, 0, 1))', 'timezoneoffset(utc(1900, 0, 1))', 'timezoneoffset(utc(2007, 2, 11, 7))', 'timezoneoffset(utc(2007, 2, 11, 6, 59))', 'timezoneoffset(utc(2007, 10, 4, 5))', 'timezoneoffset(utc(2007, 10, 4, 6))',
  'datum.d + ""', '"" + datum.d', 'toString(datum.d)', 'String', 'datum.d + 1', 'datum.d - 1', 'join([datum.d], "|")', 'pad(datum.d, 1)', 'length(datum.d + "")', 'datum.d < 1e12', 'datum.d > "2020"', 'datum.d == "x"', 'datum.d + datum.d', 'datum.d * 2', '-datum.d', '+datum.d', '~datum.d', '!datum.d', 'datum.d && 1', 'datum.d || 1', 'datum.d ? 1 : 2', 'datum.d | 0', 'datum.d >>> 0', 'toString([datum.d])', 'toString({a: datum.d})', 'toNumber([datum.d])', 'utc(2020, 0, 1) + ""', 'datetime(2020, 0, 1) + ""', 'datetime(1500, 0, 1) + ""', 'datetime(-100, 0, 1) + ""', 'datetime(NaN) + ""', 'datetime(9999, 11, 31) + ""', 'datetime(10000, 0, 1) + ""', 'datetime(20, 0, 1) + ""',
  'timeOffset("month", datum.d, 1)', 'timeOffset("day", datum.d, -1)', 'timeOffset("year", datum.d)', 'utcOffset("month", datum.d, 13)', 'timeSequence("day", datum.d, datum.d + 4 * 86400000)', 'utcSequence("month", 0, 1e11)', 'timeOffset("bogus", 0, 1)', 'timeUnitSpecifier(["year", "month"])']);

// formatting through the view's default (en-US) locale
const fmtNums = ['0', '1234.5678', '-1234.5678', '0.000123', '1e21', '1e-7', 'NaN', '1/0', '-1/0', 'null', 'datum.u', "'12'", "'abc'", 'true', '[]', '[5]', 'datum.d', 'datum.x', 'datum.f'];
const fmtSpecs = ["''", "'d'", "',d'", "',.2f'", "'.2f'", "'.3s'", "'.0%'", "'$,.2f'", "'+.1e'", "'08.2f'", "'x'", "'b'", "'c'", "'o'", "'(,.2f'", "'.4r'", "'.2g'", "'~s'", "'>10,.1f'", "'^10d'", "'#x'", "'.0f'", "'s'", "'e'", "'g'", "'r'", "'+'", "'~%'", "'z'", "'.-1f'", "'nope'", "'d3'", 'null', 'datum.u', '5'];
// format type "c" stringifies its argument, which for a Date depends on the host time zone name
for (const v of fmtNums) for (const sp of fmtSpecs) if (!(sp === "'c'" && v === 'datum.d')) add(`format(${v}, ${sp})`);
const tfSpecs = ["'%Y-%m-%d'", "'%Y-%m-%dT%H:%M:%S.%L'", "'%B %e, %Y'", "'%b %d'", "'%A %a %j %U %W %u %w'", "'%I:%M %p'", "'%y %q'", "'%Z'", "'%%'", "'%c'", "'%x %X'", "'%s %Q'", "'%V %G %g'", "''", "'abc'", "'%'", 'null', 'datum.u'];
const tfVals = ['datum.d', '0', '1e12', '-1e12', 'NaN', 'null', 'datum.u', "'2020-01-15'", "'x'", 'true', '[]', 'datetime(2020, 0, 1)', 'datetime(2020, 11, 31, 23, 59, 59, 999)', 'datetime(1999, 0, 3)', 'datetime(2021, 0, 3)', 'datetime(2020, 2, 8, 2, 30)', 'datetime(2020, 10, 1, 1, 30)', 'datetime(2000, 5, 5, 0)', 'datetime(50, 0, 1)', 'datetime(275760, 8, 13)'];
for (const f of ['timeFormat', 'utcFormat']) for (const v of tfVals) for (const sp of tfSpecs) add(`${f}(${v}, ${sp})`, {date: 1});
const tpStrs = ["'2020-01-15'", "'2020-01-15 10:30'", "'Jan 15 2020'", "'15/01/2020'", "'12/25/99'", "'10:30 PM'", "'x'", "''", 'null', 'datum.u', 'datum.s', 'datum.d', '5', '"2020-13-45"', '"1e3"', '"  2020-01-15"', '"2020-01-15  "', '"2020-01-15T10:30:15.123Z"'];
const tpSpecs = ["'%Y-%m-%d'", "'%Y-%m-%d %H:%M'", "'%b %d %Y'", "'%d/%m/%Y'", "'%m/%d/%y'", "'%I:%M %p'", "'%Y-%m-%dT%H:%M:%S.%LZ'", "'%s'", "'%Q'", "'%Z'", "'%H'", "''", "'%Y'", "'%y'", "'%j'", 'null', 'datum.u'];
for (const f of ['timeParse', 'utcParse']) for (const v of tpStrs) for (const sp of tpSpecs) add(`${f}(${v}, ${sp})`, {date: 1});
addAll(['monthFormat(0)', 'monthFormat(11)', 'monthFormat(12)', 'monthFormat(-1)', 'monthFormat(1.5)', 'monthFormat("1")', 'monthFormat(NaN)', 'monthFormat(null)', 'monthFormat()', 'monthFormat(1/0)', 'monthAbbrevFormat(0)', 'monthAbbrevFormat(5)', 'monthAbbrevFormat(13)', 'monthAbbrevFormat(1.5)', 'monthAbbrevFormat(datum.d)', 'dayFormat(0)', 'dayFormat(6)', 'dayFormat(7)', 'dayFormat(-1)', 'dayFormat(2.5)', 'dayFormat("1")', 'dayFormat()', 'dayFormat(1/0)', 'dayAbbrevFormat(0)', 'dayAbbrevFormat(3)', 'dayAbbrevFormat(8)', 'dayAbbrevFormat(NaN)', 'dayAbbrevFormat(null)', 'dayFormat(-8)', 'dayFormat(30)', 'monthFormat(1e6)', 'monthFormat(-25)'], {date: 1});

// colors
const cvals = ["'red'", "'#f00'", "'#ff0000'", "'#FF000080'", "'#f008'", "'rgb(255, 0, 0)'", "'rgba(255,0,0,0.5)'", "'rgb(100%, 0%, 0%)'", "'rgba(100%,0%,0%,0.25)'", "'hsl(120, 50%, 50%)'", "'hsla(120,50%,50%,0.3)'", "'transparent'", "'  Steelblue '", "'notacolor'", "''", "'#12'", "'#12345'", "'#1234567'", "'#gggggg'", "'rgb(1,2,3,4)'", "'rgb(1.5,2,3)'", "'rgb(300,-5,3)'", "'rgba(1,2,3,-1)'", "'rgba(1,2,3,2)'", "'hsl(400, 200%, 50%)'", "'hsl(120,0%,50%)'", "'hsl(120,50%,0%)'", "'hsl(120,50%,100%)'", "'rgb(1 2 3)'", "'rgb( 1 , 2 , 3 )'", "'rgb(+1,-2,3)'", "'RGB(1,2,3)'", "'rgb(1e1,2,3)'", "'rgb(.5%,1%,2%)'", 'null', 'undefined', '123', 'true', '[]', 'datum.s', "'currentcolor'", "'rebeccapurple'", "'lightgoldenrodyellow'", "'grey'", "'gray'", "'#ABC'", "'#aabbcc'", "'hsl(-30, 50%, 50%)'", "'hsl(30deg, 50%, 50%)'", "'hsl(30, 50, 50)'"];
for (const f of ['rgb', 'hsl', 'lab', 'hcl']) for (const c of cvals) { add(`${f}(${c})`, {tol: 1}); add(`${f}(${c}) + ""`, {tol: 1}); }
for (const c of cvals) { add(`luminance(${c})`, {tol: 1}); add(`contrast(${c}, "white")`, {tol: 1}); add(`contrast("black", ${c})`, {tol: 1}); }
addAll(['rgb(1,2,3)', 'rgb(1,2,3,0.5)', 'rgb(1,2,3,0)', 'rgb(1,2,3,null)', 'rgb(1,2,3,undefined)', 'rgb(1,2,3,NaN)', 'rgb(1,2)', 'rgb()', 'rgb(undefined)', 'rgb("1","2","3")', 'rgb(300,-5,3.5)', 'rgb(255.5, 0.5, -0.5)', 'rgb(NaN,0,0)', 'rgb(1,2,3) + ""', 'rgb(1,2,3,0.5) + ""', 'rgb(1,2,3,0) + ""', 'rgb(1,2,3,NaN) + ""', 'rgb(NaN,NaN,NaN) + ""', 'rgb(255.5,0,0) + ""', 'rgb(0.5,1.5,2.5) + ""', 'rgb(-0.5,254.5,255.4) + ""', 'rgb(1,2,3,0.123456789) + ""', 'rgb(1,2,3,1e-7) + ""', 'rgb(1,2,3).r', 'rgb(1,2,3).opacity', 'rgb("red").g', 'rgb(rgb(1,2,3))', 'rgb(hsl(120, 0.5, 0.5))', 'rgb(hsl(0, 0, 0.5))', 'rgb(lab(50, 20, 30))', 'rgb(hcl(30, 40, 50))', 'rgb(hcl(NaN, 0, 50))', 'rgb(lab(50, NaN, NaN))', 'rgb(hsl(NaN, NaN, 0.5))', 'rgb(hsl(400, 1.5, 0.5))', 'rgb(hsl(-40, 0.5, 0.5))',
  'hsl(120, 0.5, 0.5)', 'hsl(120, 0.5, 0.5, 0.3)', 'hsl(120, 0.5)', 'hsl()', 'hsl(NaN, NaN, NaN)', 'hsl(120, 0.5, 0.5) + ""', 'hsl(120, 0.5, 0.5).h', 'hsl(rgb(255, 0, 0))', 'hsl(rgb(0,0,0))', 'hsl(rgb(255,255,255))', 'hsl(rgb(128,128,128))', 'hsl(rgb(255,128,0))', 'hsl(rgb(0,128,255))', 'hsl(rgb(128,0,255))', 'hsl(lab(50,20,30))', 'hsl(hcl(30,40,50))', 'hsl(hsl(10,0.2,0.3))', 'hsl(rgb(1,2,3,0))',
  'lab(50, 20, 30)', 'lab(50, 20, 30, 0.5)', 'lab(50, 20)', 'lab()', 'lab(50, 20, 30) + ""', 'lab(rgb(255, 0, 0))', 'lab(rgb(0, 0, 0))', 'lab(rgb(255, 255, 255))', 'lab(rgb(128, 128, 128))', 'lab(hsl(120, 0.5, 0.5))', 'lab(hcl(30, 40, 50))', 'lab(lab(1,2,3))', 'lab(NaN,NaN,NaN)', 'lab(120, 0, 0) + ""', 'lab(-20, 0, 0) + ""', 'lab(50, 200, -200) + ""',
  'hcl(30, 40, 50)', 'hcl(30, 40, 50, 0.5)', 'hcl(30, 40)', 'hcl()', 'hcl(30, 40, 50) + ""', 'hcl(rgb(255, 0, 0))', 'hcl(rgb(0, 0, 0))', 'hcl(rgb(255, 255, 255))', 'hcl(rgb(128, 128, 128))', 'hcl(lab(50, 0, 0))', 'hcl(lab(50, 20, 30))', 'hcl(lab(150, 0, 0))', 'hcl(hsl(120, 0.5, 0.5))', 'hcl(hcl(1,2,3))', 'hcl(NaN, NaN, NaN)', 'hcl(NaN, 0, 50) + ""', 'hcl(400, 30, 50) + ""', 'hcl(30, -40, 50) + ""',
  'luminance("red")', 'luminance("white")', 'luminance("black")', 'luminance(rgb(1,2,3))', 'luminance(hsl(120, 0.5, 0.5))', 'luminance(lab(50,0,0))', 'luminance()', 'luminance("transparent")', 'contrast("white", "black")', 'contrast("red", "blue")', 'contrast("#777", "#fff")', 'contrast("black", "black")', 'contrast()', 'contrast("red")', 'contrast(rgb(1,2,3), hsl(4, 0.5, 0.5))', 'isObject(rgb(1,2,3))', 'isDate(rgb(1,2,3))', 'toString(rgb(1,2,3))', 'rgb(1,2,3) == "rgb(1, 2, 3)"', 'rgb(1,2,3) === rgb(1,2,3)', 'length(rgb(1,2,3))', 'datum.s + rgb(1,2,3)', 'rgb(datum.x, datum.y, 3) + ""', 'rgb(datum.s) + ""', 'rgb(datum.d) + ""', '"a" + hsl(1,0.5,0.5)', '"a" + lab(1,2,3)', '"a" + hcl(1,2,3)', 'join([rgb(1,2,3), hsl(1,0.5,0.5)], "|")', 'pad(rgb(1,2,3), 20, "-")'].map(e => e), {tol: 1});

// probability functions
const pv = ['0', '0.5', '1', '-1', '2', '0.1', '0.999', '1e-9', '-3', '40', 'NaN', 'undefined', "'1'"];
const pm = [[], ['0'], ['1'], ['1', '2'], ['-1', '0.5'], ['0', '0'], ['null', 'null'], ['undefined', '5'], ['0', 'null'], ['"2"', '"3"'], ['2', '-1']];
for (const f of ['densityNormal', 'cumulativeNormal', 'quantileNormal', 'densityLogNormal', 'cumulativeLogNormal', 'quantileLogNormal', 'densityUniform', 'cumulativeUniform', 'quantileUniform'])
  for (const v of pv) for (const m of pm) add(`${f}(${[v, ...m].join(', ')})`, {tol: 1});
addAll(['densityNormal()', 'cumulativeNormal()', 'quantileNormal()', 'quantileNormal(0)', 'quantileNormal(1)', 'quantileNormal(0.5)', 'quantileNormal(0.975)', 'quantileNormal(1e-300)', 'quantileNormal(1-1e-16)', 'quantileNormal(0.999999)', 'cumulativeNormal(-8)', 'cumulativeNormal(8)', 'cumulativeNormal(-7.0710678)', 'cumulativeNormal(7.0710679)', 'cumulativeNormal(1.96)', 'cumulativeNormal(38)', 'cumulativeNormal(-38)', 'densityNormal(0, 0, 0)', 'densityUniform(0.5)', 'densityUniform(0.5, 0, 0)', 'densityUniform(5, 1, 2)', 'densityUniform(1.5, 1, 2)', 'cumulativeUniform(1.5, 1, 2)', 'cumulativeUniform(0.5, 1, 2)', 'cumulativeUniform(3, 1, 2)', 'quantileUniform(0.5, 1, 2)', 'quantileUniform(2, 1, 2)', 'quantileUniform(-1, 1, 2)', 'quantileUniform(0.5, 10)', 'cumulativeLogNormal(1)', 'cumulativeLogNormal(-1)', 'cumulativeLogNormal(0)', 'quantileLogNormal(0.5, 1, 2)', 'densityLogNormal(1)', 'densityLogNormal(0)', 'densityLogNormal(-1)', 'densityLogNormal(2, 1, 0.5)'], {tol: 1});
const seeded = [];
for (const e of ['random()', 'random() + random()', 'sampleNormal()', 'sampleNormal(5)', 'sampleNormal(5, 2)', 'sampleNormal(null, null)', 'sampleNormal() + sampleNormal() + sampleNormal()', 'sampleLogNormal()', 'sampleLogNormal(1, 0.5)', 'sampleLogNormal() * sampleNormal()', 'sampleUniform()', 'sampleUniform(5)', 'sampleUniform(1, 3)', 'sampleUniform(null, 3)', 'sampleUniform(3, 1)', 'length(sequence(3)) + random()', '[random(), random(), random()]', 'sampleNormal(0, 0)', 'sampleNormal("1", "2")', 'floor(random() * 10)', 'sampleNormal(1, 1) * sampleUniform(1, 2) + random()', 'sampleLogNormal("1")', 'sampleUniform("1", "2")', 'sampleUniform(1, undefined)', 'sampleUniform(undefined, 2)']) for (const seed of [1, 42, 12345]) seeded.push({e, seed, tol: 1});
cases.push(...seeded);

// scale / data / env functions with an empty runtime
addAll(['scale("nosuch", 1)', 'domain("nosuch")', 'range("nosuch")', 'bandwidth("nosuch")', 'invert("nosuch", 1)', 'copy("nosuch")', 'bandspace(3, 0.1, 0.2)', 'bandspace(3)', 'bandspace()', 'bandspace(0, 1, 1)', 'bandspace(1, 1, 0)', 'bandspace(1, 2, 0)', 'bandspace(3, 0.5, 0.5)', 'bandspace("3", "0.5", "0.5")', 'bandspace(null, 1, 1)', 'bandspace(5, null, undefined)', 'bandspace(2, 1.5, 0)', 'bandspace(NaN, 1, 1)', 'bandspace(3, NaN, 0)', 'bandspace(2.5, 0, 0)', 'bandspace(-1, 0, 0)', 'bandspace(1e10, 0.3, 0.1)',
  'scale(datum.s, 1)', 'scale()', 'domain(1)', 'data("tbl")', 'length(data("tbl"))', 'pluck(data("tbl"), "a")', 'data("tbl")[1].b', 'data("nosuch")', 'length(data("nosuch"))', 'data()', 'data(datum.s)', 'data("tbl", 1)', 'indata("tbl", "a", 2)', 'indata("tbl", "a", 5)', 'indata("tbl", "b", "x")', 'indata("nosuch", "a", 1)', 'indata("tbl", datum.s, 1)', 'indata(datum.s, "a", 1)', 'indata("tbl")', 'treePath("tbl", 1, 2)', 'treeAncestors("tbl", 1)', 'treePath("nosuch", 1, 2)', 'treeAncestors("nosuch", 1)', 'treePath(datum.s, 1, 2)', 'geoArea("p", 1)', 'geoBounds("p", 1)', 'geoCentroid("p", 1)', 'geoScale("p")', 'gradient("nosuch", [0,0], [1,0])',
  'containerSize()', 'windowSize()', 'screen()', 'encode()', 'encode(null, "x")', 'encode(null, "x", 5)', 'encode(datum, "x")', 'inScope(null)', 'inScope(datum)', 'intersect()', 'intersect(null)', 'intersect([[0,0],[1,1]])', 'warn("x")', 'warn("x", 5)', 'warn()', 'info(1, 2, 3)', 'debug("d")', 'warn(datum.x)', 'isTuple(datum)', 'isTuple({})', 'lassoAppend([], 1, 2)', 'lassoAppend([[0,0]], 1, 2)', 'lassoAppend([[0,0]], 10, 2)', 'lassoAppend([[0,0]], 3, 4)', 'lassoAppend([[0,0]], 3, 4.0001)', 'lassoAppend([[0,0]], 1, 1, 1)', 'lassoAppend([[0,0]], 1, 1, 1.4)', 'lassoAppend([[0,0]], 1, 1, 1.5)', 'lassoAppend(null, 1, 2)', 'lassoAppend(undefined, 1, 2)', 'lassoAppend([[0,0]], NaN, 2)', 'lassoAppend([[0,0]], 6, 0, undefined)', 'lassoAppend([[0,0]], 6, 0, null)', 'lassoAppend([[0,0]], 6, 0, 10)', 'lassoAppend(5, 1, 2)', 'lassoAppend([undefined], 1, 2)', 'lassoAppend([null], 1, 2)', 'lassoAppend([[0,0],[5,5]], "10", "10")', 'lassoAppend("ab", 1, 2)', 'lassoAppend()', 'lassoPath([])', 'lassoPath([[0,0]])', 'lassoPath([[0,0],[1,1]])', 'lassoPath([[0,0],[1,1],[2,2]])', 'lassoPath([[0,0],[1,1],[2,2],[3,3]])', 'lassoPath([[0.5,1.5],[1e21,-0],[NaN,3]])', 'lassoPath(null)', 'lassoPath(undefined)', 'lassoPath([null])', 'lassoPath([5])', 'lassoPath(["ab","cd"])', 'lassoPath([[1],[2],[3]])', 'lassoPath([[1,2,3],[4,5,6]])', 'lassoPath()', 'lassoPath([[0,0]], 5)', 'lassoPath({})', 'lassoPath("ab")', 'lassoPath([[datum.d, 1],[2,3]])',
  'pinchDistance({touches: [{clientX: 0, clientY: 0}, {clientX: 3, clientY: 4}]})', 'pinchAngle({touches: [{clientX: 0, clientY: 0}, {clientX: 3, clientY: 4}]})', 'pinchAngle({touches: [{clientX: 3, clientY: 4}, {clientX: 0, clientY: 0}]})', 'pinchDistance({touches: [{clientX: 1e200, clientY: 0}, {clientX: 0, clientY: 1e200}]})', 'pinchDistance({touches: [{clientX: "1"}, {clientX: 3}]})', 'pinchDistance({})', 'pinchDistance(null)', 'pinchDistance()', 'pinchDistance({touches: []})', 'pinchDistance({touches: [{}]})', 'pinchAngle({touches: [{clientX: 0, clientY: 0}, {clientX: 0, clientY: 0}]})', 'pinchAngle({touches: [{clientX: -1, clientY: 0}, {clientX: 1, clientY: -0}]})', 'pinchAngle({touches: [{clientX: 0, clientY: 1}, {clientX: 0, clientY: 0}]})', 'pinchAngle(event)', 'pinchDistance(datum)', 'pinchAngle({touches: 5})', 'pinchAngle({touches: "ab"})'], {tol: 1});
const pz = ['[0, 10]', '[10, 0]', '[-5, 5]', '[1, 100]', '[-100, -1]', '[0, 0]', '[5]', '[]', '["0", "10"]', '[undefined, 10]', '[0, 1, 2, 10]', 'null', '5', '[1e-9, 1e9]', 'datum.a'];
for (const f of ['panLinear', 'panLog', 'panPow', 'panSymlog']) for (const d of pz) for (const dl of ['0.1', '-0.25', '0', 'undefined']) for (const ex of f === 'panPow' || f === 'panSymlog' ? ['2', '0.5', 'undefined'] : ['']) add(`${f}(${[d, dl, ex].filter(x => x).join(', ')})`, {tol: 1});
for (const f of ['zoomLinear', 'zoomLog', 'zoomPow', 'zoomSymlog']) for (const d of pz) for (const an of ['5', 'null', '0', '"3"']) for (const sc of ['2', '0.5', '0', 'undefined']) for (const ex of f === 'zoomPow' || f === 'zoomSymlog' ? ['2', '0.5', 'undefined'] : ['']) add(`${f}(${[d, an, sc, ex].filter(x => x).join(', ')})`, {tol: 1});
addAll(['panLinear()', 'panLinear([0,10])', 'zoomLinear([0,10])', 'zoomLinear([0,10], 5)', 'zoomLog([1,100], 10, 2)', 'zoomPow([0,10], 5, 2)', 'zoomPow([0,10], 5, 2, 0.5)', 'zoomSymlog([0,10], 5, 2, 1)', 'panSymlog([-10,10], 0.5, 1)', 'panPow([1,9], 0.5, 0.5)', 'panPow([-9,9], 0.5, 0.5)', 'zoomPow([-9,9], 0, 2, 0.5)', 'zoomLog([-100,-1], -10, 2)', 'panLog([-100,-1], 0.1)'], {tol: 1});

// data-driven selection functions, exercised in the Go test with the same store
addAll(['vlSelectionTest("nosel", datum)', 'vlSelectionTest("nosel", datum, "intersect")', 'vlSelectionTest("tbl", datum)', 'vlSelectionTest(datum.s, datum)', 'vlSelectionTest()', 'vlSelectionResolve("nosel")']);
const noDatum = new Set(['sel']);
for (const st of Object.keys(stores)) {
  for (const op of ['', ', "union"', ', "intersect"', ', "other"']) {
    add(`vlSelectionTest("${st}", datum${op})`, {date: 1, solo: st === 'selD'});
    add(`vlSelectionIdTest("${st}", datum${op})`);
  }
  for (const args of ['', ', "union"', ', "intersect"', ', "other"', ', undefined', ', "union", true', ', "intersect", true', ', "union", true, true', ', "intersect", true, true', ', null, false, true']) add(`vlSelectionResolve("${st}"${args})`, {date: 1, solo: st === 'selD'});
}
// The field definitions of the base object are mutated by upstream (a cached getter is
// attached), so only the tuple values are compared when there are fields.
addAll(['pluck(vlSelectionTuples([{datum: datum}, {datum: {x: 1, y: 2}}], {unit: "u1", fields: [{field: "x", type: "E"}]}), "values")', 'pluck(vlSelectionTuples([{datum: datum}, {datum: {x: 1, y: 2}}], {unit: "u1", fields: [{field: "x", type: "E"}]}), "unit")', 'vlSelectionTuples([{datum: datum}], {unit: "u1"})', 'vlSelectionTuples([{datum: {_vgsid_: 9}}], {unit: "u1", values: 5})', 'vlSelectionTuples([], {})', 'vlSelectionTuples(1, {})', 'vlSelectionTuples([], 1)', 'vlSelectionTuples([], null)', 'pluck(vlSelectionTuples([{datum: datum}], {fields: [{field: "o.a.b"}, {field: "a[1]"}]}), "values")', 'pluck(vlSelectionTuples([{}], {fields: []}), "values")', 'vlSelectionTuples([{datum: null}], {fields: [{field: "x"}]})', 'vlSelectionTuples([{datum: null}], {})', 'vlSelectionTuples([{datum: datum}], {fields: "x"})', 'vlSelectionTuples([{datum: datum}], [1])', 'vlSelectionTuples([{datum: datum}], "ab")', 'vlSelectionTuples([{datum: datum}], /a/)', 'vlSelectionTuples([{datum: datum}], {unit: "u", values: 7})', 'vlSelectionTuples([{datum: datum}], {_vgsid_: 4})']);

stores.sel = stores.sel;
const out = {stores: Object.fromEntries(Object.entries(stores).map(([k, v]) => [k, v.map(enc)])), datums: datums.map(enc), tbl: tbl.map(enc), signals: Object.fromEntries(Object.entries(signals).map(([k, v]) => [k, enc(v)])), cases: []};

// ---- evaluation ----
// A datum-independent expression gives the same result for every datum; keep one.
function compress(rs) {
  const first = JSON.stringify(rs[0]);
  return rs.every(r => JSON.stringify(r) === first) ? [rs[0]] : rs;
}

function specFor(exprs, only) {
  return {
    signals: Object.entries(signals).map(([name, value]) => ({name, value})),
    // Data sets referenced by expressions must be declared before the one that
    // uses them.
    data: [
      {name: 'tbl', values: tbl},
      ...Object.entries(structuredClone(stores)).map(([name, values]) => ({name, values})),
      {name: 'd', values: only ? datums.slice(0, 1) : datums, transform: exprs.map((e, i) => ({type: 'formula', expr: e, as: 'r' + i}))},
    ],
  };
}

async function evalBatch(exprs, only) {
  const errs = [];
  const logger = {level() { return this; }, error(...a) { errs.push(a.map(String).join(' ')); return this; }, warn() { return this; }, info() { return this; }, debug() { return this; }};
  const view = new vega.View(vega.parse(specFor(exprs, only)), {renderer: 'none', logger});
  await view.runAsync();
  const rows = view.data('d');
  if (!rows) return {errs};
  return {rows, errs};
}

async function evalOne(c) {
  const rec = {e: c.e};
  if (c.seed) rec.seed = c.seed;
  if (c.tol) rec.tol = 1;
  if (c.date) rec.date = 1;
  let pre = null;
  try {
    if (c.seed) {
      // sampleNormal keeps the second value of each Box-Muller pair in module
      // state; drain it so every seeded case starts clean.
      let used = 0;
      const seq = [0.25, 0.75, 0.6, 0.3];
      vega.setRandom(() => seq[used++ % 4]);
      vega.sampleNormal();
      if (used > 0) vega.sampleNormal();
      vega.setRandom(vega.randomLCG(c.seed));
    }
    pre = await evalBatch([c.e], !!c.seed);
  } catch (err) {
    rec.parseError = String(err.message || err);
    return rec;
  }
  if (!pre.rows || pre.errs.length) { rec.error = pre.errs.join(' | ') || 'no rows'; return rec; }
  rec.results = compress(pre.rows.map(t => enc(t.r0)));
  if (!c.seed && (c.e.includes('now()') || c.e.includes('datetime()'))) rec.skip = 1;
  return rec;
}

// Batch evaluate the well-behaved cases quickly; anything that throws is
// re-run alone to attribute the failure.
const selected = cases.filter(c => mode !== 'dates' || c.date || /date|time|year|month|day|hour|minute|second|week|quarter|utc|toDate/i.test(c.e));
const seen = new Set();
const uniq = selected.filter(c => { const k = c.e + '|' + (c.seed || ''); if (seen.has(k)) return false; seen.add(k); return true; });
const plain = uniq.filter(c => !c.seed && !c.solo);
const BATCH = 40;
const results = new Map();
for (let i = 0; i < plain.length; i += BATCH) {
  const chunk = plain.slice(i, i + BATCH);
  if (process.env.GEN_DEBUG) console.error('batch', i, chunk[0].e);
  let ok = false;
  try {
    const r = await evalBatch(chunk.map(c => c.e));
    if (r.rows && !r.errs.length) {
      chunk.forEach((c, j) => {
        const rec = {e: c.e, results: compress(r.rows.map(t => enc(t['r' + j])))};
        if (c.tol) rec.tol = 1;
        if (c.date) rec.date = 1;
        if (c.e.includes('now()') || c.e.includes('datetime()')) rec.skip = 1;
        results.set(c.e, rec);
      });
      ok = true;
    }
  } catch (err) { /* fall through to one-by-one */ }
  if (!ok) for (const c of chunk) results.set(c.e, await evalOne(c));
}
for (const c of uniq) out.cases.push(c.seed || c.solo ? await evalOne(c) : results.get(c.e));
process.stdout.write(JSON.stringify(out) + '\n');
