// Records golden vectors for the small parsers vegalite reimplements:
// vega-event-selector, vega-expression's datum dependencies, and escape().
// Usage: NODE_PATH=<node_modules with vega-expression, vega-event-selector, vega-lite> node gen_helpers.mjs > helpers.json
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(path.join(process.env.NODE_PATH, 'x.js'));
const es = await import(require.resolve('vega-event-selector'));
const parseSelector = es.default ?? es.parseSelector;
const ve = await import(require.resolve('vega-expression'));
const parse = ve.parse ?? ve.parseExpression ?? ve.default;

const selectors = [
  ['click', 'scope'], ['pointerover', 'view'], ['dblclick', 'view'], ['wheel!', 'scope'],
  ['[pointerdown, window:pointerup] > window:pointermove!', 'scope'],
  ['[mousedown, window:mouseup] > window:mousemove!', 'scope'],
  ['mouseover[event.shiftKey]', 'view'], ['click[event.altKey][!event.shiftKey]', 'scope'],
  ['@brush:click', 'scope'], ['rect:pointerdown', 'scope'], ['window:keydown', 'scope'],
  ['click{100}', 'scope'], ['pointermove{50, 200}', 'view'], ['timer{1}', 'view'],
  ['click, dblclick', 'view'], ['[click, dblclick] > pointermove', 'view'],
  ['@name:click[event.item], symbol:mouseover', 'scope'], ['*:pointermove', 'scope'],
  ['[pointerdown, pointerup] > [click, dblclick] > pointermove', 'view'],
];
const expressions = [
  'datum.a', "datum['a b']", 'datum.a.b', "datum.a['b']", 'datum.a + datum.b * 2', 'datum.x > 1 ? datum.y : datum.z',
  "isValid(datum.a) && indexof([1,2], datum.b) !== -1", 'toDate(datum["x"]) < now()', 'datum[0]', 'datum[foo]',
  'if(datum.type === "a", -2, 0) + if(datum.x, 1, 2)', 'length(data("brush_store")) && vlSelectionTest("brush_store", datum)',
  "{'a': datum.b, c: [datum.d]}", 'foo(datum.a).b', 'datum', '1 + 2', '"a" + datum.a.b.c', 'datum.a.b[c].d',
  '!datum.a || (datum.b && datum.c)', "datetime(year(datum['date']), 0, 1)", 'datum.a >= 0x10 && datum.b <= 1e3',
];
const escapes = ['', 'a b', ' of Mean', 'x"y', 'héllo', '日本語', 'a+b-c*d/e@f_g.h', 'tab\there', '100%', 'emoji 😀'];

const out = {
  selectors: selectors.map(([s, src]) => ({ input: s, source: src, output: parseSelector(s, src) })),
  expressions: expressions.map((e) => {
    const ast = parse(e);
    const deps = [];
    function name(n) {
      if (n.type === 'Identifier') return [n.name];
      if (n.type === 'Literal') return [n.value];
      if (n.type === 'MemberExpression') return [...name(n.object), ...name(n.property)];
      return [];
    }
    function starts(n) {
      if (n.object.type === 'MemberExpression') return starts(n.object);
      return n.object.name === 'datum';
    }
    ast.visit((n) => { if (n.type === 'MemberExpression' && starts(n)) deps.push(name(n).slice(1).join('.')); });
    return { input: e, deps: [...new Set(deps)] };
  }),
  escapes: escapes.map((s) => ({ input: s, output: escape(s) })),
};
process.stdout.write(JSON.stringify(out, null, 1) + '\n');
