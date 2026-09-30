// Package layout is the Go counterpart of vega-view-transforms: it positions
// the axes, legends and titles of a group, lays out groups on a grid (the
// `layout` group property, including trellis headers, footers and titles),
// computes the autosize adjustment of the view, removes overlapping labels
// and bounds scenegraph items.
//
// It operates on the scenegraph the runtime builds (package scene). Guide
// groups are recognised by mark role ("axis", "legend", "title", ...), exactly
// as upstream does, and read their layout inputs from item properties:
//
//   - orient lives in scene.Item.Orient; every other guide property that the
//     scene package does not model (offset, position, range, minExtent,
//     maxExtent, titlePadding, translate, padding, frame, anchor, auto, row,
//     column, ...) is read from scene.Item.Extra, so the runtime must store the
//     encoded value of such properties there;
//   - x, y, width and height are the typed scene.Item fields.
//
// Upstream tracks "dirty" items to schedule incremental redraws; this package
// does not, since output is produced from a single finished scenegraph.
//
// Results that depend on floating point bit patterns (positions and bounds)
// avoid fused multiply-add: products that feed an addition are wrapped in an
// explicit float64 conversion.
package layout
