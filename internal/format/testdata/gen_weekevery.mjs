// Records d3's timeWeek.every(k) (Sunday weeks, local time) for the process
// time zone, one JSON object per line: floor and ceil of instants, and the
// range of a few boundaries from each. Large k walk week by week in d3; the
// engine searches instead (weeksearch.go), and these check it is exact.
// Regenerate with testdata/gen.sh.
import { createRequire } from 'node:module';
import path from 'node:path';
const require = createRequire(path.resolve(process.env.NODE_PATH, 'x.js'));
const d3 = await import(require.resolve('d3-time'));
const week = 7 * 864e5;
const ks = [65, 70, 1000, 65537, 100000, 1000000];
const ts = [-6e12, -2.5e12, -1, 0, 1e12, 1.6e12, 1.7e12, 4e12];
const recs = [];
for (const k of ks) {
  const iv = d3.timeWeek.every(k);
  for (const t of ts) {
    const r = iv.range(new Date(t), new Date(t + 3.5 * k * week)).map(Number);
    recs.push({ k, t, floor: +iv.floor(new Date(t)), ceil: +iv.ceil(new Date(t)), range: r });
  }
}
console.log(JSON.stringify({ zone: process.env.TZ, recs }));
