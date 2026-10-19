// Package scale implements the scale layer of Vega: d3-scale's scales,
// d3-interpolate's interpolators, d3-array's ticks, vega-scale's registry, tick
// and label helpers, and the colour schemes. Colour parsing and conversion
// (d3-color) live in the color subpackage, which has no dependency on the value
// model so that the expression layer can use it too.
//
// # Scales
//
// New builds a scale from its vega-scale type name ("linear", "band",
// "sequential-log", "time", ...) and the Is* functions answer vega-scale's
// metadata questions. Scales are dynamically typed the way Vega's are: domains
// and ranges are slices of jsval.Value, and Apply takes a jsval.Value. Every
// scale implements Scale; optional behaviour (Clamper, Niceable, Ticker,
// Inverter, ...) is exposed through small capability interfaces that mirror
// the methods a d3 scale object may or may not have, so callers test with a
// type assertion where vega-encode tests `isFunction(scale[key])`. Set and Get
// do the name-based dispatch for the plain property setters.
//
// The concrete types (Continuous, Sequential, Diverging, Band, Ordinal,
// Quantile, Quantize, Threshold, BinOrdinal, Identity, Time) also offer
// allocation-free numeric entry points where they matter (ApplyFloat,
// Band.Position, InvertNumber, ...).
//
// # Ticks and labels
//
// Ticks, TickStep and TickIncrement are d3-array's, including its
// floating point behaviour. TickCountFor, TickValues, ValidTicks, TickFormat,
// LabelValues, LabelFormat, LabelFraction and DomainCaption are vega-scale's;
// the formatters come from a format.Locale.
//
// # Numeric parity
//
// Results are meant to match V8 bit for bit where the arithmetic is plain
// IEEE-754. Products that upstream rounds before adding are written
// float64(x*y)+z so the compiler cannot fuse them into an FMA (arm64 does).
// Math's functions (Pow, Exp, Log10, Round, ...) are jsmath's, which follows
// V8 on arm64; node on x86-64 differs from it in the last bit for a small
// share of arguments (see package jsmath), and so can a scale's tick or
// position by an ulp. Values are compared with jsval's relational
// comparison (strings by UTF-16 code unit), as JavaScript's < compares them.
//
// # Replay-only API
//
// A few exported pieces have no caller in the engine, because Vega never
// reaches them: the radial scale (Radial), the d3-scale tickFormat methods
// of the scales (vega formats ticks through TickFormat in vegaticks.go), and
// interpolateZoom.rho (Zoom). They are kept as the targets of the d3-scale
// and d3-interpolate vector replays (upstream_test.go), which hold them to
// upstream; the code they share with what the engine runs is verified
// through them.
package scale
