// Records upstream's output for gallery and fixture specs compiled under each
// named theme config of themes.json (the vega-themes configs). Each line:
//   {"name":"group/file.vl","theme":"dark","vega":{...}}
// Usage: NODE_PATH=<node_modules with vega-lite@6.4.3> node gen_reference_themes.mjs > reference_themes.jsonl
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vl = await import(require.resolve('vega-lite'));
const here = path.dirname(fileURLToPath(import.meta.url));
const themes = JSON.parse(fs.readFileSync(path.join(here, 'themes.json'), 'utf8'));
const names = Object.keys(themes);
console.warn = () => {}; console.log = () => {};
const out = [];
let i = 0;
for (const group of ['gallery', 'fixtures']) {
  const dir = path.join(here, 'specs', group);
  for (const f of fs.readdirSync(dir).filter((f) => f.endsWith('.json')).sort()) {
    const theme = names[i++ % names.length];
    const name = `${group}/${f.replace(/\.json$/, '')}`;
    try {
      const input = JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
      const { spec } = vl.compile(input, { config: structuredClone(themes[theme]) });
      out.push(JSON.stringify({ name, theme, vega: spec }));
    } catch (e) {
      out.push(JSON.stringify({ name, theme, error: String(e && e.message || e) }));
    }
  }
}
process.stdout.write(out.join('\n') + '\n');
