// Regenerate utf8.json with: node testdata/gen_utf8.mjs > testdata/utf8.json
// (node 24.21.0): the text WHATWG's UTF-8 decoder (TextDecoder, as
// Response.text() uses it) makes of malformed byte sequences, as hex.
const cases = [
  'f09f98', 'f09f9841', 'e282', 'e28241', 'c3', 'c341', '80', '8080', 'bf41', 'c0af', 'c1bf', 'e080af',
  'eda080', 'edbfbf', 'f4908080', 'f5', 'ff', 'fe', 'f8888080', 'f09f98f09f9880', 'e2', 'e28228',
  'f0', 'f090', 'f09080', 'f4', 'f48f', 'f48fbf', 'f48fbfbf', 'e0a0', 'e0a080', 'eda0', 'ed9f', 'ed9fbf',
  'efbfbd', '41c2', 'c280', 'df', 'dfbf', 'f1808080', 'f180', 'f18080', '41f1808041', 'e1bfc0', 'f3bfbfc0',
];
const dec = new TextDecoder();
const out = cases.map((h) => ({ in: h, out: Buffer.from(dec.decode(Buffer.from(h, 'hex')), 'utf8').toString('hex') }));
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
