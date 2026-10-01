// The Deneb templates (avatorl/Deneb-Vega-Templates, pinned, fetched into
// testdata/corpora-cache/deneb by scripts/fetch-corpora.sh), made into plain
// Vega both engines can run. Three things in them belong to Power BI, and each
// is undone here in the open:
//
//  1. No data. The root dataset is {"name": "dataset"}, injected at run time;
//     rows are synthesised, deterministically, from the columns declared in
//     usermeta.dataset (key __0__, __1__, ...; type text, numeric or dateTime).
//     The values are plausible, not meaningful: the question is whether two
//     engines make the same thing of one specification.
//  2. pbiColor(n[, shade]) is Deneb's: each call is replaced by the literal
//     colour it would return from Power BI's default palette.
//  3. // comments, in one template, are stripped.
//
// Headless there is no element for containerSize(), which would make a
// width of NaN, so that update is dropped and the signal keeps its own value
// (the fallback size the author wrote for this case).
//
// pbiPatternSVG, a second Deneb function that returns a generated SVG
// pattern, has no literal to substitute without inventing the chart: the one
// template that calls it is skipped, with that reason.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

const PALETTE = ['#118DFF', '#12239E', '#E66C37', '#6B007B', '#E044A7', '#744EC2'];

// pbiColor(index, shade): towards white for a positive shade, black for a negative one.
function pbiColor(index, shade = 0) {
  const hex = PALETTE[index % PALETTE.length];
  const channels = [1, 3, 5].map((at) => parseInt(hex.slice(at, at + 2), 16));
  const towards = shade >= 0 ? 255 : 0;
  const mixed = channels.map((c) => Math.round(c + (towards - c) * Math.abs(shade)));
  return '#' + mixed.map((c) => c.toString(16).padStart(2, '0')).join('');
}

const LABELS = ['Alpha', 'Bravo', 'Charlie', 'Delta', 'Echo', 'Foxtrot', 'Golf', 'Hotel', 'India', 'Juliett', 'Kilo', 'Lima'];
const ROWS = LABELS.length;

// A number that varies without being random: a linear congruential sequence
// seeded by the column.
function numbers(seed) {
  const out = [];
  let state = (seed + 1) * 7919;
  for (let i = 0; i < ROWS; i++) {
    state = (state * 1103515245 + 12345) % 2147483648;
    out.push(1 + (state % 1000) / 10);
  }
  return out;
}

const dates = () => Array.from({ length: ROWS }, (_, i) => `2020-${String(i + 1).padStart(2, '0')}-01`);

// `//` to end of line, outside of strings.
function stripComments(text) {
  let out = '';
  let inString = false;
  let escaped = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inString) {
      out += c;
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') inString = false;
      continue;
    }
    if (c === '"') { inString = true; out += c; continue; }
    if (c === '/' && text[i + 1] === '/') {
      while (i < text.length && text[i] !== '\n') i++;
      out += '\n';
      continue;
    }
    out += c;
  }
  return out;
}

function walk(dir) {
  const found = [];
  for (const entry of readdirSync(dir).sort()) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) found.push(...walk(path));
    else if (entry.endsWith('.json')) found.push(path);
  }
  return found;
}

export function generate({ repo }) {
  const root = join(repo, 'testdata/corpora-cache/deneb');
  if (!existsSync(root)) throw new Error('corpus not fetched: run scripts/fetch-corpora.sh');
  const cases = [];
  const skips = [];
  for (const path of walk(root)) {
    const rel = relative(root, path);
    const name = rel.replace(/\.deneb-template\.json$|\.json$/, '').replace(/[/\\]/g, '__');
    const family = rel.split('/')[0];
    const skip = (reason) => skips.push({ family, property: name, reason });
    const raw = readFileSync(path, 'utf8');
    if (raw.includes('pbiPatternSVG')) {
      skip('pbiPatternSVG (Deneb) returns a generated SVG pattern; nothing honest to substitute');
      continue;
    }
    let template;
    try {
      template = JSON.parse(stripComments(raw));
    } catch (error) {
      skip(`unreadable: ${error.message}`);
      continue;
    }
    if (!String(template.$schema || '').includes('vega.github.io/schema/vega/')) {
      skip('not a Vega specification');
      continue;
    }
    const columns = template.usermeta?.dataset ?? [];
    if (columns.length === 0) {
      skip('declares no dataset to stand in for');
      continue;
    }
    const parse = {};
    const values = columns.map((column, index) => {
      if (column.type === 'numeric') return numbers(index);
      if (column.type === 'dateTime') { parse[column.key] = 'date'; return dates(); }
      return LABELS;
    });
    const rows = [];
    for (let row = 0; row < ROWS; row++) {
      rows.push(Object.fromEntries(columns.map((c, i) => [c.key, values[i][row]])));
    }
    delete template.usermeta;
    const body = JSON.stringify(template).replace(/pbiColor\(\s*(-?\d+)\s*(?:,\s*(-?[\d.]+)\s*)?\)/g, (_, index, shade) =>
      `'${pbiColor(Number(index), shade === undefined ? 0 : Number(shade))}'`);
    const spec = JSON.parse(body);
    for (const signal of spec.signals ?? []) {
      if (typeof signal.update === 'string' && signal.update.includes('containerSize()')) {
        if (signal.value === undefined) signal.value = 400;
        delete signal.update;
      }
    }
    const supplied = (spec.data ?? []).find((d) => d.name === 'dataset');
    if (!supplied) {
      skip('no root dataset called "dataset"');
      continue;
    }
    supplied.values = rows;
    if (Object.keys(parse).length) supplied.format = { parse };
    cases.push({ name, family, spec });
  }
  return { cases, skips };
}
