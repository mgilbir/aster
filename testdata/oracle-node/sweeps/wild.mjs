// The "wild" Vega-Lite corpus: specifications other people wrote for their own
// charts, from hyungkwonko/chart-llm's docs/data/chart (pinned, fetched into
// testdata/corpora-cache/wild by scripts/fetch-corpora.sh). The claim tested
// is agreement with upstream, refusals included, not that a spec is valid.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

export function generate({ repo }) {
  const dir = join(repo, 'testdata/corpora-cache/wild/docs/data/chart');
  if (!existsSync(dir)) throw new Error('corpus not fetched: run scripts/fetch-corpora.sh');
  const cases = [];
  const skips = [];
  for (const file of readdirSync(dir).filter((f) => f.endsWith('.vl.json')).sort()) {
    const name = file.replace(/\.vl\.json$/, '');
    let spec;
    try {
      spec = JSON.parse(readFileSync(join(dir, file), 'utf8'));
    } catch {
      skips.push({ family: 'wild', property: name, reason: 'not parsable as JSON' });
      continue;
    }
    // The schema version the author wrote against, as the family.
    const m = /\/schema\/vega-lite\/v(\d+)/.exec(String(spec?.$schema ?? ''));
    cases.push({ name, family: m ? `v${m[1]}` : 'no-schema', lite: true, spec });
  }
  return { cases, skips };
}
