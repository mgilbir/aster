// Rewrites package.json so every dependency is pinned to the exact version
// vendored for the root engine's Vega-Lite 5.8 build. Run after re-vendoring,
// then `npm install`:
//
//   node sync.mjs && npm install
import fs from 'node:fs';

const manifest = JSON.parse(fs.readFileSync('../../../internal/js/modules/vl5_8/manifest.json', 'utf8'));
const pkg = JSON.parse(fs.readFileSync('package.json', 'utf8'));
const deps = Object.fromEntries(manifest.modules.map((m) => [m.name, m.version]).sort());
pkg.dependencies = deps;
// vega-lite asks for its own vega-expression; the vendored bundle shares the
// single copy vega uses, so node must resolve the same one.
pkg.overrides = { 'vega-expression': deps['vega-expression'] };
fs.writeFileSync('package.json', JSON.stringify(pkg, null, 2) + '\n');
console.log(`pinned ${Object.keys(deps).length} modules: vega ${deps.vega}, vega-lite ${deps['vega-lite']}`);
