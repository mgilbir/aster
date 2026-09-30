// Writes the targeted layout specifications to ./specs (checked in; rerun to
// regenerate). They complement the corpus specs with systematic sweeps over
// axis orients, legend orients and types, title placement, autosize modes and
// grid layouts.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.join(here, 'specs');
fs.mkdirSync(out, { recursive: true });
for (const f of fs.readdirSync(out)) fs.unlinkSync(path.join(out, f));

const rows = [
  { a: 'Alpha', b: 28, c: 'x', d: 1 }, { a: 'Bravo', b: 55, c: 'y', d: 2 }, { a: 'Charlie', b: 43, c: 'x', d: 3 },
  { a: 'Delta', b: 91, c: 'z', d: 4 }, { a: 'Echo', b: 81, c: 'y', d: 5 }, { a: 'Foxtrot', b: 53, c: 'z', d: 6 },
];
const data = [{ name: 't', values: rows }];
const scales = [
  { name: 'x', type: 'band', domain: { data: 't', field: 'a' }, range: 'width', padding: 0.1 },
  { name: 'y', type: 'linear', domain: { data: 't', field: 'b' }, range: 'height', nice: true },
  { name: 'color', type: 'ordinal', domain: { data: 't', field: 'c' }, range: { scheme: 'category10' } },
  { name: 'ramp', type: 'linear', domain: { data: 't', field: 'd' }, range: { scheme: 'blues' } },
  { name: 'size', type: 'linear', domain: { data: 't', field: 'b' }, range: [10, 200] },
  { name: 'shape', type: 'ordinal', domain: { data: 't', field: 'c' }, range: 'symbol' },
  { name: 'quant', type: 'quantize', domain: [0, 100], range: { scheme: 'greens', count: 4 } },
];
const marks = [{ type: 'rect', from: { data: 't' }, encode: { enter: {
  x: { scale: 'x', field: 'a' }, width: { scale: 'x', band: 1 }, y: { scale: 'y', field: 'b' }, y2: { scale: 'y', value: 0 } } } }];
const base = extra => ({ width: 200, height: 120, padding: 5, data, scales, marks, ...extra });
const save = (name, spec) => fs.writeFileSync(path.join(out, name + '.vg.json'), JSON.stringify(spec));

// ---- axes
for (const o of ['top', 'bottom', 'left', 'right']) {
  const sc = o === 'top' || o === 'bottom' ? 'x' : 'y';
  save(`axis-${o}-plain`, base({ axes: [{ orient: o, scale: sc }] }));
  save(`axis-${o}-titled`, base({ axes: [{ orient: o, scale: sc, title: 'A title', grid: true }] }));
  save(`axis-${o}-offset`, base({ axes: [{ orient: o, scale: sc, title: 'Offset', offset: 8, position: 12, translate: 0 }] }));
  save(`axis-${o}-extent`, base({ axes: [{ orient: o, scale: sc, title: 'Extent', minExtent: 40, maxExtent: 20, titlePadding: 12, labelAngle: 35, labelPadding: 6 }] }));
  save(`axis-${o}-multiline`, base({ axes: [{ orient: o, scale: sc, title: ['Line one', 'Line two', 'Line three'], titleFontSize: 14 }] }));
  save(`axis-${o}-notitle-labels`, base({ axes: [{ orient: o, scale: sc, labels: false, ticks: false, domain: true, title: 'Bare' }] }));
  save(`axis-${o}-fixed-title`, base({ axes: [{ orient: o, scale: sc, title: 'Fixed', titleX: 10, titleY: 20 }] }));
  save(`axis-${o}-overlap`, base({
    scales: [...scales, { name: 'many', type: 'linear', domain: [0, 100000], range: o === 'top' || o === 'bottom' ? 'width' : 'height' }],
    axes: [{ orient: o, scale: 'many', tickCount: 40, labelOverlap: 'greedy', labelBound: true, labelSeparation: 3, title: 'Overlap' }] }));
  save(`axis-${o}-parity`, base({
    scales: [...scales, { name: 'many', type: 'linear', domain: [0, 100000], range: o === 'top' || o === 'bottom' ? 'width' : 'height' }],
    axes: [{ orient: o, scale: 'many', tickCount: 40, labelOverlap: 'parity', labelFlush: 3 }] }));
}
save('axis-all-sides', base({ axes: [
  { orient: 'top', scale: 'x', title: 'Top' }, { orient: 'bottom', scale: 'x', title: 'Bottom' },
  { orient: 'left', scale: 'y', title: 'Left' }, { orient: 'right', scale: 'y', title: 'Right' }] }));
save('axis-signal-orient', base({ signals: [{ name: 'o', value: 'top' }], axes: [{ orient: { signal: 'o' }, scale: 'x', title: 'Sig' }] }));

// ---- legends
const legendKinds = {
  symbol: { fill: 'color', title: 'Symbols' },
  multi: { fill: 'color', size: 'size', shape: 'shape', title: 'Multi channel' },
  gradient: { fill: 'ramp', type: 'gradient', title: 'Gradient' },
  gradientH: { fill: 'ramp', type: 'gradient', title: 'Horizontal', direction: 'horizontal', gradientLength: 120 },
  discrete: { fill: 'quant', type: 'gradient', title: 'Discrete' },
  untitled: { fill: 'color' },
};
for (const o of ['left', 'right', 'top', 'bottom', 'top-left', 'top-right', 'bottom-left', 'bottom-right', 'none']) {
  for (const [k, l] of Object.entries(legendKinds)) {
    if (o === 'none' && k !== 'symbol' && k !== 'gradientH') continue;
    const lg = { ...l, orient: o };
    if (o === 'none') { lg.legendX = 30; lg.legendY = 40; }
    save(`legend-${o}-${k}`, base({ legends: [lg] }));
  }
}
for (const to of ['top', 'left', 'right', 'bottom']) for (const ta of ['start', 'middle', 'end']) {
  save(`legend-title-${to}-${ta}`, base({ legends: [{ fill: 'color', title: 'Title placement', titleOrient: to, titleAnchor: ta, orient: 'right' }] }));
  save(`legend-gradtitle-${to}-${ta}`, base({ legends: [{ fill: 'ramp', type: 'gradient', title: 'Grad title', titleOrient: to, titleAnchor: ta, orient: 'right' }] }));
  save(`legend-gradtitleH-${to}-${ta}`, base({ legends: [{ fill: 'ramp', type: 'gradient', direction: 'horizontal', title: 'Grad title', titleOrient: to, titleAnchor: ta, orient: 'bottom' }] }));
}
save('legend-stacked-right', base({ legends: [{ fill: 'color', title: 'One' }, { size: 'size', title: 'Two' }, { fill: 'ramp', type: 'gradient', title: 'Three' }] }));
save('legend-stacked-bottom', base({ legends: [{ fill: 'color', title: 'One', orient: 'bottom' }, { size: 'size', title: 'Two', orient: 'bottom' }] }));
save('legend-columns', base({ legends: [{ fill: 'color', columns: 2, orient: 'bottom', direction: 'horizontal', title: 'Columns' }, { shape: 'shape', columns: 3, rowPadding: 6, columnPadding: 20, orient: 'right' }] }));
save('legend-padding-offset', base({ legends: [{ fill: 'color', padding: 8, offset: 20, cornerRadius: 4, strokeColor: '#ccc', fillColor: '#eee', title: 'Boxed' }] }));
save('legend-config-layout', base({
  config: { legend: { layout: { left: { anchor: 'middle' }, right: { anchor: 'end', margin: 12 }, top: { anchor: 'middle', center: true, direction: 'horizontal' }, bottom: { frame: 'bounds', anchor: 'end', margin: { row: 4, column: 16 } }, offset: 10 } } },
  axes: [{ orient: 'left', scale: 'y', title: 'Y' }, { orient: 'bottom', scale: 'x', title: 'X' }],
  legends: [{ fill: 'color', orient: 'left', title: 'L' }, { fill: 'color', orient: 'right', title: 'R' }, { fill: 'color', orient: 'top', title: 'T' }, { fill: 'color', orient: 'bottom', title: 'B' }] }));
save('legend-with-axes', base({ axes: [{ orient: 'bottom', scale: 'x', title: 'X' }, { orient: 'right', scale: 'y', title: 'Y' }], legends: [{ fill: 'color', title: 'Right' }, { fill: 'ramp', type: 'gradient', title: 'Bottom', orient: 'bottom', direction: 'horizontal' }] }));

// ---- titles
for (const o of ['top', 'bottom', 'left', 'right']) for (const a of ['start', 'middle', 'end']) for (const f of ['group', 'bounds']) {
  save(`title-${o}-${a}-${f}`, base({ title: { text: 'A chart title', subtitle: ['Subtitle', 'Second line'], orient: o, anchor: a, frame: f, offset: 6 }, axes: [{ orient: 'bottom', scale: 'x', title: 'X' }, { orient: 'left', scale: 'y', title: 'Y' }] }));
}
save('title-plain', base({ title: 'Just a title' }));
save('title-multiline', base({ title: { text: ['Title line 1', 'Line 2'], subtitlePadding: 8, subtitle: 'sub', dy: 3 } }));
save('title-empty-subtitle', base({ title: { text: 'Title', subtitle: '' } }));
save('title-and-legends', base({ title: { text: 'Title', orient: 'top', frame: 'bounds' }, legends: [{ fill: 'color', orient: 'top', title: 'Legend' }, { fill: 'ramp', type: 'gradient', orient: 'bottom', direction: 'horizontal', title: 'Grad' }] }));

// ---- autosize
for (const type of ['pad', 'fit', 'fit-x', 'fit-y', 'none']) for (const contains of ['content', 'padding']) {
  save(`autosize-${type}-${contains}`, base({ padding: { left: 10, right: 20, top: 5, bottom: 15 }, autosize: { type, contains }, axes: [{ orient: 'bottom', scale: 'x', title: 'X title' }, { orient: 'left', scale: 'y', title: 'Y title' }], legends: [{ fill: 'color', title: 'Legend' }], title: { text: 'Title', subtitle: 'sub' } }));
  save(`autosize-${type}-${contains}-legend-bottom`, base({ autosize: { type, contains }, axes: [{ orient: 'left', scale: 'y' }], legends: [{ fill: 'ramp', type: 'gradient', orient: 'bottom', direction: 'horizontal' }, { fill: 'color', orient: 'left' }] }));
  save(`autosize-${type}-${contains}-clip`, base({ autosize: { type, contains }, axes: [{ orient: 'left', scale: 'y', title: 'Y' }], marks: [{ type: 'group', clip: true, encode: { enter: { width: { signal: 'width' }, height: { signal: 'height' } } }, marks }] }));
}
save('autosize-pad-resize', base({ autosize: { type: 'pad', resize: true }, axes: [{ orient: 'left', scale: 'y' }] }));
save('autosize-string', base({ autosize: 'fit', axes: [{ orient: 'left', scale: 'y', title: 'Y' }] }));

// ---- grid layouts
const cells = (layout, extraMarks = []) => ({
  width: 300, height: 200, padding: 5,
  data: [{ name: 'table', values: rows.map((r, i) => ({ ...r, col: i % 3, row: i % 2 })) }],
  scales: [{ name: 'x', type: 'linear', domain: { data: 'table', field: 'd' }, range: [0, 60] }, { name: 'y', type: 'linear', domain: { data: 'table', field: 'b' }, range: [40, 0] }],
  layout,
  marks: [{
    type: 'group', from: { facet: { name: 'facet', data: 'table', groupby: ['col'] } },
    encode: { update: { width: { value: 60 }, height: { value: 40 } } },
    axes: [{ orient: 'left', scale: 'y', title: 'b' }, { orient: 'bottom', scale: 'x' }],
    marks: [{ type: 'symbol', from: { data: 'facet' }, encode: { enter: { x: { scale: 'x', field: 'd' }, y: { scale: 'y', field: 'b' }, size: { value: 20 } } } }],
  }, ...extraMarks],
});
const headerGroup = (role, title) => ({ type: 'group', role, from: { data: 'cols' }, title: { text: { signal: 'parent.col' }, offset: 4 }, encode: { update: { width: { value: 60 }, height: { value: 10 } } } });
for (const align of ['all', 'each', 'none']) for (const bounds of ['full', 'flush']) {
  save(`grid-${align}-${bounds}`, cells({ align, bounds, columns: 2, padding: 16 }));
  save(`grid-${align}-${bounds}-center`, cells({ align: { row: align, column: align }, bounds, columns: 2, padding: { row: 8, column: 24 }, center: true }));
}
save('grid-columns-1', cells({ columns: 1, padding: 10, align: 'each' }));
save('grid-columns-5', cells({ columns: 5, padding: 10, align: 'all' }));
save('grid-anchor', cells({ columns: 2, padding: 10, align: 'each', bounds: 'flush' }));
const headers = {
  data: [{ name: 'table', values: rows.map((r, i) => ({ ...r, col: i % 3, row: i % 2 })) }, { name: 'cols', source: 'table', transform: [{ type: 'aggregate', groupby: ['col'] }] }, { name: 'rows', source: 'table', transform: [{ type: 'aggregate', groupby: ['row'] }] }],
};
save('grid-headers', { ...cells({ columns: 3, padding: { row: 20, column: 20 }, align: 'each', offset: { columnHeader: 10, columnFooter: 8, rowTitle: 12, columnTitle: 12 }, headerBand: { column: 0.5 }, footerBand: { column: 0.5 }, titleBand: { row: 0.25, column: 0.75 }, titleAnchor: { row: 'end', column: 'end' } }, [
  { type: 'group', role: 'column-header', from: { data: 'cols' }, title: { text: 'header', offset: 2 }, encode: { update: { width: { value: 60 }, height: { value: 8 } } } },
  { type: 'group', role: 'column-footer', from: { data: 'cols' }, title: { text: 'footer', offset: 2 }, encode: { update: { width: { value: 60 }, height: { value: 8 } } } },
  { type: 'group', role: 'row-title', title: { text: 'Rows' }, encode: { update: { width: { value: 5 }, height: { value: 5 } } } },
  { type: 'group', role: 'column-title', title: { text: 'Columns' }, encode: { update: { width: { value: 5 }, height: { value: 5 } } } },
]), data: headers.data });
save('grid-headers-flush', { ...cells({ columns: 3, bounds: 'flush', padding: 12, offset: 6 }, [
  { type: 'group', role: 'column-header', from: { data: 'cols' }, title: { text: 'H' }, encode: { update: { width: { value: 60 }, height: { value: 8 } } } },
  { type: 'group', role: 'row-header', from: { data: 'rows' }, title: { text: 'R' }, encode: { update: { width: { value: 8 }, height: { value: 40 } } } },
  { type: 'group', role: 'row-footer', from: { data: 'rows' }, title: { text: 'RF' }, encode: { update: { width: { value: 8 }, height: { value: 40 } } } },
]), data: headers.data });

// ---- label overlap on marks
save('overlap-labels', {
  width: 200, height: 40, padding: 5, data: [{ name: 't', values: Array.from({ length: 30 }, (_, i) => ({ i, l: 'label' + i })) }],
  scales: [{ name: 'x', type: 'linear', domain: [0, 30], range: 'width' }],
  marks: [{ type: 'text', from: { data: 't' }, overlap: { method: 'parity', separation: 2 }, encode: { enter: { x: { scale: 'x', field: 'i' }, text: { field: 'l' }, fontSize: { value: 10 } } } }],
});
save('overlap-greedy', {
  width: 200, height: 40, padding: 5, data: [{ name: 't', values: Array.from({ length: 30 }, (_, i) => ({ i, l: 'label' + (i * 37 % 11) })) }],
  scales: [{ name: 'x', type: 'linear', domain: [0, 30], range: 'width' }],
  marks: [{ type: 'text', from: { data: 't' }, overlap: { method: 'greedy', separation: 0, order: 'datum.i', bound: { scale: 'x', orient: 'bottom', tolerance: 0 } }, encode: { enter: { x: { scale: 'x', field: 'i' }, text: { field: 'l' }, fontSize: { value: 10 }, angle: { value: 0 } } } }],
});
