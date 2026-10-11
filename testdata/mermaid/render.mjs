// Renders Mermaid diagrams with Mermaid's own renderer, as GitHub does in the
// browser, on jsdom instead of a browser, for TestDocsMermaid.
//
// stdin: a JSON array of {"name": "...", "source": "..."}.
// stdout: a JSON array of {"name": "...", "error": "..."}, error "" when the
// diagram rendered. With MERMAID_OUT=dir, each SVG is written there too.
//
// jsdom does no layout, so text is sized by an estimate (0.6 em a character,
// 1.2 em high). That is all Mermaid's layout needs to place it: the parse, the
// layout and the SVG writing all run, and any of them failing is reported.
// Positions in the SVG are therefore approximate, not what a browser draws.
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { pretendToBeVisual: true });
for (const k of Object.getOwnPropertyNames(dom.window)) {
  if (!(k in globalThis)) {
    try { globalThis[k] = dom.window[k]; } catch { /* read-only global */ }
  }
}
const estimate = function () {
  const n = (this.textContent || '').length;
  return { x: 0, y: 0, width: n * 9.6, height: n ? 19.2 : 0 };
};
for (const C of [dom.window.SVGElement, dom.window.SVGGraphicsElement]) C.prototype.getBBox = estimate;
dom.window.SVGElement.prototype.getComputedTextLength = function () { return (this.textContent || '').length * 9.6; };

const { default: mermaid } = await import('mermaid');
mermaid.initialize({ startOnLoad: false, securityLevel: 'strict' });

const diagrams = JSON.parse(fs.readFileSync(0, 'utf8'));
const out = process.env.MERMAID_OUT;
const results = [];
for (const [i, d] of diagrams.entries()) {
  let error = '';
  try {
    const { svg } = await mermaid.render(`d${i}`, d.source);
    if (!svg.startsWith('<svg') || svg.includes('Syntax error in text')) error = 'rendered an error diagram';
    else if (out) fs.writeFileSync(path.join(out, `${i}.svg`), svg);
  } catch (e) {
    error = String(e && e.message ? e.message : e);
  }
  results.push({ name: d.name, error });
}
process.stdout.write(JSON.stringify(results));
