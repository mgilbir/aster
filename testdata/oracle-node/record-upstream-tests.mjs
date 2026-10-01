// Records what upstream's own test suites ask of the packages the engine ports, as vectors.
//
// Vega's packages have ~3,700 assertions and d3's thousands more, and they are the most carefully
// chosen inputs anyone has produced for this grammar. Transcribing their *expectations* into Go
// would be a large, error-prone copy, and would inherit assertions that are deliberately loose
// (`t.ok(x > 0)`) where an exact value is what a differential port needs. So this does not read their
// assertions at all. It runs a package's test files against the **installed** package, the same
// version every reference in this repository is generated from, with two pieces of scaffolding:
//
//   1. a test-runner shim (tape for Vega, mocha-style `it`/`describe` with node:assert for d3), so a
//      test body runs without the real runner and without its assertions mattering; and
//   2. a recording proxy around the package's exports, so every call the test makes is captured with
//      its arguments and with upstream's actual answer.
//
// What comes out is a vector file: input, and what upstream really returns. The Go tests replay it
// (internal/upstream).
//
// Usage:  node record-upstream-tests.mjs <clone of the package's repository> <package>
// Writes: testdata/upstream-vectors-cache/<package>.json (git-ignored; see scripts/record-upstream-vectors.sh)
import {readFileSync, writeFileSync, mkdirSync, readdirSync, existsSync, rmSync} from 'fs';
import {basename, dirname, join, resolve} from 'path';
import {fileURLToPath, pathToFileURL} from 'url';
import {register} from 'module';
import {parse} from 'acorn';

// Pinned before anything reads a clock or builds a Date. A `local` time unit is *local*, so the
// recorded answers depend on the zone the recorder ran in. The driving script sets it (a d3 package's
// own test script names its zone) and the zone is written into the vector file, so a replay knows
// which one produced the answers.
process.env.TZ = process.env.TZ || 'Europe/Amsterdam';

/**
 * Pins the random source and the clock, so a recording is reproducible.
 *
 * Regenerating a vector file must produce the same bytes, or a change of upstream arrives buried in
 * noise. Upstream's own tests build data with `Math.random` — one `sample`
 * test moved 3,216 lines between two runs — and read `Date.now`.
 *
 * **Order matters here and cost an hour.** The constants are inlined rather than imported from
 * a helper module, because importing one that imports *vega* and vega captures `Math.random`
 * into its own module-level generator as it loads. Overriding afterwards pins the tests' own calls
 * and leaves the `sample` transform drawing from the real one. Nothing may be imported before these
 * two lines.
 *
 * The clock is the instant `oracle.mjs` pins (2026-01-01 UTC), so a vector and a rendering agree about
 * what "now" means.
 */
const SEED = 42;
const NOW = 1767225600000;
let seedState = SEED;
Math.random = () => {
  // vega-statistics' `randomLCG`, inlined for the same reason the constants are.
  seedState = (1103515245 * seedState + 12345) % 2147483647;
  return seedState / 2147483647;
};
Date.now = () => NOW;
// `new Date()` reads the clock directly, so pinning `Date.now` alone leaves it moving — d3-time's
// tests are full of `new Date` and would record a different answer every run.
const RealDate = Date;
globalThis.Date = class extends RealDate {
  constructor(...args) {
    super(...(args.length === 0 ? [NOW] : args));
  }

  static now() {
    return NOW;
  }
};

const here = dirname(fileURLToPath(import.meta.url));
const outputDir = resolve(here, '../upstream-vectors-cache');

/**
 * Where a function came from, for the vectors that take a function as an argument.
 *
 * `timeTicks(a, b, timeMinute)` and `scaleLinear().interpolate(interpolateHsl)` pass one of the
 * package's own functions. Recorded by name alone (`interval`, `scale`) the argument could not be told
 * from another, so a function that is an export, or was returned by a call on one, is recorded with
 * that origin: `{export: 'timeMinute'}` or `{from: 'timeMinute.every', args: [5]}`.
 */
const functionOrigins = new WeakMap();

/** An origin may be a function: for something a test goes on configuring, it is read when used. */
function originOf(value) {
  const origin = functionOrigins.get(value);
  return typeof origin === 'function' ? origin() : origin;
}

function originate(value, origin) {
  const followed = typeof value === 'function' || (value !== null && typeof value === 'object');
  if (followed && !functionOrigins.has(value)) functionOrigins.set(value, origin);
}

/**
 * A value as JSON, or a marker saying why it is not.
 *
 * Recording "undefined" as null would be a lie a Go test could not tell from a real null, so
 * everything unrepresentable is tagged instead and the replay skips it — visibly.
 */
function encode(value, seen = new Set()) {
  if (value === undefined) return {$: 'undefined'};
  if (value === null) return null;
  if (typeof value === 'number') {
    if (Number.isNaN(value)) return {$: 'NaN'};
    // **Negative zero.** `JSON.stringify(-0)` is `"0"`, and the difference is not academic here: d3
    // decides a value's sign with `1 / value < 0`, so `format("+f")(-0)` writes `−0.000000` where
    // `+0` writes `+0.000000`. Recorded as itself rather than lost.
    if (value === 0 && 1 / value < 0) return {$: '-0'};
    if (value === Infinity) return {$: 'Infinity'};
    if (value === -Infinity) return {$: '-Infinity'};
    return value;
  }
  if (typeof value === 'bigint') return {$: 'bigint', value: value.toString()};
  if (typeof value === 'function') {
    // A Vega **accessor** is a function that carries the field path it reads, and a transform's
    // parameters are full of them — `{field: field('v')}`. Recorded as an opaque function they would
    // make every transform vector unreplayable; recorded by their `fields` they are exactly what a
    // Go adapter needs.
    // A **comparator** carries `fields` as well, so `fields` alone cannot tell one from an
    // accessor — and reducing `compare(['count'], ['descending'])` to the string "count" silently
    // drops the direction, which is how a sorted `collect` looked like an unsorted one.
    if (Array.isArray(value.fields) && Array.isArray(value.orders)) {
      return {$: 'comparator', fields: value.fields, orders: value.orders};
    }
    if (Array.isArray(value.fields)) {
      // An accessor carries a **name** as well as the field it reads, and they are not always the
      // same: `field('k1', 'key')` reads `k1` and is named `key`, and upstream names an aggregate's
      // output column after the *name*. Recording only the field made that vector look like a
      // column-naming bug here.
      return {$: 'accessor', fields: value.fields, ...(value.fname ? {name: value.fname} : {})};
    }
    const origin = originOf(value);
    return {$: 'function', name: value.name || '(anonymous)', ...(origin ? {origin} : {})};
  }
  if (typeof value === 'symbol') return {$: 'symbol'};
  if (value instanceof Date) return {$: 'date', epochMillis: encode(value.getTime())};
  if (value instanceof RegExp) return {$: 'regexp', source: value.source, flags: value.flags};
  if (typeof value !== 'object') return value;
  if (seen.has(value)) return {$: 'circular'};
  seen.add(value);
  try {
    // d3-array's tests build million-element arrays; encoding one costs minutes and the vector would
    // be dropped for its size anyway. Marked instead, and `pushCall` turns the mark into a note.
    // A typed array keeps its type: `interpolate([0, 0], new Uint8Array(...))` truncates to integers,
    // and a plain array of the same numbers would not say so.
    if (ArrayBuffer.isView(value) && !(value instanceof DataView)) {
      if (value.length > MAX_ARRAY) return {$: 'oversized', length: value.length};
      return {$: 'typed', type: value.constructor.name, values: Array.from(value, v => encode(v, seen))};
    }
    if (Array.isArray(value)) {
      if (value.length > MAX_ARRAY) return {$: 'oversized', length: value.length};
      return value.map(v => encode(v, seen));
    }
    // A Map and a Set have no own keys, so they would be recorded as `{}`.
    if (value instanceof Map) {
      if (value.size > MAX_ARRAY) return {$: 'oversized', length: value.size};
      return {$: 'map', entries: [...value].map(([k, v]) => [encode(k, seen), encode(v, seen)])};
    }
    if (value instanceof Set) {
      if (value.size > MAX_ARRAY) return {$: 'oversized', length: value.size};
      return {$: 'set', values: [...value].map(v => encode(v, seen))};
    }
    const out = {};
    // An instance of a package's own class (a colour, a locale) says which: `rgb(...)` and
    // `cubehelix(...)` have different own keys, but `hsl` and `cubehelix` share theirs.
    const proto = Object.getPrototypeOf(value);
    if (proto && proto !== Object.prototype && value.constructor?.name) out.$class = value.constructor.name;
    for (const key of Object.keys(value)) out[key] = encode(value[key], seen);
    // An object a call made (`randomNormal(0, 1)` handed to `randomMixture`) says which call.
    const origin = originOf(value);
    if (origin) out.$origin = origin;
    return out;
  } finally {
    seen.delete(value);
  }
}

/** Wraps every exported function so a call records itself. Values pass through untouched. */
function recordExports(moduleNamespace, packageName, calls) {
  const wrapped = {};
  for (const name of Object.keys(moduleNamespace)) {
    const value = moduleNamespace[name];
    if (typeof value !== 'function') {
      wrapped[name] = value;
      continue;
    }
    // A Proxy rather than a wrapper function, because a good third of what these packages export is
    // a **class** — a renderer, a handler, a transform — and `Class(...)` without `new` is a
    // TypeError. The proxy records a plain call and leaves a construction to behave exactly as it
    // would have; a constructed object's own behaviour is not something a vector can capture, so it
    // is noted and not pretended about.
    wrapped[name] = new Proxy(value, {
      apply(target, thisArg, args) {
        const encodedArgs = args.map(a => encode(a));
        let result, threw = null;
        const outer = enter();
        try {
          result = Reflect.apply(target, thisArg, args);
        } catch (error) {
          threw = error;
        } finally {
          leave();
        }
        if (outer) pushCall(calls, {
          package: packageName,
          fn: name,
          args: encodedArgs,
          ...(threw ? {threw: threw.message.split('\n')[0]} : {result: encode(result)}),
        });
        if (threw) throw threw;
        originate(result, {from: name, args: encodedArgs});
        // A function **returned** is where a whole family of packages keeps its meaning:
        // `interpolateRgb(a, b)` hands back the interpolator, `timeFloor('year')` hands back the
        // flooring function. Recording only the construction records nothing at all — the result is
        // `{$: 'function'}` — so the returned function is wrapped too, and every call on it becomes a
        // vector carrying the arguments it was *built* with. One level only: a longer chain is a
        // builder, and belongs to the instance-and-sequence recording the transforms use.
        if (typeof result !== 'function' && !followable(result)) return result;
        const proxied = applied(result, packageName, name, args, calls);
        // Configured since (`geoPath().projection(geoMercator().scale(100))`): read when it is handed on.
        originate(proxied, () => ({
          from: name,
          args: encodedArgs,
          ...(chainOf(result).length ? {chain: encodeChain(chainOf(result))} : {}),
        }));
        return proxied;
      },
      construct(target, args, newTarget) {
        return Reflect.construct(target, args, newTarget);
      },
      // d3 keeps most of its meaning on **methods of an exported object**: `timeDay` is a function
      // with `floor`, `ceil`, `range` and `count` hanging off it, and a test calls those far more
      // often than it calls the export itself. Reading one returns a wrapper that records as
      // `timeDay.floor`, so the vector says which method it was.
      get(target, key, receiver) {
        const property = Reflect.get(target, key, receiver);
        if (typeof property !== 'function' || typeof key !== 'string') return property;
        // `bind`, `call` and friends belong to every function alive and say nothing about the
        // library; recording them produced vectors named `monthAbbrevFormat.bind`.
        if (key === 'constructor' || key.startsWith('_') || key in Function.prototype) return property;
        return function (...args) {
          const encodedArgs = args.map(a => encode(a));
          let result, threw = null;
          const outer = enter();
          try {
            result = property.apply(target, args);
          } catch (error) {
            threw = error;
          } finally {
            leave();
          }
          originate(result, {from: `${name}.${key}`, args: encodedArgs});
          if (outer) pushCall(calls, {
            package: packageName,
            fn: `${name}.${key}`,
            args: encodedArgs,
            ...(threw ? {threw: threw.message.split('\n')[0]} : {result: encode(result)}),
          });
          if (threw) throw threw;
          // `timeDay.every(3)` and `interpolateRgb.gamma(2.2)` hand back something that is used
          // like an export: followed, and recorded as `timeDay.every()` built with its arguments.
          if (typeof result !== 'function' && !followable(result)) return result;
          const proxied = applied(result, packageName, `${name}.${key}`, args, calls);
          originate(proxied, {from: `${name}.${key}`, args: encodedArgs});
          return proxied;
        };
      },
    });
    originate(value, {export: name});
    originate(wrapped[name], {export: name});
  }
  return wrapped;
}

/**
 * Runs one test file with `tape` and the package's exports replaced.
 *
 * The rewrite is narrow: the relative import of the package under test becomes the recording
 * wrapper, and `tape` becomes the shim. A file that reaches for anything else — an internal
 * `../src/...` path, a fixture on disk — is reported and skipped rather than half-run.
 */
async function runTestFile(file, packageName, calls, skipped, checkout) {
  const source = readFileSync(file, 'utf8');
  // Recorded relative to the checkout: an absolute path records whose machine ran the recorder, and
  // makes a regenerated file differ from the previous one for no reason anyone cares about.
  const where = file.startsWith(checkout) ? file.slice(checkout.length + 1) : file;
  const namespace = await import(packageName);
  const wrapped = recordExports(namespace, packageName, calls);
  const restoreTransforms = recordTransforms(namespace, packageName, calls);
  globalThis.__vegaRecorded = wrapped;
  globalThis.__vegaTape = tapeShim(where, skipped);
  globalThis.__vegaVitest = vitestShim(where, skipped);
  installMochaShim(where, skipped);

  let rewritten;
  try {
    rewritten = rewriteImports(source, file);
  } catch (error) {
    skipped.push({file: where, reason: `could not parse: ${error.message.split('\n')[0]}`});
    return;
  }
  // Written to a real file inside `testdata/oracle-node` rather than imported from a `data:` URL. A data URL
  // has no resolution base, so every bare specifier a test reaches for — `vega-util`,
  // `vega-datasets`, `d3-array` — fails to resolve; from here they resolve against
  // `testdata/oracle-node/node_modules`, the installed versions the whole repository compares against.
  const scratch = join(here, '.recorder-scratch');
  mkdirSync(scratch, {recursive: true});
  // Named after the test file and nothing else. A timestamp here reached the *recorded* text —
  // Node names the importer in a resolution failure — so two runs disagreed on a file that had not
  // changed. One package per process means no collision.
  const temporary = join(scratch, `${where.replace(/[\\/]/g, '__')}.mjs`);
  writeFileSync(temporary, rewritten);
  try {
    await import(pathToFileURL(temporary).href);
  } catch (error) {
    skipped.push({file: where, reason: `threw while running: ${scrub(error.message.split('\n')[0], scratch)}`});
  } finally {
    rmSync(temporary, {force: true});
    restoreTransforms();
  }
}

/**
 * Rewrites a test file's imports, by **AST** rather than by pattern.
 *
 * This is the part that has to survive a version upgrade, and the regex version it replaces did not
 * survive the *current* version: it handled `import {a, b}` and missed `import * as vega`, which is
 * the form 55 of these files use. A parser handles every form there is, and a form it has never seen
 * fails loudly instead of silently producing a file that still imports the real module.
 *
 * Four cases, and nothing else is touched:
 * - the package under test becomes the recording wrapper;
 * - `tape` becomes the shim;
 * - a relative helper (`./util.js`) is resolved to an absolute URL, since the rewritten source runs
 *   from a scratch directory, not from beside the test;
 * - a bare import (`fs`, `d3-array`) is left alone, to resolve from `testdata/oracle-node/node_modules` —
 *   which is the same installed version every reference in this repository comes from.
 */
/** The paths a test may import the package under test by (set once the checkout is known). */
const packageEntries = new Set();

function rewriteImports(source, file) {
  const ast = parse(source, {ecmaVersion: 'latest', sourceType: 'module'});
  const edits = [];
  for (const node of ast.body) {
    if (node.type !== 'ImportDeclaration') continue;
    const from = node.source.value;
    // `../index.js` is Vega's shape; `../src/index.js` is d3's. Both mean "the package
    // under test", and both are pointed at the *installed* build, which is what every reference
    // and vector in this repository is generated from.
    // A test in a subdirectory (d3-geo's `test/projection`) reaches it with more `../`: so the
    // import is resolved and compared with the package's entry points.
    const isPackage = from.startsWith('.') && packageEntries.has(resolve(dirname(file), from));
    if (!isPackage && from !== 'tape' && from !== 'vitest' && !from.startsWith('.')) continue;

    if (from.startsWith('.') && !isPackage) {
      // Rebuilt from the AST rather than patched in the text. The first version swapped `'${from}'`
      // for the resolved URL and quietly did nothing to d3, which writes its imports with double
      // quotes — 36 of 36 files then failed to resolve a helper that was right beside them.
      const url = pathToFileURL(resolve(dirname(file), from)).href;
      const clauses = [];
      const named = [];
      for (const specifier of node.specifiers) {
        if (specifier.type === 'ImportDefaultSpecifier') clauses.push(specifier.local.name);
        else if (specifier.type === 'ImportNamespaceSpecifier') clauses.push(`* as ${specifier.local.name}`);
        else if (specifier.imported.name === specifier.local.name) named.push(specifier.local.name);
        else named.push(`${specifier.imported.name} as ${specifier.local.name}`);
      }
      if (named.length) clauses.push(`{${named.join(', ')}}`);
      edits.push([
        node.start,
        node.end,
        clauses.length ? `import ${clauses.join(', ')} from ${JSON.stringify(url)};` : `import ${JSON.stringify(url)};`,
      ]);
      continue;
    }
    const binding = isPackage
      ? 'globalThis.__vegaRecorded'
      : from === 'vitest'
        ? 'globalThis.__vegaVitest'
        : 'globalThis.__vegaTape';
    const parts = [];
    for (const specifier of node.specifiers) {
      // `vitest` is imported by name — `import {it, expect} from "vitest"` — so each specifier is
      // taken off the shim object rather than bound to the whole of it.
      if (
        from === 'vitest' &&
        (specifier.type === 'ImportDefaultSpecifier' || specifier.type === 'ImportNamespaceSpecifier')
      ) {
        parts.push(`const ${specifier.local.name} = ${binding};`);
        continue;
      }
      if (specifier.type === 'ImportDefaultSpecifier' || specifier.type === 'ImportNamespaceSpecifier') {
        // `import tape from 'tape'` and `import * as vega from '../index.js'` both bind the whole
        // thing; for the package that is exactly the wrapper object.
        parts.push(`const ${specifier.local.name} = ${binding};`);
      } else {
        parts.push(`const ${specifier.local.name} = ${binding}[${JSON.stringify(specifier.imported.name)}];`);
      }
    }
    edits.push([node.start, node.end, parts.join(' ')]);
  }
  let out = source;
  for (const [start, end, text] of edits.sort((a, b) => b[0] - a[0])) {
    out = out.slice(0, start) + text + out.slice(end);
  }
  return out;
}

/**
 * Records what a **transform operator** does, which is where the interesting packages keep their
 * meaning.
 *
 * `vega-transforms` and its neighbours export operator *classes*, not functions: a test constructs
 * one through a `Dataflow` and pushes tuples at it, so nothing crosses the export boundary with plain
 * arguments and the export-level recorder sees nothing at all. One level in is a seam that sees
 * everything — `prototype.transform(_, pulse)`, called once per pulse with the resolved parameters.
 *
 * What is captured is a transform's whole contract: the parameters, the tuples that went in, and the
 * tuples that came out. Tuple identity is a `Symbol`, so it does not appear in the JSON and the
 * vectors stay comparable.
 *
 * Output is **capped**. Some of upstream's tests push tens of thousands of tuples through a
 * `crossfilter`; a vector that large is unreadable, slow to replay, and proves nothing the first two
 * hundred rows do not. Anything larger is recorded as a count, so the skip is visible rather than a
 * silently short array.
 */
/**
 * Whether a returned object is worth following: a plain object or an instance of a package's own
 * class (a locale, a colour). A built-in (Map, Set, Date, a typed array) is left alone, because a
 * proxy breaks its methods.
 */
function followable(value) {
  return (
    value !== null &&
    typeof value === 'object' &&
    Object.prototype.toString.call(value) === '[object Object]'
  );
}

/**
 * Wraps a value a call returned (a function, or an object with methods) so that what is done with it
 * is recorded together with how it was obtained.
 *
 * It also follows a **builder chain**, which is the shape `d3-scale` and `d3-shape` are written in:
 * `scaleLinear().domain([0, 1]).range([0, 100])` configures an object by chained calls and only then
 * asks it something. A single call is not a vector there, *the state is the input*, so every
 * chainable call (one that returns the object itself) is appended to a chain, and a call that
 * answers with anything else records the chain that produced it.
 *
 * Something a method hands back that is not the object itself is followed one more level, because it
 * carries meaning of its own: `scale.tickFormat(5)` is a function whose answers depend on its
 * arguments, and `formatLocale(definition).format(specifier)` is a function made by a method of an
 * object made by a call. Those records carry `via`, the steps taken from the first result to the one
 * asked: `[[method, args], ...]`, with a null method for a plain call.
 *
 *     {"fn": "scaleLinear()", "constructedWith": [], "chain": [["domain", [0, 1]]],
 *      "via": [["tickFormat", [5]]], "args": [0.5], "result": "0.5"}
 *
 * A replay rebuilds the object from the chain, takes the same steps and asks the same question.
 * Configuration order is kept because it matters: a `nice()` before a `domain()` nices a different
 * domain.
 */
/**
 * Only the outermost call is a vector. A call made while another is running is the package calling
 * itself through something the test handed it (`randomMixture` asks its components, `setRandom`
 * installs a generator that every draw calls), and recording those as well would put the draws of
 * one question in front of the answer to another, where a replay cannot tell them from its own.
 */
let callDepth = 0;
const enter = () => callDepth++ === 0;
const leave = () => {
  callDepth--;
};

const MAX_VIA = 2;

const chains = new WeakMap();

/**
 * The configuration a builder has accumulated, kept **on the object** rather than on a wrapper.
 *
 * A test does not have to chain: `const s = scaleLinear(); s.domain([1, 2]); s(0.5)` configures the
 * same object through a call whose return value it throws away. Holding the chain in the wrapper
 * loses that, the next question is asked through the original wrapper, whose chain is still empty,
 * and the recorded vector then says `scaleLinear()(0.5)` is 1.5, which is true of the configured
 * scale and nonsense on its own.
 */
function chainOf(target) {
  if (!chains.has(target)) chains.set(target, []);
  return chains.get(target);
}

// The chain holds arguments already encoded: a test may change an array after handing it over
// (`scale.domain(values); values.pop()`), and the scale took a copy.
//
// An argument that is something a call made (a projection handed to `geoPath().projection(...)`) is
// encoded again when the chain is read: the test may go on configuring it, and the answer depends on
// its state when asked.
function encodeChain(chain) {
  return chain.map(([m, a, raw]) => [
    m,
    a.map((encoded, i) => (raw && refreshable(encoded) ? encode(raw[i]) : encoded)),
  ]);
}

function refreshable(encoded) {
  return encoded !== null && typeof encoded === 'object' && (encoded.origin !== undefined || encoded.$origin !== undefined);
}

/**
 * An ordinal scale with an implicit domain (`d3.scaleOrdinal()`, whose `unknown()` is a symbol) grows
 * its domain when it is asked about a value, so asking is part of its state.
 */
function isImplicitOrdinal(target) {
  try {
    return typeof target.unknown === 'function' && typeof target.unknown() === 'symbol';
  } catch {
    return false;
  }
}

function applied(value, packageName, name, constructedWith, calls, via = [], fixedChain = null) {
  const encodedConstruction = constructedWith.map(a => encode(a));
  // The base of every record this wrapper writes: how the object was made and configured.
  const base = target => {
    // The root's configuration, then (for something derived from it) the steps to it, then what
    // was done to the derived object itself: `scale('band')()` is a scale a test then configures.
    const chain = fixedChain ?? encodeChain(chainOf(target));
    const derived = fixedChain ? encodeChain(chainOf(target)) : [];
    return {
      package: packageName,
      fn: `${name}()`,
      constructedWith: encodedConstruction,
      ...(chain.length ? {chain} : {}),
      ...(via.length ? {via} : {}),
      ...(derived.length ? {viaChain: derived} : {}),
    };
  };
  // What a call returned: followed if it is something a later call can ask about.
  const follow = (result, target, step) => {
    if (via.length >= MAX_VIA) return result;
    // A getter (`scale.interpolate()`) hands back what was stored, and a test compares it by
    // identity: wrapping it would make that comparison fail and cut the case short.
    if (step[0] !== null && step[0] !== 'copy' && step[1].length === 0) return result;
    if (typeof result !== 'function' && !followable(result)) return result;
    const next = [...via, step];
    // A copy starts where its parent stands: the same configuration, then its own.
    if (step[0] === 'copy' && typeof result === 'function') {
      chains.set(result, [...chainOf(target)]);
      return applied(result, packageName, name, constructedWith, calls, via);
    }
    const wrapped = applied(result, packageName, name, constructedWith, calls, next, encodeChain(chainOf(target)));
    // What was done to it since matters when it is handed on (`tickCount(scale, 10)`): its own
    // configuration, after the root's and the steps to it.
    originate(wrapped, () => ({
      from: `${name}()`,
      constructedWith: encodedConstruction,
      ...(encodeChain(chainOf(target)).length ? {chain: encodeChain(chainOf(target))} : {}),
      steps: next,
      ...(chainOf(result).length ? {viaChain: encodeChain(chainOf(result))} : {}),
    }));
    return wrapped;
  };
  return new Proxy(value, {
    apply(target, thisArg, args) {
      // Encoded **before** the call, because a function may mutate what it was handed and some
      // do: `boundStroke(bounds, item)` expands `bounds` in place and returns it, so encoding
      // afterwards recorded the answer as the question and lost the input entirely. Each closure
      // takes its own snapshot, reading one from an enclosing scope silently recorded nothing for
      // every method call in d3-time.
      const encodedArgs = args.map(a => encode(a));
      const record = base(target);
      let result, threw = null;
      const outer = enter();
      try {
        result = Reflect.apply(target, thisArg, args);
      } catch (error) {
        threw = error;
      } finally {
        leave();
      }
      if (outer) pushCall(calls, {
        ...record,
        args: encodedArgs,
        ...(threw ? {threw: threw.message.split('\n')[0]} : {result: encode(result)}),
      });
      if (isImplicitOrdinal(target)) chainOf(target).push([null, encodedArgs]);
      if (threw) throw threw;
      return follow(result, target, [null, encodedArgs]);
    },
    // A test sets a field and then asks (`c.opacity = NaN; c.rgb()`): the write is part of the state
    // the question is asked of, so it joins the chain, as a method named `set:<field>`.
    set(target, key, newValue) {
      if (typeof key === 'string' && !key.startsWith('_')) chainOf(target).push([`set:${key}`, [encode(newValue)]]);
      return Reflect.set(target, key, newValue);
    },
    get(target, key, receiver) {
      const property = Reflect.get(target, key, receiver);
      if (typeof property !== 'function' || typeof key !== 'string') return property;
      // `toString` of an object that has its own (d3-path's `Path`, a d3-color colour) is the answer a
      // test asks for with `path + ''`, so it is recorded; on a function it is everyone's.
      const ownToString = key === 'toString' && typeof target === 'object' && target.toString !== Object.prototype.toString;
      if (key === 'constructor' || key.startsWith('_') || (key in Function.prototype && !ownToString)) return property;
      return function (...args) {
        const encodedArgs = args.map(a => encode(a));
        const record = base(target);
        let result, threw = null;
        const outer = enter();
        try {
          result = property.apply(target, args);
        } catch (error) {
          threw = error;
        } finally {
          leave();
        }
        if (threw) {
          if (outer) pushCall(calls, {...record, method: key, args: encodedArgs, threw: threw.message.split('\n')[0]});
          throw threw;
        }
        // Chainable: the call configured the object and handed it back. Remember it and keep
        // wrapping, so the next question carries the whole configuration with it. Configured, not
        // asked: remembered against the object itself, so a later question sees it whether or not
        // the test kept the returned value.
        if (result === target || result === receiver) {
          chainOf(target).push([key, encodedArgs, args]);
          return receiver;
        }
        if (outer) pushCall(calls, {...record, method: key, args: encodedArgs, result: encode(result)});
        // A method of an object that returns nothing changes the object (`path.moveTo(0, 0)`), so
        // what follows is asked of the object as it then is.
        if (result === undefined && typeof target === 'object') chainOf(target).push([key, encodedArgs, args]);
        return follow(result, target, [key, encodedArgs]);
      };
    },
  });
}

function recordTransforms(moduleNamespace, packageName, calls) {
  const patched = [];
  const instanceCounter = {n: 0};
  for (const name of Object.keys(moduleNamespace)) {
    const operator = moduleNamespace[name];
    const proto = operator && operator.prototype;
    if (!proto || typeof proto.transform !== 'function' || proto.__vegaRecorded) continue;
    const original = proto.transform;
    proto.__vegaRecorded = true;
    proto.transform = function (params, pulse) {
      // A transform operator is **stateful**: `aggregate` accumulates the values it has seen, and a
      // later pulse's output depends on every pulse before it. A replay that calls a pure
      // `apply(rows, params)` can only reproduce the *first* call on a fresh operator, so each call
      // is stamped with which operator it belongs to and how many calls that operator has had. Vector
      // 2 of the cross-product test is what taught this: its input has no `b: 2` and its output does.
      if (this.__vegaInstance === undefined) this.__vegaInstance = ++instanceCounter.n;
      const sequence = (this.__vegaSequence = (this.__vegaSequence ?? -1) + 1);
      const input = capturePulse(pulse);
      const output = original.call(this, params, pulse);
      // Several operators put their answer on **themselves** rather than in the pulse: `extent`
      // leaves `[min, max]` in `this.value`, and a chart reads it as a parameter of the next
      // operator. A vector without it would record that `extent` changed nothing, which is true of
      // the tuples and useless as a check.
      const value = this.value === undefined || typeof this.value === 'function' ? undefined : encode(this.value);
      pushCall(calls, {
        package: packageName,
        op: name,
        instance: this.__vegaInstance,
        sequence,
        params: encodeParams(params),
        input,
        output: capturePulse(output && output.add !== undefined ? output : pulse),
        ...(value === undefined ? {} : {value}),
      });
      return output;
    };
    patched.push(() => {
      proto.transform = original;
      delete proto.__vegaRecorded;
    });
  }
  return () => patched.forEach(undo => undo());
}

const MAX_TUPLES = 200;

/** Packages whose answers depend on the calls before them: their vectors are not deduplicated. */
const STATEFUL_PACKAGES = new Set(['vega-statistics']);

/** The longest array `encode` follows; a longer one cannot fit in a vector (see MAX_VECTOR_BYTES). */
const MAX_ARRAY = 20000;

/** The most JSON one vector may occupy, before its payload is replaced by a note. */
const MAX_VECTOR_BYTES = 64 * 1024;

/**
 * Records one call, or a note that it was too big to record.
 *
 * Every site goes through here, which the transform seam learned the hard way and the d3 seam
 * learned again: `d3-array`'s tests operate on million-element arrays, and stringifying those
 * exceeded the maximum length of a JavaScript string — the recorder died rather than writing a file.
 * A vector nobody can read is worth no more than a note saying how big it was.
 */
function pushCall(calls, call) {
  let text;
  try {
    text = JSON.stringify(call);
  } catch {
    text = null;
  }
  if (text !== null && text.length <= MAX_VECTOR_BYTES && !text.includes('"$":"oversized"')) {
    calls.push(call);
    return;
  }
  const {package: pkg, fn, op, instance, sequence} = call;
  calls.push({
    package: pkg,
    ...(fn ? {fn} : {}),
    ...(op ? {op, instance, sequence} : {}),
    oversized: text === null ? 'unserialisable' : text.length,
  });
}


/** A pulse's three change sets and its backing source, each capped and encoded. */
function capturePulse(pulse) {
  if (!pulse || typeof pulse !== 'object') return null;
  const part = tuples => {
    if (!Array.isArray(tuples)) return undefined;
    if (tuples.length > MAX_TUPLES) return {$: 'truncated', count: tuples.length};
    return tuples.map(t => encode(t));
  };
  const out = {};
  for (const key of ['add', 'rem', 'mod', 'source']) {
    const value = part(pulse[key]);
    if (value !== undefined && (!Array.isArray(value) || value.length)) out[key] = value;
  }
  return out;
}

/** Parameters, with an operator parameter replaced by the value it holds. */
function encodeParams(params) {
  if (!params || typeof params !== 'object') return encode(params);
  const out = {};
  for (const key of Object.keys(params)) {
    if (key === 'pulse' || key.startsWith('$')) continue;
    const value = params[key];
    // A parameter can be an `Operator` — `df.add([0, 10])` — and what a Go adapter needs is the
    // value it currently holds, not the operator wrapper.
    out[key] = value && typeof value === 'object' && typeof value.value === 'function'
      ? encode(value.value())
      : encode(value);
  }
  return out;
}

/**
 * Replaces this machine's paths in a recorded message.
 *
 * A failure message names the importing file, and an absolute path records where somebody's checkout
 * lives — which makes a regenerated vector file differ from the previous one on another machine, for
 * no change in behaviour.
 */
function scrub(message, scratch) {
  return message
    .split(`${scratch}/`)
    .join('<recorder>/')
    .split(`${here}/`)
    .join('<oracle-node>/');
}

/**
 * Enough of **mocha** to run a d3 test body.
 *
 * d3 runs `mocha`, which injects `it` as a *global* rather than something a file imports — so unlike
 * the `tape` shim this is installed on `globalThis` and the file is left alone. Assertions come from
 * Node's `assert` and are left to throw: a case that fails is recorded as a skip with its reason,
 * which is information rather than an error, since the vectors come from what upstream *returned*.
 */
function installMochaShim(where, skipped) {
  const run = (name, body) => {
    try {
      const done = body?.();
      if (done && typeof done.then === 'function') done.catch(() => {});
    } catch (error) {
      skipped.push({file: where, case: name, reason: `case threw: ${error.message.split('\n')[0]}`});
    }
  };
  run.skip = () => {};
  run.only = run;
  globalThis.it = run;
  globalThis.describe = (_name, body) => body?.();
  globalThis.before = globalThis.after = globalThis.beforeEach = globalThis.afterEach = () => {};
}

/**
 * Enough of **vitest** to run a test body, which is what the newer d3 packages use.
 *
 * Three runners now — `tape` in Vega, `mocha` in most of d3, `vitest` in the packages d3 has
 * migrated — and the recorder cares about none of their assertions: `expect(x).toBe(y)` is accepted
 * and dropped like the rest, because the vector is what the library *returned*, not what the test
 * believed about it. `expect` therefore answers any method with itself, so a chain of them runs.
 */
function vitestShim(where, skipped) {
  const chainable = new Proxy(() => chainable, {
    get: (target, key) => (key === 'not' || key === 'resolves' || key === 'rejects' ? chainable : () => chainable),
    apply: () => chainable,
  });
  const run = (name, body) => {
    try {
      const done = body?.();
      if (done && typeof done.then === 'function') done.catch(() => {});
    } catch (error) {
      skipped.push({file: where, case: name, reason: `case threw: ${error.message.split('\n')[0]}`});
    }
  };
  run.skip = () => {};
  run.only = run;
  run.each = () => run;
  return {
    it: run,
    test: run,
    describe: (_name, body) => body?.(),
    expect: () => chainable,
    assert: new Proxy({}, {get: () => () => {}}),
    beforeEach: () => {},
    afterEach: () => {},
    beforeAll: () => {},
    afterAll: () => {},
  };
}

/** Enough of `tape` to run a test body: assertions are accepted and ignored. */
function tapeShim(file, skipped) {
  const noop = () => {};
  const t = new Proxy(
    {end: noop, plan: noop, comment: noop, skip: noop, pass: noop, fail: noop},
    {get: (target, key) => (key in target ? target[key] : noop)}
  );
  const run = (name, body) => {
    try {
      const done = body(t);
      if (done && typeof done.then === 'function') done.catch(() => {});
    } catch (error) {
      skipped.push({file, case: name, reason: `case threw: ${error.message.split('\n')[0]}`});
    }
  };
  run.skip = noop;
  run.only = run;
  return run;
}

// One package per process, and the script that drives this enforces it. Vega numbers every tuple it
// creates from a **module-level counter**, and those ids reach the data — a `lookup` transform keys
// its index by them. Recording two packages in one process therefore makes the second one's vectors
// depend on how many tuples the first one built, so a regenerated file differs from the previous one
// with no change in behaviour. A fresh process starts the counter at zero.
/** The version actually installed, which is what was recorded — not what a checkout claims. */
function installedVersion(packageName) {
  try {
    return JSON.parse(readFileSync(resolve(here, 'node_modules', packageName, 'package.json'), 'utf8')).version;
  } catch {
    return null;
  }
}

// Resolved now: the recorder changes directory to each package's own while it runs its tests.
const [, , checkoutArgument, ...packages] = process.argv;
const checkout = checkoutArgument && resolve(checkoutArgument);
if (packages.length > 1) {
  console.error('record one package per process: tuple ids are a module-level counter');
  process.exit(2);
}
if (!checkout || packages.length === 0) {
  console.error('usage: node record-upstream-tests.mjs <vega-checkout> <package>...');
  process.exit(2);
}
mkdirSync(outputDir, {recursive: true});

// One copy of the package under test: a helper that imports the cloned entry point gets the
// installed one (see record-upstream-hooks.mjs).
/** The test files under a directory, in a stable order: d3-geo keeps some in subdirectories. */
function testFiles(dir, prefix = '') {
  const out = [];
  for (const entry of readdirSync(join(dir, prefix), {withFileTypes: true})) {
    const relative = join(prefix, entry.name);
    if (entry.isDirectory()) out.push(...testFiles(dir, relative));
    else if (entry.name.endsWith('-test.js') || entry.name.endsWith('-test.mjs')) out.push(relative);
  }
  return out.sort();
}

if (packages.length === 1) {
  for (const entry of [join(checkout, 'src', 'index'), join(checkout, 'index'), join(checkout, 'packages', packages[0], 'index')]) {
    packageEntries.add(entry);
    packageEntries.add(`${entry}.js`);
  }
  const installed = import.meta.resolve(packages[0]);
  register('./record-upstream-hooks.mjs', {
    parentURL: import.meta.url,
    data: {
      redirects: [
        [pathToFileURL(join(resolve(checkout), 'src', 'index.js')).href, installed],
        [pathToFileURL(join(resolve(checkout), 'packages', packages[0], 'index.js')).href, installed],
      ],
    },
  });
}

/**
 * Gives the functions of the packages a test imports besides the one under test an origin too:
 * `scaleTime().nice(timeDay)` passes d3-time's `timeDay`, which is not wrapped (it is not the package
 * being recorded), and would otherwise be recorded as an unnamed function.
 */
async function registerDependencyOrigins(packageName) {
  for (const dependency of ['d3-array', 'd3-time', 'd3-interpolate', 'd3-color', 'd3-format', 'd3-time-format', 'd3-ease']) {
    if (dependency === packageName) continue;
    let namespace;
    try {
      namespace = await import(dependency);
    } catch {
      continue;
    }
    for (const [name, value] of Object.entries(namespace)) {
      originate(value, {export: name, package: dependency});
    }
  }
}
await registerDependencyOrigins(packages[0]);

for (const packageName of packages) {
  // Two layouts: Vega is a monorepo with `packages/<name>/test`, and each d3 package is its own
  // repository with `test` at the root.
  const candidates = [
    join(resolve(checkout), 'packages', packageName, 'test'),
    join(resolve(checkout), 'test'),
  ];
  const testDir = candidates.find(existsSync) ?? candidates[0];
  if (!existsSync(testDir)) {
    console.error(`${packageName}: no test directory at ${testDir}`);
    continue;
  }
  // A package's tests read their fixtures relative to the package (`./locale/en-US.json`,
  // `./test/data/barley.json`), so they run from its directory.
  process.chdir(dirname(testDir));
  const calls = [];
  const skipped = [];
  const files = testFiles(testDir);
  for (const file of files) {
    await runTestFile(join(testDir, file), packageName, calls, skipped, checkout);
  }
  // A test that reads a fixture through a callback (`csv-spectrum`) makes its calls after the file has
  // been imported; give the event loop a moment to run them.
  await new Promise(resolve => setTimeout(resolve, 200));
  // Identical calls are recorded once: a test that calls `bin` with the same parameters in three
  // assertions is one vector, and a duplicate proves nothing the first did not. "Identical" is the
  // whole record: the object a method was asked of, its configuration and the steps to the question
  // are part of it.
  //
  // Except where the answer depends on how many calls came before. A random stream is the case
  // (`vega-statistics` draws from a generator a test seeds with `setRandom`): a replay feeds the same
  // stream and expects the next draw, so a draw dropped as a duplicate would shift every later one.
  const stateful = STATEFUL_PACKAGES.has(packageName);
  const unique = [];
  const seen = new Set();
  for (const call of calls) {
    const key = JSON.stringify(call);
    if (!stateful && seen.has(key)) continue;
    seen.add(key);
    unique.push(call);
  }
  const byFunction = {};
  for (const call of unique) { const k = call.fn || call.op; byFunction[k] = (byFunction[k] || 0) + 1; }
  // One vector per line: a file is large, and a line is something a person can grep and diff.
  const header = {
    package: packageName,
    version: installedVersion(packageName),
    // Recorded, because a `local` interval's answer depends on it: d3-time's own suite runs in
    // America/Los_Angeles and Vega's in another zone, so a replay has to know which.
    timeZone: process.env.TZ,
    arch: process.arch,
    files: files.length,
    skipped,
  };
  writeFileSync(
    join(outputDir, `${packageName}.json`),
    `${JSON.stringify(header).slice(0, -1)},"calls":[\n${unique.map(call => JSON.stringify(call)).join(',\n')}\n]}\n`
  );
  console.log(
    `${packageName}: ${unique.length} vectors from ${files.length - skipped.filter(s => !s.case).length}/${files.length} files` +
      `${skipped.length ? `, ${skipped.length} skipped` : ''}`
  );
  console.log('  ' + Object.entries(byFunction).sort((a, b) => b[1] - a[1]).map(([f, n]) => `${f}:${n}`).join(' '));
}
