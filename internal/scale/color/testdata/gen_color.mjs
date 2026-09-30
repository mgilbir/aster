// Records d3-color behaviour. Run:
//   NODE_PATH=testdata/oracle-node/node_modules node testdata/gen_color.mjs > testdata/color.json
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const d3 = await import(require.resolve('d3-color'));
const src = fs.readFileSync(path.join(process.env.NODE_PATH, 'd3-color/src/color.js'), 'utf8');
const names = [...src.matchAll(/^\s+([a-z]+): 0x[0-9a-f]+,?$/gm)].map(m => m[1]);

// numbers: finite as-is, others as strings ("NaN", "Infinity", "-Infinity")
const n = x => (Number.isFinite(x) ? x : String(x));
const fields = (c, ks) => ks.map(k => n(c[k]));
const spaces = {
  rgb: ['r', 'g', 'b', 'opacity'],
  hsl: ['h', 's', 'l', 'opacity'],
  lab: ['l', 'a', 'b', 'opacity'],
  hcl: ['h', 'c', 'l', 'opacity'],
  cubehelix: ['h', 's', 'l', 'opacity'],
};
function kind(c) { return c instanceof d3.rgb(0,0,0).constructor ? 'rgb' : c instanceof d3.hsl(0,0,0).constructor ? 'hsl' : c instanceof d3.lab(0,0,0).constructor ? 'lab' : c instanceof d3.hcl(0,0,0).constructor ? 'hcl' : 'cubehelix'; }
function describe(c) {
  const k = kind(c);
  const o = { space: k, f: fields(c, spaces[k]), hex: c.formatHex(), hex8: c.formatHex8(), rgb: c.formatRgb(), str: c.toString(), hsl: c.formatHsl(), disp: c.displayable() };
  if (k === 'rgb' || k === 'hsl') { const cl = c.clamp(); o.clamp = fields(cl, spaces[k]); }
  return o;
}

const strings = [
  ...names, 'transparent', 'TRANSPARENT', 'Red', 'ReD', ' red ', '\tblue\n',
  '#fff', '#FFF', '#f00', '#ff0000', '#FF0000', '#f008', '#ff000080', '#ff000000', '#0000', '#123456', '#abc', '#abcd', '#abcdef', '#abcdef12',
  '#', '#f', '#ff', '#fffff', '#fffffff', '#fffffffff', '#ggg', '#12345g', 'fff', ' #fff ',
  'rgb(255, 0, 0)', 'rgb(255,0,0)', 'rgb( 255 , 0 , 0 )', 'RGB(1,2,3)', 'rgb(300, -20, 128)', 'rgb(+1,+2,+3)', 'rgb(1.5, 2, 3)', 'rgb(1e2, 2, 3)',
  'rgb(100%, 0%, 0%)', 'rgb(50%,50%,50%)', 'rgb(12.5%, 33.3%, 99.9%)', 'rgb(1e1%, 0%, 0%)', 'rgb(100%, 0, 0%)', 'rgb(100, 0%, 0)', 'rgb(-10%, 110%, 50%)', 'rgb(.5%, 1.%, 2%)', 'rgb(.5%, 1.5%, 2%)',
  'rgb(255, 0, 0, 1)', 'rgb(255, 0)', 'rgb()', 'rgb(255 0 0)', 'rgb(255, 0, 0', 'rgb(255, 0, 0))', 'rgb (255,0,0)',
  'rgba(255, 0, 0, 1)', 'rgba(255, 0, 0, 0.5)', 'rgba(255,0,0,.5)', 'rgba(255, 0, 0, 0)', 'rgba(255, 0, 0, -1)', 'rgba(255, 0, 0, 2)', 'rgba(255, 0, 0, 1e-1)', 'rgba(255, 0, 0, 0.30000000000000004)', 'rgba(255,0,0,1e-7)',
  'rgba(100%, 0%, 0%, 0.25)', 'rgba(100%, 0%, 0%, 1)', 'rgba(100%, 0%, 0%, 0)', 'rgba(255, 0, 0)', 'rgba(1.5, 0, 0, 1)', 'rgba(255, 0, 0, 1.)', 'rgba(255, 0, 0, 1e)', 'rgba(255, 0, 0, 1e+)', 'rgba(255, 0, 0, 1e+2)', 'rgba(255, 0, 0, .)',
  'hsl(120, 50%, 50%)', 'hsl(0, 100%, 50%)', 'hsl(360, 100%, 50%)', 'hsl(-120, 50%, 50%)', 'hsl(480, 50%, 50%)', 'hsl(120.5, 50.5%, 50.5%)', 'hsl(1e2, 50%, 50%)', 'HSL(120,50%,50%)',
  'hsl(120, 0%, 50%)', 'hsl(120, 50%, 0%)', 'hsl(120, 50%, 100%)', 'hsl(120, -5%, 50%)', 'hsl(120, 150%, 50%)', 'hsl(120, 50%, 150%)',
  'hsl(120, 50, 50)', 'hsl(120, 50%, 50)', 'hsl(120%, 50%, 50%)', 'hsl(120, 50%, 50%, 0.5)', 'hsl(120, 50%)',
  'hsla(120, 50%, 50%, 0.5)', 'hsla(120, 50%, 50%, 1)', 'hsla(120, 50%, 50%, 0)', 'hsla(120, 0%, 50%, 0.5)', 'hsla(120, 50%, 100%, 0.5)', 'hsla(120, 50%, 50%)', 'hsla(120, 50%, 50%, .3)',
  '', ' ', 'garbage', 'notacolor', 'rgb', 'rgb(', 'hsl', 'red blue', 'constructor', 'toString', '__proto__', 'hasOwnProperty', 'currentcolor', 'inherit',
  'rgb(255,\n0,\t0)', 'rgb(255, 0,0)', 'rgb( 5, 1,1)', 'rgb(1,2,3) ', 'rgb(٣,2,1)', 'rgb(1,2,3)x',
];

const out = { parse: [], convert: [], adjust: [], construct: [], names };

for (const s of strings) {
  const c = d3.color(s);
  const e = { in: s, ok: !!c };
  if (c) e.c = describe(c);
  e.rgb = fields(d3.rgb(s), spaces.rgb);
  e.hsl = fields(d3.hsl(s), spaces.hsl);
  e.lab = fields(d3.lab(s), spaces.lab);
  e.hcl = fields(d3.hcl(s), spaces.hcl);
  e.cubehelix = fields(d3.cubehelix(s), spaces.cubehelix);
  out.parse.push(e);
}

// conversions and adjust: over a colour grid
const grid = [];
for (const r of [0, 1, 51, 128, 200, 255]) for (const g of [0, 7, 128, 255]) for (const b of [0, 64, 129, 255]) grid.push(`rgb(${r},${g},${b})`);
grid.push('rgba(10,20,30,0.5)', 'transparent', 'hsl(200,30%,40%)', 'hsla(20,90%,60%,.3)', 'hsl(0,0%,50%)', 'hsl(0,0%,0%)', 'hsl(0,0%,100%)');
for (const s of grid) {
  const c = d3.color(s);
  out.convert.push({ in: s, hsl: fields(d3.hsl(c), spaces.hsl), lab: fields(d3.lab(c), spaces.lab), hcl: fields(d3.hcl(c), spaces.hcl), cubehelix: fields(d3.cubehelix(c), spaces.cubehelix),
    labRgb: describe(d3.lab(c).rgb()), hclRgb: describe(d3.hcl(c).rgb()), cubeRgb: describe(d3.cubehelix(c).rgb()), hslRgb: describe(d3.hsl(c).rgb()) });
}
for (const s of ['steelblue', 'rgb(255,128,0)', 'hsl(200,30%,40%)', 'rgba(10,20,30,0.5)', '#fff', 'black']) {
  const c = d3.color(s);
  for (const sp of ['rgb', 'hsl', 'lab', 'hcl', 'cubehelix']) {
    const cc = d3[sp](c);
    for (const k of [undefined, 0, 0.5, 1, 2.5, -1]) {
      out.adjust.push({ in: s, space: sp, k: k === undefined ? null : k, brighter: describe(cc.brighter(k)), darker: describe(cc.darker(k)) });
    }
  }
}
// direct construction
const cons = [];
for (const v of [[50, 20, -30, 1], [0, 0, 0, 1], [100, 0, 0, 0.5], [120, 60, 40, 1], [70, -80, 80, 1], [30, 128, -128, 0.25], [-10, 5, 5, 1], [50, NaN, 20, 1]]) {
  cons.push({ op: 'lab', args: v, r: describe(d3.lab(...v)) });
  cons.push({ op: 'hcl', args: v, r: describe(d3.hcl(...v)) });
  cons.push({ op: 'lch', args: v, r: describe(d3.lch(...v)) });
}
for (const v of [[0, 0, 0, 1], [120, 0.5, 0.5, 1], [360, 1, 0.5, 0.5], [-30, 0.7, 0.3, 1], [NaN, 0, 0.5, 1], [200, NaN, 0.5, 1], [400, 0.2, 1.2, 1], [30, 1, 0.5, 0]]) {
  cons.push({ op: 'hsl', args: v, r: describe(d3.hsl(...v)) });
  cons.push({ op: 'cubehelix', args: v, r: describe(d3.cubehelix(...v)) });
}
for (const v of [[0, 0, 0, 1], [255, 255, 255, 1], [300.5, -20, 127.5, 0.3], [12.4, 12.5, 12.6, 1], [NaN, 1, 1, 1], [1, 2, 3, NaN], [-0.4, 255.4, 255.6, 1], [1, 2, 3, 0.1], [1, 2, 3, 1e-9], [-3, -3, -3, 1], [2.5, 3.5, -2.5, 1]]) {
  cons.push({ op: 'rgb', args: v, r: describe(d3.rgb(...v)) });
}
for (const l of [0, 30, 50, 100]) cons.push({ op: 'gray', args: [l], r: describe(d3.gray(l)) });
out.construct = cons.map(c => ({ ...c, args: c.args.map(n) }));

console.log(JSON.stringify(out));
