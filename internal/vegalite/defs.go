package vegalite

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// ---- type.ts ----

func isContinuousType(t string) bool { return t == "quantitative" || t == "temporal" }
func isDiscreteType(t string) bool   { return t == "ordinal" || t == "nominal" }

// getFullName expands 'q'/'t'/'o'/'n' and lower-cases (toLowerCase, so
// "quantİtative" is no type, as upstream finds none); "" is undefined.
func getFullName(t string) string {
	switch jsval.JSLower(t) {
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

func isArgminDef(cc *compileCtx, v Value) bool { return isArgDef(cc, v, "argmin") }
func isArgmaxDef(cc *compileCtx, v Value) bool { return isArgDef(cc, v, "argmax") }

// isArgDef is hasProperty(a, key) in 6.4; 5.8 tests !!a && !!a[key], so an
// argmax of 0, false or null is no argmax (and an invalid aggregate), and
// one Object.prototype holds (see argDefOf) makes every aggregate one.
func isArgDef(cc *compileCtx, v Value, key string) bool {
	if cc.v5 {
		return v.IsTruthy() && argDefOf(cc, v, key).IsTruthy()
	}
	return hasProperty(v, key)
}

// argDefOf is a[key] of an argmin or argmax def. In 5.8 an aggregate over
// a field named "__proto__" writes its op on Object.prototype
// (meas['__proto__'][op] = new Set(...), see jsRealm), and from then on
// every aggregate that does not hold the key itself, a string op included,
// reads that Set, whose text is "[object Set]".
func argDefOf(cc *compileCtx, a Value, key string) Value {
	if !cc.v5 || (a.IsObj() && a.ObjValue().Has(key)) {
		return a.Get(key)
	}
	x, ok := cc.jsr.protoProp(key)
	if !ok {
		return a.Get(key)
	}
	switch x := x.(type) {
	case Value:
		return x
	case *sset:
		o := jsval.NewObject(0)
		o.SetStringer(func(*Object) string { return "[object Set]" })
		return jsval.Obj(o)
	}
	return jsval.Obj(jsval.NewObject(0))
}
func isAggregateOp(cc *compileCtx, v Value) bool {
	if cc.v5 && v.IsStr() && (v.StrValue() == "exponential" || v.StrValue() == "exponentialb") {
		return false // 6.x only
	}
	if cc.v5 && v.IsStr() {
		// !!AGGREGATE_OP_INDEX[a]: the members an object inherits count.
		if isInheritedKey(v.StrValue()) {
			return true
		}
	}
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

// isParameterExtent is hasProperty(extent, 'param') in 6.4; 5.8 reads
// extent?.['param'], so a falsy param is no parameter extent.
func isParameterExtent(cc *compileCtx, v Value) bool {
	if cc.v5 {
		return !v.IsNullish() && jsProp(v, "param").IsTruthy()
	}
	return hasProperty(v, "param")
}

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
func binToString(cc *compileCtx, bin Value) string {
	if bin.IsBool() {
		bin = normalizeBin(bin, "")
	}
	b := newSbuf(cc)
	b.WriteString("bin")
	// keys(bin) and bin[p]: a string's characters and an array's elements
	// by index.
	read := propReader(bin)
	for _, p := range jsKeysOrCC(cc, bin) {
		cc.work(1)
		x := read.get(p)
		if isParameterExtent(cc, x) {
			// `${entries(x)}` stringifies [[k, v], ...] with commas.
			var parts []string
			for _, k := range keysOf(x) {
				parts = append(parts, k, joinItem(x.Get(k))) // [k, v].join(","): null and undefined as ""
			}
			b.WriteString(varName("_" + p + "_" + joinCC(cc, parts, ",")))
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
	case !jsProp(bin, "maxbins").IsTruthy() && !jsProp(bin, "step").IsTruthy():
		// {...bin, maxbins}: a string's or an array's indices too.
		return jsval.Obj(spread(jsval.NewObject(bin.Len()+1), bin, mkv("maxbins", autoMaxBins(channel))))
	}
	return bin
}

// ---- sort.ts ----

const defaultSortOp = "min"

var sortByChannel = strSet([]string{
	chX, chY, chColor, chFill, chStroke, chStrokeWidth, chSize, chShape,
	chFillOpacity, chStrokeOpacity, chOpacity, chText,
})

// isSortByEncoding is hasProperty(sort, 'encoding') in 6.4; 5.8 tests
// !!sort?.['encoding'].
func isSortByEncoding(cc *compileCtx, v Value) bool {
	if cc.v5 {
		return !v.IsNullish() && jsProp(v, "encoding").IsTruthy()
	}
	return hasProperty(v, "encoding")
}
func isSortField(cc *compileCtx, v Value) bool {
	if cc.v5 {
		return v.IsTruthy() && (v.Get("op").IsStr() && v.Get("op").StrValue() == "count" || v.Get("field").IsTruthy())
	}
	return v.IsObj() && (v.Get("op").IsStr() && v.Get("op").StrValue() == "count" || hasProperty(v, "field"))
}
func isSortArray(v Value) bool { return v.IsArr() }

// ---- timeunit.ts ----

var timeUnitParts = []string{
	"year", "quarter", "month", "week", "day", "dayofyear", "date", "hours", "minutes", "seconds", "milliseconds",
}

// isLocalSingleTimeUnit: 5.8 tests !!LOCAL_SINGLE_TIMEUNIT_INDEX[unit], so a
// name every object inherits passes.
func isLocalSingleTimeUnit(cc *compileCtx, u string) bool {
	return contains(timeUnitParts, u) || (cc != nil && cc.v5 && isInheritedKey(u))
}

func isBinnedTimeUnit(cc *compileCtx, tu Value) bool {
	if cc.v5 {
		return false // binned time units are a 6.x feature
	}
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
func normalizeTimeUnit(cc *compileCtx, tu Value) Value {
	if !tu.IsTruthy() {
		return undef
	}
	var params *Object
	switch {
	case tu.IsStr():
		s := tu.StrValue()
		if !cc.v5 && strings.HasPrefix(s, "binned") {
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
		// params is undefined: params.unit is a TypeError.
		throw("Cannot read properties of undefined (reading 'unit')")
	}
	// isUTCTimeUnit(params.unit) calls unit.startsWith.
	switch u := params.Lookup("unit"); {
	case u.IsNullish():
		throw("Cannot read properties of %s (reading 'startsWith')", nullishName(u))
	case !u.IsStr():
		throw("t.startsWith is not a function")
	}
	if u := params.Lookup("unit"); u.IsStr() && isUTCTimeUnit(u.StrValue()) {
		params.Set("utc", jsval.True)
		params.Set("unit", jsval.Str(u.StrValue()[3:]))
	}
	return jsval.Obj(params)
}

// timeUnitToString is upstream's timeUnitToString (e.g. "yearmonth",
// "utcyearmonth", "timeunit_maxbins_10").
func timeUnitToString(cc *compileCtx, tu Value) string {
	n := normalizeTimeUnit(cc, tu)
	utc := n.Get("utc").IsTruthy()
	rest := omit(n, "utc")
	pfx := ""
	if utc {
		pfx = "utc"
	}
	b := newSbuf(cc)
	if rest.Lookup("unit").IsTruthy() {
		b.WriteString(pfx)
		for _, p := range rest.Keys() {
			cc.work(1)
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
		cc.work(1)
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
	return "timeUnitSpecifier(" + stringify(nil, strsVal(parts)) + ", " + stringify(nil, jsval.Obj(vegaliteTimeFormat)) + ")"
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

// timeUnitFieldExprV is upstream's fieldExpr of a field as it holds it.
func timeUnitFieldExprV(cc *compileCtx, full string, field Value, end bool) string {
	fieldRef := accessPathWithDatumV(cc, field, "datum")
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

// getDateTimePartAndStep is upstream's: the date part a unit steps and by
// how much. It answers undefined (ok false) for a unit it does not know,
// which its callers destructure.
func getDateTimePartAndStep(unit string, step float64) (part string, st float64, ok bool) {
	switch unit {
	case "year", "month", "date", "hours", "minutes", "seconds", "milliseconds":
		return unit, step, true
	case "day", "dayofyear":
		return "date", step, true
	case "quarter":
		return "month", step * 3, true
	case "week":
		return "date", step * 7, true
	}
	return "", step, false
}

// durationExpr is upstream's durationExpr with the identity wrapper unless
// wrap is given.
func durationExpr(cc *compileCtx, tu Value, wrap func(string) string) string {
	n := normalizeTimeUnit(cc, tu)
	smallest := getSmallestTimeUnitPart(n.Get("unit").AsString())
	if smallest != "" && smallest != "day" {
		start := mk("year", 2001, "month", 1, "date", 1, "hours", 0, "minutes", 0, "seconds", 0, "milliseconds", 0)
		// The step as upstream holds it: `+startDate[part] + step` is
		// JavaScript's +, which concatenates a step that is a string or an
		// array; quarters and weeks multiply it first.
		stepV := n.Get("step")
		end := cloneObj(start)
		if cc.v5 {
			// 5.8: step || 1, and it bumps the smallest part itself (a
			// quarter is three months); parts a datetime does not have,
			// week and dayofyear, add nothing.
			if !stepV.IsTruthy() {
				stepV = jsval.Int(1)
			}
			part := smallest
			if smallest == "quarter" {
				end.Set("month", jsval.Num(1+float64(jsval.ToNumber(stepV)*3)))
			} else if start.Has(part) {
				end.Set(part, jsNumPlus(start.Lookup(part).NumValue(), stepV))
			}
		} else {
			if stepV.IsUndefined() { // getDateTimePartAndStep(unit, step = 1)
				stepV = jsval.Int(1)
			}
			part, _, ok := getDateTimePartAndStep(smallest, 1)
			if !ok {
				throw("Cannot destructure property 'step' of 'getDateTimePartAndStep(...)' as it is undefined.")
			}
			switch smallest {
			case "quarter":
				stepV = jsval.Num(jsval.ToNumber(stepV) * 3)
			case "week":
				stepV = jsval.Num(jsval.ToNumber(stepV) * 7)
			}
			end.Set(part, jsNumPlus(start.Lookup(part).NumValue(), stepV))
		}
		if wrap == nil {
			wrap = func(s string) string { return s }
		}
		return wrap(dateTimeToExpr(cc, jsval.Obj(end))) + " - " + wrap(dateTimeToExpr(cc, jsval.Obj(start)))
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
	if isNumericV(v) { // if (isNumeric(m)) m = +m
		v = jsval.Num(jsval.ToNumber(v))
	}
	if v.IsNum() {
		if what == "month" {
			return v.NumValue() - 1
		}
		return v.NumValue()
	}
	if v.IsNullish() {
		throw("Cannot read properties of %s (reading 'toLowerCase')", nullishName(v))
	}
	if !v.IsStr() {
		// m.toLowerCase() (d for a day) of a value that is not a string.
		throw("%s.toLowerCase is not a function", what[:1])
	}
	lower := jsval.JSLower(v.StrValue())
	for i, n := range names {
		if n == lower {
			return float64(i)
		}
	}
	short := jsval.UTF16Prefix(lower, 3) // lower.substr(0, 3)
	for i, n := range names {
		if n[:3] == short {
			return float64(i)
		}
	}
	throw("%s", msgInvalidTimeUnit(nil, what, v))
	return 0
}

// dateTimeParts is upstream's dateTimeParts: the arguments of datetime(...)
// as text. With normalize=true month/quarter/day names and 1-based numbers are
// converted to the 0-based values datetime() expects.
// dateTimeParts are the arguments upstream's dateTimeParts collects, as the
// values it pushes: the ones the datetime gives, as given, and the numbers
// and expression texts it makes. A timestamp converts each with ToNumber
// (Date.UTC(...parts)); an expression joins them (dateTimePartsText).
func dateTimeParts(cc *compileCtx, d Value, normalize bool) []Value {
	var parts []Value
	if normalize && !d.Get("day").IsUndefined() {
		if d.Len() > 1 {
			cc.warn(msgDroppedDay(cc, d))
			d = jsval.Obj(omit(d, "day"))
		}
	}
	if y := d.Get("year"); !y.IsUndefined() {
		parts = append(parts, y)
	} else {
		parts = append(parts, jsval.Int(2012))
	}
	switch {
	case !d.Get("month").IsUndefined():
		m := d.Get("month")
		if normalize {
			parts = append(parts, jsval.Num(normalizeNamed(m, monthNames, "month")))
		} else {
			parts = append(parts, m)
		}
	case !d.Get("quarter").IsUndefined():
		q := d.Get("quarter")
		if normalize {
			if isNumericV(q) {
				q = jsval.Num(jsval.ToNumber(q))
			}
			if !q.IsNum() {
				throw("%s", msgInvalidTimeUnit(cc, "quarter", q))
			}
			if q.NumValue() > 4 {
				cc.warn(msgInvalidTimeUnit(cc, "quarter", q))
			}
			parts = append(parts, jsval.Num((q.NumValue()-1)*3))
		} else if q.IsNum() {
			parts = append(parts, jsval.Num(q.NumValue()*3))
		} else {
			parts = append(parts, jsval.Str(tmpl(q)+"*3"))
		}
	default:
		parts = append(parts, jsval.Int(0))
	}
	switch {
	case !d.Get("date").IsUndefined():
		parts = append(parts, d.Get("date"))
	case !d.Get("day").IsUndefined():
		day := d.Get("day")
		if normalize {
			if isNumericV(day) {
				day = jsval.Num(jsval.ToNumber(day))
			}
			var f float64
			if day.IsNum() {
				f = math.Mod(day.NumValue(), 7)
			} else {
				f = normalizeNamed(day, dayNames, "day")
			}
			parts = append(parts, jsval.Num(f+1))
		} else if day.IsNum() {
			parts = append(parts, jsval.Num(day.NumValue()+1))
		} else {
			parts = append(parts, jsval.Str(tmpl(day)+"+1"))
		}
	default:
		parts = append(parts, jsval.Int(1))
	}
	for _, u := range []string{"hours", "minutes", "seconds", "milliseconds"} {
		x := d.Get(u)
		if x.IsUndefined() {
			parts = append(parts, jsval.Int(0))
		} else {
			parts = append(parts, x)
		}
	}
	return parts
}

// dateTimePartsText is parts.join(', '), which writes null and undefined as
// nothing (a null part makes "datetime(2000, 0, , ...)", which upstream's
// expression parser then rejects).
func dateTimePartsText(parts []Value) string {
	ss := make([]string, len(parts))
	for i, p := range parts {
		ss[i] = joinItem(p)
	}
	return strings.Join(ss, ", ")
}

func dateTimeToExpr(cc *compileCtx, d Value) string {
	s := dateTimePartsText(dateTimeParts(cc, d, true))
	if d.Get("utc").IsTruthy() {
		return "utc(" + s + ")"
	}
	return "datetime(" + s + ")"
}

func dateTimeExprToExpr(d Value) string {
	s := dateTimePartsText(dateTimeParts(nil, d, false))
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
	if tc.IsNullish() { // const {anchor, …} = titleConfig
		throw("Cannot destructure property 'anchor' of 'titleConfig' as it is %s.", nullishName(tc))
	}
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
func upper(s string) string                      { return jsval.JSUpper(s) }
