// Lists every name in upstream's expression function table and constants.
//   node testdata/gen_functions.mjs > testdata/functions.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const vf = await import(require.resolve('vega-functions'));
const gen = vega.codegenExpression(vega.codegenParams);
const extra = ['view', 'item', 'group', 'xy', 'x', 'y', '_bandwidth', '_range', '_scale'];
const names = new Set([...Object.keys(gen.functions), ...Object.keys(vf.functionContext), ...extra]);
names.delete('__bandwidth'); // helper added to the function context by the scale code generator
// The string-named members every value of each kind inherits, which the `in`
// operator finds, and the object literal keys the code generator rejects.
const util = await import(require.resolve('vega-util'));
const members = o => Object.getOwnPropertyNames(o).sort();
const prototypes = {
  object: members(Object.prototype), array: members(Array.prototype),
  regexp: members(RegExp.prototype), date: members(Date.prototype),
};
process.stdout.write(JSON.stringify({
  functions: [...names].sort(), constants: Object.keys(gen.constants).sort(),
  prototypes, disallowedProperties: [...util.DisallowedObjectProperties].sort(),
}) + '\n');
