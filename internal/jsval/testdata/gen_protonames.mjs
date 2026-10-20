// Regenerate protonames.json with: node testdata/gen_protonames.mjs > testdata/protonames.json
// (node 24.21.0, the oracle's). It records the own property names of the
// prototypes of the JSON kinds and of Function.prototype, each with the
// length of the function it holds (null for a member that is no function, or
// that a strict function cannot read), which Member answers as upstream's
// reads do.
// Object.fromEntries defines each member, "__proto__" included (an
// assignment of that name would set the prototype instead).
const names = (p) => Object.fromEntries(
  Object.getOwnPropertyNames(p).sort().map((k) => {
    const d = Object.getOwnPropertyDescriptor(p, k);
    return [k, typeof d.value === 'function' ? d.value.length : null];
  }),
);
console.log(JSON.stringify({
  object: names(Object.prototype),
  array: names(Array.prototype),
  string: names(String.prototype),
  number: names(Number.prototype),
  boolean: names(Boolean.prototype),
  function: names(Function.prototype),
}, null, 1));
