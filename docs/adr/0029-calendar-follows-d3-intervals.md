# 0029: Dates are what `new Date(+v)` makes, counted with d3's intervals, quirks included

**Status:** accepted, 2026-10-19.

## Context

The 2026-10-18 audit found the date code of `internal/format` giving two answers to one question, and reading times JavaScript never holds:

- An instant was a bare `float64`. d3 and vega-time make a `Date` of every argument (`new Date(+v)`, `setTime`), which TimeClips it: `-0.5` is 1970, `1.5` is 1. Some callers clipped (the time scale, the expression evaluator), the formats, floors, intervals and vega-time's day numbers did not, so `utcFormat(-0.5)` printed 1969 (C119).
- `%j %U %W %V` and vega-time's `dayofyear`, `week` and `isoweek` were computed from civil day numbers, while `Interval.Count` replicated d3's counts, which correct a span by the change of `getTimezoneOffset()`, truncated to whole minutes. In a year a zone left local mean time with a seconds offset (Africa/Monrovia 1972, New York 1883) d3 counts a day or a week less; the labels and the counts disagreed (C120).
- vega-time's UTC helpers find a year's start with `Date.UTC(y, 0, 1)`, which reads a year from 0 to 99 as 1900 plus it; its UTC date builder makes an Invalid Date for those years. The engine reproduced the second quirk and not the first (C161).
- `String(date)` printed tzdata's abbreviation (`EST`, `-03`) where V8 prints ICU's long name (`Eastern Standard Time`) (C121).

## Decision

- **An instant is TimeClip'd where it enters the package**: `Zone.Fields` and `TimezoneOffset`, every `Interval` operation (`Floor`, `Ceil`, `Offset`, `Count`, and `Range` and `Round` through them), `Floorer.Floor`, the day and week numbers, the formats, `DateToString` and `ISOFormat`. `format.TimeClip` is the one TimeClip; the expression evaluator uses it.
- **Day and week numbers are d3's and vega-time's interval counts**, written as upstream writes them: `%j` is `1 + day.count(year(d), d)`, `%U`/`%W` `sunday/monday.count(year(d) - 1, d)`, `%V` the count from dISO's year to dISO(d), `dayofyear` `day.count(yearStart - 1, d)`, and so on. There is one calendar algorithm, the intervals', which the d3-time replay holds to upstream.
- **vega-time's 0-99 quirks are reproduced, all of them**: its UTC helpers' `Date.UTC` reading of a year from 0 to 99, and its UTC builder's Invalid Date. A chart of the years 0 to 99 in UTC prints what Vega prints.
- **`String(date)` prints V8's zone names**, from a table recorded from node (`zonenames_table.go`, made by `zonenames_gen.go` from `internal/format/testdata/gen_zonenames.mjs`): each tzdata name's ICU long name, standard and daylight, the daylight one when V8's DST flag is set, which V8 decides outside 1970-2037 at ECMA-262's equivalent year. The table is 36 KB of source: all 598 tzdata names, 240 zone names. A zone that moved to its summer offset all year (Vancouver and Edmonton from November 2026) keeps ICU's daylight name for it, as CLDR still files it under its old metazone; the table records from when (`daylightFrom`).

## Why

The bugs were the same bug each time: a JavaScript invariant (a Date is clipped; a count is the interval's) restated by each caller, or a second computation of what the intervals already compute. Putting the invariant at the package's edge, and deriving every number from the intervals, makes agreement structural: a new entry point clips or fails review, and a new day number is a count.

The 0-99 quirks are upstream's defects. Reproducing all of them, rather than some, is the only policy under which a chart's output does not depend on which helper upstream happened to call.

## Consequences

- Fractional and negative-fractional instants format, floor and count as Vega does (`internal/format/testdata/calendar.jsonl.gz`, recorded per zone, replays every entry point).
- An ISO week label costs about twice what it did (332 ns against 166 ns): it is computed through interval floors and counts, as d3 computes it. Floors of UTC intervals are faster (an integer remainder replaces `math.Mod`).
- The names are V8's for node's tzdata release (2026c); the rules are the host's, as the engine reads the host's zoneinfo. A host whose release models a zone otherwise names it by that release's DST flag: tzdata 2026d and 2026e move Inuvik and Winnipeg to their summer offset all year, as standard time, so the engine on such a host prints "Central Standard Time" for a Winnipeg summer that 2026c's V8 calls "Central Daylight Time" (and that a V8 carrying 2026e will likely call daylight all year, as it does Vancouver). Re-recording the table with a node that carries the host's release closes the gap; nothing in tzdata predicts CLDR's naming.
- Tests compare with node's recordings in node's release, compiled as ICU compiles it (`internal/nodetz`), never in the host's: `TestDateToStringZoneNames` compares all 8,288 recorded instants with no tolerance, but those of six legacy names node does not resolve from `TZ` (it takes their offset from the host).
