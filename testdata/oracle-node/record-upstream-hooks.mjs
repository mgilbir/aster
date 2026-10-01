// Module-resolution hooks for record-upstream-tests.mjs.
//
// A test's helper module (d3's `asserts.js`) imports the package under test by its relative path in
// the cloned repository, `../src/index.js`. That is a second copy of the package next to the
// installed one the recorder runs the test against, with its own classes, so a helper's
// `instanceof rgb` is false for a value the installed package made, and the case fails before it has
// recorded anything. These hooks resolve the cloned entry point to the installed package, so there is
// one copy.
let redirects = new Map();

export async function initialize(data) {
  redirects = new Map(data.redirects);
}

export async function resolve(specifier, context, nextResolve) {
  const resolved = await nextResolve(specifier, context);
  const target = redirects.get(resolved.url);
  return target ? {...resolved, url: target, shortCircuit: true} : resolved;
}
