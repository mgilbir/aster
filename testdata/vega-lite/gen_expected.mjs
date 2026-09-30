// Renders the Vega-Lite example specs to the expected SVGs the root package's
// tests compare against, the way Vega-Lite's own build does
// (scripts/build-example.sh: vl2vg, then `vg2svg --seed 123456789` with
// node-canvas measuring text), but with the Vega / Vega-Lite versions this
// repository vendors instead of whatever Vega-Lite's lockfile held.
//
// Two deliberate differences from upstream's build, both so the root engine
// can match the output:
//   - TZ=UTC rather than America/Los_Angeles: the engine runs in UTC.
//   - DejaVu Sans is registered as the sans-serif face explicitly; upstream
//     gets it implicitly from Ubuntu's fontconfig, while macOS would pick
//     Helvetica.
//
// Usage, from the repository root:
//
//   (cd purego/testdata/oracle-node && npm ci)
//   TZ=UTC NODE_PATH=purego/testdata/oracle-node/node_modules \
//     node testdata/vega-lite/gen_expected.mjs v6.4.3
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

if (process.env.TZ !== 'UTC') {
  console.error('run with TZ=UTC');
  process.exit(2);
}
const require = createRequire(path.join(path.resolve(process.env.NODE_PATH), 'x.js'));

// Vega measures text in canvas mode by setting a CSS font string
// (vega.font(item), e.g. "bold 11px sans-serif") on a canvas context and
// calling measureText. Map the generic families to the registered DejaVu
// faces at that point, as fontconfig does on Ubuntu, so upstream's own
// measurement code (trimming, `limit` truncation, caching) runs unmodified.
// This must happen before vega loads: vega-scenegraph creates its context at
// import time.
const canvas = require('canvas');
const fontDir = 'internal/textmeasure/fonts/dejavu';
for (const [file, family, weight, style] of [
  ['DejaVuSans.ttf', 'DejaVu Sans', 'normal', 'normal'],
  ['DejaVuSans-Bold.ttf', 'DejaVu Sans', 'bold', 'normal'],
  ['DejaVuSans-Oblique.ttf', 'DejaVu Sans', 'normal', 'italic'],
  ['DejaVuSans-BoldOblique.ttf', 'DejaVu Sans', 'bold', 'italic'],
  ['DejaVuSansMono.ttf', 'DejaVu Sans Mono', 'normal', 'normal'],
  ['DejaVuSansMono-Bold.ttf', 'DejaVu Sans Mono', 'bold', 'normal'],
  ['DejaVuSansMono-Oblique.ttf', 'DejaVu Sans Mono', 'normal', 'italic'],
  ['DejaVuSansMono-BoldOblique.ttf', 'DejaVu Sans Mono', 'bold', 'italic'],
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

const vega = await import(require.resolve('vega'));
const vl = await import(require.resolve('vega-lite'));
const { resetSVGDefIds } = await import(require.resolve('vega-scenegraph'));
if (vega.textMetrics.width !== vega.textMetrics.measureWidth) {
  console.error('vega is not using canvas text measurement');
  process.exit(2);
}

const datasets = path.resolve('testdata/vega-datasets');
// Offline data: relative URLs resolve against vega-datasets, and the CDN
// URLs some examples use are served from the same local copy.
function localize(uri) {
  const m = /^https?:\/\/(?:cdn\.jsdelivr\.net\/npm\/vega-datasets@[^/]+|raw\.githubusercontent\.com\/vega\/vega-datasets\/[^/]+|vega\.github\.io\/vega-datasets)\/(data\/.+)$/.exec(uri);
  return m ? m[1] : uri;
}
// Upstream renders from the directory holding data/ with no base URL, so an
// image mark's href stays relative ("data/ffox.png"); do the same.
const base = vega.loader({ mode: 'file' });
const loader = {
  ...base,
  sanitize: (uri, options) => base.sanitize(localize(uri), options),
  load: (uri, options) => base.load(localize(uri), options),
};

const version = process.argv[2] || 'v6.4.3';
const specDir = path.resolve('testdata/vega-lite', version, 'specs');
const outDir = path.resolve('testdata/vega-lite', version, 'expected');
process.chdir(datasets);
fs.mkdirSync(outDir, { recursive: true });

let ok = 0;
const failed = [];
for (const file of fs.readdirSync(specDir).filter((f) => f.endsWith('.vl.json')).sort()) {
  const name = file.slice(0, -'.vl.json'.length);
  try {
    const spec = JSON.parse(fs.readFileSync(path.join(specDir, file), 'utf8'));
    const vg = vl.compile(spec).spec;
    // Each file starts its clip-path/gradient ids at zero, as the root
    // engine's bridge does per render.
    resetSVGDefIds();
    vega.setRandom(vega.randomLCG(123456789));
    const view = new vega.View(vega.parse(vg), { loader, renderer: 'none', logLevel: vega.Error });
    const svg = await view.toSVG();
    view.finalize();
    fs.writeFileSync(path.join(outDir, name + '.svg'), svg);
    ok++;
  } catch (err) {
    failed.push(`${name}: ${err.message}`);
  }
}
console.error(`rendered ${ok}, failed ${failed.length}`);
for (const f of failed) console.error('  ' + f);
