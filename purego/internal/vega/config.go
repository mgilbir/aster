package vega

import (
	_ "embed"
	"sync"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// defaultConfigJSON is vega-parser's config.js defaults. Every default matters:
// mark colours, axis and legend metrics and the named scale ranges all come
// from here.
//
//go:embed config_default.json
var defaultConfigJSON []byte

var (
	defaultConfigOnce sync.Once
	defaultConfigVal  jsval.Value
)

func defaultConfig() jsval.Value {
	defaultConfigOnce.Do(func() {
		v, err := jsval.ParseJSON(defaultConfigJSON)
		if err != nil {
			panic("vega: bad embedded default config: " + err.Error())
		}
		defaultConfigVal = v
	})
	return defaultConfigVal
}

func illegalKey(k string) bool { return k == "__proto__" || k == "constructor" || k == "prototype" }

// mergeConfig is vega-util's mergeConfig: later configs override earlier ones.
// Signals are merged by name (later wins), legend.layout is merged recursively,
// style is merged at every level, and every other object replaces wholesale
// after a one-level key copy.
func mergeConfig(configs ...jsval.Value) jsval.Value {
	out := jsval.NewObject(16)
	for _, src := range configs {
		so := src.ObjValue()
		if so == nil {
			continue
		}
		for i := 0; i < so.Len(); i++ {
			key := so.KeyAt(i)
			val := so.ValueAt(i)
			if key == "signals" {
				out.Set("signals", mergeNamed(out.Lookup("signals"), val))
				continue
			}
			switch key {
			case "legend":
				writeConfig(out, key, val, recLayout)
			case "style":
				writeConfig(out, key, val, recAll)
			default:
				writeConfig(out, key, val, recNone)
			}
		}
	}
	return jsval.Obj(out)
}

type recurse uint8

const (
	recNone recurse = iota
	recLayout
	recAll
)

func writeConfig(out *jsval.Object, key string, v jsval.Value, r recurse) {
	if illegalKey(key) {
		return
	}
	if v.IsObj() {
		var o *jsval.Object
		if cur := out.Lookup(key); cur.IsObj() {
			o = cur.ObjValue()
		} else {
			o = jsval.NewObject(v.Len())
			out.Set(key, jsval.Obj(o))
		}
		vo := v.ObjValue()
		for i := 0; i < vo.Len(); i++ {
			k := vo.KeyAt(i)
			if r == recAll || (r == recLayout && k == "layout") {
				writeConfig(o, k, vo.ValueAt(i), recNone)
			} else if !illegalKey(k) {
				o.Set(k, vo.ValueAt(i))
			}
		}
		return
	}
	out.Set(key, v)
}

// mergeNamed merges two arrays of named objects; b takes precedence.
func mergeNamed(a, b jsval.Value) jsval.Value {
	if a.IsNullish() {
		return b
	}
	if b.IsNullish() {
		return a
	}
	seen := map[string]bool{}
	var res []jsval.Value
	add := func(it jsval.Value) {
		n := it.Get("name").AsString()
		if !seen[n] {
			seen[n] = true
			res = append(res, it)
		}
	}
	for _, it := range b.Items() {
		add(it)
	}
	for _, it := range a.Items() {
		add(it)
	}
	return jsval.Arr(res)
}
