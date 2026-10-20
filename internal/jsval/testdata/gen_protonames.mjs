// Regenerate protonames.json with: node testdata/gen_protonames.mjs > testdata/protonames.json
// (node 24.21.0, the oracle's). It records the own property names of the
// built-in prototypes a value of the expression language inherits from (the
// JSON kinds' and Function.prototype, and the pattern's and the date's), each
// with the name and the length of the function it holds (null for a member
// that is no function, or that a strict function cannot read). This is the
// one record of those members: jsval's tables (protos.go) are checked against
// it, and the expression evaluator reads its members through them.
// Object.fromEntries defines each member, "__proto__" included (an
// assignment of that name would set the prototype instead).
const names = (p) => Object.fromEntries(
  Object.getOwnPropertyNames(p).sort().map((k) => {
    const d = Object.getOwnPropertyDescriptor(p, k);
    return [k, typeof d.value === 'function' ? { name: d.value.name, length: d.value.length } : null];
  }),
);
console.log(JSON.stringify({
  object: names(Object.prototype),
  array: names(Array.prototype),
  string: names(String.prototype),
  number: names(Number.prototype),
  boolean: names(Boolean.prototype),
  function: names(Function.prototype),
  regexp: names(RegExp.prototype),
  date: names(Date.prototype),
}, null, 1));
