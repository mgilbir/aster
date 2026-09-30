// Package format ports the number, time and label formatting layers of Vega:
// d3-format, d3-time, d3-time-format, vega-time and vega-format.
//
// Dates are float64 epoch milliseconds throughout (NaN is an Invalid Date),
// the same representation jsval.KindTimestamp carries. Calendar arithmetic is
// always relative to an explicit [Zone]: [UTC] or a local zone backed by a
// *time.Location. Nothing here reads time.Local; the engine's default is UTC.
//
// # Numbers
//
// [NumberLocale] compiles d3 format specifiers ("," ".2f" "$,.0f" "~s" ...)
// into [NumberFormat] values. Exact JavaScript digit generation
// (toFixed/toPrecision/toExponential) is delegated to jsval, with a fast path
// for the cases where Go's correctly rounded strconv agrees with it.
//
// # Time
//
// [Interval] is d3-time's interval family (floor, ceil, round, offset, range,
// count, every) for both UTC and local calendars. [TimeLocale] compiles
// strftime-style specifiers into [TimeFormat] and [TimeParser] values.
// [ParseDate] is V8's Date.parse. The vega-time layer ([NewFloor], [Bin],
// [TimeUnitSpecifier], [IntervalFor], [DetectUnits], ...) builds on those.
//
// # Vega glue
//
// [Locale] pairs a number and a time locale, memoises formatters by
// specifier as vega-format does, and provides formatFloat, formatSpan and the
// multi-scale time format.
package format
