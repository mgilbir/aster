package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Predicates (vega-lite/src/predicate.ts) and logical compositions (logical.ts).

func isSelectionPredicate(p Value) bool { return hasProperty(p, "param") }

func fieldTruthy(p Value) bool { return p.IsObj() && p.Get("field").IsTruthy() }

func isFieldEqualPredicate(p Value) bool { return fieldTruthy(p) && !p.Get("equal").IsUndefined() }
func isFieldLTPredicate(p Value) bool    { return fieldTruthy(p) && !p.Get("lt").IsUndefined() }
func isFieldLTEPredicate(p Value) bool   { return fieldTruthy(p) && !p.Get("lte").IsUndefined() }
func isFieldGTPredicate(p Value) bool    { return fieldTruthy(p) && !p.Get("gt").IsUndefined() }
func isFieldGTEPredicate(p Value) bool   { return fieldTruthy(p) && !p.Get("gte").IsUndefined() }
func isFieldValidPredicate(p Value) bool { return fieldTruthy(p) && !p.Get("valid").IsUndefined() }

func isFieldRangePredicate(p Value) bool {
	if fieldTruthy(p) {
		r := p.Get("range")
		if r.IsArr() && r.Len() == 2 {
			return true
		}
		if isSignalRef(r) {
			return true
		}
	}
	return false
}

func isFieldOneOfPredicate(p Value) bool {
	return fieldTruthy(p) && (p.Get("oneOf").IsArr() || p.Get("in").IsArr())
}

func isFieldPredicate(p Value) bool {
	return isFieldOneOfPredicate(p) || isFieldEqualPredicate(p) || isFieldRangePredicate(p) ||
		isFieldLTPredicate(p) || isFieldGTPredicate(p) || isFieldLTEPredicate(p) || isFieldGTEPredicate(p)
}

func predicateValueExpr(v Value, timeUnit string) string {
	tu := undef
	if timeUnit != "" {
		tu = jsval.Str(timeUnit)
	}
	s, _ := valueExpr(v, tu, "", true, false)
	return s
}

func fieldFilterExpression(p Value, useInRange bool) string {
	field := p.Get("field").AsString()
	nt := normalizeTimeUnit(p.Get("timeUnit"))
	unit := ""
	if u := nt.Get("unit"); u.IsTruthy() {
		unit = u.AsString()
	}
	binned := nt.Get("binned").IsTruthy()
	rawFieldExpr := vgField(p, fieldRefOption{expr: "datum"})
	fieldExpr := rawFieldExpr
	if unit != "" {
		// A timeUnit'ed predicate compares the truncated time, computed from the raw
		// field unless the field was pre-binned.
		if !binned {
			fieldExpr = "time(" + timeUnitFieldExpr(unit, field, false) + ")"
		} else {
			fieldExpr = "time(" + rawFieldExpr + ")"
		}
	}
	switch {
	case isFieldEqualPredicate(p):
		return fieldExpr + "===" + predicateValueExpr(p.Get("equal"), unit)
	case isFieldLTPredicate(p):
		return fieldExpr + "<" + predicateValueExpr(p.Get("lt"), unit)
	case isFieldGTPredicate(p):
		return fieldExpr + ">" + predicateValueExpr(p.Get("gt"), unit)
	case isFieldLTEPredicate(p):
		return fieldExpr + "<=" + predicateValueExpr(p.Get("lte"), unit)
	case isFieldGTEPredicate(p):
		return fieldExpr + ">=" + predicateValueExpr(p.Get("gte"), unit)
	case isFieldOneOfPredicate(p):
		var parts []string
		for _, v := range p.Get("oneOf").Items() {
			parts = append(parts, predicateValueExpr(v, unit))
		}
		return "indexof([" + strings.Join(parts, ",") + "], " + fieldExpr + ") !== -1"
	case isFieldValidPredicate(p):
		valid := true
		if v := p.Get("valid"); !v.IsUndefined() {
			valid = v.IsTruthy()
		}
		return fieldValidPredicate(fieldExpr, valid)
	case isFieldRangePredicate(p):
		rp := replaceExprRefObj(p)
		rng := rp.Get("range")
		var lower, upper Value
		if isSignalRef(rng) {
			lower = mkv("signal", signalOf(rng)+"[0]")
			upper = mkv("signal", signalOf(rng)+"[1]")
		} else {
			lower, upper = rng.Index(0), rng.Index(1)
		}
		if !lower.IsNull() && !upper.IsNull() && useInRange {
			return "inrange(" + fieldExpr + ", [" + predicateValueExpr(lower, unit) + ", " + predicateValueExpr(upper, unit) + "])"
		}
		var exprs []string
		if !lower.IsNull() {
			exprs = append(exprs, fieldExpr+" >= "+predicateValueExpr(lower, unit))
		}
		if !upper.IsNull() {
			exprs = append(exprs, fieldExpr+" <= "+predicateValueExpr(upper, unit))
		}
		if len(exprs) > 0 {
			return strings.Join(exprs, " && ")
		}
		return "true"
	}
	throw("Invalid field predicate: %s", stringify(p))
	return ""
}

func fieldValidPredicate(fieldExpr string, valid bool) string {
	if valid {
		return "isValid(" + fieldExpr + ") && isFinite(+" + fieldExpr + ")"
	}
	return "!isValid(" + fieldExpr + ") || !isFinite(+" + fieldExpr + ")"
}

func normalizePredicate(f Value) Value {
	if isFieldPredicate(f) && f.Get("timeUnit").IsTruthy() {
		o := cloneObj(f.ObjValue())
		o.Set("timeUnit", normalizeTimeUnit(f.Get("timeUnit")))
		return jsval.Obj(o)
	}
	return f
}

// replaceExprRefObj is replaceExprRef with level 0 (each top-level property).
func replaceExprRefObj(idx Value) Value { return replaceExprRef(idx, 0) }

// replaceExprRef converts ExprRefs to SignalRefs in the properties of idx,
// recursing level more times through nested objects.
func replaceExprRef(idx Value, level int) Value {
	out := jsval.NewObject(idx.Len())
	if idx.IsObj() {
		o := idx.ObjValue()
		for i := 0; i < o.Len(); i++ {
			v := o.ValueAt(i)
			if level == 0 {
				out.Set(o.KeyAt(i), signalRefOrValue(v))
			} else {
				out.Set(o.KeyAt(i), replaceExprRef(v, level-1))
			}
		}
	} else if idx.IsStr() {
		// Object.keys of a string are its character indices.
		for i, u := range utf16Len(idx.StrValue()) {
			out.Set(jsval.JSNumberString(float64(i)), jsval.Str(string(rune(u))))
		}
	} else if idx.IsArr() {
		for i, v := range idx.Items() {
			k := jsval.JSNumberString(float64(i))
			if level == 0 {
				out.Set(k, signalRefOrValue(v))
			} else {
				out.Set(k, replaceExprRef(v, level-1))
			}
		}
	}
	return jsval.Obj(out)
}

// ---- logical.ts ----

func isLogicalOr(op Value) bool  { return hasProperty(op, "or") }
func isLogicalAnd(op Value) bool { return hasProperty(op, "and") }
func isLogicalNot(op Value) bool { return hasProperty(op, "not") }

func forEachLeaf(op Value, fn func(Value), depth int) {
	if depth > maxDepth*4 {
		throw("logical composition nested too deeply")
	}
	switch {
	case isLogicalNot(op):
		forEachLeaf(op.Get("not"), fn, depth+1)
	case isLogicalAnd(op):
		for _, s := range op.Get("and").Items() {
			forEachLeaf(s, fn, depth+1)
		}
	case isLogicalOr(op):
		for _, s := range op.Get("or").Items() {
			forEachLeaf(s, fn, depth+1)
		}
	default:
		fn(op)
	}
}

func normalizeLogicalComposition(op Value, normalizer func(Value) Value, depth int) Value {
	if depth > maxDepth*4 {
		throw("logical composition nested too deeply")
	}
	switch {
	case isLogicalNot(op):
		return mkv("not", normalizeLogicalComposition(op.Get("not"), normalizer, depth+1))
	case isLogicalAnd(op):
		return mkv("and", mapVals(op.Get("and"), func(o Value) Value { return normalizeLogicalComposition(o, normalizer, depth+1) }))
	case isLogicalOr(op):
		return mkv("or", mapVals(op.Get("or"), func(o Value) Value { return normalizeLogicalComposition(o, normalizer, depth+1) }))
	}
	return normalizer(op)
}

// logicalExpr renders a logical composition through cb for each leaf.
func logicalExpr(op Value, cb func(Value) string, depth int) string {
	if depth > maxDepth*4 {
		throw("logical composition nested too deeply")
	}
	switch {
	case isLogicalNot(op):
		return "!(" + logicalExpr(op.Get("not"), cb, depth+1) + ")"
	case isLogicalAnd(op):
		var parts []string
		for _, a := range op.Get("and").Items() {
			parts = append(parts, logicalExpr(a, cb, depth+1))
		}
		return "(" + strings.Join(parts, ") && (") + ")"
	case isLogicalOr(op):
		var parts []string
		for _, a := range op.Get("or").Items() {
			parts = append(parts, logicalExpr(a, cb, depth+1))
		}
		return "(" + strings.Join(parts, ") || (") + ")"
	}
	return cb(op)
}

// mapVals is Array.prototype.map over an array value.
func mapVals(a Value, f func(Value) Value) Value {
	items := a.Items()
	out := make([]Value, len(items))
	for i, it := range items {
		out[i] = f(it)
	}
	return jsval.Arr(out)
}
