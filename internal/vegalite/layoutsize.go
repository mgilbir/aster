package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Layout size parsing and signals — vega-lite/src/compile/layoutsize/*.

func getViewConfigContinuousSize(view Value, channel string) Value {
	if v := jsGet(view, channel); !v.IsNullish() {
		return v
	}
	if channel == "width" {
		return view.Get("continuousWidth")
	}
	return view.Get("continuousHeight")
}

func getViewConfigDiscreteSize(view Value, channel string) Value {
	var size Value
	if v := jsGet(view, channel); !v.IsNullish() {
		size = v
	} else if channel == "width" {
		size = view.Get("discreteWidth")
	} else {
		size = view.Get("discreteHeight")
	}
	return firstDefined(size, mkv("step", view.Get("step")))
}

// getViewConfigDiscreteStep is the step of the view config's discrete size
// as given (a step of null, "" or [] included), else the default step;
// callers convert it as upstream's arithmetic (step - 2) or template does.
func getViewConfigDiscreteStep(view Value, channel string) Value {
	size := getViewConfigDiscreteSize(view, channel)
	if isStep(size) {
		return size.Get("step")
	}
	return jsval.Num(defaultStep)
}

// initLayoutSize drops a step size for a continuous position channel (mutating size, as upstream does).
func initLayoutSize(cc *compileCtx, encoding Value, size *Object) *Object {
	for _, channel := range positionScaleChannels {
		sizeType := getSizeChannel(channel)
		if isStep(size.Lookup(sizeType)) {
			if isContinuousFieldOrDatumDef(cc, encoding.Get(channel)) {
				size.Delete(sizeType)
				cc.warn(msgStepDropped(sizeType))
			}
		}
	}
	return size
}

func parseLayerLayoutSize(m Model) {
	parseChildrenLayoutSize(m)
	parseNonUnitLayoutSizeForChannel(m, "width")
	parseNonUnitLayoutSizeForChannel(m, "height")
}

func parseConcatLayoutSize(m *concatModel) {
	parseChildrenLayoutSize(m)
	widthType := "childWidth"
	if c := m.layout.Lookup("columns"); c.IsNum() && c.NumValue() == 1 {
		widthType = "width"
	}
	heightType := "childHeight"
	if m.layout.Lookup("columns").IsUndefined() {
		heightType = "height"
	}
	parseNonUnitLayoutSizeForChannel(m, widthType)
	parseNonUnitLayoutSizeForChannel(m, heightType)
}

func parseChildrenLayoutSize(m Model) {
	cc := m.b().ctx
	for _, child := range m.children() {
		cc.work(modelStep)
		child.parseLayoutSize()
	}
}

func parseNonUnitLayoutSizeForChannel(m Model, layoutSizeType string) {
	cc := m.b().ctx
	b := m.b()
	sizeType := getSizeTypeFromLayoutSizeType(layoutSizeType)
	channel := getPositionScaleChannel(sizeType)
	resolve := b.comp.resolve
	var mergedSize *withExplicit
	for _, child := range m.children() {
		cc.work(modelStep)
		childSize := child.b().comp.layoutSize.getWithExplicit(sizeType)
		scaleResolve := resolve.scale[channel]
		if scaleResolve == "" {
			scaleResolve = defaultScaleResolve(channel, m)
		}
		if scaleResolve == "independent" && childSize.value.IsStr() && childSize.value.StrValue() == "step" {
			mergedSize = nil
			break
		}
		if mergedSize != nil {
			if scaleResolve == "independent" && !jsval.SameRef(mergedSize.value, childSize.value) {
				mergedSize = nil
				break
			}
			mv := mergeValuesWithExplicit(cc, mergedSize, childSize, sizeType, "", nil)
			mergedSize = &mv
		} else {
			cs := childSize
			mergedSize = &cs
		}
	}
	if mergedSize != nil {
		for _, child := range m.children() {
			cc.work(modelStep)
			b.renameSignal(child.b().getName(sizeType), b.getName(layoutSizeType))
			child.b().comp.layoutSize.set(sizeType, jsval.Str("merged"), false)
		}
		b.comp.layoutSize.setWithExplicit(layoutSizeType, *mergedSize)
	} else {
		b.comp.layoutSize.setWithExplicit(layoutSizeType, withExplicit{false, undef})
	}
}

func parseUnitLayoutSize(u *unitModel) {
	cc := u.b().ctx

	for _, channel := range positionScaleChannels {
		sizeType := getSizeChannel(channel)
		if sz := u.size.Lookup(sizeType); (cc.v5 && sz.IsTruthy()) || (!cc.v5 && !sz.IsNullish()) {
			if isStep(sz) {
				u.comp.layoutSize.set(sizeType, jsval.Str("step"), true)
			} else {
				u.comp.layoutSize.set(sizeType, sz, true)
			}
		} else {
			u.comp.layoutSize.set(sizeType, defaultUnitSize(u, sizeType), false)
		}
	}
}

func defaultUnitSize(u *unitModel, sizeType string) Value {
	channel := chY
	if sizeType == "width" {
		channel = chX
	}
	config := u.config
	sc := u.getScaleComponent(channel)
	if sc != nil {
		scaleType := sc.get("type")
		rng := sc.get("range")
		if hasDiscreteDomain(scaleType) {
			size := getViewConfigDiscreteSize(config.Get("view"), sizeType)
			if isVgRangeStep(u.b().ctx, rng) || isStep(size) {
				return jsval.Str("step")
			}
			return size
		}
		return getViewConfigContinuousSize(config.Get("view"), sizeType)
	} else if u.hasProjection() || strIs(u.mark(), "arc") {
		return getViewConfigContinuousSize(config.Get("view"), sizeType)
	}
	size := getViewConfigDiscreteSize(config.Get("view"), sizeType)
	if isStep(size) {
		return size.Get("step")
	}
	return size
}

// ---- assemble ----

func assembleLayoutSignals(m Model) []Value {
	var out []Value
	for _, t := range []string{"width", "height", "childWidth", "childHeight"} {
		out = append(out, sizeSignals(m, t)...)
	}
	return out
}

func sizeSignals(m Model, sizeType string) []Value {
	cc := m.b().ctx

	b := m.b()
	channel := chY
	if sizeType == "width" {
		channel = chX
	}
	size := b.comp.layoutSize.get(sizeType)
	if (!cc.v5 && size.IsNullish()) || (cc.v5 && !size.IsTruthy()) || (size.IsStr() && size.StrValue() == "merged") {
		return nil
	}
	name := signalOf(b.getSizeSignalRef(sizeType)) // .signal: a TypeError of null
	if size.IsStr() && size.StrValue() == "step" {
		sc := b.getScaleComponent(channel)
		if sc != nil {
			typ, rng := sc.get("type"), sc.get("range")
			if hasDiscreteDomain(typ) && isVgRangeStep(m.b().ctx, rng) {
				scaleName := b.scaleName(channel, false)
				if b.parent != nil && isFacetModel(b.parent) {
					if b.parent.b().comp.resolve.scale[channel] == "independent" {
						return []Value{stepSignal(cc, scaleName, rng)}
					}
				}
				return []Value{
					stepSignal(cc, scaleName, rng),
					mkv("name", name, "update", sizeExpr(cc, scaleName, sc, "domain('"+scaleName+"').length")),
				}
			}
		}
		throw("layout size is step although width/height is not step.")
	} else if looseEq(size, "container") { // size == 'container'
		isWidth := strings.HasSuffix(name, "width")
		expr := "containerSize()[1]"
		ch := "height"
		if isWidth {
			expr, ch = "containerSize()[0]", "width"
		}
		defaultValue := getViewConfigContinuousSize(b.config.Get("view"), ch)
		safeExpr := "isFinite(" + expr + ") ? " + expr + " : " + defaultValue.AsString()
		return []Value{mkv("name", name, "init", safeExpr, "on", arr(mkv("update", safeExpr, "events", "window:resize")))}
	}
	return []Value{mkv("name", name, "value", size)}
}

func stepSignal(cc *compileCtx, scaleName string, rng Value) Value {
	name := scaleName + "_step"
	if isSignalRef(cc, rng.Get("step")) {
		return mkv("name", name, "update", signalOf(rng.Get("step")))
	}
	return mkv("name", name, "value", rng.Get("step"))
}

func sizeExpr(cc *compileCtx, scaleName string, sc *scaleComponent, cardinality string) string {
	typ := sc.get("type")
	padding := sc.get("padding")
	paddingOuter := firstDefined(sc.get("paddingOuter"), padding)
	paddingInner := sc.get("paddingInner")
	if strIs(typ, "band") {
		if paddingInner.IsUndefined() {
			paddingInner = padding
		}
	} else {
		paddingInner = jsval.Int(1)
	}
	return "bandspace(" + cardinality + ", " + signalOrStringValue(cc, paddingInner).AsString() + ", " + signalOrStringValue(cc, paddingOuter).AsString() + ") * " + scaleName + "_step"
}
