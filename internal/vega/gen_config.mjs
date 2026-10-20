// Writes config_default.json: vega-parser's default configuration
// (src/config.js), which the runtime embeds (config.go).
//
//   NODE_PATH=testdata/oracle-node/node_modules node gen_config.mjs > config_default.json
import path from 'node:path';

const defaults = await import(path.join(path.resolve(process.env.NODE_PATH), 'vega-parser/src/config.js'));
process.stdout.write(JSON.stringify(defaults.default(), null, 2) + '\n');
