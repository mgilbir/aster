package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Shared helpers from vega-lite/src/compile/common.ts.

const binRangeDelimiter = " – "

// signalRefOrValue turns an ExprRef {expr} into a SignalRef {signal}.
func signalRefOrValue(v Value) Value {
	if isExprRef(v) {
		rest := omit(v, "expr")
		o := jsval.NewObject(rest.Len() + 1)
		o.Set("signal", v.Get("expr"))
		spread(o, jsval.Obj(rest))
		return jsval.Obj(o)
	}
	return v
}

func signalOrValueRefWithCondition(v Value) Value {
	cond := v.Get("condition")
	var c Value
	if cond.IsArr() {
		items := make([]Value, cond.Len())
		for i, it := range cond.Items() {
			items[i] = signalRefOrValue(it)
		}
		c = jsval.Arr(items)
	} else {
		c = signalRefOrValue(cond)
	}
	o := cloneObj(signalRefOrValue(v).ObjValue())
	o.Set("condition", c)
	return jsval.Obj(o)
}

// signalOrValueRef makes a value ref: {signal} stays, anything else becomes {value}.
func signalOrValueRef(v Value) Value {
	if isExprRef(v) {
		return signalRefOrValue(v)
	}
	if isSignalRef(v) {
		return v
	}
	if !v.IsUndefined() {
		return mkv("value", v)
	}
	return undef
}

func exprFromSignalRefOrValue(ref Value) string {
	if isSignalRef(ref) {
		return signalOf(ref)
	}
	return stringValue(ref)
}

func exprFromValueRefOrSignalRef(ref Value) string {
	if isSignalRef(ref) {
		return signalOf(ref)
	}
	return stringValue(ref.Get("value"))
}

func signalOrStringValue(v Value) Value {
	if isSignalRef(v) {
		return jsval.Str(signalOf(v))
	}
	if v.IsNullish() {
		return jsval.Null
	}
	return jsval.Str(stringValue(v))
}

// getStyles is [].concat(mark.type, mark.style ?? []).
func getStyles(mark Value) []string {
	out := []string{mark.Get("type").AsString()}
	st := mark.Get("style")
	switch {
	case st.IsArr():
		for _, s := range st.Items() {
			out = append(out, s.AsString())
		}
	case !st.IsNullish():
		out = append(out, st.AsString())
	}
	return out
}

func getStyleConfig(p string, styles []string, styleIndex Value) Value {
	v := undef
	for _, s := range styles {
		sc := styleIndex.Get(s)
		if hasProperty(sc, p) {
			v = sc.Get(p)
		}
	}
	return v
}

func getMarkStyleConfig(prop string, mark Value, styleIndex Value) Value {
	return getStyleConfig(prop, getStyles(mark), styleIndex)
}

// getMarkConfig looks a property up in style configs, then the mark-type
// config, then the general mark config. With vgChannel the Vega channel name is
// tried first at each level.
func getMarkConfig(channel string, mark Value, config Value, vgChannel string) Value {
	cfg := getMarkStyleConfig(channel, mark, config.Get("style"))
	mt := config.Get(mark.Get("type").AsString())
	if vgChannel != "" {
		return firstDefined(cfg, cfg, mt.Get(vgChannel), mt.Get(channel), config.Get("mark").Get(vgChannel))
	}
	return firstDefined(undef, cfg, undef, mt.Get(channel), config.Get("mark").Get(channel))
}

// getMarkPropOrConfig reads the mark property, falling back to config.
func getMarkPropOrConfig(channel string, mark Value, config Value, vgChannel string, ignoreVgConfig bool) Value {
	if vgChannel != "" && hasProperty(mark, vgChannel) {
		return mark.Get(vgChannel)
	} else if v := mark.Get(channel); !v.IsUndefined() {
		return v
	} else if ignoreVgConfig && (vgChannel == "" || vgChannel == channel) {
		return undef
	}
	return getMarkConfig(channel, mark, config, vgChannel)
}

func getMarkPropOrConfigSimple(channel string, mark, config Value) Value {
	return getMarkPropOrConfig(channel, mark, config, "", false)
}

// sortParams builds the field/order arrays of a Vega compare.
func sortParams(cc *compileCtx, orderDef Value, opt fieldRefOption) (fields, orders []Value) {
	defs := arrayOf(orderDef)
	for _, d := range defs {
		fields = append(fields, jsval.Str(vgField(cc, d, opt)))
		orders = append(orders, coalesce(d.Get("sort"), jsval.Str("ascending")))
	}
	return
}

// arrayOf is vega-util's array(): undefined is [], an array is itself,
// anything else is [x].
func arrayOf(v Value) []Value {
	if v.IsNullish() {
		return nil
	}
	if v.IsArr() {
		return v.Items()
	}
	return []Value{v}
}

func mergeTitleFieldDefs(f1, f2 []Value) []Value {
	merged := append([]Value(nil), f1...)
outer:
	for _, fd := range f2 {
		for _, m := range merged {
			if deepEqual(m, fd) {
				continue outer
			}
		}
		merged = append(merged, fd)
	}
	return merged
}

func mergeTitle(t1, t2 Value) Value {
	if deepEqual(t1, t2) || !t2.IsTruthy() {
		return t1
	} else if !t1.IsTruthy() {
		return t2
	}
	var parts []string
	for _, x := range append(append([]Value(nil), arrayOf(t1)...), arrayOf(t2)...) {
		parts = append(parts, x.AsString())
	}
	return jsval.Str(strings.Join(parts, ", "))
}

// mergeTitleComponent merges two title components (text or field-def lists).
func mergeTitleComponent(v1, v2 withExplicit) withExplicit {
	a, b := v1.value, v2.value
	aText := isText(a) || isSignalRef(a)
	bText := isText(b) || isSignalRef(b)
	switch {
	case a.IsNullish() || b.IsNull():
		return withExplicit{v1.explicit, jsval.Null}
	case aText && bText:
		return withExplicit{v1.explicit, mergeTitle(a, b)}
	case aText:
		return withExplicit{v1.explicit, a}
	case bText:
		return withExplicit{v1.explicit, b}
	}
	return withExplicit{v1.explicit, jsval.Arr(mergeTitleFieldDefs(a.Items(), b.Items()))}
}

// inheritedObjectKey reports a key that reads as present on any plain object
// through its prototype: `out[key]` of an empty `{}` is truthy for them.
func inheritedObjectKey(key string) bool {
	switch key {
	case "constructor", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toString", "valueOf",
		"toLocaleString", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__", "__proto__":
		return true
	}
	return false
}
