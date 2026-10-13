package guides

import (
	"errors"
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
	if hasKey(cfg, prop) {
		return cfg.Get(prop)
	}
	if hasKey(axisCfg, prop) {
		return axisCfg.Get(prop)
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
		return style.Get(guideTitleStyle).Get(styleProp)
	case strings.HasPrefix(prop, "label"):
		switch prop {
		case "labelColor":
			styleProp = "fill"
		case "labelFont", "labelFontSize":
			styleProp = lowerFirst(prop[5:])
		}
		return style.Get(guideLabelStyle).Get(styleProp)
	}
	return jsval.Null
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// keyUnion collects the property names of the given objects.
func keyUnion(sc Scope, objects ...Value) []string {
	var keys []string
	seen := map[string]bool{}
	for _, o := range objects {
		ob := o.ObjValue()
		for i := 0; i < ob.Len(); i++ {
			work(sc, 1)
			if k := ob.KeyAt(i); !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// axisConfig merges the axis config blocks that apply to one axis, in
// vega-parser's precedence order: config.axis, then axisX/axisY, then
// axisTop/Bottom/Left/Right, then axisBand for band scales. When the orient is
// a signal the x/y and orient variants are folded into conditional signal
// expressions instead.
func axisConfig(spec Value, sc Scope) (Value, error) {
	cfg := sc.Config()
	style := cfg.Get("style")
	axis := cfg.Get("axis")
	band := jsval.False
	if sc.ScaleType(spec.Get("scale").AsString()) == "band" {
		band = cfg.Get("axisBand")
	}
	orient := spec.Get("orient")

	var xy, or Value
	if isSignal(orient) {
		axisX, axisY := cfg.Get("axisX"), cfg.Get("axisY")
		xyObj := jsval.NewObject(0)
		// A key builds conditional expressions of its fallbacks: 4 steps.
		for _, key := range keyUnion(sc, axisX, axisY) {
			work(sc, 4)
			xyObj.Set(key, ifX(orient, axisFallback(key, axisX, axis, style), axisFallback(key, axisY, axis, style)))
		}
		xy = jsval.Obj(xyObj)

		axisTop, axisBottom := cfg.Get("axisTop"), cfg.Get("axisBottom")
		axisLeft, axisRight := cfg.Get("axisLeft"), cfg.Get("axisRight")
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
		if !orient.IsStr() || orient.StrValue() == "" {
			return jsval.Undefined, errors.New("axis: missing or invalid orient")
		}
		o := orient.StrValue()
		if isXOrient(orient) {
			xy = cfg.Get("axisX")
		} else {
			xy = cfg.Get("axisY")
		}
		or = cfg.Get("axis" + strings.ToUpper(o[:1]) + o[1:])
	}

	if xy.IsTruthy() || or.IsTruthy() || band.IsTruthy() {
		// Once per axis, over the whole axis config: the copies of a large
		// config for many axes are charged key by key.
		return jsval.Obj(extendWork(sc, jsval.NewObject(0), axis, xy, or, band)), nil
	}
	return axis, nil
}
