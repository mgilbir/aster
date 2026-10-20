package guides

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// Scope is what the guide parsers need from the parser scope.
type Scope interface {
	// Config is the merged specification config (defaults included).
	Config() Value
	// ScaleType returns the type name of the named scale ("" if unknown).
	ScaleType(name string) string
	// Work charges n steps of a guide's preparation to the render, which
	// polls the call's context (ADR 0010): the loops over the config and
	// the specification's guide blocks charge each step. Once the call is
	// over it panics with the *budget.Stop the renderer recovers.
	Work(n int)
}

// axisFallback resolves one property for the signal-orient case: an axis
// config variant wins over the base axis config; title and label properties
// then fall back to the guide styles, which is what a static orient gets via
// the guide mark's own style lookup.
func axisFallback(prop string, cfg, axisCfg, style Value) Value {
	if cfg.IsTruthy() && hasKey(cfg, prop) {
		return member(cfg, prop)
	}
	if hasKey(axisCfg, prop) {
		return member(axisCfg, prop)
	}
	var styleProp string
	switch {
	case strings.HasPrefix(prop, "title"):
		switch prop {
		case "titleColor":
			styleProp = "fill"
		case "titleFont", "titleFontSize", "titleFontWeight":
			styleProp = lowerFirst(prop[5:])
		}
		return styleProp1(style, guideTitleStyle, styleProp)
	case strings.HasPrefix(prop, "label"):
		switch prop {
		case "labelColor":
			styleProp = "fill"
		case "labelFont", "labelFontSize":
			styleProp = lowerFirst(prop[5:])
		}
		return styleProp1(style, guideLabelStyle, styleProp)
	}
	return jsval.Null
}

// styleProp1 is style[name][prop]; a prop of no style is undefined, the
// key `${undefined}`.
func styleProp1(style Value, name, prop string) Value {
	if prop == "" {
		prop = "undefined"
	}
	return member(member(style, name), prop)
}

// lowerFirst is prop[5].toLowerCase() + prop.slice(6) after the prefix.
func lowerFirst(s string) string { return jsval.LowerFirst(s) }

// keyUnion is axis-config's keys(): the names `for (key in obj)` enumerates
// over the truthy objects given, each once, in the order Object.keys lists a
// plain object's: integer keys ascending first, then the others as first
// seen.
func keyUnion(sc Scope, objects ...Value) []string {
	m := jsval.NewObject(0)
	for _, o := range objects {
		if !o.IsTruthy() {
			continue
		}
		forIn(sc, o, func(k string, _ Value) { m.Set(k, jsval.Int(1)) })
	}
	return m.Keys()
}

// axisConfig merges the axis config blocks that apply to one axis, in
// vega-parser's precedence order: config.axis, then axisX/axisY, then
// axisTop/Bottom/Left/Right, then axisBand for band scales. When the orient is
// a signal the x/y and orient variants are folded into conditional signal
// expressions instead.
func axisConfig(spec Value, sc Scope) Value {
	cfg := sc.Config()
	style := member(cfg, "style")
	axis := member(cfg, "axis")
	band := jsval.False
	if sc.ScaleType(scaleKey(member(spec, "scale"))) == "band" {
		band = member(cfg, "axisBand")
	}
	orient := member(spec, "orient")

	var xy, or Value
	if isSignal(orient) {
		axisX, axisY := member(cfg, "axisX"), member(cfg, "axisY")
		xyObj := jsval.NewObject(0)
		// A key builds conditional expressions of its fallbacks: 4 steps.
		for _, key := range keyUnion(sc, axisX, axisY) {
			work(sc, 4)
			xyObj.Set(key, ifX(orient, axisFallback(key, axisX, axis, style), axisFallback(key, axisY, axis, style)))
		}
		xy = jsval.Obj(xyObj)

		axisTop, axisBottom := member(cfg, "axisTop"), member(cfg, "axisBottom")
		axisLeft, axisRight := member(cfg, "axisLeft"), member(cfg, "axisRight")
		orObj := jsval.NewObject(0)
		for _, key := range keyUnion(sc, axisTop, axisBottom, axisLeft, axisRight) {
			work(sc, 4)
			orObj.Set(key, ifOrient(signalOf(orient),
				axisFallback(key, axisTop, axis, style),
				axisFallback(key, axisBottom, axis, style),
				axisFallback(key, axisLeft, axis, style),
				axisFallback(key, axisRight, axis, style)))
		}
		or = jsval.Obj(orObj)
	} else {
		if isXOrient(orient) {
			xy = member(cfg, "axisX")
		} else {
			xy = member(cfg, "axisY")
		}
		or = member(cfg, "axis"+orientName(orient))
	}

	if xy.IsTruthy() || or.IsTruthy() || band.IsTruthy() {
		// Once per axis, over the whole axis config: the copies of a large
		// config for many axes are charged key by key.
		return jsval.Obj(extendWork(sc, jsval.NewObject(0), axis, xy, or, band))
	}
	return axis
}

// orientName is `orient[0].toUpperCase() + orient.slice(1)`, the suffix of
// the config block of a static orient: "bottom" names axisBottom, and as
// upstream reads any value so, ["bottom"] names axisBOTTOM and ["a", "b"]
// axisAb. A value with no first element that is a string (a number, "",
// [], {}, [1]) is upstream's TypeError.
func orientName(orient Value) string {
	if orient.IsNullish() {
		member(orient, "0")
	}
	first := member(orient, "0")
	if first.IsNullish() {
		throw("Cannot read properties of %s (reading 'toUpperCase')", first.Kind())
	}
	if !first.IsStr() {
		throw("orient[0].toUpperCase is not a function")
	}
	switch orient.Kind() {
	case jsval.KindStr:
		return jsval.UpperFirst(orient.StrValue())
	case jsval.KindArr:
		// String(orient.slice(1)): the rest joined by commas
		return jsval.JSUpper(first.StrValue()) + jsval.Arr(orient.Items()[1:]).AsString()
	}
	throw("orient.slice is not a function")
	return ""
}

// scaleKey is the name `scales[spec.scale]` looks up: the property key of
// the value (["x"] names x).
func scaleKey(v Value) string {
	k, ok := v.PropertyKey(jsval.MaxStringLength)
	if !ok {
		throw("guide: scale name longer than %d bytes", jsval.MaxStringLength)
	}
	return k
}
