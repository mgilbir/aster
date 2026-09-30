// Renders Vega / Vega-Lite specs with upstream Vega in node, the way
// testdata/vega-lite/gen_expected.mjs does (node-canvas measuring DejaVu Sans,
// TZ=UTC, seed 123456789), to confirm which engine is right when purego and
// the QuickJS reference disagree.
//
//   TZ=UTC NODE_PATH=purego/testdata/oracle-node/node_modules \
//     node purego/testdata/nodesvg.mjs OUTDIR spec1.vg.json spec2.vl.json ...
//
// Run from the repository root. Data resolves against testdata/vega-datasets,
// then purego/testdata/data. One "<name>.svg" (or "<name>.err") per spec.
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
const fontDir = 'internal/fonts/dejavu';
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

const roots = [path.resolve('testdata/vega-datasets'), path.resolve('purego/testdata/data')];
function localize(uri) {
  const m = /^https?:\/\/(?:cdn\.jsdelivr\.net\/npm\/vega-datasets@[^/]+|raw\.githubusercontent\.com\/vega\/vega-datasets\/[^/]+|vega\.github\.io\/vega-datasets)\/(data\/.+)$/.exec(uri);
  return m ? m[1] : uri;
}
const base = vega.loader({ mode: 'file' });
const resolve = (uri) => {
  uri = localize(uri);
  for (const r of roots) {
    const p = path.join(r, uri);
    if (fs.existsSync(p)) return p;
  }
  return uri;
};
const loader = {
  ...base,
  sanitize: (uri, options) => base.sanitize(resolve(uri), options),
  load: (uri, options) => base.load(resolve(uri), options),
};

const [outDir, ...files] = process.argv.slice(2);
fs.mkdirSync(outDir, { recursive: true });
for (const file of files) {
  const name = path.basename(file).replace(/\.(vl|vg)\.json$/, '');
  try {
    let spec = JSON.parse(fs.readFileSync(file, 'utf8'));
    if (file.endsWith('.vl.json')) spec = vl.compile(spec).spec;
    resetSVGDefIds();
    vega.setRandom(vega.randomLCG(123456789));
    const view = new vega.View(vega.parse(spec), { loader, renderer: 'none', logLevel: vega.Error });
    const svg = await view.toSVG();
    view.finalize();
    fs.writeFileSync(path.join(outDir, name + '.svg'), svg);
  } catch (err) {
    fs.writeFileSync(path.join(outDir, name + '.err'), String(err.message));
  }
}
