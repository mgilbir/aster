// The node side of enginebench: times upstream Vega 6.4.0 / Vega-Lite 6.4.3
// (testdata/oracle-node) on the same specs, with the same in-memory
// data, seed and timing loop as main.go, and writes the same JSON format.
// Text is measured by node-canvas with DejaVu, as the test oracle does; PNG
// is Vega's canvas renderer drawing on node-canvas (cairo) and encoding.
//
// Usage, from the repository root (see README.md):
//
//   TZ=UTC NODE_PATH=testdata/oracle-node/node_modules \
//     node internal/cmd/enginebench/bench.mjs [-budget ms] [-stages vl2vg,svg,png] [-filter re] > node.json
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';

const startup = performance.now();
const args = process.argv.slice(2);
const flag = (name, def) => {
  const i = args.indexOf('-' + name);
  return i >= 0 ? args[i + 1] : def;
};
// -budget takes main.go's Go duration syntax ("300ms", "1s") or milliseconds.
const budget = ((s) => {
  const m = /^([\d.]+)(ms|s)?$/.exec(s);
  if (!m) throw new Error('bad -budget ' + s);
  return Number(m[1]) * (m[2] === 's' ? 1000 : 1);
})(flag('budget', '300ms'));
const minRuns = Number(flag('min', '3'));
const maxRuns = Number(flag('max', '200'));
const stages = flag('stages', 'vl2vg,svg,png').split(',');
const filter = flag('filter', '') ? new RegExp(flag('filter', '')) : null;

const require = createRequire(path.join(path.resolve(process.env.NODE_PATH), 'x.js'));
const canvas = require('canvas');
const fontDir = 'internal/fonts/dejavu';
for (const [file, family, weight, style] of [
  ['DejaVuSans.ttf', 'DejaVu Sans', 'normal', 'normal'],
  ['DejaVuSans-Bold.ttf', 'DejaVu Sans', 'bold', 'normal'],
  ['DejaVuSans-Oblique.ttf', 'DejaVu Sans', 'normal', 'italic'],
  ['DejaVuSans-BoldOblique.ttf', 'DejaVu Sans', 'bold', 'italic'],
  ['DejaVuSansMono.ttf', 'DejaVu Sans Mono', 'normal', 'normal'],
  ['DejaVuSansMono-Bold.ttf', 'DejaVu Sans Mono', 'bold', 'normal'],
]) {
  canvas.registerFont(path.join(fontDir, file), { family, weight, style });
}
const generic = (font) =>
  font.replace(/\bsans-serif\b/g, '"DejaVu Sans"').replace(/\bmonospace\b/g, '"DejaVu Sans Mono"');
const proto = canvas.CanvasRenderingContext2D.prototype;
const fontProp = Object.getOwnPropertyDescriptor(proto, 'font');
Object.defineProperty(proto, 'font', {
  ...fontProp,
  set(v) {
    fontProp.set.call(this, generic(String(v)));
  },
});

const t0 = performance.now();
const vega = await import(require.resolve('vega'));
const vl = await import(require.resolve('vega-lite'));
const { resetSVGDefIds } = await import(require.resolve('vega-scenegraph'));
const initMS = performance.now() - t0;
if (vega.textMetrics.width !== vega.textMetrics.measureWidth) {
  console.error('vega is not using canvas text measurement');
  process.exit(2);
}

// In-memory datasets, with the CDN URLs mapped onto the local copy (main.go's
// memLoader does the same).
const dataDirs = ['testdata/vega-datasets', 'testdata/data'].map((d) => path.resolve(d));
const cdn = /^https?:\/\/(?:cdn\.jsdelivr\.net\/npm\/vega-datasets@[^/]+|raw\.githubusercontent\.com\/vega\/vega-datasets\/[^/]+|vega\.github\.io\/vega-datasets)\/(data\/.+)$/;
const cache = new Map();
const localize = (uri) => {
  const m = cdn.exec(uri);
  return m ? m[1] : uri;
};
const loader = vega.loader();
// sanitize also serves hyperlink hrefs, so only load rejects non-local URIs.
loader.sanitize = async (uri) => ({ href: localize(uri) });
loader.load = async (uri) => {
  uri = localize(uri);
  if (uri.includes('://') || uri.includes('..') || path.isAbsolute(uri)) throw new Error('not a local dataset: ' + uri);
  if (cache.has(uri)) return cache.get(uri);
  for (const d of dataDirs) {
    try {
      const s = fs.readFileSync(path.join(d, uri), 'utf8');
      cache.set(uri, s);
      return s;
    } catch {}
  }
  throw new Error('no such dataset: ' + uri);
};

function view(vg, renderer) {
  resetSVGDefIds();
  vega.setRandom(vega.randomLCG(123456789));
  return new vega.View(vega.parse(vg), { loader, renderer, logger: quiet });
}

async function toSVG(vg) {
  const v = view(vg, 'none');
  try {
    return await v.toSVG();
  } finally {
    v.finalize();
  }
}

async function toPNG(vg) {
  const v = view(vg, 'none');
  try {
    const c = await v.toCanvas();
    return c.toBuffer('image/png');
  } finally {
    v.finalize();
  }
}

// run returns the function one timed run calls, from the raw spec text (the
// Go engines also parse the spec on every call).
// Compiler warnings are dropped, as the Go engines drop them.
const quiet = vega.logger(vega.None);
const compile = (text) => vl.compile(JSON.parse(text), { logger: quiet }).spec;
function runner(text, lite, stage) {
  if (stage === 'vl2vg') return async () => JSON.stringify(compile(text));
  const vg = lite ? () => compile(text) : () => JSON.parse(text);
  return stage === 'svg' ? async () => toSVG(vg()) : async () => toPNG(vg());
}

const specs = [];
for (const [suite, dir, ext] of [
  ['vl-examples', 'testdata/vega-lite/v6.4.3/specs', '.vl.json'],
  ['vg-gallery', 'testdata/corpus/vg-gallery', '.vg.json'],
]) {
  for (const f of fs.readdirSync(dir).filter((f) => f.endsWith(ext)).sort()) {
    const name = f.slice(0, -ext.length);
    if (filter && !filter.test(suite + '/' + name)) continue;
    specs.push({ suite, name, lite: ext === '.vl.json', text: fs.readFileSync(path.join(dir, f), 'utf8') });
  }
}

const bar = fs.readFileSync('testdata/vega-lite/v6.4.3/specs/bar.vl.json', 'utf8');
let t1 = performance.now();
await runner(bar, true, 'svg')();
const firstMS = performance.now() - t1;

const errMsg = (e) => String((e && e.message) || e).split('\n')[0].slice(0, 200);
const results = [];
const jobs = [];
for (const s of specs) {
  for (const stage of stages) {
    if (stage === 'vl2vg' && !s.lite) continue;
    const run = runner(s.text, s.lite, stage);
    try {
      await run();
      jobs.push({ s, stage, run });
    } catch (e) {
      results.push({ suite: s.suite, name: s.name, stage, err: errMsg(e) });
    }
  }
}
console.error(`node: ${jobs.length} timed cases, ${results.length} failed in the warm pass`);

let done = 0;
for (const { s, stage, run } of jobs) {
  const times = [];
  const start = performance.now();
  let err = null;
  while (times.length < maxRuns) {
    const a = performance.now();
    try {
      await run();
    } catch (e) {
      err = errMsg(e);
      break;
    }
    const d = performance.now() - a;
    times.push(d);
    if (d > 2000 || (times.length >= minRuns && performance.now() - start >= budget)) break;
  }
  if (err) {
    results.push({ suite: s.suite, name: s.name, stage, err });
  } else {
    times.sort((a, b) => a - b);
    results.push({ suite: s.suite, name: s.name, stage, runs: times.length, median_ms: times[times.length >> 1], min_ms: times[0] });
  }
  if (++done % 100 === 0) console.error(`node: ${done}/${jobs.length}`);
}

process.stdout.write(
  JSON.stringify(
    {
      engine: 'node ' + process.versions.node.split('.')[0],
      version: `Vega ${vega.version} / Vega-Lite ${vl.version}, node-canvas ${require('canvas/package.json').version}`,
      env: `node ${process.version} ${process.platform}/${process.arch}, ${os.cpus()[0].model}`,
      init_ms: initMS + startup, // module import plus the node startup before this script ran
      first_svg_ms: firstMS,
      peak_rss_mb: process.resourceUsage().maxRSS / 1024,
      budget_ms: budget,
      results,
    },
    null,
    1,
  ) + '\n',
);
