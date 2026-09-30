// Package transforms implements Vega's data transforms (vega-transforms,
// vega-statistics, vega-regression and the non-mark parts of vega-encode) as
// plain Go functions over tuples.
//
// A tuple is a jsval.Value holding an object. Transforms are one-shot: they
// take the complete input, typed parameters and accessors, and return the
// output tuples. The dataflow runtime parses specification parameters into the
// parameter structs defined here and supplies the accessors; nothing in this
// package knows about signals, pulses or operators.
//
// Transforms that upstream implements by mutating tuples (formula, stack,
// window, joinaggregate, ...) write fields into the objects they are given.
// Transforms that create tuples (aggregate, bin does not, fold, pivot, ...)
// allocate new objects in the same field order as upstream.
//
// Sub-packages hierarchy, force, voronoi, contour, label and wordcloud hold
// the transforms that need their own algorithms.
package transforms
