package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Predicates (vega-lite/src/predicate.ts) and logical compositions (logical.ts).

func isSelectionPredicate(p Value) bool { return hasProperty(p, "param") }

// isSelectionPredicateOf is isSelectionPredicate as the version has it: 5.8
// reads predicate?.['param'] for its truthiness, so a param of "" is a field
// predicate there.
func isSelectionPredicateOf(cc *compileCtx, p Value) bool {
	if cc.v5 {
		return isObject(p) && p.Get("param").IsTruthy()
	}
	return isSelectionPredicate(p)
}

func fieldTruthy(p Value) bool { return p.IsObj() && p.Get("field").IsTruthy() }

func isFieldEqualPredicate(p Value) bool { return fieldTruthy(p) && !p.Get("equal").IsUndefined() }
func isFieldLTPredicate(p Value) bool    { return fieldTruthy(p) && !p.Get("lt").IsUndefined() }
func isFieldLTEPredicate(p Value) bool   { return fieldTruthy(p) && !p.Get("lte").IsUndefined() }
func isFieldGTPredicate(p Value) bool    { return fieldTruthy(p) && !p.Get("gt").IsUndefined() }
func isFieldGTEPredicate(p Value) bool   { return fieldTruthy(p) && !p.Get("gte").IsUndefined() }
func isFieldValidPredicate(p Value) bool { return fieldTruthy(p) && !p.Get("valid").IsUndefined() }

func isFieldRangePredicate(cc *compileCtx, p Value) bool {
	if fieldTruthy(p) {
		r := p.Get("range")
		if r.IsArr() && r.Len() == 2 {
			return true
		}
		if isSignalRef(cc, r) {
			return true
		}
	}
	return false
}

func isFieldOneOfPredicate(p Value) bool {
	return fieldTruthy(p) && (p.Get("oneOf").IsArr() || p.Get("in").IsArr())
}

func isFieldPredicate(cc *compileCtx, p Value) bool {
	return isFieldOneOfPredicate(p) || isFieldEqualPredicate(p) || isFieldRangePredicate(cc, p) ||
		isFieldLTPredicate(p) || isFieldGTPredicate(p) || isFieldLTEPredicate(p) || isFieldGTEPredicate(p)
}

func predicateValueExpr(cc *compileCtx, v Value, timeUnit string) string {
	tu := undef
	if timeUnit != "" {
		tu = jsval.Str(timeUnit)
	}
	s, _ := valueExpr(cc, v, tu, "", true, false)
	return s
}

func fieldFilterExpression(cc *compileCtx, p Value, useInRange bool) string {
	nt := normalizeTimeUnit(cc, p.Get("timeUnit"))
	unit := ""
	if u := nt.Get("unit"); u.IsTruthy() {
		unit = u.AsString()
	}
	binned := !cc.v5 && nt.Get("binned").IsTruthy()
	rawFieldExpr := vgField(cc, p, fieldRefOption{expr: "datum"})
	fieldExpr := rawFieldExpr
	if unit != "" {
		// A timeUnit'ed predicate compares the truncated time, computed from the raw
		// field unless the field was pre-binned.
		if !binned {
			fieldExpr = "time(" + timeUnitFieldExprV(cc, unit, p.Get("field"), false) + ")"
		} else {
			fieldExpr = "time(" + rawFieldExpr + ")"
		}
	}
	switch {
	case isFieldEqualPredicate(p):
		return fieldExpr + "===" + predicateValueExpr(cc, p.Get("equal"), unit)
	case isFieldLTPredicate(p):
		return fieldExpr + "<" + predicateValueExpr(cc, p.Get("lt"), unit)
	case isFieldGTPredicate(p):
		return fieldExpr + ">" + predicateValueExpr(cc, p.Get("gt"), unit)
	case isFieldLTEPredicate(p):
		return fieldExpr + "<=" + predicateValueExpr(cc, p.Get("lte"), unit)
	case isFieldGTEPredicate(p):
		return fieldExpr + ">=" + predicateValueExpr(cc, p.Get("gte"), unit)
	case isFieldOneOfPredicate(p):
		// predicateValuesExpr(predicate.oneOf): the legacy `in` makes this a
		// oneOf predicate too, but only oneOf is read, so `in` alone is a
		// TypeError upstream (C49).
		var parts []string
		for _, v := range listOf(p.Get("oneOf"), "vals", "map") {
			cc.work(1)
			parts = append(parts, predicateValueExpr(cc, v, unit))
		}
		return "indexof([" + joinCC(cc, parts, ",") + "], " + fieldExpr + ") !== -1"
	case isFieldValidPredicate(p):
		valid := true
		if v := p.Get("valid"); !v.IsUndefined() {
			valid = v.IsTruthy()
		}
		return fieldValidPredicate(fieldExpr, valid)
	case isFieldRangePredicate(cc, p):
		rp := p
		if !cc.v5 {
			rp = replaceExprRefObj(cc, p)
		}
		rng := rp.Get("range")
		var lower, upper Value
		if isSignalRef(cc, rng) {
			lower = mkv("signal", signalOf(rng)+"[0]")
			upper = mkv("signal", signalOf(rng)+"[1]")
		} else {
			lower, upper = rng.Index(0), rng.Index(1)
		}
		if !lower.IsNull() && !upper.IsNull() && useInRange {
			return "inrange(" + fieldExpr + ", [" + predicateValueExpr(cc, lower, unit) + ", " + predicateValueExpr(cc, upper, unit) + "])"
		}
		var exprs []string
		if !lower.IsNull() {
			exprs = append(exprs, fieldExpr+" >= "+predicateValueExpr(cc, lower, unit))
		}
		if !upper.IsNull() {
			exprs = append(exprs, fieldExpr+" <= "+predicateValueExpr(cc, upper, unit))
		}
		if len(exprs) > 0 {
			return joinCC(cc, exprs, " && ")
		}
		return "true"
	}
	throw("Invalid field predicate: %s", stringify(cc, p))
	return ""
}

func fieldValidPredicate(fieldExpr string, valid bool) string {
	if valid {
		return "isValid(" + fieldExpr + ") && isFinite(+" + fieldExpr + ")"
	}
	return "!isValid(" + fieldExpr + ") || !isFinite(+" + fieldExpr + ")"
}

func normalizePredicate(cc *compileCtx, f Value) Value {
	if isFieldPredicate(cc, f) && f.Get("timeUnit").IsTruthy() {
		o := cloneObj(f.ObjValue())
		if cc.v5 {
			// 5.8 keeps only the unit's name (a UTC unit loses its `utc` flag).
			o.Set("timeUnit", normalizeTimeUnit(cc, f.Get("timeUnit")).Get("unit"))
		} else {
			o.Set("timeUnit", normalizeTimeUnit(cc, f.Get("timeUnit")))
		}
		return jsval.Obj(o)
	}
	return f
}

// replaceExprRefObj is replaceExprRef with level 0 (each top-level property).
func replaceExprRefObj(cc *compileCtx, idx Value) Value { return replaceExprRef(cc, idx, 0) }

// replaceExprRef converts ExprRefs to SignalRefs in the properties of idx,
// recursing level more times through nested objects. It reads idx as
// upstream does, `keys(index || {})` and `index[prop]`: a string's
// characters and an array's elements by index, nothing of a number.
func replaceExprRef(cc *compileCtx, idx Value, level int) Value {
	out := jsval.NewObject(idx.Len())
	read := propReader(idx)
	for _, k := range jsKeysOrCC(cc, idx) {
		cc.work(1)
		v := read.get(k)
		if level == 0 {
			jsSet(cc, out, k, signalRefOrValue(cc, v))
		} else {
			jsSet(cc, out, k, replaceExprRef(cc, v, level-1))
		}
	}
	return jsval.Obj(out)
}

// ---- logical.ts ----

func isLogicalOr(cc *compileCtx, op Value) bool  { return isLogicalOp(cc, op, "or") }
func isLogicalAnd(cc *compileCtx, op Value) bool { return isLogicalOp(cc, op, "and") }
func isLogicalNot(cc *compileCtx, op Value) bool { return isLogicalOp(cc, op, "not") }

// isLogicalOp is hasProperty(op, key) in 6.4; 5.8 tests !!op[key], so a
// falsy operand ({"not": ""}) is a leaf, and a null op fails the read.
func isLogicalOp(cc *compileCtx, op Value, key string) bool {
	if cc.v5 {
		return jsProp(op, key).IsTruthy()
	}
	return hasProperty(op, key)
}

func forEachLeaf(cc *compileCtx, op Value, fn func(Value), depth int) {
	if depth > maxDepth*4 {
		exceeded("logical composition nested too deeply")
	}
	switch {
	case isLogicalNot(cc, op):
		forEachLeaf(cc, op.Get("not"), fn, depth+1)
	case isLogicalAnd(cc, op):
		for _, s := range iterate(op.Get("and"), "op.and") {
			cc.work(1)
			forEachLeaf(cc, s, fn, depth+1)
		}
	case isLogicalOr(cc, op):
		for _, s := range iterate(op.Get("or"), "op.or") {
			cc.work(1)
			forEachLeaf(cc, s, fn, depth+1)
		}
	default:
		fn(op)
	}
}

func normalizeLogicalComposition(cc *compileCtx, op Value, normalizer func(Value) Value, depth int) Value {
	if depth > maxDepth*4 {
		exceeded("logical composition nested too deeply")
	}
	switch {
	case isLogicalNot(cc, op):
		return mkv("not", normalizeLogicalComposition(cc, op.Get("not"), normalizer, depth+1))
	case isLogicalAnd(cc, op):
		return mkv("and", mapList(cc, op.Get("and"), "op.and", func(o Value) Value { return normalizeLogicalComposition(cc, o, normalizer, depth+1) }))
	case isLogicalOr(cc, op):
		return mkv("or", mapList(cc, op.Get("or"), "op.or", func(o Value) Value { return normalizeLogicalComposition(cc, o, normalizer, depth+1) }))
	}
	return normalizer(op)
}

// logicalExpr renders a logical composition through cb for each leaf.
func logicalExpr(cc *compileCtx, op Value, cb func(Value) string, depth int) string {
	if depth > maxDepth*4 {
		exceeded("logical composition nested too deeply")
	}
	switch {
	case isLogicalNot(cc, op):
		return "!(" + logicalExpr(cc, op.Get("not"), cb, depth+1) + ")"
	case isLogicalAnd(cc, op):
		var parts []string
		for _, a := range listOf(op.Get("and"), "op.and", "map") {
			cc.work(1)
			parts = append(parts, logicalExpr(cc, a, cb, depth+1))
		}
		return "(" + joinCC(cc, parts, ") && (") + ")"
	case isLogicalOr(cc, op):
		var parts []string
		for _, a := range listOf(op.Get("or"), "op.or", "map") {
			cc.work(1)
			parts = append(parts, logicalExpr(cc, a, cb, depth+1))
		}
		return "(" + joinCC(cc, parts, ") || (") + ")"
	}
	return cb(op)
}

// mapVals is Array.prototype.map over an array value.
// mapList is expr.map(f) of a value upstream maps without checking it is an
// array (see listOf).
// mapList is a.map(f) of a list the specification gives, a step charged to
// cc per item.
func mapList(cc *compileCtx, a Value, expr string, f func(Value) Value) Value {
	items := listOf(a, expr, "map")
	out := make([]Value, len(items))
	for i, it := range items {
		cc.work(1)
		out[i] = f(it)
	}
	return jsval.Arr(out)
}

// mapVals is mapList of an array, charging cc the same.
func mapVals(cc *compileCtx, a Value, f func(Value) Value) Value {
	items := a.Items()
	out := make([]Value, len(items))
	for i, it := range items {
		cc.work(1)
		out[i] = f(it)
	}
	return jsval.Arr(out)
}
