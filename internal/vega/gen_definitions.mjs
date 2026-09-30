// Dumps every registered transform definition (vega-dataflow's `definition`
// registry) so the Go runtime can drive parameter parsing from the same table
// vega-parser uses.
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const out = {};
for (const name of Object.keys(vega.transforms).sort()) {
  const def = vega.definition(name);
  if (def) out[name.toLowerCase()] = def;
}
process.stdout.write(JSON.stringify(out, null, 1));
