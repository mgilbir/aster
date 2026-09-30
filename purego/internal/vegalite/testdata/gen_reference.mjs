// Records the Vega that upstream vega-lite compile() emits for every spec under
// specs/<group>/*.vl.json, one minified JSON object per line:
//   {"name":"group/file","vega":{...}}   (or {"name":..,"error":"..."})
// Usage: NODE_PATH=<node_modules with vega-lite@6.4.3> node gen_reference.mjs | gzip -9 > reference.jsonl.gz
// For the Vega-Lite 5.8 references, point NODE_PATH at testdata/oracle-node-vl5/node_modules and write reference_vl5.jsonl.gz.
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vl = await import(require.resolve('vega-lite'));
const here = path.dirname(fileURLToPath(import.meta.url));
const specs = path.join(here, 'specs');
const optsFile = path.join(here, 'options.json'); // optional {"group/file": {config: ...}}
const opts = fs.existsSync(optsFile) ? JSON.parse(fs.readFileSync(optsFile, 'utf8')) : {};
const warn = console.warn; console.warn = () => {}; console.log = () => {};
const out = [];
for (const group of fs.readdirSync(specs).sort()) {
  for (const f of fs.readdirSync(path.join(specs, group)).filter((f) => f.endsWith('.json')).sort()) {
    const name = `${group}/${f.replace(/\.json$/, '')}`;
    try {
      const input = JSON.parse(fs.readFileSync(path.join(specs, group, f), 'utf8'));
      const { spec } = vl.compile(input, opts[name]);
      out.push(JSON.stringify({ name, vega: spec }));
    } catch (e) {
      out.push(JSON.stringify({ name, error: String(e && e.message || e) }));
    }
  }
}
process.stdout.write(out.join('\n') + '\n');
