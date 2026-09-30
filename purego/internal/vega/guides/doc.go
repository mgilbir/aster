// Package guides is the Go counterpart of vega-parser's guide parsers
// (parsers/axis.js, legend.js, title.js and parsers/guides/*) together with
// the two datum-producing operators the guides feed on (vega-encode's
// AxisTicks and LegendEntries).
//
// Upstream compiles every axis, legend and title into ordinary group, rule,
// text, rect and symbol mark definitions that the regular mark machinery
// evaluates; ViewLayout (package layout) then positions the resulting groups.
// This package produces exactly those mark definitions, as jsval values in
// Vega specification form, so the runtime can hand them to its mark parser.
//
// Each guide is planned in two steps because the definitions refer to the data
// sources ("refs") that feed them and the runtime owns those:
//
//	plan, err := guides.NewAxis(spec, scope)   // config lookup, datum, operator params
//	// runtime creates the data sources from plan.Datum / plan.Ticks ...
//	mark := plan.Mark(dataRef, ticksRef)        // the axis group definition
//
// The refs are opaque jsval values that are copied verbatim into the `from`
// property of the generated marks.
package guides
