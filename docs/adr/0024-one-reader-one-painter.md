# 0024: One reader of SVG fonts, one painter of PDF groups

**Status:** accepted, 2026-10-19.

## Context

Layout, the PNG writer and the PDF writer each turned the same SVG into what they draw, and drifted. The PNG writer rebuilt a CSS font string from an element's `font-family`, `font-weight` and `font-style`, rounding the weight to a multiple of 100 and turning a comma in a quoted family into a space, then had it parsed again; the PDF writer read the parts its own way; layout reads the canvas font string Vega measures with. A weight of 549 over faces at 300 and 900 was laid out and drawn in PDF in the Black face and drawn in PNG in the Light one, so a label overflowed its box (C31, audit 2026-10-18).

The PDF writer mirrors part of the PDF graphics state (alphas, caps, dashes) so that a leaf sets only what differs from the stream's. A `Do` of a form and a `sh` of a shading are composited as a whole with the state's constant alpha, blend mode and soft mask, and each caller had to remember to set them: the group of an element with a `mix-blend-mode` was composited at the alpha its previous sibling left set (C30), and a colour glyph's blend composite the same. Opacity was multiplied down the tree onto each paint, so a translucent `<g>`, and a translucent element that both fills and strokes, were drawn piece by piece, each piece showing through the others, where SVG composites them once (C115).

## Decision

- **One reader of an SVG element's font.** `text.ParseFontFamily`, `ParseFontWeight` and `ParseFontStyle` read the three properties for both writers, into the `text.CSSFont` they shape with through `Measurer.ShapeFont`, which resolves families to faces as layout does. Where SVG's reading and layout's differ (a quoted family with a comma is two families to node-canvas, one to CSS; a weight of 1000.5 is 1000 to node-canvas, invalid to CSS), the reader takes layout's, so that a label is drawn in the face it was laid out in. No font goes through a string to be read back.
- **One painter of groups, images and shadings.** `contentWriter.paintXObject` and `paintShading` set the alpha, blend mode and soft mask the caller states (the mirror tracks the blend mode and soft mask too) before writing `Do` or `sh`; they are the only writers of either operator, which `TestPaintedOnlyThroughComposite` checks in the source. A soft mask is read in the space it is set in, so its caller sets it and names it; the painter refuses to paint under one it was not told of.
- **Groups where SVG composites once.** A container (`<g>`, `<a>`, `<svg>`) at an opacity between 0 and 1 is a transparency group composited at it; a leaf that both fills and strokes at such an opacity is one too; an element with a blend mode is one under it. A leaf that paints once keeps its alpha on the paint, which is the same and costs nothing.

## Why

Two readers of one property are two answers to one question, and the tests of each pass. A shared reader and a sweep that asserts the same face from layout, PNG and PDF over every weight from 1 to 1000, every style and the awkward family lists (`TestFontFaceParity`) make the agreement a property rather than a coincidence. Likewise a mirrored state is only safe if every reader of it reconciles it; one primitive makes that true by construction, and a source check keeps it so.

## Consequences

- A chart with translucent marks that are filled and stroked (points, bars, areas with an outline) draws them in PDF as the PNG and a browser do. Each is a form XObject: its PDF is larger, several times for a chart of hundreds of such marks (`circle_natural_disasters`: 33 KB to 232 KB). Correct output was chosen over size; the forms share one group dictionary.
- PNG output changes only for weights that are not multiples of 100 and for quoted families with commas: the corpus is unchanged.
- A new path that paints a form or a shading uses `paintXObject` or `paintShading`; writing the operator directly fails the source check.
