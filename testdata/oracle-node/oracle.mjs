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
//             {"id": 3, "op": "svg", "spec": "...", "wordcloud": "box"}
//   response: {"id": 1, "svg": "...", "vega": {...}, "png": "<base64>", "err": "...",
//              "warnings": ["..."], "tainted": "Object.prototype.mean", "oracleErr": "..."}
//
// "err" is upstream's answer: the error it throws for the input. "oracleErr"
// is the oracle failing to answer (a request it cannot parse, an unknown op,
// a generator that throws, resvg-napi missing): it is not an answer, and the
// Go side never caches it and fails the test that asked.
//
// "svg" renders (compiling first when lite) and also returns the compiled
// Vega for Vega-Lite; "compile" only compiles, and lists the compiler's
// warnings; "png" rasterizes an SVG with
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
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import { fileURLToPath } from 'node:url';
import { Worker } from 'node:worker_threads';
import { boxContext } from './wordcloud-box.mjs';

// node must not outlive the Go process that drives it. An input can keep the
// main thread busy for good (upstream looping on it), so it cannot watch for
// that itself: a worker thread polls the parent the Go side names and kills
// the process once that parent is gone.
if (process.env.ASTER_ORACLE_PARENT) {
  new Worker(
    `const parent = ${Number(process.env.ASTER_ORACLE_PARENT)};
     setInterval(() => {
       try { process.kill(parent, 0); } catch (e) { if (e.code === 'ESRCH') process.kill(process.pid, 'SIGKILL'); }
     }, 1000);`,
    { eval: true },
  ).unref();
}

// Paths are resolved from the repository root, whatever the working
// directory (oracle_test.go runs node in the module set's directory so the
// node version pinned there applies).
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

// The clock is pinned, before anything reads it: now() compiles to Date.now
// and datetime() with no arguments to new Date(), so a chart that draws the
// current time renders the same instant on every run. The engine's tests pin
// the same instant (WithClockForTest in export_test.go).
const PINNED_NOW = Date.UTC(2026, 0, 1);
{
  const RealDate = Date;
  globalThis.Date = class extends RealDate {
    constructor(...args) {
      super(...(args.length === 0 ? [PINNED_NOW] : args));
    }
    static now() {
      return PINNED_NOW;
    }
  };
}

// TZ selects the zone local time is in: UTC for the corpus, other zones for
// the time-zone sweep. It is part of the version, hence of the cache key.
if (!process.env.TZ) {
  console.error('set TZ (UTC, or an IANA zone)');
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
// Fontconfig (node-canvas's font lookup on Linux) must see only the
// repository's fonts: with the machine's configuration and fonts, CI's
// runner resolved the registered faces differently. Set before node-canvas
// loads, since fontconfig reads its configuration once.
{
  // The Go side makes the directory and removes it when it stops this
  // process (a killed process runs no exit handler); run by hand, make one.
  const dir = process.env.ASTER_ORACLE_FONTDIR || fs.mkdtempSync(path.join(os.tmpdir(), 'aster-oracle-fonts-'));
  const conf = path.join(dir, 'fonts.conf');
  fs.writeFileSync(
    conf,
    `<?xml version="1.0"?>\n<!DOCTYPE fontconfig SYSTEM "fonts.dtd">\n<fontconfig>\n` +
      ['dejavu', 'liberation', 'notoemoji'].map((d) => `  <dir>${path.join(repo, 'internal/fonts', d)}</dir>\n`).join('') +
      // the oracle's own tests plant host fonts here (internal/oracle)
      (process.env.ASTER_ORACLE_TEST_FONTCONF || '') +
      `  <cachedir>${path.join(dir, 'cache')}</cachedir>\n</fontconfig>\n`,
  );
  process.env.FONTCONFIG_FILE = conf;
  process.on('exit', () => fs.rmSync(dir, { recursive: true, force: true }));
}
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

// node-canvas's Image forgets a remote URL: its src getter returns '' before,
// and even after, the fetch (node-canvas#118), so upstream in node writes
// href="" for every remote image, and it fetches over the network. A browser
// keeps the URL. The oracle does too, and never fetches: a remote image fails
// to load, offline, as in the engine's tests.
{
  const src = Object.getOwnPropertyDescriptor(canvas.Image.prototype, 'src');
  const remote = new WeakMap();
  Object.defineProperty(canvas.Image.prototype, 'src', {
    configurable: true,
    set(v) {
      if (typeof v === 'string' && /^\s*https?:\/\//.test(v)) {
        remote.set(this, v);
        setTimeout(() => this.onerror && this.onerror(new Error('oracle: offline')));
        return;
      }
      remote.delete(this);
      src.set.call(this, v);
    },
    get() {
      return remote.has(this) ? remote.get(this) : src.get.call(this);
    },
  });
}

// mapFonts rewrites the family list of a CSS font shorthand
// ("italic bold 11px \"Helvetica Neue\", sans-serif") to one registered family.
// The pattern is the engine's (canvasFontFamilyRe in internal/text): a size
// with any unit the canvas parser reads, in either case, white space and
// newlines as the canvas parser reads them, and a font with no family at all
// ("12px", "12px ": the canvas then takes the host's default family). A string
// it does not match is given to the canvas as it is, with whatever family list
// it has; see unmappedFont for what happens when the canvas accepts one.
const fontFamilyRe = /^([\s\S]*?\d[\d.]*(?:px|pt|em|rem|cm|mm|in|pc|%)(?:[\t\n\f\r ]*\/[\t\n\f\r ]*[^\t\n\f\r ]+)?)(?:[\t\n\f\r ]+([\s\S]*))?$/i;
function mapFonts(font) {
  const m = fontFamilyRe.exec(font);
  if (!m) return font;
  let family = 'DejaVu Sans';
  for (let f of (m[2] || '').split(',')) {
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
  return m[1] + ` "${family}", "Noto Emoji"`;
}
const proto = canvas.CanvasRenderingContext2D.prototype;
const fontProp = Object.getOwnPropertyDescriptor(proto, 'font');
// A canvas context keeps its font when it is given a string it cannot parse
// (node-canvas, as the HTML specification says), and vega-scenegraph measures
// with one context and a width cache that live as long as the process. An
// answer that depends on an unparseable font then depends on the fonts the
// process measured before: it is a fresh process's only if the process was
// fresh. So a rejected assignment is noted (the context's font did not change
// and this string never changed it), and the answer carries it (fontRejected),
// for the Go side to ask again of a fresh process. A valid string that changes
// nothing (the font already held, in a spelling not seen before) is taken for
// a rejected one: the cost is a repeated answer, never a wrong one.
let fontRejected = false;
const fontAccepted = new Map();
// The canvas accepts a font string the pattern above does not match only if it
// is one the pattern should have matched; its family list would then be
// resolved by the host, and the answer is not one the engine can be compared
// with (the answer is marked, see unmappedFont).
const scratch = canvas.createCanvas(1, 1).getContext('2d');
const accepts = (font) =>
  ['1px Sentinel1', '2px Sentinel2'].some((sentinel) => {
    fontProp.set.call(scratch, sentinel);
    const held = fontProp.get.call(scratch);
    fontProp.set.call(scratch, font);
    return fontProp.get.call(scratch) !== held;
  });
// A new context starts at the canvas default, 10px sans-serif, and keeps its
// font when it is given a string it rejects. Left to the canvas, "sans-serif"
// is resolved by the host (Pango's own font map: a macOS system font on
// macOS, fontconfig's choice on Linux), and the font a rejected string leaves
// behind would be the host's. So every context is given, as soon as it is
// made, what the engine means by "10px sans-serif": the font mapFonts makes
// of it. (The engine's context starts at the same string and resolves it
// through its default family; internal/text/canvasctx.go.)
const initialFont = mapFonts('10px sans-serif');
const initialized = new WeakSet();
const getContext = canvas.Canvas.prototype.getContext;
canvas.Canvas.prototype.getContext = function (...args) {
  const ctx = getContext.apply(this, args);
  if (ctx instanceof canvas.CanvasRenderingContext2D && !initialized.has(ctx)) {
    initialized.add(ctx);
    fontProp.set.call(ctx, initialFont);
    fontAccepted.set(initialFont, fontProp.get.call(ctx));
  }
  return ctx;
};
let unmappedFont = '';
Object.defineProperty(proto, 'font', {
  ...fontProp,
  set(v) {
    const mapped = mapFonts(String(v));
    if (mapped === String(v) && !fontFamilyRe.test(mapped) && accepts(mapped)) unmappedFont = mapped;
    const before = fontProp.get.call(this);
    fontProp.set.call(this, mapped);
    const after = fontProp.get.call(this);
    if (after === before && fontAccepted.get(mapped) !== after) fontRejected = true;
    else fontAccepted.set(mapped, after);
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
// The URLs that serve copies of vega-datasets, shared with the engine's test
// loader (compare_test.go) so both map exactly the same ones.
const datasetURLs = JSON.parse(fs.readFileSync(path.join(repo, 'testdata/oracle-node/dataset-urls.json'), 'utf8')).patterns.map(
  (p) => new RegExp(p),
);
const localize = (uri) => {
  for (const re of datasetURLs) {
    const m = re.exec(uri);
    if (m) return m[1];
  }
  return uri;
};
const base = vega.loader({ mode: 'file' });
const loader = {
  ...base,
  // Only loading maps a CDN URL onto the local copy: an href keeps the URL,
  // as the engine's loader keeps it.
  sanitize: (uri, options) => base.sanitize(uri, options),
  load: async (uri, options) => {
    const u = localize(uri);
    if (!u.includes('://') && !path.isAbsolute(u)) {
      for (const r of roots) {
        const p = path.join(r, u);
        if (p.startsWith(r + path.sep) && fs.existsSync(p)) return fs.readFileSync(p, 'utf8');
      }
    }
    // Offline, like the engine's test loader: a URL the local copy does not
    // serve fails to load rather than reaching the network.
    if (/^data:/.test(u)) return base.load(u, options);
    throw new Error('oracle: offline, not a local dataset: ' + uri);
  },
};
process.chdir(roots[0]);

// Rasterization reference: resvg (resvg-napi) with the engine's default font
// plan, the embedded Liberation faces plus Noto Emoji and no system fonts.
let resvg = null;
let rasterFonts = null;
let resvgError = '';
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
} catch (e) {
  resvg = null;
  resvgError = String((e && e.message) || e).split('\n')[0];
}
// OracleError is a failure of the oracle, not upstream's answer.
class OracleError extends Error {}
function rasterize(svg, scale) {
  if (!resvg) throw new OracleError('resvg-napi did not load in this module set: ' + resvgError);
  const doc = new resvg.Resvg(svg, { fontFamily: 'Liberation Sans' }, rasterFonts);
  return doc.renderPng({ scale: scale || 1 }).toString('base64');
}

// A static render is the chart's first frame: timer event streams never fire,
// as in the engine (there is no event loop). In node they would fire while a
// render awaits, by wall-clock time.
vega.View.prototype.timer = function () {};

const quiet = vega.logger(vega.None);
const compile = (spec) => vl.compile(spec, { logger: quiet }).spec;

// compileWarnings compiles a Vega-Lite spec and collects what the compiler
// logs as warnings (and errors it logs without throwing), each as the text it
// would print.
function compileWarnings(spec) {
  const warnings = [];
  let lvl = vega.Warn;
  const text = (args) => args.map((a) => (typeof a === 'string' ? a : String(a))).join(' ');
  const logger = {
    level(l) {
      if (l === undefined) return lvl;
      lvl = l;
      return logger;
    },
    error(...args) {
      warnings.push(text(args));
      return logger;
    },
    warn(...args) {
      warnings.push(text(args));
      return logger;
    },
    info() {
      return logger;
    },
    debug() {
      return logger;
    },
  };
  return { vg: vl.compile(spec, { logger }).spec, warnings };
}

function newView(vg) {
  // Each render starts its clip-path/gradient ids at zero and reseeds the
  // random generator, as the engine does per render.
  if (scenegraph.resetSVGDefIds) scenegraph.resetSVGDefIds();
  vega.setRandom(vega.randomLCG(123456789));
  return new vega.View(vega.parse(vg), { loader, renderer: 'none', logger: quiet });
}

// renderAfter renders, writes each signal (View.signal, then runAsync, as a
// binding or a host does) and renders again; the second SVG is the answer.
async function renderAfter(vg, writes) {
  const view = newView(vg);
  try {
    await view.toSVG();
    for (const { name, value } of writes) {
      view.signal(name, value);
      await view.runAsync();
    }
    return await view.toSVG();
  } finally {
    view.finalize();
  }
}

// Sweep generators live in sweeps/<name>.mjs and export generate(ctx), which
// returns { cases, skips }; ctx carries the modules, since a generator cannot
// import them by name (NODE_PATH does not apply to ES modules).
async function generate(name) {
  if (!/^[a-z0-9-]+$/.test(name)) throw new OracleError('bad sweep name: ' + name);
  try {
    const mod = await import(path.join(repo, 'testdata/oracle-node/sweeps', name + '.mjs'));
    return await mod.generate({ vega, vl, require, repo });
  } catch (e) {
    // A generator is the oracle's own code: its failure (a corpus not
    // fetched, a bug) is not an answer of upstream's.
    throw new OracleError(`generator ${name}: ` + String((e && e.message) || e).split('\n')[0]);
  }
}

// The "box" wordcloud: while a request asks for it, a canvas context made
// for the wordcloud layout (the only canvas a render creates: text is
// measured on the context vega-scenegraph made at import) is the
// deterministic one of wordcloud-box.mjs, which the engine's
// wordcloud.BoxRenderer reproduces, so the transform is compared without the
// platform's glyph rasterization.
let boxWordcloud = false;
{
  const getContext = canvas.Canvas.prototype.getContext;
  canvas.Canvas.prototype.getContext = function (...args) {
    return boxWordcloud ? boxContext(this.width, this.height) : getContext.apply(this, args);
  };
}

async function render(vg) {
  const view = newView(vg);
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
// The text stack is part of the version: node-canvas measures with the Pango
// and Cairo it was built against, and a probe width catches any difference
// in how the registered fonts resolve.
const probe = canvas.createCanvas(1, 1).getContext('2d');
probe.font = '11px sans-serif';
const probeWidth = probe.measureText('Hello World 123').width;
write({
  ready: true,
  version:
    `vega ${vega.version} / vega-lite ${vl.version} / node ${process.version} / canvas ${require('canvas/package.json').version}` +
    (resvg ? ` / resvg-napi ${require('resvg-napi/package.json').version}` : ' / resvg-napi none') +
    ` / pango ${canvas.pangoVersion} / cairo ${canvas.cairoVersion} / probe ${probeWidth}` +
    ` / platform ${process.platform}-${process.arch}` +
    (process.env.TZ === 'UTC' ? '' : ` / TZ ${process.env.TZ}`),
});

// Upstream can change the built-ins: Vega-Lite's aggregate node, given the
// field "__proto__", assigns its measures to Object.prototype, and every later
// compilation in the process then sees them (they appear in its config, which
// vega-util's mergeConfig builds with for…in). An answer must be what a
// fresh process gives, so after each request the built-ins are compared with
// their state before the first one; when they changed, the answer (valid: the
// change happened while computing it) is marked tainted and the Go side
// replaces the process before the next request.
const builtins = Object.entries({
  globalThis,
  Object, 'Object.prototype': Object.prototype,
  Array, 'Array.prototype': Array.prototype,
  Function, 'Function.prototype': Function.prototype,
  String, 'String.prototype': String.prototype,
  Number, 'Number.prototype': Number.prototype,
  Boolean, 'Boolean.prototype': Boolean.prototype,
  Symbol, 'Symbol.prototype': Symbol.prototype,
  BigInt, 'BigInt.prototype': BigInt.prototype,
  Date, 'Date.prototype': Date.prototype,
  RegExp, 'RegExp.prototype': RegExp.prototype,
  Error, 'Error.prototype': Error.prototype,
  Map, 'Map.prototype': Map.prototype,
  Set, 'Set.prototype': Set.prototype,
  WeakMap, 'WeakMap.prototype': WeakMap.prototype,
  Promise, 'Promise.prototype': Promise.prototype,
  JSON, Math, Reflect,
  'Iterator.prototype': Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]())),
  'TypedArray.prototype': Object.getPrototypeOf(Uint8Array.prototype),
});
const snapshotBuiltins = () =>
  builtins.map(([name, obj]) => [
    name,
    Object.getPrototypeOf(obj),
    Object.isExtensible(obj),
    // node keeps its own lazily made state on globalThis under symbols
    // (Symbol(undici.globalDispatcher.N), made by the first fetch), so there
    // only the string keys, which scripts reach, are compared.
    new Map(
      (obj === globalThis ? Object.getOwnPropertyNames(obj) : Reflect.ownKeys(obj)).map((k) => [
        k,
        Object.getOwnPropertyDescriptor(obj, k),
      ]),
    ),
  ]);
const pristine = snapshotBuiltins();
const changedBuiltin = () => {
  const now = snapshotBuiltins();
  for (let i = 0; i < now.length; i++) {
    const [name, proto, ext, props] = now[i];
    const [, proto0, ext0, props0] = pristine[i];
    if (proto !== proto0 || ext !== ext0) return name;
    for (const k of props0.keys()) if (!props.has(k)) return `${name}.${String(k)}`;
    for (const [k, d] of props) {
      const d0 = props0.get(k);
      if (!d0) return `${name}.${String(k)}`;
      for (const f of ['value', 'get', 'set', 'writable', 'enumerable', 'configurable']) {
        if (!Object.is(d[f], d0[f])) return `${name}.${String(k)}`;
      }
    }
  }
  return '';
};
// Module state upstream keeps between renders (registries, caches) can be
// changed by an input in ways the built-ins snapshot cannot see. A canary
// specification exercising the parser, the scales, the guides and the text
// metrics is rendered at the start and after every request; an answer that
// differs from the first means a request changed something, and the process
// is replaced (the answer itself, computed before, stands).
const canarySpec = {
  width: 120, height: 60, padding: 5,
  signals: [{ name: 'active', value: 1 }, { name: 'k', update: 'width / 4' }],
  data: [{ name: 't', values: [{ c: 'a', v: 1 }, { c: 'b', v: 3 }] }],
  scales: [
    { name: 'x', type: 'band', domain: { data: 't', field: 'c' }, range: 'width' },
    { name: 'y', type: 'linear', domain: { data: 't', field: 'v' }, range: 'height', nice: true },
    { name: 'col', type: 'ordinal', domain: { data: 't', field: 'c' }, range: 'category' },
  ],
  axes: [{ orient: 'bottom', scale: 'x', title: 'X' }, { orient: 'left', scale: 'y', title: 'Y', grid: true }],
  legends: [{ fill: 'col', title: 'L' }],
  title: { text: 'T', subtitle: 'S' },
  marks: [
    {
      type: 'rect', from: { data: 't' },
      encode: {
        enter: { x: { scale: 'x', field: 'c' }, width: { scale: 'x', band: 1 }, y: { scale: 'y', field: 'v' }, y2: { scale: 'y', value: 0 }, fill: { scale: 'col', field: 'c' } },
        update: { opacity: { signal: 'active' } },
      },
    },
    { type: 'text', encode: { enter: { x: { signal: 'k' }, y: { value: 5 }, text: { value: 'canary' }, limit: { value: 30 } } } },
  ],
};
// Rendered with estimated text widths: the canary must not touch the canvas
// context or the width cache, whose state is part of what an answer depends on.
const renderCanary = async () => {
  vega.textMetrics.canvas(false);
  try {
    return await render(JSON.parse(JSON.stringify(canarySpec)));
  } finally {
    vega.textMetrics.canvas(true);
  }
};
const canaryBefore = await renderCanary();
fontRejected = false;
unmappedFont = '';
const canaryChanged = async () => {
  try {
    return (await renderCanary()) !== canaryBefore;
  } catch {
    return true;
  }
};
let currentRequest = '';
const answer = async (res) => {
  // a request that names __proto__ can reach the prototype of any object it
  // is stored in: the process is not trusted after it
  if (currentRequest.includes('__proto__')) res.tainted = res.tainted || '__proto__';
  // d3-geo's measuring streams keep module-level variables (the first point of
  // the last ring ...) that a ring without points reads: a request that calls
  // them leaves state for the next one, and an answer is a fresh process's
  if (/geo(Area|Bounds|Centroid|Length)/.test(currentRequest)) res.tainted = res.tainted || 'd3-geo state';
  if (!res.tainted && (await canaryChanged())) {
    res.tainted = 'canary';
    console.error('oracle: the canary render changed; this process is replaced');
  }
  if (fontRejected) {
    res.fontRejected = true;
    fontRejected = false;
  }
  if (unmappedFont) {
    res.oracleErr = `the canvas accepted a font string whose families are not mapped: ${JSON.stringify(unmappedFont)}`;
    unmappedFont = '';
  }
  const changed = changedBuiltin();
  if (changed) {
    res.tainted = changed;
    console.error(`oracle: upstream changed ${changed}; this process is replaced`);
  }
  write(res);
};

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of rl) {
  if (!line.trim()) continue;
  currentRequest = line;
  let req;
  try {
    req = JSON.parse(line);
  } catch (e) {
    await answer({ id: null, oracleErr: 'bad request: ' + e.message });
    continue;
  }
  const res = { id: req.id };
  if (!['svg', 'compile', 'png', 'signals', 'generate'].includes(req.op) || (req.wordcloud && req.wordcloud !== 'box')) {
    await answer({ id: req.id, oracleErr: `bad request: op ${req.op}, wordcloud ${req.wordcloud}` });
    continue;
  }
  boxWordcloud = req.wordcloud === 'box';
  try {
    if (req.op === 'generate') {
      res.data = await generate(req.sweep);
      await answer(res);
      continue;
    }
    if (req.op === 'signals') {
      res.svg = await renderAfter(JSON.parse(req.spec), req.writes || []);
      await answer(res);
      continue;
    }
    if (req.op === 'png') {
      res.png = rasterize(req.svg, req.scale);
      await answer(res);
      continue;
    }
    const spec = JSON.parse(req.spec);
    let vg = spec;
    if (req.lite) {
      const c = compileWarnings(spec);
      vg = c.vg;
      if (req.op === 'compile' && c.warnings.length) res.warnings = c.warnings;
    }
    // Snapshot before rendering: vega.parse mutates the spec it is given
    // (it fills in defaults such as a CSV format's delimiter).
    // As JSON, which is what the compiled specification is: a value JSON
    // cannot hold (a function a repeat reference resolved to) is dropped,
    // as JSON.stringify drops it, rather than failing the clone.
    if (req.lite) res.vega = JSON.parse(JSON.stringify(vg));
    if (req.op === 'svg') res.svg = await render(vg);
  } catch (e) {
    const msg = String((e && e.message) || e).split('\n')[0];
    if (e instanceof OracleError) res.oracleErr = msg;
    else res.err = msg;
  } finally {
    boxWordcloud = false;
  }
  await answer(res);
}
// stdin closed: the Go side is done with this process.
process.exit(0);
