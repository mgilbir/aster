// Records the zone name V8's Date.prototype.toString prints, for every name
// of tzdata (zones and links): ICU's long name of the zone's current
// metazone, standard or daylight as V8's DST flag for the instant says
// (C121 of the 2026-10-18 audit).
//   node testdata/gen_zonenames.mjs testdata/tzdata.zi samples > /tmp/samples.json
//     every month of 1970-2037 for each name, which zonenames_gen.go reads
//     to make zonenames_table.go;
//   node testdata/gen_zonenames.mjs testdata/tzdata.zi vectors | gzip -n -9 > testdata/zonenames.json.gz
//     a few instants a name, inside and outside 1970-2037, with V8's text
//     and offset, which TestDateToStringZoneNames replays.
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
const [, self, zi, mode] = process.argv;
if (process.env.ZONENAMES_ZONE) { // the child: one name's samples, run with TZ set to it
  const ts = [];
  if (mode === 'samples') {
    for (let y = 1970; y <= 2037; y++) for (let m = 0; m < 12; m++) ts.push(Date.UTC(y, m, 15, 12));
  } else {
    for (const [y, m] of [[1850, 0], [1850, 6], [1925, 0], [1925, 6], [1985, 0], [1985, 6], [2021, 0], [2021, 6], [2030, 0],
      [2030, 6], [2045, 0], [2045, 6], [2200, 3], [-300, 9]]) ts.push(Date.UTC(y, m, 15, 12));
  }
  process.stdout.write(JSON.stringify(ts.map((t) => {
    const s = String(new Date(t));
    return [t, s.slice(s.indexOf('(') + 1, -1), new Date(t).getTimezoneOffset()];
  })));
} else {
  const names = readFileSync(zi, 'utf8').split('\n').flatMap((l) => (l.startsWith('Z ') ? [l.split(' ')[1]]
    : l.startsWith('L ') ? [l.split(' ')[2]] : []));
  const out = { node: process.version, tz: process.versions.tz, zones: {} };
  for (const z of names.sort()) {
    out.zones[z] = JSON.parse(execFileSync(process.execPath, [self, zi, mode], { env: { ...process.env, TZ: z, ZONENAMES_ZONE: z } }).toString());
  }
  process.stdout.write(JSON.stringify(out));
}
