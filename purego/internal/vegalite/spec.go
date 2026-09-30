package vegalite

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Spec-type predicates and top-level helpers (vega-lite/src/spec/*.ts, parameter.ts).

func isUnitSpec(s Value) bool    { return hasProperty(s, "mark") }
func isLayerSpec(s Value) bool   { return hasProperty(s, "layer") }
func isFacetSpec(s Value) bool   { return hasProperty(s, "facet") }
func isRepeatSpec(s Value) bool  { return hasProperty(s, "repeat") }
func isConcatSpec(s Value) bool  { return hasProperty(s, "concat") }
func isVConcatSpec(s Value) bool { return hasProperty(s, "vconcat") }
func isHConcatSpec(s Value) bool { return hasProperty(s, "hconcat") }
func isAnyConcatSpec(s Value) bool {
	return isVConcatSpec(s) || isHConcatSpec(s) || isConcatSpec(s)
}
func isLayerRepeatSpec(s Value) bool {
	return !s.Get("repeat").IsArr() && hasProperty(s.Get("repeat"), "layer")
}
func isFacetMapping(f Value) bool { return hasProperty(f, "row") || hasProperty(f, "column") }

func isStep(size Value) bool { return hasProperty(size, "step") }

func isFrameMixins(o Value) bool {
	return hasProperty(o, "view") || hasProperty(o, "width") || hasProperty(o, "height")
}

func isSelectionParameter(p Value) bool { return p.IsObj() && p.Get("select").IsTruthy() }

// assembleParameterSignals converts variable parameters to Vega signals;
// selection parameters are compiled elsewhere.
func assembleParameterSignals(params Value) []Value {
	var signals []Value
	for _, p := range params.Items() {
		if isSelectionParameter(p) {
			continue
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
func extractCompositionLayout(spec Value, specType string, config Value) *Object {
	compositionConfig := config.Get(specType)
	layout := jsval.NewObject(4)
	spacingConfig, columns := compositionConfig.Get("spacing"), compositionConfig.Get("columns")
	if !spacingConfig.IsUndefined() {
		layout.Set("spacing", spacingConfig)
	}
	if !columns.IsUndefined() {
		if (isFacetSpec(spec) && !isFacetMapping(spec.Get("facet"))) || isConcatSpec(spec) {
			layout.Set("columns", columns)
		}
	}
	if isVConcatSpec(spec) {
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
					"row", coalesce(v.Get("row"), spacingConfig),
					"column", coalesce(v.Get("column"), spacingConfig),
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
func extractTopLevelProperties(t Value, includeParams bool) *Object {
	o := jsval.NewObject(3)
	for _, p := range []string{"background", "padding"} {
		if t.IsObj() && !t.Get(p).IsUndefined() {
			o.Set(p, signalRefOrValue(t.Get(p)))
		}
	}
	if includeParams {
		o.Set("params", t.Get("params"))
	}
	return o
}
