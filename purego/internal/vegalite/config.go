package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Configuration defaults, merging and Vega config extraction — vega-lite/src/config.ts.

const (
	defaultStep    = 20
	defaultSpacing = 20
)

var markConfigs = []string{
	"mark", "arc", "area", "bar", "circle", "image", "line", "point", "rect", "rule",
	"square", "text", "tick", "trail", "geoshape",
}

var axisConfigs = []string{
	"axis", "axisBand", "axisBottom", "axisDiscrete", "axisLeft", "axisPoint", "axisQuantitative",
	"axisRight", "axisTemporal", "axisTop", "axisX", "axisXBand", "axisXDiscrete", "axisXPoint",
	"axisXQuantitative", "axisXTemporal", "axisY", "axisYBand", "axisYDiscrete", "axisYPoint",
	"axisYQuantitative", "axisYTemporal",
}

var headerConfigs = []string{"header", "headerRow", "headerColumn", "headerFacet"}

// primitiveMarks is keys(Mark), in upstream order.
var primitiveMarks = []string{
	"arc", "area", "bar", "image", "line", "point", "rect", "rule", "text", "tick", "trail",
	"circle", "square", "geoshape",
}

func defaultConfig() *Object {
	rectConfig := func() *Object {
		return mk("binSpacing", 0, "continuousBandSize", 5, "minBandSize", 0.25, "timeUnitBandPosition", 0.5)
	}
	bar := rectConfig()
	bar.Set("binSpacing", jsval.Int(1))
	tick := rectConfig()
	tick.Set("thickness", jsval.Int(1))
	legend := mk(
		"gradientHorizontalMaxLength", 200,
		"gradientHorizontalMinLength", 100,
		"gradientVerticalMaxLength", 200,
		"gradientVerticalMinLength", 64,
		"unselectedOpacity", 0.35,
	)
	return mk(
		"background", "white",
		"padding", 5,
		"timeFormat", "%b %d, %Y",
		"countTitle", "Count of Records",
		"view", mk("continuousWidth", 300, "continuousHeight", 300, "step", defaultStep),
		"mark", mk("color", "#4c78a8", "invalid", "break-paths-show-path-domains", "timeUnitBandSize", 1),
		"arc", mk(),
		"area", mk(),
		"bar", bar,
		"circle", mk(),
		"geoshape", mk(),
		"image", mk(),
		"line", mk(),
		"point", mk(),
		"rect", rectConfig(),
		"rule", mk("color", "black"),
		"square", mk(),
		"text", mk("color", "black"),
		"tick", tick,
		"trail", mk(),
		"boxplot", mk("size", 14, "extent", 1.5, "box", mk(), "median", mk("color", "white"), "outliers", mk(), "rule", mk(), "ticks", jsval.Null),
		"errorbar", mk("center", "mean", "rule", true, "ticks", false),
		"errorband", mk("band", mk("opacity", 0.3), "borders", false),
		"scale", defaultScaleConfig(),
		"projection", mk(),
		"legend", legend,
		"header", mk("titlePadding", 10, "labelPadding", 10),
		"headerColumn", mk(),
		"headerRow", mk(),
		"headerFacet", mk(),
		"selection", defaultSelectionConfig(),
		"style", mk(),
		"title", mk(),
		"facet", mk("spacing", defaultSpacing),
		"concat", mk("spacing", defaultSpacing),
		"normalizedNumberFormat", ".0%",
	)
}

const selectionID = "_vgsid_"

func defaultSelectionConfig() *Object {
	return mk(
		"point", mk(
			"on", "click",
			"fields", arr(selectionID),
			"toggle", "event.shiftKey",
			"resolve", "global",
			"clear", "dblclick",
		),
		"interval", mk(
			"on", "[pointerdown, window:pointerup] > window:pointermove!",
			"encodings", arr("x", "y"),
			"translate", "[pointerdown, window:pointerup] > window:pointermove!",
			"zoom", "wheel!",
			"mark", mk("fill", "#333", "fillOpacity", 0.125, "stroke", "white"),
			"resolve", "global",
			"clear", "dblclick",
		),
	)
}

var defaultColorSignals = func() *Object {
	tab10 := []string{"#4c78a8", "#f58518", "#e45756", "#72b7b2", "#54a24b", "#eeca3b", "#b279a2", "#ff9da6", "#9d755d", "#bab0ac"}
	o := mk(
		"blue", tab10[0], "orange", tab10[1], "red", tab10[2], "teal", tab10[3], "green", tab10[4],
		"yellow", tab10[5], "purple", tab10[6], "pink", tab10[7], "brown", tab10[8],
	)
	for i, g := range []string{"#000", "#111", "#222", "#333", "#444", "#555", "#666", "#777", "#888", "#999", "#aaa", "#bbb", "#ccc", "#ddd", "#eee", "#fff"} {
		o.Set("gray"+jsval.JSNumberString(float64(i)), jsval.Str(g))
	}
	return o
}()

var defaultFontSize = func() *Object {
	return mk("text", 11, "guideLabel", 10, "guideTitle", 11, "groupTitle", 13, "groupSubtitle", 12)
}

func colorSignalConfig(color Value) *Object {
	base := cloneObj(defaultColorSignals)
	value := jsval.Obj(base)
	if isObject(color) {
		value = jsval.Obj(spread(cloneObj(defaultColorSignals), color))
	}
	cat := make([]any, 0, 10)
	for _, n := range []string{"blue", "orange", "red", "teal", "green", "yellow", "purple", "pink", "brown", "grey8"} {
		cat = append(cat, mkv("signal", "color."+n))
	}
	return mk(
		"signals", arr(mkv("name", "color", "value", value)),
		"mark", mk("color", mkv("signal", "color.blue")),
		"rule", mk("color", mkv("signal", "color.gray0")),
		"text", mk("color", mkv("signal", "color.gray0")),
		"style", mk(
			"guide-label", mk("fill", mkv("signal", "color.gray0")),
			"guide-title", mk("fill", mkv("signal", "color.gray0")),
			"group-title", mk("fill", mkv("signal", "color.gray0")),
			"group-subtitle", mk("fill", mkv("signal", "color.gray0")),
			"cell", mk("stroke", mkv("signal", "color.gray8")),
		),
		"axis", mk(
			"domainColor", mkv("signal", "color.gray13"),
			"gridColor", mkv("signal", "color.gray8"),
			"tickColor", mkv("signal", "color.gray13"),
		),
		"range", mk("category", cat),
	)
}

func fontSizeSignalConfig(fontSize Value) *Object {
	value := jsval.Obj(defaultFontSize())
	if isObject(fontSize) {
		value = jsval.Obj(spread(defaultFontSize(), fontSize))
	}
	return mk(
		"signals", arr(mkv("name", "fontSize", "value", value)),
		"text", mk("fontSize", mkv("signal", "fontSize.text")),
		"style", mk(
			"guide-label", mk("fontSize", mkv("signal", "fontSize.guideLabel")),
			"guide-title", mk("fontSize", mkv("signal", "fontSize.guideTitle")),
			"group-title", mk("fontSize", mkv("signal", "fontSize.groupTitle")),
			"group-subtitle", mk("fontSize", mkv("signal", "fontSize.groupSubtitle")),
		),
	)
}

func fontConfig(font Value) *Object {
	return mk(
		"text", mk("font", font),
		"style", mk(
			"guide-label", mk("font", font),
			"guide-title", mk("font", font),
			"group-title", mk("font", font),
			"group-subtitle", mk("font", font),
		),
	)
}

// ---- vega-util mergeConfig / writeConfig ----

func isLegalKey(k string) bool { return k != "__proto__" && k != "constructor" && k != "prototype" }

// writeConfig writes value at output[key]. recurse is nil (shallow), or a
// predicate over child keys; recurseAll makes every level recurse.
func writeConfig(output *Object, key string, value Value, recurseAll bool, recurseKeys map[string]bool) {
	if !isLegalKey(key) {
		return
	}
	if value.IsObj() {
		var target *Object
		if cur := output.Lookup(key); cur.IsObj() || cur.IsArr() {
			if cur.IsObj() {
				target = cur.ObjValue()
			} else {
				// An array is an object in JS; keys are written as properties on it. Rare; replace.
				target = jsval.NewObject(4)
				output.Set(key, jsval.Obj(target))
			}
		} else {
			target = jsval.NewObject(4)
			output.Set(key, jsval.Obj(target))
		}
		vo := value.ObjValue()
		for i := 0; i < vo.Len(); i++ {
			k := vo.KeyAt(i)
			if recurseAll || recurseKeys[k] {
				writeConfig(target, k, vo.ValueAt(i), false, nil)
			} else if isLegalKey(k) {
				target.Set(k, vo.ValueAt(i))
			}
		}
		return
	}
	output.Set(key, value)
}

func mergeNamed(a, b Value) Value {
	if a.IsNullish() {
		return b
	}
	if b.IsNullish() {
		return a
	}
	seen := map[string]bool{}
	var out []Value
	add := func(it Value) {
		n := it.Get("name").AsString()
		if !seen[n] {
			seen[n] = true
			out = append(out, it)
		}
	}
	for _, it := range b.Items() {
		add(it)
	}
	for _, it := range a.Items() {
		add(it)
	}
	return jsval.Arr(out)
}

// mergeConfig is vega-util's mergeConfig.
func mergeConfig(configs ...Value) *Object {
	out := jsval.NewObject(8)
	for _, src := range configs {
		if !src.IsObj() {
			continue
		}
		so := src.ObjValue()
		for i := 0; i < so.Len(); i++ {
			key := so.KeyAt(i)
			if key == "signals" {
				out.Set("signals", mergeNamed(out.Lookup("signals"), so.ValueAt(i)))
				continue
			}
			switch key {
			case "legend":
				writeConfig(out, key, so.ValueAt(i), false, map[string]bool{"layout": true})
			case "style":
				writeConfig(out, key, so.ValueAt(i), true, nil)
			default:
				writeConfig(out, key, so.ValueAt(i), false, nil)
			}
		}
	}
	return out
}

func isConditionalAxisValue(v Value) bool { return v.Get("condition").IsTruthy() }

func getAxisConfigInternal(axisConfig Value) Value {
	if axisConfig.IsStr() {
		out := jsval.NewObject(0)
		spread(out, axisConfig)
		return jsval.Obj(out)
	}
	out := jsval.NewObject(axisConfig.Len())
	for _, prop := range keysOf(axisConfig) {
		val := axisConfig.Get(prop)
		if isConditionalAxisValue(val) {
			out.Set(prop, signalOrValueRefWithCondition(val))
		} else {
			out.Set(prop, signalRefOrValue(val))
		}
	}
	return jsval.Obj(out)
}

func getStyleConfigInternal(styleConfig Value) Value {
	out := jsval.NewObject(styleConfig.Len())
	for _, prop := range keysOf(styleConfig) {
		out.Set(prop, getAxisConfigInternal(styleConfig.Get(prop)))
	}
	return jsval.Obj(out)
}

var configPropsWithExpr = func() []string {
	l := append([]string{}, markConfigs...)
	l = append(l, axisConfigs...)
	l = append(l, headerConfigs...)
	return append(l, "background", "padding", "legend", "lineBreak", "scale", "style", "title", "view")
}()

// initConfig merges the specified config over the defaults and converts
// ExprRefs to SignalRefs.
func initConfig(specified Value) Value {
	if specified.IsNullish() {
		specified = jsval.Obj(jsval.NewObject(0))
	}
	color, font, fontSize, selection := specified.Get("color"), specified.Get("font"), specified.Get("fontSize"), specified.Get("selection")
	rest := omit(specified, "color", "font", "fontSize", "selection")
	parts := []Value{jsval.Obj(jsval.NewObject(0)), deepClone(jsval.Obj(defaultConfig()))}
	if font.IsTruthy() {
		parts = append(parts, jsval.Obj(fontConfig(font)))
	}
	if color.IsTruthy() {
		parts = append(parts, jsval.Obj(colorSignalConfig(color)))
	}
	if fontSize.IsTruthy() {
		parts = append(parts, jsval.Obj(fontSizeSignalConfig(fontSize)))
	}
	parts = append(parts, jsval.Obj(rest))
	merged := mergeConfig(parts...)
	if selection.IsTruthy() {
		writeConfig(merged, "selection", selection, true, nil)
	}
	mv := jsval.Obj(merged)
	out := omit(mv, configPropsWithExpr...)
	for _, prop := range []string{"background", "lineBreak", "padding"} {
		if v := merged.Lookup(prop); v.IsTruthy() {
			out.Set(prop, signalRefOrValue(v))
		}
	}
	for _, m := range markConfigs {
		if v := merged.Lookup(m); v.IsTruthy() {
			out.Set(m, replaceExprRef(v, 0))
		}
	}
	for _, a := range axisConfigs {
		if v := merged.Lookup(a); v.IsTruthy() {
			out.Set(a, getAxisConfigInternal(v))
		}
	}
	for _, h := range headerConfigs {
		if v := merged.Lookup(h); v.IsTruthy() {
			out.Set(h, replaceExprRef(v, 0))
		}
	}
	if v := merged.Lookup("legend"); v.IsTruthy() {
		out.Set("legend", replaceExprRef(v, 0))
	}
	if sc := merged.Lookup("scale"); sc.IsTruthy() {
		invalid := sc.Get("invalid")
		other := jsval.Obj(omit(sc, "invalid"))
		newInvalid := replaceExprRef(invalid, 1)
		o := cloneObj(replaceExprRef(other, 0).ObjValue())
		if newInvalid.Len() > 0 {
			o.Set("invalid", newInvalid)
		}
		out.Set("scale", jsval.Obj(o))
	}
	if v := merged.Lookup("style"); v.IsTruthy() {
		out.Set("style", getStyleConfigInternal(v))
	}
	if v := merged.Lookup("title"); v.IsTruthy() {
		out.Set("title", replaceExprRef(v, 0))
	}
	if v := merged.Lookup("view"); v.IsTruthy() {
		out.Set("view", replaceExprRef(v, 0))
	}
	return jsval.Obj(out)
}

// ---- stripAndRedirectConfig ----

var vlOnlyConfigProperties = []string{
	"color", "fontSize", "background", "padding", "facet", "concat", "numberFormat", "numberFormatType",
	"normalizedNumberFormat", "normalizedNumberFormatType", "timeFormat", "countTitle", "header",
	"axisQuantitative", "axisTemporal", "axisDiscrete", "axisPoint", "axisXBand", "axisXPoint",
	"axisXDiscrete", "axisXQuantitative", "axisXTemporal", "axisYBand", "axisYPoint", "axisYDiscrete",
	"axisYQuantitative", "axisYTemporal", "scale", "selection", "overlay",
}

var vlOnlyMarkConfigProperties = []string{
	"color", "filled", "invalid", "order", "radius2", "theta2", "timeUnitBandSize", "timeUnitBandPosition",
}

var vlOnlyLegendConfig = []string{
	"gradientHorizontalMaxLength", "gradientHorizontalMinLength", "gradientVerticalMaxLength",
	"gradientVerticalMinLength", "unselectedOpacity",
}

var vlOnlyRectConfig = []string{"binSpacing", "continuousBandSize", "discreteBandSize", "minBandSize"}

func vlOnlyMarkSpecificConfig(markType string) []string {
	switch markType {
	case "view":
		return []string{"continuousWidth", "continuousHeight", "discreteWidth", "discreteHeight", "step"}
	case "area":
		return []string{"line", "point"}
	case "bar", "rect":
		return vlOnlyRectConfig
	case "line":
		return []string{"point"}
	case "tick":
		return append([]string{"bandSize", "thickness"}, vlOnlyRectConfig...)
	}
	return nil
}

// stripAndRedirectConfig removes Vega-Lite-only config and moves mark configs
// into Vega style configs; it returns undefined when nothing is left.
func stripAndRedirectConfig(cfg Value) Value {
	c := deepClone(cfg).ObjValue()
	for _, p := range vlOnlyConfigProperties {
		c.Delete(p)
	}
	if axis := c.Lookup("axis"); axis.IsObj() {
		ao := axis.ObjValue()
		for _, p := range append([]string(nil), ao.Keys()...) {
			if isConditionalAxisValue(ao.Lookup(p)) {
				ao.Delete(p)
			}
		}
	}
	if lg := c.Lookup("legend"); lg.IsObj() {
		for _, p := range vlOnlyLegendConfig {
			lg.ObjValue().Delete(p)
		}
	}
	if mk_ := c.Lookup("mark"); mk_.IsObj() {
		mo := mk_.ObjValue()
		for _, p := range vlOnlyMarkConfigProperties {
			mo.Delete(p)
		}
		if t := mo.Lookup("tooltip"); t.IsTruthy() && isObject(t) {
			mo.Delete("tooltip")
		}
	}
	if params := c.Lookup("params"); !params.IsUndefined() {
		sigs := append([]Value(nil), c.Lookup("signals").Items()...)
		sigs = append(sigs, assembleParameterSignals(params)...)
		c.Set("signals", jsval.Arr(sigs))
		c.Delete("params")
	}
	for _, markType := range append([]string{"view"}, primitiveMarks...) {
		mc := c.Lookup(markType)
		if mo := mc.ObjValue(); mo != nil {
			for _, p := range vlOnlyMarkConfigProperties {
				mo.Delete(p)
			}
			for _, p := range vlOnlyMarkSpecificConfig(markType) {
				mo.Delete(p)
			}
		}
		redirectConfigToStyleConfig(c, markType)
	}
	for _, m := range []string{"boxplot", "errorbar", "errorband"} {
		c.Delete(m)
	}
	redirectTitleConfig(c)
	for _, p := range append([]string(nil), c.Keys()...) {
		if v := c.Lookup(p); isObject(v) && isEmptyObj(v) {
			c.Delete(p)
		}
	}
	if c.Len() == 0 {
		return undef
	}
	return jsval.Obj(c)
}

func redirectTitleConfig(c *Object) {
	parts := extractTitleConfig(c.Lookup("title"))
	style := c.Lookup("style").ObjValue()
	if style == nil {
		style = jsval.NewObject(2)
		c.Set("style", jsval.Obj(style))
	}
	if parts.titleMarkConfig.Len() > 0 {
		style.Set("group-title", jsval.Obj(merged(style.Lookup("group-title"), jsval.Obj(parts.titleMarkConfig))))
	}
	if parts.subtitleMarkConfig.Len() > 0 {
		style.Set("group-subtitle", jsval.Obj(merged(style.Lookup("group-subtitle"), jsval.Obj(parts.subtitleMarkConfig))))
	}
	if parts.subtitle.Len() > 0 {
		c.Set("title", jsval.Obj(parts.subtitle))
	} else {
		c.Delete("title")
	}
}

func redirectConfigToStyleConfig(c *Object, prop string) {
	toProp := prop
	if prop == "view" {
		toProp = "cell"
	}
	style := c.Lookup("style").ObjValue()
	if style == nil {
		style = jsval.NewObject(2)
		c.Set("style", jsval.Obj(style))
	}
	st := merged(c.Lookup(prop), style.Lookup(toProp))
	if st.Len() > 0 {
		style.Set(toProp, jsval.Obj(st))
	}
	c.Delete(prop)
}
