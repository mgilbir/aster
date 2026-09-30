package vegalite

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// ---- type.ts ----

func isContinuousType(t string) bool { return t == "quantitative" || t == "temporal" }
func isDiscreteType(t string) bool   { return t == "ordinal" || t == "nominal" }

// getFullName expands 'q'/'t'/'o'/'n' and lower-cases; "" is undefined.
func getFullName(t string) string {
	switch strings.ToLower(t) {
	case "q", "quantitative":
		return "quantitative"
	case "t", "temporal":
		return "temporal"
	case "o", "ordinal":
		return "ordinal"
	case "n", "nominal":
		return "nominal"
	case "geojson":
		return "geojson"
	}
	return ""
}

// ---- aggregate.ts ----

var aggregateOps = strSet([]string{
	"argmax", "argmin", "average", "count", "distinct", "exponential", "exponentialb", "product",
	"max", "mean", "median", "min", "missing", "q1", "q3", "ci0", "ci1", "stderr", "stdev",
	"stdevp", "sum", "valid", "values", "variance", "variancep",
})

func isArgminDef(v Value) bool { return hasProperty(v, "argmin") }
func isArgmaxDef(v Value) bool { return hasProperty(v, "argmax") }
func isAggregateOp(v Value) bool {
	return v.IsStr() && aggregateOps[v.StrValue()]
}
func isCountingAggregateOp(v Value) bool {
	if !v.IsStr() {
		return false
	}
	switch v.StrValue() {
	case "count", "valid", "missing", "distinct":
		return true
	}
	return false
}
func isMinMaxOp(v Value) bool {
	return v.IsStr() && (v.StrValue() == "min" || v.StrValue() == "max")
}

var multiDomainSortOps = strSet([]string{"count", "min", "max"})
var sharedDomainOps = strSet([]string{"mean", "average", "median", "q1", "q3", "min", "max"})

// ---- bin.ts ----

func isBinning(bin Value) bool {
	if bin.IsBool() && bin.BoolValue() {
		return true
	}
	return isObject(bin) && !bin.Get("binned").IsTruthy()
}

func isBinned(bin Value) bool {
	if bin.IsStr() && bin.StrValue() == "binned" {
		return true
	}
	return isObject(bin) && bin.Get("binned").IsBool() && bin.Get("binned").BoolValue()
}

func isParameterExtent(v Value) bool { return hasProperty(v, "param") }

func autoMaxBins(channel string) int {
	switch channel {
	case chRow, chColumn, chSize, chColor, chFill, chStroke, chStrokeWidth, chOpacity,
		chFillOpacity, chStrokeOpacity, chShape:
		return 6
	case chStrokeDash:
		return 4
	}
	return 10
}

// binToString is upstream's binToString: "bin" plus one varName'd segment per
// bin parameter, in key order.
func binToString(bin Value) string {
	if bin.IsBool() {
		bin = normalizeBin(bin, "")
	}
	var b strings.Builder
	b.WriteString("bin")
	for _, p := range keysOf(bin) {
		x := bin.Get(p)
		if isParameterExtent(x) {
			// `${entries(x)}` stringifies [[k, v], ...] with commas.
			var parts []string
			for _, k := range keysOf(x) {
				parts = append(parts, k, x.Get(k).AsString())
			}
			b.WriteString(varName("_" + p + "_" + strings.Join(parts, ",")))
		} else {
			b.WriteString(varName("_" + p + "_" + x.AsString()))
		}
	}
	return b.String()
}

func normalizeBin(bin Value, channel string) Value {
	switch {
	case bin.IsBool():
		return mkv("maxbins", autoMaxBins(channel))
	case bin.IsStr() && bin.StrValue() == "binned":
		return mkv("binned", true)
	case !bin.Get("maxbins").IsTruthy() && !bin.Get("step").IsTruthy():
		return jsval.Obj(spread(cloneObj(bin.ObjValue()), mkv("maxbins", autoMaxBins(channel))))
	}
	return bin
}

// ---- sort.ts ----

const defaultSortOp = "min"

var sortByChannel = strSet([]string{
	chX, chY, chColor, chFill, chStroke, chStrokeWidth, chSize, chShape,
	chFillOpacity, chStrokeOpacity, chOpacity, chText,
})

func isSortByEncoding(v Value) bool { return hasProperty(v, "encoding") }
func isSortField(v Value) bool {
	return v.IsObj() && (v.Get("op").IsStr() && v.Get("op").StrValue() == "count" || hasProperty(v, "field"))
}
func isSortArray(v Value) bool { return v.IsArr() }

// ---- timeunit.ts ----

var timeUnitParts = []string{
	"year", "quarter", "month", "week", "day", "dayofyear", "date", "hours", "minutes", "seconds", "milliseconds",
}

func isLocalSingleTimeUnit(u string) bool { return contains(timeUnitParts, u) }

func isBinnedTimeUnit(tu Value) bool {
	if isObject(tu) {
		return tu.Get("binned").IsTruthy()
	}
	return tu.IsStr() && strings.HasPrefix(tu.StrValue(), "binned")
}

func isUTCTimeUnit(t string) bool { return strings.HasPrefix(t, "utc") }

func containsTimeUnit(full, unit string) bool {
	index := strings.Index(full, unit)
	if index < 0 {
		return false
	}
	// 'seconds' is also the tail of 'milliseconds'.
	if index > 0 && unit == "seconds" && full[index-1] == 'i' {
		return false
	}
	// 'day' is also the head of 'dayofyear'.
	if len(full) > index+3 && unit == "day" && full[index+3] == 'o' {
		return false
	}
	// 'year' is also the tail of 'dayofyear'.
	if index > 0 && unit == "year" && full[index-1] == 'f' {
		return false
	}
	return true
}

func getTimeUnitParts(tu string) []string {
	var out []string
	for _, p := range timeUnitParts {
		if containsTimeUnit(tu, p) {
			out = append(out, p)
		}
	}
	return out
}

func getSmallestTimeUnitPart(tu string) string {
	parts := getTimeUnitParts(tu)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// normalizeTimeUnit turns any timeUnit spelling into the params object
// {unit, [binned], [utc], ...}. Undefined stays undefined.
func normalizeTimeUnit(tu Value) Value {
	if !tu.IsTruthy() {
		return undef
	}
	var params *Object
	switch {
	case tu.IsStr():
		s := tu.StrValue()
		if strings.HasPrefix(s, "binned") {
			params = mk("unit", s[6:], "binned", true)
		} else {
			params = mk("unit", s)
		}
	case isObject(tu):
		params = cloneObj(tu.ObjValue())
		if tu.Get("unit").IsTruthy() {
			params.Set("unit", tu.Get("unit"))
		}
	default:
		return undef
	}
	if u := params.Lookup("unit"); u.IsStr() && isUTCTimeUnit(u.StrValue()) {
		params.Set("utc", jsval.True)
		params.Set("unit", jsval.Str(u.StrValue()[3:]))
	}
	return jsval.Obj(params)
}

// timeUnitToString is upstream's timeUnitToString (e.g. "yearmonth",
// "utcyearmonth", "timeunit_maxbins_10").
func timeUnitToString(tu Value) string {
	n := normalizeTimeUnit(tu)
	utc := n.Get("utc").IsTruthy()
	rest := omit(n, "utc")
	pfx := ""
	if utc {
		pfx = "utc"
	}
	var b strings.Builder
	if rest.Lookup("unit").IsTruthy() {
		b.WriteString(pfx)
		for _, p := range rest.Keys() {
			if p == "unit" {
				b.WriteString(varName(rest.Lookup(p).AsString()))
			} else {
				b.WriteString(varName("_" + p + "_" + rest.Lookup(p).AsString()))
			}
		}
		return b.String()
	}
	b.WriteString(pfx + "timeunit")
	for _, p := range rest.Keys() {
		b.WriteString(varName("_" + p + "_" + rest.Lookup(p).AsString()))
	}
	return b.String()
}

var vegaliteTimeFormat = mk("year-month", "%b %Y ", "year-month-date", "%b %d, %Y ")

func timeUnitSpecifierExpression(tu string) string {
	if tu == "" {
		return ""
	}
	parts := getTimeUnitParts(tu)
	return "timeUnitSpecifier(" + stringify(strsVal(parts)) + ", " + stringify(jsval.Obj(vegaliteTimeFormat)) + ")"
}

func formatExpression(tu, field string, isUTCScale bool) string {
	if tu == "" {
		return ""
	}
	expr := timeUnitSpecifierExpression(tu)
	utc := isUTCScale || isUTCTimeUnit(tu)
	fn := "time"
	if utc {
		fn = "utc"
	}
	return fn + "Format(" + field + ", " + expr + ")"
}

// timeUnitFieldExpr is fieldExpr in timeunit.ts.
func timeUnitFieldExpr(full, field string, end bool) string {
	fieldRef := accessPathWithDatum(field, "datum")
	utc := ""
	if isUTCTimeUnit(full) {
		utc = "utc"
	}
	fn := func(unit string) string {
		if unit == "quarter" {
			return "(" + utc + "quarter(" + fieldRef + ")-1)"
		}
		return utc + unit + "(" + fieldRef + ")"
	}
	last := ""
	dateExpr := jsval.NewObject(8)
	for _, part := range timeUnitParts {
		if containsTimeUnit(full, part) {
			dateExpr.Set(part, jsval.Str(fn(part)))
			last = part
		}
	}
	if end && last != "" {
		dateExpr.Set(last, jsval.Str(dateExpr.Lookup(last).StrValue()+"+1"))
	}
	return dateTimeExprToExpr(jsval.Obj(dateExpr))
}

func getDateTimePartAndStep(unit string, step float64) (string, float64) {
	switch unit {
	case "year", "month", "date", "hours", "minutes", "seconds", "milliseconds":
		return unit, step
	case "day", "dayofyear":
		return "date", step
	case "quarter":
		return "month", step * 3
	case "week":
		return "date", step * 7
	}
	return "", step
}

// durationExpr is upstream's durationExpr with the identity wrapper unless
// wrap is given.
func durationExpr(tu Value, wrap func(string) string) string {
	n := normalizeTimeUnit(tu)
	smallest := getSmallestTimeUnitPart(n.Get("unit").AsString())
	if smallest != "" && smallest != "day" {
		start := mk("year", 2001, "month", 1, "date", 1, "hours", 0, "minutes", 0, "seconds", 0, "milliseconds", 0)
		step := 1.0
		if s := n.Get("step"); s.IsNum() {
			step = s.NumValue()
		}
		part, st := getDateTimePartAndStep(smallest, step)
		end := cloneObj(start)
		end.Set(part, jsval.Num(start.Lookup(part).NumValue()+st))
		if wrap == nil {
			wrap = func(s string) string { return s }
		}
		return wrap(dateTimeToExpr(jsval.Obj(end))) + " - " + wrap(dateTimeToExpr(jsval.Obj(start)))
	}
	return ""
}

// ---- datetime.ts ----

var (
	monthNames = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	dayNames   = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
)

func isDateTime(v Value) bool {
	if v.IsTruthy() && isObject(v) {
		for _, p := range timeUnitParts {
			if hasProperty(v, p) {
				return true
			}
		}
	}
	return false
}

func normalizeNamed(v Value, names []string, what string) float64 {
	if v.IsStr() && isNumericString(v.StrValue()) {
		v = jsval.Num(jsval.ToNumber(v))
	}
	if v.IsNum() {
		if what == "month" {
			return v.NumValue() - 1
		}
		return v.NumValue()
	}
	if !v.IsStr() {
		throw("Invalid %s value: %s", what, v.AsString())
	}
	lower := strings.ToLower(v.StrValue())
	for i, n := range names {
		if n == lower {
			return float64(i)
		}
	}
	short := lower
	if r := []rune(short); len(r) > 3 {
		short = string(r[:3])
	}
	for i, n := range names {
		if n[:3] == short {
			return float64(i)
		}
	}
	throw("Invalid %s value: %s", what, v.StrValue())
	return 0
}

// dateTimeParts is upstream's dateTimeParts: the arguments of datetime(...)
// as text. With normalize=true month/quarter/day names and 1-based numbers are
// converted to the 0-based values datetime() expects.
func dateTimeParts(d Value, normalize bool) []string {
	var parts []string
	if normalize && !d.Get("day").IsUndefined() {
		if d.Len() > 1 {
			d = jsval.Obj(omit(d, "day"))
		}
	}
	str := func(v Value) string { return v.AsString() }
	if y := d.Get("year"); !y.IsUndefined() {
		parts = append(parts, str(y))
	} else {
		parts = append(parts, "2012")
	}
	switch {
	case !d.Get("month").IsUndefined():
		m := d.Get("month")
		if normalize {
			parts = append(parts, jsval.JSNumberString(normalizeNamed(m, monthNames, "month")))
		} else {
			parts = append(parts, str(m))
		}
	case !d.Get("quarter").IsUndefined():
		q := d.Get("quarter")
		if normalize {
			if q.IsStr() && isNumericString(q.StrValue()) {
				q = jsval.Num(jsval.ToNumber(q))
			}
			if !q.IsNum() {
				throw("Invalid quarter value: %s", q.AsString())
			}
			parts = append(parts, jsval.JSNumberString((q.NumValue()-1)*3))
		} else if q.IsNum() {
			parts = append(parts, jsval.JSNumberString(q.NumValue()*3))
		} else {
			parts = append(parts, str(q)+"*3")
		}
	default:
		parts = append(parts, "0")
	}
	switch {
	case !d.Get("date").IsUndefined():
		parts = append(parts, str(d.Get("date")))
	case !d.Get("day").IsUndefined():
		day := d.Get("day")
		if normalize {
			if day.IsStr() && isNumericString(day.StrValue()) {
				day = jsval.Num(jsval.ToNumber(day))
			}
			var f float64
			if day.IsNum() {
				f = math.Mod(day.NumValue(), 7)
			} else {
				f = normalizeNamed(day, dayNames, "day")
			}
			parts = append(parts, jsval.JSNumberString(f+1))
		} else if day.IsNum() {
			parts = append(parts, jsval.JSNumberString(day.NumValue()+1))
		} else {
			parts = append(parts, str(day)+"+1")
		}
	default:
		parts = append(parts, "1")
	}
	for _, u := range []string{"hours", "minutes", "seconds", "milliseconds"} {
		x := d.Get(u)
		if x.IsUndefined() {
			parts = append(parts, "0")
		} else {
			parts = append(parts, str(x))
		}
	}
	return parts
}

func dateTimeToExpr(d Value) string {
	s := strings.Join(dateTimeParts(d, true), ", ")
	if d.Get("utc").IsTruthy() {
		return "utc(" + s + ")"
	}
	return "datetime(" + s + ")"
}

func dateTimeExprToExpr(d Value) string {
	s := strings.Join(dateTimeParts(d, false), ", ")
	if d.Get("utc").IsTruthy() {
		return "utc(" + s + ")"
	}
	return "datetime(" + s + ")"
}

// ---- title.ts ----

type titleConfigParts struct {
	titleMarkConfig, subtitleMarkConfig, nonMarkTitleProperties, subtitle *Object
}

func extractTitleConfig(tc Value) titleConfigParts {
	rest := omit(tc, "anchor", "frame", "offset", "orient", "angle", "limit", "color", "subtitleColor",
		"subtitleFont", "subtitleFontSize", "subtitleFontStyle", "subtitleFontWeight", "subtitleLineHeight", "subtitlePadding")
	titleMark := cloneObj(rest)
	if c := tc.Get("color"); c.IsTruthy() {
		titleMark.Set("fill", c)
	}
	nonMark := jsval.NewObject(6)
	for _, k := range []string{"anchor", "frame", "offset", "orient"} {
		if v := tc.Get(k); v.IsTruthy() {
			nonMark.Set(k, v)
		}
	}
	for _, k := range []string{"angle", "limit"} {
		if v := tc.Get(k); !v.IsUndefined() {
			nonMark.Set(k, v)
		}
	}
	subtitle := jsval.NewObject(4)
	for _, k := range []string{"subtitleColor", "subtitleFont", "subtitleFontSize", "subtitleFontStyle",
		"subtitleFontWeight", "subtitleLineHeight", "subtitlePadding"} {
		if v := tc.Get(k); v.IsTruthy() {
			subtitle.Set(k, v)
		}
	}
	subMark := pick(tc, "align", "baseline", "dx", "dy", "limit")
	return titleConfigParts{titleMark, subMark, nonMark, subtitle}
}

// isText is upstream's isText: a string or an array whose first element is one.
func isText(v Value) bool {
	return v.IsStr() || (v.IsArr() && v.Index(0).IsStr())
}

func joinStrings(ss []string, sep string) string { return strings.Join(ss, sep) }
func upper(s string) string                      { return strings.ToUpper(s) }
