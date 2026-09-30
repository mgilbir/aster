// Records vega-scale / d3-scale behaviour: each case builds a scale through
// vega-scale's registry, applies a list of setter operations, then runs queries.
// Run: NODE_PATH=<node_modules> node gen_scales.mjs | gzip -9 > scales.json.gz
import { C, emit, NaNv, U, INF, NINF, D, O } from './gen_common.mjs';
const A = (xs) => xs.map((x) => ['apply', x]);
const NUMS = [-1e9, -10.5, -1, 0, 0.25, 0.5, 1, 1.5, 3, 5, 7.5, 10, 11, 100, 1e6, NaNv, null, U, '5', 'abc', true, INF, NINF, '', [7], D(5)];
const SMALL = [-1, 0, 0.25, 0.3, 0.5, 1, 2, 5, 7.5, 10, 20, NaNv, null, U];
const INV = [-1e6, -1, 0, 0.5, 1, 5, 10, 50, 100, 200, NaNv, null, U, '50'];
const TICKCOUNTS = [null, 1, 3, 5, 10, 100, 0, NaNv, INF];

// ---- linear ------------------------------------------------------------------
const doms = [[0, 1], [0, 10], [10, 0], [5, 5], [], [0], [0, 5, 10], [10, 5, 0], [0, NaNv], [0, 10, 5], [-1e9, 1e9], ['0', '10'], [null, 10]];
const rngs = [[0, 100], [100, 0], [0, 1, 2], [0], [5, 5], [0, 100, 50]];
for (const type of ['linear']) for (const d of doms) for (const r of rngs) for (const clamp of [false, true]) {
  C(`${type} d=${JSON.stringify(d)} r=${JSON.stringify(r)} clamp=${clamp}`, type,
    [['domain', d], ['range', r], ['clamp', clamp]],
    [...A(NUMS), ...INV.map((y) => ['invert', y]), ['domain'], ['range'], ['clamp'], ...TICKCOUNTS.map((c) => ['ticks', c])]);
}
// unknown / rangeRound / copy
C('linear unknown', 'linear', [['domain', [0, 10]], ['range', [0, 1]], ['unknown', '?']], A(NUMS));
C('linear unknown num', 'linear', [['domain', [0, 10]], ['range', [0, 1]], ['unknown', -1]], A(NUMS));
C('linear rangeRound', 'linear', [['domain', [0, 3]], ['rangeRound', [0, 10]]], A(NUMS));
C('linear rangeRound rev', 'linear', [['domain', [0, 3]], ['rangeRound', [10, 0]], ['clamp', true]], A(NUMS));
C('linear rangeRound poly', 'linear', [['domain', [0, 1, 3]], ['rangeRound', [0, 5, 6.5]]], A(NUMS));
C('linear copy', 'linear', [['domain', [0, 3]], ['range', [0, 10]], ['clamp', true], ['copy'], ['domain', [0, 6]]], [...A(NUMS), ['domain'], ['clamp']]);
C('linear nice', 'linear', [['domain', [0.13, 9.7]], ['range', [0, 1]], ['nice', null]], [['domain'], ...TICKCOUNTS.map((c) => ['ticks', c])]);
for (const d of [[0.13, 9.7], [9.7, 0.13], [1234.5, 98765.4], [-3.7, 4.1], [0, 0], [5, 5], [0.0001, 0.00043], [1e-9, 5e-9], [0, 1e21], [NaNv, 5], [3.3, 3.31, 7.4]])
  for (const n of [null, 1, 2, 3, 5, 10, 20, 0.5, 100]) {
    C(`linear nice d=${JSON.stringify(d)} n=${n}`, 'linear', [['domain', d], ['nice', n]], [['domain'], ['ticks', null], ['ticks', 5]]);
  }
// polyline scale queries
for (const d of [[0, 5, 10, 20], [20, 10, 5, 0], [0, 5, 5, 10], [1, 10, 100]])
  C(`linear poly ${JSON.stringify(d)}`, 'linear', [['domain', d], ['range', [0, 10, 40, 50].slice(0, d.length)]],
    [...A([-5, 0, 1, 5, 7, 10, 15, 20, 25, 50, 100, NaNv, null]), ...[-5, 0, 5, 10, 20, 30, 40, 50, 60, null, NaNv].map((y) => ['invert', y])]);

// ---- color ranges ---------------------------------------------------------------
const colorRanges = [['red', 'blue'], ['#ff0000', '#00ff00', '#0000ff'], ['rgba(255,0,0,0.5)', 'hsl(120,50%,50%)'], ['red'], ['notacolor', 'blue'], ['red', 5], ['#fff', 'transparent'], ['steelblue', 'orange', 'green', 'purple']];
const interps = [null, ['rgb'], ['rgb', 2.2], ['rgb', 0.5], ['hsl'], ['hsl-long'], ['lab'], ['hcl'], ['hcl-long'], ['cubehelix'], ['cubehelix-long'], ['cubehelix', 2], ['number'], ['round'], ['string']];
const cq = [-1, 0, 0.1, 0.3, 0.5, 0.7, 1, 2, 5, 7.5, 10, NaNv, null, U];
for (const r of colorRanges) for (const it of interps) for (const d of [[0, 1], [0, 10], [10, 0], [0, 5, 10]]) {
  if (d.length === 3 && r.length < 3) continue;
  C(`color r=${JSON.stringify(r)} i=${JSON.stringify(it)} d=${JSON.stringify(d)}`, 'linear',
    [['domain', d], ['range', r], ...(it ? [['interpolate', it]] : [])], A(cq));
}
// other value types in ranges
for (const r of [['0px', '100px'], ['M0,0L1,1', 'M10,20L30,40'], ['a', 'b'], [true, false], [null, 5], [O({ a: 0 }), O({ a: 10, b: 1 })], [[0, 0], [10, 20]], [[0, 0], [10, 20, 30]], [D(0), D(1e12)], ['10', '20'], ['1e3', '2e3'], ['translate(0,0)', 'translate(10,20)']])
  C(`value range ${JSON.stringify(r)}`, 'linear', [['domain', [0, 1]], ['range', r]], A(cq));
C('linear string interp 3', 'linear', [['domain', [0, 1, 2]], ['range', ['0px 0px', '10px 5px', '20px 50px']]], A([-1, 0, 0.5, 1, 1.5, 2, 3]));

// ---- log -------------------------------------------------------------------
const logDoms = [[1, 10], [1, 1000], [0.1, 100], [-1000, -1], [-10, 10], [0, 10], [1, 1], [10, 1], [1e-5, 1e5], [1, 2, 4, 8], [0.5, 50], [3, 7], [1e-300, 1e300], [-1, -1000], [5, 5000]];
const logPos = [0.001, 0.5, 1, 2, 3, 5, 9, 10, 50, 99, 100, 1000, 12345, 1e6, 0, -1, -50, NaNv, null, U];
for (const base of [10, 2, Math.E, 1.5, 1, 0.5])
  for (const d of logDoms) for (const clamp of [false, true]) {
    C(`log base=${base} d=${JSON.stringify(d)} clamp=${clamp}`, 'log',
      [['base', base], ['domain', d], ['range', [0, 100]], ['clamp', clamp]],
      [...A(logPos), ...[-1, 0, 25, 50, 100, 150, NaNv].map((y) => ['invert', y]), ['base'], ['domain'], ...TICKCOUNTS.map((c) => ['ticks', c])]);
  }
for (const d of logDoms) for (const base of [10, 2, 3]) {
  C(`log nice base=${base} d=${JSON.stringify(d)}`, 'log', [['base', base], ['domain', d], ['nice', null]], [['domain'], ['ticks', null], ['ticks', 3]]);
}
C('log color', 'log', [['domain', [1, 100]], ['range', ['red', 'blue']]], A([0.5, 1, 3, 10, 50, 100, 200]));
C('log copy', 'log', [['base', 2], ['domain', [1, 64]], ['range', [0, 6]], ['copy'], ['domain', [1, 32]]], [...A([1, 2, 4, 8, 32, 64]), ['base'], ['domain']]);
for (const d of [[-1000, 1000], [-100, -1], [0.5, 500], [1, 1e6], [1e-6, 1], [0, 1000]])
  for (const n of [1, 2, 3, 4, 5, 10, 30, 100]) C(`log ticks d=${JSON.stringify(d)} n=${n}`, 'log', [['domain', d]], [['ticks', n]]);
for (const base of [16, 2.5, 8]) C(`log ticks base=${base}`, 'log', [['base', base], ['domain', [1, 1e6]]], [['ticks', 10], ['ticks', 2]]);

// ---- pow / sqrt / symlog ----------------------------------------------------
const powDoms = [[0, 1], [-10, 10], [1, 100], [0, 100], [100, 0], [4, 4], [-100, -1], [0, 1, 4]];
const powX = [-100, -10, -4, -1, -0.25, 0, 0.25, 1, 2, 4, 9, 10, 50, 100, NaNv, null, U];
for (const e of [1, 0.5, 2, 3, -1, 0.3, 0, 1 / 3, 1.5]) for (const d of powDoms) for (const clamp of [false, true]) {
  C(`pow e=${e} d=${JSON.stringify(d)} clamp=${clamp}`, 'pow',
    [['exponent', e], ['domain', d], ['range', d.length === 3 ? [0, 50, 100] : [0, 100]], ['clamp', clamp]],
    [...A(powX), ...[-10, 0, 25, 50, 75, 100, 200, NaNv].map((y) => ['invert', y]), ['exponent'], ['ticks', null], ['ticks', 4]]);
}
for (const d of powDoms) C(`sqrt d=${JSON.stringify(d)}`, 'sqrt', [['domain', d], ['range', d.length === 3 ? [0, 50, 100] : [0, 100]]],
  [...A(powX), ...[0, 25, 50, 100, NaNv].map((y) => ['invert', y]), ['exponent'], ['ticks', 5]]);
C('pow nice', 'pow', [['exponent', 2], ['domain', [0.13, 9.7]], ['nice', null]], [['domain'], ['ticks', 5]]);
C('pow copy', 'pow', [['exponent', 3], ['domain', [0, 2]], ['copy'], ['domain', [0, 3]]], [...A([0, 1, 2, 3]), ['exponent']]);
for (const k of [1, 10, 0.1, 100, 0, -1, 0.5]) for (const d of [[-100, 100], [0, 1000], [-1, 1], [1, 100], [100, -100]]) for (const clamp of [false, true]) {
  C(`symlog c=${k} d=${JSON.stringify(d)} clamp=${clamp}`, 'symlog', [['constant', k], ['domain', d], ['range', [0, 100]], ['clamp', clamp]],
    [...A([-1000, -100, -10, -1, -0.1, 0, 0.1, 1, 10, 100, 1000, NaNv, null]), ...[-10, 0, 25, 50, 100, 200].map((y) => ['invert', y]), ['constant'], ['ticks', null], ['ticks', 4]]);
}
C('symlog nice', 'symlog', [['domain', [-13.7, 921]], ['nice', null]], [['domain'], ['ticks', 5]]);

// ---- identity ---------------------------------------------------------------
C('identity', 'identity', [['domain', [0, 10]]], [...A(NUMS), ...INV.map((y) => ['invert', y]), ['domain'], ['range'], ['ticks', 5], ['ticks', null]]);
C('identity unknown', 'identity', [['unknown', 'x']], A(NUMS));
C('identity nice', 'identity', [['domain', [0.13, 9.7]], ['nice', 5]], [['domain'], ['range']]);

// ---- sequential / diverging --------------------------------------------------
const seqTypes = ['sequential', 'sequential-linear', 'sequential-log', 'sequential-pow', 'sequential-sqrt', 'sequential-symlog'];
const seqDoms = [[0, 1], [0, 100], [100, 0], [1, 1000], [5, 5], [-10, 10], [NaNv, 1], [0.1, 10]];
const seqX = [-100, -10, -1, 0, 0.1, 0.5, 1, 2, 5, 10, 50, 100, 500, 1000, NaNv, null, U, '3'];
for (const t of seqTypes) for (const d of seqDoms) for (const clamp of [false, true]) {
  for (const r of [['red', 'blue'], [0, 10], null]) {
    C(`${t} d=${JSON.stringify(d)} clamp=${clamp} r=${JSON.stringify(r)}`, t,
      [['domain', d], ...(r ? [['range', r]] : []), ['clamp', clamp]],
      [...A(seqX), ['domain'], ['range'], ['clamp'], ['ticks', null], ['ticks', 4]]);
  }
}
C('sequential rangeRound', 'sequential', [['domain', [0, 3]], ['rangeRound', [0, 10]]], A([-1, 0, 0.5, 1, 2, 3, 4]));
C('sequential unknown', 'sequential', [['domain', [0, 3]], ['range', [0, 10]], ['unknown', 'u']], A([NaNv, null, U, 1]));
C('sequential nice', 'sequential', [['domain', [0.13, 9.7]], ['nice', null]], [['domain'], ['ticks', 5]]);
C('sequential-log nice', 'sequential-log', [['domain', [0.13, 970]], ['nice', null]], [['domain']]);
C('sequential exponent', 'sequential-pow', [['exponent', 2], ['domain', [0, 10]], ['range', [0, 1]]], [...A([0, 5, 10]), ['exponent']]);
C('sequential-log base', 'sequential-log', [['base', 2], ['domain', [1, 8]], ['range', [0, 1]]], [...A([1, 2, 4, 8]), ['base']]);
C('sequential-symlog constant', 'sequential-symlog', [['constant', 10], ['domain', [0, 100]], ['range', [0, 1]]], [...A([0, 10, 100]), ['constant']]);
C('sequential copy', 'sequential-sqrt', [['domain', [0, 4]], ['range', ['red', 'blue']], ['clamp', true], ['copy'], ['domain', [0, 9]]], [...A([0, 4, 9, 16]), ['clamp']]);
const divTypes = ['diverging-linear', 'diverging-log', 'diverging-pow', 'diverging-sqrt', 'diverging-symlog'];
const divDoms = [[0, 0.5, 1], [-10, 0, 10], [10, 0, -10], [0, 0, 10], [0, 10, 10], [1, 1, 1], [0.1, 1, 10], [1, 10, 1000], [-5, 1, 20], [NaNv, 0, 1], [0, 5, 100]];
for (const t of divTypes) for (const d of divDoms) for (const clamp of [false, true]) {
  for (const r of [['red', 'white', 'blue'], [0, 5, 20], null]) {
    C(`${t} d=${JSON.stringify(d)} clamp=${clamp} r=${JSON.stringify(r)}`, t,
      [['domain', d], ...(r ? [['range', r]] : []), ['clamp', clamp]],
      [...A([...seqX, -1000, 0.05, 0.9, 20]), ['domain'], ['range'], ['ticks', null], ['ticks', 4]]);
  }
}
C('diverging null', 'diverging-linear', [['domain', [-1, 0, 1]], ['range', [0, 1, 2]], ['unknown', 'unk']], A([null, U, NaNv, 0, '']));
C('diverging nice', 'diverging-linear', [['domain', [-9.7, 0, 3.2]], ['nice', null]], [['domain']]);
C('diverging rangeRound', 'diverging-linear', [['domain', [0, 5, 10]], ['rangeRound', [0, 3, 10]]], A([0, 1, 5, 6, 10]));

// ---- discretizing -------------------------------------------------------------
const qdoms = [[1, 2, 3, 4, 5, 6, 7, 8, 9, 10], [10, 1, 5, 3], [3], [], [1, NaNv, null, U, 'x', '4', 8, 2], [5, 5, 5, 5], [0, 100], [1.5, 2.5, 2.5, 100, -3]];
for (const d of qdoms) for (const r of [[0, 1], [0, 1, 2, 3], ['a', 'b', 'c'], ['x'], [], [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]]) {
  C(`quantile d=${JSON.stringify(d)} r=${JSON.stringify(r)}`, 'quantile', [['domain', d], ['range', r]],
    [...A([-10, 0, 1, 2, 2.5, 3, 4.9, 5, 6, 7, 8, 9, 10, 100, NaNv, null, U, '7']), ['quantiles'], ['domain'],
      ...r.slice(0, 5).map((y) => ['invertExtent', y]), ['invertExtent', 'zzz'], ['invertRange', [1, 5]], ['invertRange', [5, 1]], ['invertRange', [100, 200]]]);
}
C('quantile unknown', 'quantile', [['domain', [1, 2, 3, 4]], ['range', [0, 1]], ['unknown', 'u']], A([NaNv, null, 2, 5]));
C('quantile copy', 'quantile', [['domain', [1, 2, 3, 4]], ['range', [0, 1]], ['copy'], ['domain', [10, 20, 30, 40]]], [...A([1, 15, 25, 35]), ['quantiles']]);
for (const d of [[0, 10], [10, 0], [5, 5], [0, 1], [-5, 5], [0, NaNv]]) for (const r of [[0, 1], [0, 1, 2], ['a', 'b', 'c', 'd'], ['x'], [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]]) {
  C(`quantize d=${JSON.stringify(d)} r=${JSON.stringify(r)}`, 'quantize', [['domain', d], ['range', r]],
    [...A([-10, -1, 0, 1, 2, 3.3, 3.4, 5, 6.6, 7.5, 9.99, 10, 11, 100, NaNv, null, U, '5']), ['thresholds'], ['domain'],
      ...r.slice(0, 6).map((y) => ['invertExtent', y]), ['invertExtent', 'zzz'], ['invertRange', [1, 5]], ['invertRange', [5, 1]], ['ticks', 5], ['ticks', null]]);
}
C('quantize nice', 'quantize', [['domain', [0.13, 9.7]], ['range', [0, 1, 2]], ['nice', null]], [['domain'], ['thresholds']]);
C('quantize unknown', 'quantize', [['domain', [0, 10]], ['range', [0, 1]], ['unknown', 'u']], A([NaNv, null, 5]));
for (const d of [[0.5], [0, 10, 20], [10, 0], [5, 5], [], [1, 2, 3, 4, 5], [0, NaNv, 5]]) for (const r of [[0, 1], [0, 1, 2], ['a', 'b', 'c', 'd'], ['x'], [], [0, 1, 2, 3, 4, 5, 6]]) {
  C(`threshold d=${JSON.stringify(d)} r=${JSON.stringify(r)}`, 'threshold', [['domain', d], ['range', r]],
    [...A([-10, -1, 0, 0.5, 1, 5, 10, 15, 20, 25, 'b', 'c', NaNv, null, U, '10']), ['domain'], ['range'],
      ...r.slice(0, 5).map((y) => ['invertExtent', y]), ['invertExtent', 'zzz'], ['invertRange', [1, 5]], ['invertRange', [5, 1]]]);
}
C('threshold unknown', 'threshold', [['domain', [0, 10]], ['range', [0, 1, 2]], ['unknown', 'u']], A([NaNv, null, 5]));

// bin-ordinal
for (const d of [[0, 10, 20, 30], [30, 20, 10, 0], [], [5], [0, 5, 5, 10]]) for (const r of [['a', 'b', 'c'], ['a'], [], ['a', 'b', 'c', 'd', 'e']]) {
  C(`bin-ordinal d=${JSON.stringify(d)} r=${JSON.stringify(r)}`, 'bin-ordinal', [['domain', d], ['range', r]],
    [...A([-5, 0, 3, 10, 15, 20, 29.9, 30, 35, 100, NaNv, null, U, '12', 'abc']), ['domain'], ['range']]);
}

// ordinal
C('ordinal implicit', 'ordinal', [['range', ['a', 'b', 'c']]], [...A(['x', 'y', 'z', 'w', 'x', 5, '5', 5, null, U, NaNv, NaNv, true, D(5), D(6), -0, 0]), ['domain'], ['range']]);
C('ordinal explicit', 'ordinal', [['domain', ['x', 'y', 'x', 'z']], ['range', [1, 2]]], [...A(['x', 'y', 'z', 'w', 5]), ['domain']]);
C('ordinal unknown', 'ordinal', [['domain', ['x', 'y']], ['range', [1, 2]], ['unknown', 'nope']], [...A(['x', 'y', 'q']), ['domain']]);
C('ordinal unknown undef', 'ordinal', [['domain', ['x', 'y']], ['range', [1, 2]], ['unknown', U]], [...A(['x', 'y', 'q']), ['domain']]);
C('ordinal implicit reset', 'ordinal', [['domain', ['x']], ['range', [1, 2]], ['unknown', 'n'], ['implicit']], [...A(['x', 'q', 'r']), ['domain']]);
C('ordinal empty range', 'ordinal', [['domain', ['x', 'y']], ['range', []]], A(['x', 'y']));
C('ordinal mixed keys', 'ordinal', [['domain', [1, '1', 1.0, true, null, U, NaNv, D(5), 5, 0, -0, 'a']], ['range', [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]]], [...A([1, '1', true, null, U, NaNv, D(5), 5, 0, 'a', 'b']), ['domain']]);
C('ordinal copy', 'ordinal', [['domain', ['x', 'y']], ['range', [1, 2]], ['copy'], ['domain', ['q']]], [...A(['x', 'q', 'z']), ['domain']]);

// band / point
const bandDoms = [[], ['a'], ['a', 'b', 'c'], ['a', 'b', 'c', 'd', 'e'], [1, 2, 3, 4, 5, 6, 7]];
const bandRanges = [[0, 100], [100, 0], [10, 233.3], [0, 0], [0, NaNv]];
for (const t of ['band', 'point']) for (const d of bandDoms) for (const r of bandRanges) {
  for (const cfg of [
    [], [['padding', 0.1]], [['padding', 1]], [['padding', 2]], [['round', true]], [['align', 0]], [['align', 0.3]],
    [['paddingInner', 0.2], ['paddingOuter', 0.4]], [['paddingInner', 0.5], ['align', 0.25], ['round', true]], [['padding', 0.3], ['round', true], ['align', 0.9]],
  ]) {
    if (t === 'point' && cfg.some(([k]) => k === 'paddingInner')) continue;
    C(`${t} d=${JSON.stringify(d)} r=${JSON.stringify(r)} cfg=${JSON.stringify(cfg)}`, t, [['domain', d], ['range', r], ...cfg],
      [...A(['a', 'b', 'c', 'd', 'e', 'zz', 1, 2, 7, U, null]), ['bandwidth'], ['step'], ['range'], ['domain'], ['round'], ['padding'], ['align'],
        ...[-10, 0, 1, 5, 10, 20, 33, 50, 60, 99, 100, 150, 233, 500, NaNv, null].map((y) => ['invert', y]),
        ['invertRange', [0, 10]], ['invertRange', [10, 0]], ['invertRange', [20, 80]], ['invertRange', [-5, 1000]], ['invertRange', [50, 50]], ['invertRange', [null, 5]], ['invertRange', [NaNv, 5]]]);
  }
}
C('band rangeRound', 'band', [['domain', ['a', 'b', 'c']], ['rangeRound', [0, 100]]], [...A(['a', 'b', 'c']), ['bandwidth'], ['step'], ['round']]);
C('band copy', 'band', [['domain', ['a', 'b']], ['range', [0, 10]], ['padding', 0.2], ['round', true], ['copy'], ['domain', ['q', 'r', 's']]], [...A(['q', 'r', 's', 'a']), ['bandwidth'], ['padding'], ['round']]);
C('point copy', 'point', [['domain', ['a', 'b']], ['range', [0, 10]], ['padding', 0.2], ['copy'], ['domain', ['q', 'r', 's']]], [...A(['q', 'r', 's', 'a']), ['bandwidth'], ['padding']]);
C('band implicit-immune', 'band', [['domain', ['a']], ['range', [0, 10]]], [...A(['a', 'b', 'c']), ['domain']]);

// sequential / diverging scales driven by colour schemes
for (const t of seqTypes) for (const name of ['blues', 'viridis', 'turbo', 'greys', 'rainbow', 'lightgreyred', 'darkblue']) for (const d of [[0, 1], [0, 100], [100, 0], [1, 1000], [5, 5]]) {
  C(`${t} scheme=${name} d=${JSON.stringify(d)}`, t, [['domain', d], ['interpolator', name]], [...A([-10, 0, 0.1, 0.5, 1, 2, 5, 10, 50, 100, 500, 1000, NaNv, null, U]), ['range'], ['domain']]);
  C(`${t} scheme=${name} d=${JSON.stringify(d)} clamp`, t, [['domain', d], ['interpolator', name], ['clamp', true]], A([-10, 0, 0.5, 5, 50, 5000]));
}
for (const t of divTypes) for (const name of ['redblue', 'spectral', 'purplegreen', 'redyellowblue']) for (const d of [[-10, 0, 10], [0.1, 1, 10], [10, 0, -10], [0, 0.5, 1]]) {
  C(`${t} scheme=${name} d=${JSON.stringify(d)}`, t, [['domain', d], ['interpolator', name]], [...A([-100, -10, -1, 0, 0.05, 0.5, 1, 5, 10, 100, NaNv, null, U]), ['range']]);
}
emit();
