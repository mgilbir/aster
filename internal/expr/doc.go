// Package expr is the Vega expression language: the ESTree subset parsed by
// vega-expression and the function library of vega-functions.
//
// An expression is parsed to an AST (Parse), then compiled once into a tree of
// Go closures (Compile) that evaluate against a Scope:
//
//	prog, err := expr.Compile("datum.value > threshold && year(datum.date) === 2020")
//	deps := prog.Deps()          // signals, datum fields, datasets, scales it reads
//	scope := expr.NewScope(env)  // env supplies signals and the runtime hooks
//	scope.Datum = tuple
//	v, err := prog.Eval(scope)   // reuse the scope for the next datum
//
// JavaScript semantics are implemented exactly on jsval.Value: `+` concatenates
// strings or adds numbers after ToPrimitive, `==` is abstract equality, `<` the
// abstract relational comparison (strings by UTF-16 code unit, dates by time
// value), bitwise operators work on ToInt32, string functions count UTF-16
// code units, and so on. The function table is exactly upstream's: an
// identifier that is not a constant, `datum`, `event` or `item` is a signal
// reference (reported in Deps), and a call to anything not in the table is a
// compile error.
//
// # Runtime hooks
//
// Everything an expression can reach outside itself goes through the interfaces
// in env.go. Env (signal lookup) is required; DataProvider, ScaleProvider,
// GeoProvider, TreeProvider, ViewProvider, DataWriter, Logger and the rest are
// optional capabilities the runtime implements as it can. Time zone, number and
// time formats come from Scope.Locale (package format); the clock, random
// source and context come from the Scope. Opaque values (scale functions,
// gradients, projections, scenegraph items, the event) cross the boundary as
// jsval.Values whose representation the runtime chooses.
//
// # Representation choices
//
//   - d3 colours (rgb(), hsl(), lab(), hcl()) are plain objects with d3's
//     channel names ({r, g, b, opacity}, {h, s, l, opacity}, ...); an object of
//     exactly that shape converts to d3's "rgb(r, g, b)" string wherever
//     JavaScript would call toString.
//   - vlSelectionResolve returns arrays where upstream returns Sets.
//   - Dates are jsval.KindTimestamp values, so === compares them by time value.
//
// # Safety
//
// Inputs are untrusted. Parse bounds source length and nesting; evaluation bounds
// what a specification can make large (sequence, pad, truncate), honours
// Scope.Context in loops, and reports a JavaScript exception (reading a property
// of null, an invalid regular expression) as an *Error result of Eval. No path
// panics.
package expr
