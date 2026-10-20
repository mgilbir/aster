// Records what upstream Vega gives for small specifications the runtime's
// tests compare with node: each data set's tuples (JSON.stringify of
// view.data(name)), the first error the view logs, and the texts its SVG
// draws; for prefacet, also after signal writes.
//
//   NODE_PATH=testdata/oracle-node/node_modules node gen_node_records.mjs crossfilter > testdata/crossfilter.node.json
//   NODE_PATH=testdata/oracle-node/node_modules node gen_node_records.mjs prefacet > testdata/prefacet.node.json
//   NODE_PATH=testdata/oracle-node/node_modules node gen_node_records.mjs accessors > testdata/expr-accessor-names.node.json
//
// (testdata/vectors.txt runs them; crossfilter_test.go, prefacet_test.go and
// divergences_test.go read them.)
process.env.TZ = 'UTC';
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.resolve(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const here = path.dirname(fileURLToPath(import.meta.url));

// view runs spec and returns it with the errors it logged, in order.
async function view(spec) {
  const errors = [];
  const logger = vega.logger(vega.Error);
  logger.error = (...args) => {
    const e = args.find((a) => a instanceof Error);
    errors.push(e ? e.message : args.map(String).join(' '));
    return logger;
  };
  vega.setRandom(vega.randomLCG(12345));
  const v = new vega.View(vega.parse(spec), { renderer: 'none', logger });
  await v.runAsync();
  return { v, errors };
}

const texts = (svg) => [...svg.matchAll(/<text[^>]*>([^<]*)<\/text>/g)].map((m) => m[1]);
const dataNames = (spec) => (spec.data || []).map((d) => d.name);
const dump = (v, names) => Object.fromEntries(names.map((n) => [n, JSON.stringify(v.data(n))]));

const mode = process.argv[2];
const out = {};
if (mode === 'crossfilter') {
  const dir = path.join(here, 'testdata/crossfilter');
  for (const f of fs.readdirSync(dir).filter((f) => f.endsWith('.vg.json')).sort()) {
    const spec = JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
    const { v, errors } = await view(spec);
    out[f] = { data: dump(v, dataNames(spec)), error: errors[0] || '', texts: texts(await v.toSVG()) };
    v.finalize();
  }
} else if (mode === 'prefacet') {
  // Rendered, then each write of writes.json applied (View.signal, then
  // runAsync); every error logged across the runs, and the texts drawn last.
  const dir = path.join(here, 'testdata/prefacet');
  const writes = JSON.parse(fs.readFileSync(path.join(dir, 'writes.json'), 'utf8'));
  for (const f of fs.readdirSync(dir).filter((f) => f.endsWith('.vg.json')).sort()) {
    const spec = JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
    const w = writes[f] || {};
    const { v, errors } = await view(spec);
    for (const [name, value] of Object.entries(w)) {
      v.signal(name, value);
      await v.runAsync();
    }
    out[f] = { writes: w, errors, texts: texts(await v.toSVG()) };
    v.finalize();
  }
} else if (mode === 'accessors') {
  // Every data set of the spec but its source, src.
  const spec = JSON.parse(fs.readFileSync(path.join(here, 'testdata/expr-accessor-names.vg.json'), 'utf8'));
  const { v } = await view(spec);
  Object.assign(out, dump(v, dataNames(spec).filter((n) => n !== 'src')));
  v.finalize();
  process.stdout.write(JSON.stringify(out, null, 2) + '\n');
  process.exit(0);
} else {
  throw new Error('mode: crossfilter, prefacet or accessors');
}
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
