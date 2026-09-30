// Records the mark definitions vega-parser generates for axes, legends and
// titles, by running the real parsers against a stub scope (the mark parser is
// replaced by the identity so the definitions can be read back).
//
//   NODE_PATH=<node_modules> node gen_guides.mjs > guides.json
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import { pathToFileURL } from 'node:url';

const nm = process.env.NODE_PATH;
const require = createRequire(path.join(nm, 'x.js'));
const parserSrc = path.join(nm, 'vega-parser', 'src');

// stage a patched copy of vega-parser's sources
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'vp-'));
fs.cpSync(parserSrc, path.join(tmp, 'src'), { recursive: true });
fs.symlinkSync(nm, path.join(tmp, 'node_modules'));
fs.writeFileSync(path.join(tmp, 'package.json'), '{"type":"module"}');
for (const f of ['axis', 'legend', 'title']) {
  const p = path.join(tmp, 'src/parsers', f + '.js');
  let s = fs.readFileSync(p, 'utf8');
  s = s.replace("import parseMark from './mark.js';", 'const parseMark = (def) => def;');
  s = s.replace(/return parseExpression\(\s*`(max\(ceil[^`]*)`,\s*scope\s*\);/, 'return `$1`;');
  fs.writeFileSync(p, s);
}
const imp = f => import(pathToFileURL(path.join(tmp, 'src', f)).href);
const parseAxis = (await imp('parsers/axis.js')).default;
const parseLegend = (await imp('parsers/legend.js')).default;
const parseTitle = (await imp('parsers/title.js')).default;
const defaultConfig = (await imp('config.js')).default;
const { mergeConfig } = await import(require.resolve('vega-util'));

function makeScope(config, scaleTypes) {
  let id = 0;
  const ops = [];
  return {
    config,
    ops,
    add(op) { op.id = ++id; ops.push(op); return op; },
    scaleType: n => scaleTypes[n],
    scaleRef: n => ({ $scale: n }),
    objectProperty: x => x,
    property: x => x,
    signalRef: s => ({ signal: s }),
  };
}

const base = defaultConfig();
const configs = {
  default: {},
  axisXY: { axisX: { tickSize: 8, labelAngle: -45, domain: false, tickBand: 'extent' }, axisY: { grid: true, gridDash: [2, 2], labelPadding: 6, titleX: -30 } },
  axisOrient: { axisTop: { titleColor: 'red', labelFontSize: 14 }, axisBottom: { labelOverlap: 'parity', labelFlush: 4 }, axisLeft: { ticks: false }, axisRight: { labelAlign: 'left', titleAnchor: 'end' } },
  axisTypes: { axisBand: { tickOffset: 0.25, grid: true }, axisPoint: { grid: true }, axisQuantitative: { tickCount: 5 }, axisTemporal: { labelAngle: 10 }, axisDiscrete: { domain: false } },
  axisStyles: { axis: { titleColor: 'blue', labelColor: '#333', labelLimit: 100, labelBound: true, labelSeparation: 2 }, axisX: { labelFlush: true, labelFlushOffset: 3 } },
  legendVar: { legend: { orient: 'bottom', symbolType: 'square', layout: { bottom: { direction: 'horizontal' } }, gradientDirection: 'horizontal', gradientLength: 300, columns: 2, titleAnchor: 'middle', strokeColor: '#ccc', fillColor: '#eee', cornerRadius: 4, strokeWidth: 2, strokeDash: [3, 3] } },
  title: { title: { orient: 'bottom', anchor: 'start', frame: 'bounds', dx: 5, color: 'green', subtitleColor: 'gray', font: 'serif', fontSize: 20, subtitleFontSize: 12 } },
};

const cases = [];
function add(kind, name, spec, cfgName, scaleTypes) {
  const config = mergeConfig(base, configs[cfgName]);
  const scope = makeScope(config, scaleTypes || {});
  let out = {};
  const specCopy = JSON.parse(JSON.stringify(spec));
  try {
    if (kind === 'axis') out.mark = parseAxis(spec, scope);
    else if (kind === 'legend') out.mark = parseLegend(spec, scope);
    else out.mark = parseTitle(spec, scope);
  } catch (e) {
    out.error = String(e.message || e);
  }
  out.ops = scope.ops.map(o => ({ type: o.type, params: o.params, value: o.value }));
  cases.push({
    name, kind, spec: specCopy, config: cfgName,
    guideConfigTmp: {
      axis: config.axis, axisX: config.axisX, axisY: config.axisY, axisTop: config.axisTop,
      axisBottom: config.axisBottom, axisLeft: config.axisLeft, axisRight: config.axisRight,
      axisBand: config.axisBand, axisPoint: config.axisPoint, axisQuantitative: config.axisQuantitative,
      axisTemporal: config.axisTemporal, axisDiscrete: config.axisDiscrete,
      legend: config.legend, title: config.title, style: config.style,
    },
    scaleTypes: scaleTypes || {},
    result: JSON.parse(JSON.stringify(out)),
  });
}

const orients = ['bottom', 'top', 'left', 'right', { signal: 'ori' }];
const axisSpecs = {
  plain: o => ({ scale: 'x', orient: o }),
  full: o => ({ scale: 'x', orient: o, grid: true, gridScale: 'y', title: 'Title', tickCount: 5, format: '.2f', offset: 4, position: 10, zindex: 1 }),
  band: o => ({ scale: 'x', orient: o, tickBand: 'extent', labelOverlap: 'greedy', labelBound: 2, labelPadding: 3, labelAngle: 45, labelAlign: 'left', labelBaseline: 'top' }),
  flush: o => ({ scale: 'x', orient: o, labelFlush: 0, labelFlushOffset: 4, labelOverlap: true, labelSeparation: 3, title: ['a', 'b'] }),
  signals: o => ({ scale: 'x', orient: o, tickBand: { signal: 'tb' }, offset: { signal: 'off' }, tickSize: { signal: 'ts' }, labelFlush: { signal: 'lf' }, labelColor: { signal: 'lc' }, titleX: 5, titleY: { signal: 'ty' }, title: { signal: 'tt' }, labelPadding: { signal: 'lp' }, tickOffset: 2 }),
  encode: o => ({ scale: 'x', orient: o, grid: true, title: 'T', domain: true, encode: {
    axis: { name: 'ax', interactive: true, style: 'st', update: { fill: { value: 'red' } } },
    ticks: { name: 'tk', interactive: true, style: 'tks', enter: { stroke: { value: 'blue' } } },
    labels: { update: { fontSize: { value: 22 }, align: { value: 'right' } }, name: 'lb' },
    title: { update: { x: { value: 3 } }, enter: { y: { value: 2 } } },
    grid: { update: { strokeDash: { value: [1, 1] } } },
    domain: { enter: { stroke: { value: 'green' } } },
  } }),
  off: o => ({ scale: 'x', orient: o, ticks: false, labels: false, domain: false, grid: true, minExtent: 30, maxExtent: 60, titlePadding: 9, translate: 0, title: 'Z', titleAnchor: 'end', titleAngle: 90, titleAlign: 'left', titleBaseline: 'top', titleLimit: 100, tickMinStep: 2, values: [1, 2, 3], formatType: 'number', labelFontWeight: 'bold', labelLineHeight: 12, labelOpacity: 0.5, gridDash: [4, 4], gridOpacity: 0.3, tickCap: 'round', domainCap: 'square', aria: false, description: 'desc' }),
};
const cfgAxis = ['default', 'axisXY', 'axisOrient', 'axisTypes', 'axisStyles'];
const stypes = ['linear', 'band', 'point', 'time'];
let n = 0;
for (const [sn, sf] of Object.entries(axisSpecs)) {
  for (const o of orients) {
    const ci = cfgAxis[n++ % cfgAxis.length];
    const st = stypes[n % stypes.length];
    add('axis', `axis-${sn}-${typeof o === 'string' ? o : 'signal'}-${ci}-${st}`, sf(o), ci, { x: st });
    // also always default config, band scale
    add('axis', `axis-${sn}-${typeof o === 'string' ? o : 'signal'}-default-band`, sf(o), 'default', { x: 'band' });
  }
}
for (const ci of cfgAxis) for (const o of orients) {
  add('axis', `axis-cfg-${ci}-${typeof o === 'string' ? o : 'signal'}`, { scale: 'x', orient: o, title: 'x', grid: true }, ci, { x: 'linear' });
  add('axis', `axis-cfg-${ci}-${typeof o === 'string' ? o : 'signal'}-band`, { scale: 'x', orient: o, title: 'x' }, ci, { x: 'band' });
}

const legendSpecs = {
  symbolPlain: { fill: 'c' },
  symbolTitle: { fill: 'c', title: 'Legend', orient: 'left', symbolType: 'square', symbolSize: 200, labelLimit: 80, symbolOffset: 2, labelOffset: 6 },
  multi: { size: 's', fill: 'c', stroke: 'c', strokeWidth: 'w', shape: 'sh', opacity: 'o', strokeDash: 'd', title: 'T' },
  grad: { fill: 'g', type: 'gradient', title: 'G', direction: 'horizontal', gradientLength: 250, gradientThickness: 10, labelOverlap: 'greedy', labelSeparation: 4, gradientStrokeColor: 'red', gradientStrokeWidth: 1, gradientOpacity: 0.5, tickCount: 3, labelLimit: 50 },
  gradV: { fill: 'g', type: 'gradient', direction: 'vertical', titleOrient: 'left', titleAnchor: 'start' },
  gradSignal: { fill: 'g', type: 'gradient', gradientLength: { signal: 'gl' }, direction: { signal: 'dir' }, tickCount: { signal: 'tc' }, orient: { signal: 'o' } },
  disc: { fill: 'q', type: 'symbol', title: 'D' },
  discrete: { fill: 'q', type: 'gradient', title: 'D', direction: 'horizontal', format: '.1f', values: [1, 2] },
  cols: { fill: 'c', columns: 3, direction: 'horizontal', rowPadding: 4, columnPadding: 12, gridAlign: 'all', clipHeight: 20, symbolLimit: 10, offset: 6, padding: 5, legendX: 10, legendY: {signal: 'ly'}, zindex: 2, titleOrient: 'bottom' },
  encode: { fill: 'c', title: 'T', encode: {
    legend: { name: 'lg', interactive: true, update: { x: { value: 1 } } },
    title: { update: { fontSize: { value: 5 } } },
    symbols: { name: 'sy', interactive: true, update: { size: { value: 9 } }, enter: { strokeWidth: { value: 3 } } },
    labels: { name: 'lb', update: { fontSize: { value: 30 } } },
    entries: { name: 'en', interactive: true, update: { opacity: { value: 0.3 } } },
    gradient: {},
  } },
  encodeGrad: { fill: 'g', type: 'gradient', title: 'T', encode: {
    legend: { style: 'ls' }, gradient: { update: { fill: { value: 'red' } } }, labels: { update: { fontSize: { value: 7 } } },
  } },
};
const legendScaleTypes = [
  { c: 'ordinal', s: 'linear', g: 'linear', q: 'quantize', w: 'linear', sh: 'ordinal', o: 'linear', d: 'ordinal' },
  { c: 'linear', s: 'sqrt', g: 'sequential', q: 'threshold', w: 'linear', sh: 'ordinal', o: 'linear', d: 'ordinal' },
];
n = 0;
for (const [ln, ls] of Object.entries(legendSpecs)) {
  for (const ci of ['default', 'legendVar']) {
    for (const [si, st] of legendScaleTypes.entries()) {
      add('legend', `legend-${ln}-${ci}-${si}`, ls, ci, st);
    }
  }
}

const titleSpecs = {
  str: 'Hello',
  text: { text: 'Hello' },
  full: { text: ['a', 'b'], subtitle: 'sub', anchor: 'end', orient: 'left', frame: 'bounds', offset: 10, limit: 100, align: 'center', angle: 5, baseline: 'middle', dx: 3, dy: 4, color: 'red', font: 'f', fontSize: 30, fontStyle: 'italic', fontWeight: 'bold', lineHeight: 30, subtitleColor: 'blue', subtitleFont: 'g', subtitleFontSize: 9, subtitleFontStyle: 'italic', subtitleFontWeight: 300, subtitleLineHeight: 10, subtitlePadding: 7, zindex: 3, aria: false, description: 'd' },
  signals: { text: { signal: 't' }, subtitle: { signal: 's' }, orient: { signal: 'o' }, anchor: { signal: 'a' } },
  encode: { text: 'T', subtitle: 'S', encode: { group: { name: 'g', interactive: true, style: 'x', update: { x: { value: 1 } } }, title: { name: 't', update: { fill: { value: 'red' } } }, subtitle: { name: 's', update: { fill: { value: 'blue' } } } } },
  legacy: { text: 'T', name: 'nm', style: 'sty', interactive: true, encode: { update: { fill: { value: 'red' } } } },
};
for (const [tn, ts] of Object.entries(titleSpecs)) for (const ci of ['default', 'title']) add('title', `title-${tn}-${ci}`, ts, ci);

const configsOut = {};
for (const c of cases) {
  configsOut[c.config] = c.guideConfigTmp;
  delete c.guideConfigTmp;
}
process.stdout.write(JSON.stringify({ configs: configsOut, cases }));
