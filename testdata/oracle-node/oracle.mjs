// The test oracle: upstream Vega / Vega-Lite running in node, answering one
// request per line on stdin with one JSON line on stdout. The Go tests start
// it on demand (oracle_test.go) and cache its answers in testdata/oracle-cache.
//
// Rendering follows Vega-Lite's own example build (vl2vg, then vg2svg --seed
// 123456789 with node-canvas measuring text), with two deliberate choices so
// the engine can be compared with it: TZ=UTC, and text measured with DejaVu
// Sans / DejaVu Sans Mono whatever family a spec names (upstream gets DejaVu
// implicitly from Ubuntu's fontconfig for the generic families).
//
//   request:  {"id": 1, "op": "svg" | "compile", "lite": true, "spec": "<JSON text>"}
//             {"id": 2, "op": "png", "svg": "<svg ...>", "scale": 2}
//   response: {"id": 1, "svg": "...", "vega": {...}, "png": "<base64>", "err": "..."}
//
// "svg" renders (compiling first when lite) and also returns the compiled
// Vega for Vega-Lite; "compile" only compiles; "png" rasterizes an SVG with
// resvg (resvg-napi, only in testdata/oracle-node), the reference for
// the engine's rasterizer. The first line written is {"ready": true,
// "version": "..."}.
//
//   cd testdata/oracle-node && TZ=UTC NODE_PATH=node_modules node oracle.mjs
//
// The same script serves testdata/oracle-node-vl5 (Vega-Lite 5.8), run in that
// directory with NODE_PATH pointing there.
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import readline from 'node:readline';
import { fileURLToPath } from 'node:url';

// Paths are resolved from the repository root, whatever the working
// directory (oracle_test.go runs node in the module set's directory so the
// node version pinned there applies).
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

if (process.env.TZ !== 'UTC') {
  console.error('run with TZ=UTC');
  process.exit(2);
}
const require = createRequire(path.join(path.resolve(process.env.NODE_PATH), 'x.js'));

// Vega measures text in canvas mode by setting a CSS font string on a canvas
// context and calling measureText. The family list is resolved there the way
// the engine resolves it (oracleConverter in compare_test.go): the first
// family it has — the embedded Liberation faces and the DejaVu faces the
// comparison registers — with sans-serif, serif and unknown families going
// to DejaVu Sans, monospace to DejaVu Sans Mono, and Noto Emoji as the
// fallback for emoji. So node-canvas never falls back to the machine's system
// fonts and the oracle measures the same on every machine. Upstream's own
// measurement code (trimming, `limit` truncation, caching) runs unmodified.
// This must happen before vega loads: vega-scenegraph creates its context at
// import time.
const canvas = require('canvas');
const faces = [
  ...[['Sans', ''], ['Sans-Bold', 'bold'], ['Sans-Oblique', 'italic'], ['Sans-BoldOblique', 'bold italic'],
    ['SansMono', 'mono'], ['SansMono-Bold', 'mono bold'], ['SansMono-Oblique', 'mono italic'], ['SansMono-BoldOblique', 'mono bold italic'],
  ].map(([f, k]) => [`dejavu/DejaVu${f}.ttf`, k.includes('mono') ? 'DejaVu Sans Mono' : 'DejaVu Sans', k]),
  ...['Sans', 'Serif', 'Mono'].flatMap((f) =>
    [['Regular', ''], ['Bold', 'bold'], ['Italic', 'italic'], ['BoldItalic', 'bold italic']].map(([s, k]) => [
      `liberation/Liberation${f}-${s}.ttf`,
      `Liberation ${f}`,
      k,
    ]),
  ),
];
for (const [file, family, k] of faces) {
  canvas.registerFont(path.join(repo, 'internal/fonts', file), {
    family,
    weight: k.includes('bold') ? 'bold' : 'normal',
    style: k.includes('italic') ? 'italic' : 'normal',
  });
}
canvas.registerFont(path.join(repo, 'internal/fonts/notoemoji/NotoEmoji.ttf'), { family: 'Noto Emoji' });
const known = new Set(faces.map(([, family]) => family.toLowerCase()));

// mapFonts rewrites the family list of a CSS font shorthand
// ("italic bold 11px \"Helvetica Neue\", sans-serif") to one registered family.
function mapFonts(font) {
  const m = /^(.*?\d[\d.]*(?:px|pt|em|rem|%)(?:\s*\/\s*\S+)?\s+)(.+)$/.exec(font);
  if (!m) return font;
  let family = 'DejaVu Sans';
  for (let f of m[2].split(',')) {
    f = f.trim().replace(/^["']|["']$/g, '');
    if (known.has(f.toLowerCase())) {
      family = f;
      break;
    }
    if (/^monospace$/i.test(f)) {
      family = 'DejaVu Sans Mono';
      break;
    }
    if (/^(sans-serif|serif|cursive|fantasy)$/i.test(f)) break;
  }
  return m[1] + `"${family}", "Noto Emoji"`;
}
const proto = canvas.CanvasRenderingContext2D.prototype;
const fontProp = Object.getOwnPropertyDescriptor(proto, 'font');
Object.defineProperty(proto, 'font', {
  ...fontProp,
  set(v) {
    fontProp.set.call(this, mapFonts(String(v)));
  },
});

const vega = await import(require.resolve('vega'));
const vl = await import(require.resolve('vega-lite'));
const scenegraph = await import(require.resolve('vega-scenegraph'));
if (vega.textMetrics.width !== vega.textMetrics.measureWidth) {
  console.error('vega is not using canvas text measurement');
  process.exit(2);
}

// Offline data. Relative URLs resolve against testdata/vega-datasets (the
// working directory while serving, so an image mark's href stays relative,
// "data/ffox.png", as in upstream's build), then testdata/data; the CDN URLs
// some specs use are served from the same local copy.
const roots = [path.join(repo, 'testdata/vega-datasets'), path.join(repo, 'testdata/data')];
const cdn = /^https?:\/\/(?:cdn\.jsdelivr\.net\/npm\/vega-datasets@[^/]+|raw\.githubusercontent\.com\/vega\/vega-datasets\/[^/]+|vega\.github\.io\/vega-datasets)\/(data\/.+)$/;
const localize = (uri) => {
  const m = cdn.exec(uri);
  return m ? m[1] : uri;
};
const base = vega.loader({ mode: 'file' });
const loader = {
  ...base,
  sanitize: (uri, options) => base.sanitize(localize(uri), options),
  load: async (uri, options) => {
    const u = localize(uri);
    if (!u.includes('://') && !path.isAbsolute(u)) {
      for (const r of roots) {
        const p = path.join(r, u);
        if (p.startsWith(r + path.sep) && fs.existsSync(p)) return fs.readFileSync(p, 'utf8');
      }
    }
    return base.load(u, options);
  },
};
process.chdir(roots[0]);

// Rasterization reference: resvg (resvg-napi) with the engine's default font
// plan, the embedded Liberation faces plus Noto Emoji and no system fonts.
let resvg = null;
let rasterFonts = null;
try {
  resvg = require('resvg-napi');
  rasterFonts = new resvg.FontDatabase();
  for (const f of ['Sans', 'Serif', 'Mono']) {
    for (const s of ['Regular', 'Bold', 'Italic', 'BoldItalic']) {
      rasterFonts.loadFontFile(path.join(repo, `internal/fonts/liberation/Liberation${f}-${s}.ttf`));
    }
  }
  rasterFonts.loadFontFile(path.join(repo, 'internal/fonts/notoemoji/NotoEmoji.ttf'));
  rasterFonts.setSansSerifFamily('Liberation Sans');
  rasterFonts.setSerifFamily('Liberation Serif');
  rasterFonts.setMonospaceFamily('Liberation Mono');
  rasterFonts.setCursiveFamily('Liberation Sans');
  rasterFonts.setFantasyFamily('Liberation Sans');
} catch {
  resvg = null;
}
function rasterize(svg, scale) {
  if (!resvg) throw new Error('resvg-napi is not installed in this module set');
  const doc = new resvg.Resvg(svg, { fontFamily: 'Liberation Sans' }, rasterFonts);
  return doc.renderPng({ scale: scale || 1 }).toString('base64');
}

const quiet = vega.logger(vega.None);
const compile = (spec) => vl.compile(spec, { logger: quiet }).spec;

async function render(vg) {
  // Each render starts its clip-path/gradient ids at zero and reseeds the
  // random generator, as the engine does per render.
  if (scenegraph.resetSVGDefIds) scenegraph.resetSVGDefIds();
  vega.setRandom(vega.randomLCG(123456789));
  const view = new vega.View(vega.parse(vg), { loader, renderer: 'none', logger: quiet });
  try {
    return await view.toSVG();
  } finally {
    view.finalize();
  }
}

// A failing hyperlink sanitization inside the SVG renderer surfaces as an
// unhandled rejection; it must not take the oracle down.
process.on('unhandledRejection', (err) => console.error('oracle: unhandled rejection:', err && err.message));

const write = (obj) => process.stdout.write(JSON.stringify(obj) + '\n');
write({
  ready: true,
  version:
    `vega ${vega.version} / vega-lite ${vl.version} / node ${process.version} / canvas ${require('canvas/package.json').version}` +
    (resvg ? ` / resvg-napi ${require('resvg-napi/package.json').version}` : ''),
});

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of rl) {
  if (!line.trim()) continue;
  let req;
  try {
    req = JSON.parse(line);
  } catch (e) {
    write({ id: null, err: 'oracle: bad request: ' + e.message });
    continue;
  }
  const res = { id: req.id };
  try {
    if (req.op === 'png') {
      res.png = rasterize(req.svg, req.scale);
      write(res);
      continue;
    }
    const spec = JSON.parse(req.spec);
    const vg = req.lite ? compile(spec) : spec;
    // Snapshot before rendering: vega.parse mutates the spec it is given
    // (it fills in defaults such as a CSV format's delimiter).
    if (req.lite) res.vega = structuredClone(vg);
    if (req.op === 'svg') res.svg = await render(vg);
  } catch (e) {
    res.err = String((e && e.message) || e).split('\n')[0];
  }
  write(res);
}
