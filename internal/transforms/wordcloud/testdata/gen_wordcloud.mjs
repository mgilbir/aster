// Records golden layouts from upstream vega-wordcloud's CloudLayout.js.
//
// Upstream draws text on a canvas; here the upstream source is loaded with its
// `vega-canvas` import replaced by a deterministic canvas shim (boxShim below)
// that measures text as 0.6em per character and paints every word as a solid
// (rotated) box 0.8em above / 0.2em below the baseline. The Go BoxRenderer
// implements the same model, so both sides see identical bitmaps.
//
//   NODE_PATH=testdata/oracle-node/node_modules node testdata/gen_wordcloud.mjs > testdata/wordcloud.json
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const wcDir = path.join(process.env.NODE_PATH, 'vega-wordcloud');
let src = fs.readFileSync(path.join(wcDir, 'src/CloudLayout.js'), 'utf8');
src = src.replace("import {canvas} from 'vega-canvas';", "import {canvas} from './shim.mjs';");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'wc-'));
fs.writeFileSync(path.join(tmp, 'CloudLayout.mjs'), src);

fs.writeFileSync(path.join(tmp, 'shim.mjs'), `
export function canvas() {
  const cv = { width: 0, height: 0 };
  let px = null, W = 0, H = 0;
  const ctx = {
    fillStyle: '', strokeStyle: '', textAlign: '', lineWidth: 1, font: '',
    tx: 0, ty: 0, ang: 0, stack: [],
    save() { this.stack.push([this.tx, this.ty, this.ang, this.font, this.lineWidth]); },
    restore() { [this.tx, this.ty, this.ang, this.font, this.lineWidth] = this.stack.pop(); },
    clearRect() { if (px) px.fill(0); },
    translate(x, y) { this.tx += x; this.ty += y; },
    rotate(a) { this.ang += a; },
    fontPx() { return +/(\\d+)px/.exec(this.font)[1]; },
    measureText(t) { return { width: [...t].length * 0.6 * this.fontPx() }; },
    box(t, grow) {
      const p = this.fontPx(), half = [...t].length * 0.6 * p / 2;
      const x0 = -half - grow, x1 = half + grow, y0 = -0.8 * p - grow, y1 = 0.2 * p + grow;
      const s = Math.sin(this.ang), c = Math.cos(this.ang);
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const [a, b] of [[x0, y0], [x1, y0], [x0, y1], [x1, y1]]) {
        const gx = this.tx + c * a - s * b, gy = this.ty + s * a + c * b;
        minX = Math.min(minX, gx); maxX = Math.max(maxX, gx);
        minY = Math.min(minY, gy); maxY = Math.max(maxY, gy);
      }
      const ix0 = Math.max(Math.floor(minX) - 1, 0), ix1 = Math.min(Math.ceil(maxX) + 1, W - 1);
      const iy0 = Math.max(Math.floor(minY) - 1, 0), iy1 = Math.min(Math.ceil(maxY) + 1, H - 1);
      for (let y = iy0; y <= iy1; y++) for (let x = ix0; x <= ix1; x++) {
        const dx = x + 0.5 - this.tx, dy = y + 0.5 - this.ty;
        const lx = c * dx + s * dy, ly = c * dy - s * dx;
        if (lx >= x0 && lx < x1 && ly >= y0 && ly < y1) px[(y * W + x) * 4] = 255;
      }
    },
    fillText(t) { this.box(t, 0); },
    strokeText(t) { this.box(t, this.lineWidth / 2); },
    getImageData(x, y, w, h) {
      if (w === 1 && h === 1 && !px) return { data: new Uint8Array(4) };
      return { data: px };
    },
  };
  return {
    get width() { return cv.width; }, set width(v) { cv.width = v; },
    get height() { return cv.height; },
    set height(v) { cv.height = v; if (cv.width > 1) { W = cv.width; H = v; px = new Uint8Array(W * H * 4); } },
    getContext() { return ctx; },
  };
}
`);

const cloud = (await import(path.join(tmp, 'CloudLayout.mjs'))).default;
const d3 = await import(require.resolve('d3-scale'));

function lcg(seed) {
  let s = seed >>> 0;
  return () => { s = (Math.imul(s, 1664525) + 1013904223) >>> 0; return s / 4294967296; };
}

const syll = ['ka', 'lo', 'mi', 'ne', 'tu', 'ra', 'shi', 'po', 'd', 'ex'];
function mkWords(n, seed, opts = {}) {
  const r = lcg(seed), out = [];
  for (let i = 0; i < n; i++) {
    let t = '';
    const len = 1 + Math.floor(r() * 4);
    for (let k = 0; k < len; k++) t += syll[Math.floor(r() * syll.length)];
    out.push({ id: i, text: t, value: 1 + Math.floor(r() * 100), rot: [0, 90, -45, 30][Math.floor(r() * 4)] });
  }
  return out;
}

const cases = [
  { name: 'archimedean', size: [500, 500], n: 40, seed: 3, spiral: 'archimedean', fixed: 0 },
  { name: 'rectangular', size: [640, 360], n: 40, seed: 4, spiral: 'rectangular', fixed: 0 },
  { name: 'rotated_padded', size: [500, 400], n: 45, seed: 5, spiral: 'archimedean', rotate: true, padding: 2 },
  { name: 'overflow_small', size: [200, 150], n: 30, seed: 6, spiral: 'archimedean', fixed: 0 },
  { name: 'many_batches', size: [1200, 900], n: 500, seed: 7, spiral: 'archimedean', range: [20, 60], padding: 0 },
  { name: 'scaled_rect', size: [600, 600], n: 60, seed: 8, spiral: 'rectangular', range: [10, 50], rotate: true },
  { name: 'odd_size', size: [333, 250], n: 30, seed: 9, spiral: 'rectangular', range: [8, 30], padding: 3 },
];

const out = [];
for (const c of cases) {
  const words = mkWords(c.n, c.seed);
  const range = c.range;
  const fsize = d => d.value;
  let fontSize = fsize;
  if (range) {
    const ext = [Math.min(...words.map(fsize)), Math.max(...words.map(fsize))];
    const sc = d3.scaleSqrt().domain(ext).range(range);
    fontSize = x => sc(fsize(x));
  } else {
    fontSize = () => 14 + (c.fixed || 0);
  }
  const layout = cloud();
  const tags = layout.words(words)
    .text(d => d.text).size(c.size).padding(() => c.padding ?? 1)
    .spiral(c.spiral).rotate(d => c.rotate ? d.rot : 0)
    .font(() => 'sans-serif').fontStyle(() => 'normal').fontWeight(() => 'normal')
    .fontSize(fontSize).random(lcg(c.seed * 7 + 1)).layout();
  const dx = layout.size()[0] >> 1, dy = layout.size()[1] >> 1;
  out.push({
    name: c.name, size: c.size, spiral: c.spiral, rotate: !!c.rotate,
    padding: c.padding ?? 1, range: c.range ?? null, seedRandom: c.seed * 7 + 1,
    words: words.map(w => ({ id: w.id, text: w.text, value: w.value, rot: w.rot })),
    placed: tags.map(w => ({ id: w.datum.id, value: w.datum.value, x: w.x + dx, y: w.y + dy, size: w.size, rotate: w.rotate })),
  });
}
console.log(JSON.stringify(out));
