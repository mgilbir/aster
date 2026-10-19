// Lists every name in upstream's expression function table and constants.
//   NODE_PATH=testdata/oracle-node/node_modules node internal/expr/testdata/gen_functions.mjs > internal/expr/testdata/functions.json
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.resolve(process.env.NODE_PATH, 'x.js'));
const vega = await import(require.resolve('vega'));
const vf = await import(require.resolve('vega-functions'));
const gen = vega.codegenExpression(vega.codegenParams);
const extra = ['view', 'item', 'group', 'xy', 'x', 'y', '_bandwidth', '_range', '_scale'];
const names = new Set([...Object.keys(gen.functions), ...Object.keys(vf.functionContext), ...extra]);
names.delete('__bandwidth'); // helper added to the function context by the scale code generator
// The string-named members of each prototype a value of the expression
// language inherits from (the `in` operator finds them, and a read answers
// them): for each, the name of the function it holds, or null for a member
// that is not a function (an accessor such as RegExp.prototype.source). The
// d3 colours inherit from their class's prototype, then d3's Color.prototype.
const util = await import(require.resolve('vega-util'));
const members = o => Object.fromEntries(Object.getOwnPropertyNames(o).sort().map(k => {
  const d = Object.getOwnPropertyDescriptor(o, k);
  return [k, 'value' in d && typeof d.value === 'function' ? d.value.name : null];
}));
const rgb = vf.functionContext.rgb('red');
const colour = {
  Rgb: Object.getPrototypeOf(rgb), Hsl: Object.getPrototypeOf(vf.functionContext.hsl('red')),
  Lab: Object.getPrototypeOf(vf.functionContext.lab('red')), Hcl: Object.getPrototypeOf(vf.functionContext.hcl('red')),
};
const prototypes = {
  object: members(Object.prototype), array: members(Array.prototype), string: members(String.prototype),
  number: members(Number.prototype), boolean: members(Boolean.prototype),
  regexp: members(RegExp.prototype), date: members(Date.prototype),
  Color: members(Object.getPrototypeOf(colour.Rgb)),
  ...Object.fromEntries(Object.entries(colour).map(([k, p]) => [k, members(p)])),
};
// A for-in loop (vega-util's extend, which merge() is) visits a colour's
// inherited enumerable members after its own, in this order.
const colourForIn = Object.fromEntries(Object.entries(colour).map(([k, p]) => {
  const own = new Set(Object.keys(Object.create(p)));
  const out = [];
  for (const m in Object.create(p)) if (!own.has(m)) out.push(m);
  return [k, out];
}));
process.stdout.write(JSON.stringify({
  functions: [...names].sort(), constants: Object.keys(gen.constants).sort(),
  prototypes, colourForIn, disallowedProperties: [...util.DisallowedObjectProperties].sort(),
}) + '\n');
