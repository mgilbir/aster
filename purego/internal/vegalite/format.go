package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Label and tooltip formatting expressions — vega-lite/src/compile/format.ts.

func customFormatExpr(formatType, field string, format Value) string {
	f := ""
	if format.IsTruthy() {
		f = ", " + stringify(format)
	}
	return formatType + "(" + field + f + ")"
}

type formatSignalOpts struct {
	fieldOrDatumDef Value
	format          Value
	formatType      Value
	expr            string
	normalizeStack  bool
	config          Value
}

func formatSignalRef(o formatSignalOpts) Value {
	fod, format, formatType, expr, config := o.fieldOrDatumDef, o.format, o.formatType, o.expr, o.config
	if isCustomFormatType(formatType) {
		return formatCustomType(fod, format, formatType, expr, o.normalizeStack, config, "")
	}
	field := fieldToFormat(fod, expr, o.normalizeStack)
	typ := channelDefType(fod)
	if format.IsUndefined() && formatType.IsUndefined() && config.Get("customFormatTypes").IsTruthy() {
		if typ == "quantitative" {
			if o.normalizeStack && config.Get("normalizedNumberFormatType").IsTruthy() {
				return formatCustomType(fod, config.Get("normalizedNumberFormat"), config.Get("normalizedNumberFormatType"), expr, false, config, "")
			}
			if config.Get("numberFormatType").IsTruthy() {
				return formatCustomType(fod, config.Get("numberFormat"), config.Get("numberFormatType"), expr, false, config, "")
			}
		}
		if typ == "temporal" && config.Get("timeFormatType").IsTruthy() && isFieldDef(fod) && fod.Get("timeUnit").IsUndefined() {
			return formatCustomType(fod, config.Get("timeFormat"), config.Get("timeFormatType"), expr, false, config, "")
		}
	}
	if isFieldOrDatumDefForTimeFormat(fod) {
		unit, utc := "", false
		if isFieldDef(fod) {
			tu := normalizeTimeUnit(fod.Get("timeUnit"))
			if tu.Get("unit").IsTruthy() {
				unit = tu.Get("unit").AsString()
			}
			utc = tu.Get("utc").IsTruthy()
		}
		isUTCScale := utc || (isScaleFieldDef(fod) && fod.Get("scale").Get("type").AsString() == "utc")
		s := timeFormatExpression(field, unit, format, config.Get("timeFormatType"), config.Get("timeFormat"), isUTCScale)
		if s != "" {
			return sig(s)
		}
		return undef
	}
	format = numberFormat(typ, format, config, o.normalizeStack)
	if isFieldDef(fod) && isBinning(fod.Get("bin")) {
		endField := vgField(fod, fieldRefOption{expr: expr, binSuffix: "end"})
		return sig(binFormatExpression(field, endField, format, formatType, config))
	} else if format.IsTruthy() || channelDefType(fod) == "quantitative" {
		return sig(formatExpr(field, format))
	}
	return sig("isValid(" + field + ") ? " + field + ` : ""+` + field)
}

func fieldToFormat(fod Value, expr string, normalizeStack bool) string {
	if isFieldDef(fod) {
		if normalizeStack {
			return vgField(fod, fieldRefOption{expr: expr, suffix: "end"}) + "-" + vgField(fod, fieldRefOption{expr: expr, suffix: "start"})
		}
		return vgField(fod, fieldRefOption{expr: expr})
	}
	return datumDefToExpr(fod)
}

func formatCustomType(fod, format, formatType Value, expr string, normalizeStack bool, config Value, field string) Value {
	if field == "" {
		field = fieldToFormat(fod, expr, normalizeStack)
	}
	if field != "datum.value" && isFieldDef(fod) && isBinning(fod.Get("bin")) {
		endField := vgField(fod, fieldRefOption{expr: expr, binSuffix: "end"})
		return sig(binFormatExpression(field, endField, format, formatType, config))
	}
	return sig(customFormatExpr(formatType.AsString(), field, format))
}

// guideFormat returns the format of an axis/legend; undefined when a custom
// format type takes over.
func guideFormat(fod Value, typ string, format, formatType Value, config Value, omitTimeFormatConfig bool) Value {
	if formatType.IsStr() && isCustomFormatType(formatType) {
		return undef
	} else if format.IsUndefined() && formatType.IsUndefined() && config.Get("customFormatTypes").IsTruthy() {
		if channelDefType(fod) == "quantitative" {
			if config.Get("normalizedNumberFormatType").IsTruthy() && isPositionFieldOrDatumDef(fod) && fod.Get("stack").AsString() == "normalize" && fod.Get("stack").IsStr() {
				return undef
			}
			if config.Get("numberFormatType").IsTruthy() {
				return undef
			}
		}
	}
	if isPositionFieldOrDatumDef(fod) && fod.Get("stack").IsStr() && fod.Get("stack").StrValue() == "normalize" && config.Get("normalizedNumberFormat").IsTruthy() {
		return numberFormat("quantitative", undef, config, true)
	}
	if isFieldOrDatumDefForTimeFormat(fod) {
		tu := ""
		if isFieldDef(fod) {
			if u := normalizeTimeUnit(fod.Get("timeUnit")).Get("unit"); u.IsTruthy() {
				tu = u.AsString()
			}
		}
		if tu == "" && config.Get("customFormatTypes").IsTruthy() && config.Get("timeFormatType").IsTruthy() {
			return undef
		}
		return timeFormat(format, tu, config, omitTimeFormatConfig)
	}
	return numberFormat(typ, format, config, false)
}

func guideFormatType(formatType Value, fod Value, scaleType string) Value {
	if formatType.IsTruthy() && (isSignalRef(formatType) || (formatType.IsStr() && (formatType.StrValue() == "number" || formatType.StrValue() == "time"))) {
		return formatType
	}
	if isFieldOrDatumDefForTimeFormat(fod) && scaleType != "time" && scaleType != "utc" {
		if isFieldDef(fod) && normalizeTimeUnit(fod.Get("timeUnit")).Get("utc").IsTruthy() {
			return jsval.Str("utc")
		}
		return jsval.Str("time")
	}
	return undef
}

func numberFormat(typ string, specified Value, config Value, normalizeStack bool) Value {
	if specified.IsStr() {
		return specified
	}
	if typ == "quantitative" {
		if normalizeStack {
			return config.Get("normalizedNumberFormat")
		}
		return config.Get("numberFormat")
	}
	return undef
}

func timeFormat(specified Value, timeUnit string, config Value, omitTimeFormatConfig bool) Value {
	if specified.IsTruthy() {
		return specified
	}
	if timeUnit != "" {
		return sig(timeUnitSpecifierExpression(timeUnit))
	}
	if omitTimeFormatConfig {
		return undef
	}
	return config.Get("timeFormat")
}

func formatExpr(field string, format Value) string {
	f := ""
	if format.IsTruthy() {
		f = format.AsString()
	}
	return "format(" + field + ", \"" + f + "\")"
}

func binNumberFormatExpr(field string, format, formatType Value, config Value) string {
	if isCustomFormatType(formatType) {
		return customFormatExpr(formatType.AsString(), field, format)
	}
	f := undef
	if format.IsStr() {
		f = format
	}
	return formatExpr(field, coalesce(f, config.Get("numberFormat")))
}

func binFormatExpression(startField, endField string, format, formatType Value, config Value) string {
	if format.IsUndefined() && formatType.IsUndefined() && config.Get("customFormatTypes").IsTruthy() && config.Get("numberFormatType").IsTruthy() {
		return binFormatExpression(startField, endField, config.Get("numberFormat"), config.Get("numberFormatType"), config)
	}
	start := binNumberFormatExpr(startField, format, formatType, config)
	end := binNumberFormatExpr(endField, format, formatType, config)
	return fieldValidPredicate(startField, false) + ` ? "null" : ` + start + ` + "` + binRangeDelimiter + `" + ` + end
}

func timeFormatExpression(field, timeUnit string, format, formatType, rawTimeFormat Value, isUTCScale bool) string {
	if timeUnit == "" || format.IsTruthy() {
		if timeUnit == "" && formatType.IsTruthy() {
			return formatType.AsString() + "(" + field + ", " + stringifyJS(format) + ")"
		}
		if !format.IsStr() {
			format = rawTimeFormat
		}
		fn := "time"
		if isUTCScale {
			fn = "utc"
		}
		return fn + "Format(" + field + ", " + stringifyJS(format) + ")"
	}
	return formatExpression(timeUnit, field, isUTCScale)
}
