package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Spec-type predicates and top-level helpers (vega-lite/src/spec/*.ts, parameter.ts).

func isUnitSpec(cc *compileCtx, s Value) bool    { return specHas(cc, s, "mark") }
func isLayerSpec(cc *compileCtx, s Value) bool   { return specHas(cc, s, "layer") }
func isFacetSpec(cc *compileCtx, s Value) bool   { return specHas(cc, s, "facet") }
func isRepeatSpec(cc *compileCtx, s Value) bool  { return specHas(cc, s, "repeat") }
func isConcatSpec(cc *compileCtx, s Value) bool  { return specHas(cc, s, "concat") }
func isVConcatSpec(cc *compileCtx, s Value) bool { return specHas(cc, s, "vconcat") }
func isHConcatSpec(cc *compileCtx, s Value) bool { return specHas(cc, s, "hconcat") }
func isAnyConcatSpec(cc *compileCtx, s Value) bool {
	return isVConcatSpec(cc, s) || isHConcatSpec(cc, s) || isConcatSpec(cc, s)
}

// specHas is hasProperty(spec, key) in 6.4; 5.8's spec predicates use the
// in operator, a TypeError on a spec that is not an object. A nil cc (the
// engine's own view count, which upstream does not have) never throws.
func specHas(cc *compileCtx, s Value, key string) bool {
	if cc != nil && cc.v5 {
		return jsIn(s, key)
	}
	return hasProperty(s, key)
}
func isLayerRepeatSpec(cc *compileCtx, s Value) bool {
	if cc.v5 {
		// 5.8: spec.repeat['layer'] is truthy.
		return !s.Get("repeat").IsArr() && jsGet(s.Get("repeat"), "layer").IsTruthy()
	}
	return !s.Get("repeat").IsArr() && hasProperty(s.Get("repeat"), "layer")
}

// isFacetMapping is hasProperty(f, 'row') || hasProperty(f, 'column') in
// 6.4; 5.8 uses the in operator, a TypeError on a primitive.
func isFacetMapping(cc *compileCtx, f Value) bool {
	if cc != nil && cc.v5 {
		return jsIn(f, "row") || jsIn(f, "column")
	}
	return hasProperty(f, "row") || hasProperty(f, "column")
}

func isStep(size Value) bool { return hasProperty(size, "step") }

func isFrameMixins(o Value) bool {
	return hasProperty(o, "view") || hasProperty(o, "width") || hasProperty(o, "height")
}

func isSelectionParameter(p Value) bool { return p.IsObj() && p.Get("select").IsTruthy() }

// assembleParameterSignals converts variable parameters to Vega signals;
// selection parameters are compiled elsewhere.
func assembleParameterSignals(cc *compileCtx, params Value) []Value {
	var signals []Value
	var list []Value
	if params.IsTruthy() {
		list = iterate(params, "params || []")
	}
	for _, p := range list {
		cc.work(1)
		if isSelectionParameter(p) {
			continue
		}
		if p.IsNullish() {
			throw("Cannot destructure property 'expr' of 'param' as it is %s.", nullishName(p))
		}
		expr, bind := p.Get("expr"), p.Get("bind")
		rest := omit(p, "expr", "bind")
		if bind.IsTruthy() && expr.IsTruthy() {
			s := cloneObj(rest)
			s.Set("bind", bind)
			s.Set("init", expr)
			signals = append(signals, jsval.Obj(s))
		} else {
			s := cloneObj(rest)
			if expr.IsTruthy() {
				s.Set("update", expr)
			}
			if bind.IsTruthy() {
				s.Set("bind", bind)
			}
			signals = append(signals, jsval.Obj(s))
		}
	}
	return signals
}

var compositionLayoutProps = []string{"align", "bounds", "center", "columns", "spacing"}

// extractCompositionLayout collects spacing/columns/align/bounds/center from
// the spec and the composition config.
func extractCompositionLayout(cc *compileCtx, spec Value, specType string, config Value) *Object {
	compositionConfig := config.Get(specType)
	if compositionConfig.IsNullish() {
		throw("Cannot destructure property 'spacing' of 'compositionConfig' as it is %s.", nullishName(compositionConfig))
	}
	layout := jsval.NewObject(4)
	spacingConfig, columns := compositionConfig.Get("spacing"), compositionConfig.Get("columns")
	if !spacingConfig.IsUndefined() {
		layout.Set("spacing", spacingConfig)
	}
	if !columns.IsUndefined() {
		if (isFacetSpec(cc, spec) && !isFacetMapping(cc, spec.Get("facet"))) || isConcatSpec(cc, spec) {
			layout.Set("columns", columns)
		}
	}
	if isVConcatSpec(cc, spec) {
		layout.Set("columns", jsval.Int(1))
	}
	for _, prop := range compositionLayoutProps {
		v := spec.Get(prop)
		if v.IsUndefined() {
			continue
		}
		if prop == "spacing" {
			if v.IsNum() {
				layout.Set(prop, v)
			} else {
				layout.Set(prop, mkv(
					"row", coalesce(jsGet(v, "row"), spacingConfig), // spacing.row: a TypeError of null
					"column", coalesce(jsGet(v, "column"), spacingConfig),
				))
			}
		} else {
			layout.Set(prop, v)
		}
	}
	return layout
}

func isFitType(t string) bool { return t == "fit" || t == "fit-x" || t == "fit-y" }

func getFitType(sizeType string) string {
	if sizeType != "" {
		return "fit-" + getPositionScaleChannel(sizeType)
	}
	return "fit"
}

// extractTopLevelProperties picks background and padding (and params).
func extractTopLevelProperties(cc *compileCtx, t Value, includeParams bool) *Object {
	o := jsval.NewObject(3)
	for _, p := range []string{"background", "padding"} {
		if t.IsObj() && !t.Get(p).IsUndefined() {
			o.Set(p, signalRefOrValue(cc, t.Get(p)))
		}
	}
	if includeParams {
		o.Set("params", t.Get("params"))
	}
	return o
}
