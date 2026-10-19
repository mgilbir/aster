package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Shared helpers from vega-lite/src/compile/common.ts.

const binRangeDelimiter = " – "

// signalRefOrValue turns an ExprRef {expr} into a SignalRef {signal}.
func signalRefOrValue(cc *compileCtx, v Value) Value {
	if isExprRef(cc, v) {
		rest := omit(v, "expr")
		o := jsval.NewObject(rest.Len() + 1)
		o.Set("signal", v.Get("expr"))
		spread(o, jsval.Obj(rest))
		return jsval.Obj(o)
	}
	return v
}

func signalOrValueRefWithCondition(cc *compileCtx, v Value) Value {
	cond := v.Get("condition")
	var c Value
	if cond.IsArr() {
		items := make([]Value, cond.Len())
		for i, it := range cond.Items() {
			cc.work(1)
			items[i] = signalRefOrValue(cc, it)
		}
		c = jsval.Arr(items)
	} else {
		c = signalRefOrValue(cc, cond)
	}
	o := cloneObj(signalRefOrValue(cc, v).ObjValue())
	o.Set("condition", c)
	return jsval.Obj(o)
}

// signalOrValueRef makes a value ref: {signal} stays, anything else becomes {value}.
func signalOrValueRef(cc *compileCtx, v Value) Value {
	if isExprRef(cc, v) {
		return signalRefOrValue(cc, v)
	}
	if isSignalRef(cc, v) {
		return v
	}
	if !v.IsUndefined() {
		return mkv("value", v)
	}
	return undef
}

func exprFromSignalRefOrValue(cc *compileCtx, ref Value) string {
	if isSignalRef(cc, ref) {
		return signalOf(ref)
	}
	return stringValue(cc, ref)
}

func exprFromValueRefOrSignalRef(cc *compileCtx, ref Value) string {
	if isSignalRef(cc, ref) {
		return signalOf(ref)
	}
	return stringValue(cc, ref.Get("value"))
}

func signalOrStringValue(cc *compileCtx, v Value) Value {
	if isSignalRef(cc, v) {
		return jsval.Str(signalOf(v))
	}
	if v.IsNullish() {
		return jsval.Null
	}
	if v.IsNum() || v.IsBool() {
		// vega-util's stringValue returns a number or a boolean as it is,
		// so a 0 or false is falsy where the expression is tested.
		return v
	}
	return jsval.Str(stringValue(cc, v))
}

// getStyles is [].concat(mark.type, mark.style ?? []).
// getStyles is `[].concat(mark.type, mark.style ?? [])`: the values as
// given, an array's elements spread one level.
func getStyles(mark Value) []Value {
	var out []Value
	for _, x := range []Value{mark.Get("type"), mark.Get("style")} {
		switch {
		case x.IsArr():
			out = append(out, x.Items()...)
		case x.IsNullish() && len(out) > 0: // mark.style ?? []
		default:
			out = append(out, x)
		}
	}
	return out
}

func getStyleConfig(p string, styles []Value, styleIndex Value) Value {
	v := undef
	for _, s := range styles {
		sc := jsGet(styleIndex, propKey(s)) // styleConfigIndex[style]: a TypeError without one
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
	// config[mark.type][channel] and config.mark[channel] are read
	// unguarded (all of getFirstDefined's arguments are evaluated): a mark
	// config set to a falsy value is absent from the config, and reading it
	// is a TypeError.
	mt := config.Get(propKey(mark.Get("type")))
	if vgChannel != "" {
		return firstDefined(cfg, cfg, jsGet(mt, vgChannel), jsGet(mt, channel), jsGet(config.Get("mark"), vgChannel))
	}
	return firstDefined(undef, cfg, undef, jsGet(mt, channel), jsGet(config.Get("mark"), channel))
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
		cc.work(1)
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

// titleMergeDirect is the most defs mergeTitleFieldDefs compares directly;
// past it the buckets make the merge of many views linear.
const titleMergeDirect = 8

func mergeTitleFieldDefs(cc *compileCtx, f1, f2 []Value) []Value {
	// Upstream compares each new field def with every one merged so far, and
	// a shared axis merges the defs of its views one view at a time: quadratic
	// in the views (X13). Here the defs are bucketed by their stable JSON
	// (deepEqual values print the same), so a def is compared only with the
	// few of its bucket; and the buckets of the list a merge returns are kept
	// (titleMerges), so the next merge, which is given that list, extends them
	// instead of bucketing the whole list again. The list is append-only and
	// the slice returned is capped at its length, so a later append by anyone
	// else copies rather than writing into it.
	var tm *titleMerge
	if len(f1) > 0 && cc != nil {
		if t := cc.titleMerges[&f1[0]]; t != nil && len(t.items) == len(f1) {
			tm = t
			delete(cc.titleMerges, &f1[0])
		}
	}
	if tm == nil && len(f1)+len(f2) <= titleMergeDirect {
		// A few defs (a chart of one or two views): compare each with each,
		// as upstream does, without building the buckets.
		merged := append([]Value(nil), f1...)
	direct:
		for _, fd := range f2 {
			cc.work(1)
			for _, m := range merged {
				cc.work(1)
				if deepEqual(cc, m, fd) {
					continue direct
				}
			}
			merged = append(merged, fd)
		}
		return merged[:len(merged):len(merged)]
	}
	if tm == nil {
		tm = &titleMerge{items: append([]Value(nil), f1...), byKey: map[string][]int{}}
		for i, fd := range tm.items {
			k := stringify(cc, fd)
			tm.byKey[k] = append(tm.byKey[k], i)
		}
	}
outer:
	for _, fd := range f2 {
		cc.work(1)
		k := stringify(cc, fd)
		for _, i := range tm.byKey[k] {
			cc.work(1)
			if deepEqual(cc, tm.items[i], fd) {
				continue outer
			}
		}
		tm.byKey[k] = append(tm.byKey[k], len(tm.items))
		tm.items = append(tm.items, fd)
	}
	if len(tm.items) == 0 {
		return nil
	}
	if cc != nil {
		if cc.titleMerges == nil {
			cc.titleMerges = map[*Value]*titleMerge{}
		}
		cc.titleMerges[&tm.items[0]] = tm
	}
	return tm.items[:len(tm.items):len(tm.items)]
}

// titleMerge is a list of title field defs a merge returned, with its
// buckets (see mergeTitleFieldDefs).
type titleMerge struct {
	items []Value
	byKey map[string][]int
}

// mergeTitle charges cc by the titles' text: merged across views, a title
// grows with each, as upstream's does.
func mergeTitle(cc *compileCtx, t1, t2 Value) Value {
	cc.work(1 + int64(len(t1.AsString())+len(t2.AsString()))/16)
	if deepEqual(cc, t1, t2) || !t2.IsTruthy() {
		return t1
	} else if !t1.IsTruthy() {
		return t2
	}
	// [...array(title1), ...array(title2)].join(', '): null and undefined
	// elements are written as "" (C86 of the 2026-10-18 audit).
	items := append(append([]Value(nil), arrayOf(t1)...), arrayOf(t2)...)
	cc.work(int64(len(items)))
	return jsval.Str(cc.walker().Join(items, ", "))
}

// mergeTitleComponent merges two title components (text or field-def lists).
func mergeTitleComponent(cc *compileCtx, v1, v2 withExplicit) withExplicit {
	a, b := v1.value, v2.value
	aText := isText(a) || isSignalRef(cc, a)
	bText := isText(b) || isSignalRef(cc, b)
	switch {
	case a.IsNullish() || b.IsNull():
		return withExplicit{v1.explicit, jsval.Null}
	case aText && bText:
		return withExplicit{v1.explicit, mergeTitle(cc, a, b)}
	case aText:
		return withExplicit{v1.explicit, a}
	case bText:
		return withExplicit{v1.explicit, b}
	}
	// mergeTitleFieldDefs(f1, f2): [...f1], then f2.forEach.
	f1 := spreadItems(a)
	return withExplicit{v1.explicit, jsval.Arr(mergeTitleFieldDefs(cc, f1, listOf(b, "f2", "forEach")))}
}

// spreadItems is [...v]: an array's items, a string's code points; a
// TypeError for anything else.
func spreadItems(v Value) []Value {
	if v.IsArr() || v.IsStr() {
		return iterate(v, "")
	}
	return iterateNew(v)
}
