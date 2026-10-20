// Records golden layouts from upstream vega-wordcloud's CloudLayout.js.
//
// Upstream draws text on a canvas; here the upstream source is loaded with its
// `vega-canvas` import replaced by a deterministic canvas shim (wordcloud-box.mjs)
// that measures text as 0.6em per character and paints every word as a solid
// (rotated) box 0.8em above / 0.2em below the baseline. The Go BoxRenderer
// implements the same model, so both sides see identical bitmaps.
//
//   NODE_PATH=testdata/oracle-node/node_modules node testdata/gen_wordcloud.mjs > testdata/wordcloud.json
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';

const require = createRequire(path.resolve(process.env.NODE_PATH, 'x.js'));
const wcDir = path.join(process.env.NODE_PATH, 'vega-wordcloud');
let src = fs.readFileSync(path.join(wcDir, 'src/CloudLayout.js'), 'utf8');
src = src.replace("import {canvas} from 'vega-canvas';", "import {canvas} from './shim.mjs';");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'wc-'));
fs.writeFileSync(path.join(tmp, 'CloudLayout.mjs'), src);

// The shim is the oracle's (testdata/oracle-node/wordcloud-box.mjs), so the
// goldens and the oracle's wordcloud comparison lay words out alike.
const boxURL = new URL('../../../../testdata/oracle-node/wordcloud-box.mjs', import.meta.url).href;
fs.writeFileSync(path.join(tmp, 'shim.mjs'), `export { boxCanvas as canvas } from ${JSON.stringify(boxURL)};\n`);

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
