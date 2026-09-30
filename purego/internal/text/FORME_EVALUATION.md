# forme as the text engine for purego: evaluation

Measured against `internal/textmeasure` (go-text/typesetting v0.3.3, the
reference engine) with forme v0.4.2 (v0.4.1 figures kept where they show the change), on an Apple M1 Pro, Go 1.27.

## Verdict

Use forme. In the default metric model this package reproduces the reference's
widths **exactly** (0.000000 px difference on 3846 of 3846 comparable
measurements) apart from two understood classes. Cost: a cold (uncached)
measurement is now 1.4x faster than go-text (v0.4.1: 2.4x slower), and
cold measurements from several goroutines run in parallel. The measurement
cache (19 ns per hit) makes repeats free. Construction is 5.7x faster and
allocates 6x less. What v0.4.1 lacked (outlines, family/weight/style,
per-glyph adjustment) is now in forme; the remaining gaps are no font database
and no script override.

## Accuracy

`TestCompareTextmeasure` measures 96 strings x 41 CSS fonts (Latin, digits,
punctuation, accents, ligature and kern triggers, Greek, Cyrillic, Hebrew,
Arabic, Devanagari, CJK, emoji, a long paragraph; sans/serif/mono, bold,
italic, weights 300 to 700, sizes 0.5 to 100 px, pt and em units, unknown
families).

| mode | comparable cases | exact | max abs | mean abs |
|---|---|---|---|---|
| default (reference metric model), unexplained | 3846 | 3846 (100%) | 0 | 0 |
| default, all cases | 3936 | 97.7% | 30.2 px | 0.22 px |
| `WithExactAdvances` (unrounded, exact size), all | 3936 | 8.5% | 68.5 px | 0.53 px |

Getting to zero took three things that are properties of the reference, not of
text shaping, and that any drop-in replacement has to copy:

1. **Shaping size is rounded up to whole pixels.** go-text sets the HarfBuzz
   scale to `Size.Ceil() << 6`, so "10.5px" is shaped at 11 px. (`WithExactAdvances`
   drops this; it is the reason that mode differs from the reference for
   fractional sizes.)
2. **Advances are rounded to 1/64 px per glyph**, and HarfBuzz scales a glyph's
   own advance with rounding but a GPOS adjustment (kerning) with integer
   truncation, each separately. forme reports their sum; `Glyph.XAdvance` minus
   `Glyph.XAdjust` (forme v0.4.2) is the nominal advance, which is how the two
   are split again (v0.4.1: this package re-read `hmtx`). Without the split 11% of cases were off by
   1/64 to 1/16 px on kern pairs ("AVATAR", "Yo", "fi fl").
3. The CSS regexp is unanchored, so `bold italic 12px x` parses as italic
   *only* (style must precede weight). Copied verbatim, with a parse cache.

The two remaining classes, both asserted in the test so a third cannot hide:

* **Hebrew (30 cases, up to 1.4 px at 72 px, 0.7%).** The reference forces
  `Script: Latin` for every run. forme detects the script and applies Hebrew
  layout rules. forme is the more correct one.
* **Emoji in bold or italic text (60 cases).** The reference's fallback pass
  for uncovered runes keeps only user faces matching the requested weight and
  style; Noto Emoji is registered as regular, so for `bold` or `italic` text
  the emoji falls through to the sans face's `.notdef` (0.75 em) instead of
  Noto Emoji (1.27 em). This is a bug in the reference (the fallback is
  documented as always present). This package always reaches Noto Emoji, so
  PNG and measurement agree. Purego will differ from the reference on bold
  emoji labels until the reference is fixed; if bug-for-bug parity is wanted
  it is a one-line change in `faces` (drop the final unconditional
  `push(m.entries)`, the aspect-free step).

Source of truth for fallback order, mirrored in `fontset.go`: exact family
(best weight/style, one face per family, generics `serif`/`monospace`/
`cursive`/`fantasy` redirected first) then the default family, then metric
aliases (Arial to Liberation Sans, Times New Roman to Liberation Serif, Courier
New to Liberation Mono), then every registered face fitting the aspect, then
every registered face; runes nothing covers use the first registered face's
`.notdef`. Spaces, controls and default-ignorable characters never trigger a
face change. Identical CSS parsing, weight matching (CSS Fonts style matching)
and face-run splitting to the reference.

## Performance

`go test -bench . ./purego/internal/text` (M1 Pro, arm64, forme v0.4.2):

| benchmark | this package | textmeasure | ratio |
|---|---|---|---|
| repeated label (cache hit) | 19 ns, 0 allocs | 5.1 us, 24 allocs | 270x faster |
| unique 30-char label (cold) | 5.4 us, 17 allocs, 8.8 KB | 7.8 us, 29 allocs, 3.2 KB | 1.4x faster |
| unique label, `WithExactAdvances` | 5.4 us | | |
| unique label, 10 goroutines (wall time per measurement) | 2.5 us | 8.9 us | 3.6x faster |
| 216-byte paragraph, ShapeText | 43 us, 22 allocs | 59 us, 244 allocs | 1.4x faster |
| mixed emoji/CJK fallback | 8.6 us, 77 allocs | 7.5 us | 1.15x slower |
| construction (`New`) | 2.7 ms, 4.1 MB | 15.4 ms, 24.8 MB | 5.7x faster |
| `GlyphOutline` (cached glyph) | 260 ns, 1 alloc | | |

Cold path history: 18.4 us on forme v0.4.1 (90% in `applyPositioningLookup`,
GPOS mark work that cannot apply to mark-free text); 5.3 us on v0.4.2 with the
positioning gates. Chart labels repeat heavily (ticks, legend entries, per-row
axis labels), so the result cache (16k entries, cleared when full) still
dominates in practice.

Parallel cold measurements: forme v0.4.2 documents `Face.Clone` as the way to
shape from several goroutines (clones share the parse, the layouts and the
gates, and keep their own glyph record). The Measurer therefore holds its
mutex only for the caches, CSS parsing and face selection, and shapes outside
it through a `sync.Pool` of clones per face. Before, every cold measurement
serialised (5.3 us each, no matter how many goroutines). Scaling is 2.2x on 10
cores, limited by allocation (8.8 KB per cold measurement) rather than locks.

## API gaps in forme

Still open (v0.4.2):

* **No font database.** `Face.Family()` and `Face.Subfamily()` read a loaded
  face, but nothing enumerates fonts. `WithSystemFonts` walks the platform font
  directories once per process and indexes `.ttf`/`.otf` by name, OS/2 and head
  tables (`system.go`), reading only those tables so that a file is loaded
  (and handed to forme) when a CSS family names it; forme would need a load to
  answer the same. Not supported: `.ttc` collections, bare WOFF, and
  script-based fallback across system fonts (a CJK label needs a CJK family in
  the CSS to render from a system font). go-text does script fallback.
* **No script or language override.** Runs are shaped in their detected script;
  the reference forces Latin (see Hebrew above).
* **`macStyle` bold is not exposed.** A font with no OS/2 table but a bold
  `macStyle` reads as weight 400 (`Descriptor` has only the OS/2 weight). Rare.

Closed by v0.4.2 (see the next section).

## Since v0.4.2

forme v0.4.2 closed the four gaps this package had worked around
(mgilbir/forme#863 to #866):

* **Outlines (#864)**: `Face.GlyphOutline` replaces the second parse with
  `golang.org/x/image/font/sfnt`; that dependency is gone from this package.
  forme yields font units, y up, contours closed implicitly; `GlyphOutline`
  flips y, adds the explicit `Close`, scales, and keeps its per-face cache of
  the converted outlines (without it a call cost 920 ns instead of 260 ns).
  CFF2 outlines now draw (v0.4.1 sfnt returned `ErrNoOutline`); colour glyphs
  draw as their base outline, bitmap glyphs return `ErrNoOutline`.
* **Style metadata (#865)**: `Descriptor().Weight`, `.Italic`, `.Oblique` (with
  `Has(MetricWeight)`) replace `styleOf`'s hand-read OS/2 and head tables for
  `WithFont`. `Family()` is available but `WithFont` names the family
  explicitly, so it is not needed here.
* **`Glyph.XAdjust` (#866)**: replaces the `hmtx` re-read (and `readHmtx`) that
  separated the nominal advance from kerning. The 3846/3846 exact match is
  unchanged.
* **Subset panic (#863)**: the committed reproducer
  (`testdata/fuzz/FuzzLoadFont/79527b20830b3484`) no longer panics; `Subset`
  returns "the font declares 0 glyphs" (`TestSubsetReproducerDoesNotPanic`
  calls forme without any recover). `Face.Subset` still recovers, defensively.
* **Positioning gates and clone-safe shaping**: cold path 18.4 us to 5.4 us,
  and parallel cold shaping (above). Because clones keep their own glyph
  record, the Face merges the glyphs each shaping returned into the base face
  with `Use` (a lock-free bitset check, a mutex only for new glyphs) so
  `Subset` keeps everything shaped through any clone.

## Robustness

Font bytes may be user supplied. Loading, shaping and outline extraction never
propagate a panic: `newFace`, `Face.shapeGlyphs`, `GlyphOutline`, `Face.Subset`
and the system scanner all `recover`. Inputs are bounded:
64 MB per font file, 1 MiB per measured text (longer text is truncated at a
rune boundary), sizes clamped to 65536 px, 32 families per query, 16k-entry
caches, 4096 cached outlines per face, 20000 system font files scanned.

`FuzzLoadFont` (seeded with the Liberation and CFF test fonts, truncations and
bare magic numbers) ran for two 60 s sessions on v0.4.1 (2.8 M executions in the
clean one), asserting no panic and no input taking over 3 s. It found **one
panic in forme v0.4.1**, `Face.Subset` on a malformed CFF font, fixed in v0.4.2
(see above). Re-run on v0.4.2 for 40 s: no failure, but only about 15 k
executions, because mutated copies of the 300 KB Liberation seeds take 100 to
250 ms in `shape.Load` (the slowest seen, well inside the 3 s bound); v0.4.1
handled the same mutations far faster. `FuzzMeasure` ran 20 s (295 k
executions) over text and CSS strings with no failure. The unit tests also run
clean under `-race`, including a test that measures cold from 8 goroutines and
compares with the serial result.

## Recommendation

Adopt forme for text measurement and outlines, with the default metric model
(reference-compatible) so the differential comparison against the root engine is
not polluted by text. The four follow-ups filed against v0.4.1 are done. What
is left for forme: `macStyle` bold in `Descriptor`, a font enumeration API, a
script override, and the load time of mutated large fonts. Decide separately
whether purego should keep the reference's bold-emoji `.notdef` behaviour
(currently it does not).

## Since v0.4.3

Both issues filed after the v0.4.2 evaluation are fixed:

- **Bold from `head.macStyle`** (mgilbir/forme#871): a face with no OS/2 table
  now reports `Weight` 700 (declared) when macStyle's bold bit is set, as it
  already did for italic. Liberation Sans Bold and Bold Italic with OS/2
  removed read as 700 again.
- **Slow `shape.Load` on a spliced font** (mgilbir/forme#872): the reproducer
  (Liberation Sans Regular with one block spliced into itself) loads in
  7.3 ms instead of 1.76 s; the slowest of 400 structural corruptions went
  from 1.7 s to 12 ms. Clean loads are unchanged (~0.2 ms).

The comparison against internal/textmeasure is still 3846/3846 exact in the
default metric model. `FuzzLoadFont` still runs slowly (about 300 executions
a second on 300 KB mutated seeds); that cost is no longer in `shape.Load`'s
feature indexing and has not been investigated further.
