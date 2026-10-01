// Package vegalite compiles Vega-Lite specifications to Vega specifications,
// following the Vega-Lite 6.4 compiler stage by stage: config merging,
// normalization, model construction, component parsing, dataflow optimization
// and assembly. The output is a jsval value whose property order matches
// upstream's JSON.stringify output, so it can be compared byte for byte.
//
// Options.Version also selects Vega-Lite 5.8.0, which the package compiles to
// exactly what vega-lite@5.8.0's compile() produces (Vega schema v5 included).
// Only the compiler changes: the Vega specification it emits is still run by
// the engine's Vega 6.4 runtime, not by Vega 5.25, so the rendered output of a 5.8
// specification follows Vega 6.4's behaviour. The 5.8 code paths live behind
// the version switch (see version.go and vl5.go) and leave the 6.4 output
// untouched.
package vegalite

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

// Options configure a compilation.
type Options struct {
	// Config is a Vega-Lite config object merged under the specification's own
	// `config` (aster's WithTheme passes a theme here). The zero Value means none.
	Config jsval.Value
	// Location is the time zone in which datetime objects without `utc` are
	// read, JavaScript's local time. Nil means UTC; the host's time zone is
	// never consulted, so output does not depend on the machine.
	Location *time.Location
	// Version is the Vega-Lite version to follow: "6.4" (also the default for
	// "") or "5.8". Anything else is an error.
	Version string
	// Context, when set, bounds the compilation: it is checked in the loops
	// that can run long and Compile returns the context's error once it is
	// done. Nil means no bound.
	Context context.Context
}

// panicHook, when set (by tests, before any compilation starts), receives the
// stack of every panic Compile turns into an "internal error".
var panicHook func(stack []byte)

// Compile turns a Vega-Lite specification into a Vega specification. The input
// is not modified. Errors are returned for invalid specifications; Compile does
// not panic.
func Compile(spec jsval.Value, opts Options) (out jsval.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = jsval.Undefined
			switch e := r.(type) {
			case cancelError:
				err = e.err
			case compileError:
				err = e
			case limitError:
				err = e
			case exprSyntaxError:
				err = e.err()
			default:
				if panicHook != nil {
					panicHook(debug.Stack())
				}
				err = fmt.Errorf("vegalite: internal error: %v", r)
			}
		}
	}()
	v5, err := isVersion58(opts.Version)
	if err != nil {
		return jsval.Undefined, err
	}
	cc := &compileCtx{v5: v5, loc: opts.Location, ctx: opts.Context}
	if !spec.IsObj() {
		return jsval.Undefined, compileError{"Invalid spec: a Vega-Lite specification must be an object"}
	}
	if opts.Context != nil {
		if err := opts.Context.Err(); err != nil {
			return jsval.Undefined, err
		}
	}
	input := deepClone(spec)
	return compileSpec(cc, input, opts), nil
}

// CompileJSON compiles a specification (and optional config) given as JSON text
// and returns the Vega specification as JSON text.
func CompileJSON(specJSON []byte, configJSON string) ([]byte, error) {
	spec, err := jsval.ParseJSON(specJSON)
	if err != nil {
		return nil, err
	}
	var opts Options
	if configJSON != "" {
		cfg, err := jsval.ParseJSONString(configJSON)
		if err != nil {
			return nil, fmt.Errorf("vegalite: invalid config: %w", err)
		}
		opts.Config = cfg
	}
	out, err := Compile(spec, opts)
	if err != nil {
		return nil, err
	}
	return jsval.AppendJSON(nil, out), nil
}

func compileSpec(cc *compileCtx, inputSpec jsval.Value, opts Options) jsval.Value {
	config := initConfig(cc, jsval.Obj(mergeConfig(opts.Config, inputSpec.Get("config"))))
	spec := normalize(cc, inputSpec, config)
	model := buildModel(cc, spec, nil, "", jsval.NewObject(0), config, 0)
	parseModel(model)
	optimizeDataflow(model.b().comp.data, model)
	top := getTopLevelProperties(inputSpec, spec.Get("autosize"), config, model)
	out := assembleTopLevelModel(model, top, inputSpec.Get("datasets"), inputSpec.Get("usermeta"))
	resolveLazy(out)
	return out
}

func getTopLevelProperties(inputSpec jsval.Value, autosize jsval.Value, config jsval.Value, model Model) *Object {
	b := model.b()
	width, height := b.comp.layoutSize.get("width"), b.comp.layoutSize.get("height")
	var as *Object
	switch {
	case autosize.IsUndefined():
		as = mk("type", "pad")
		if b.hasAxisOrientSignalRef() {
			as.Set("resize", jsval.True)
		}
	case autosize.IsStr():
		as = mk("type", autosize)
	default:
		as = cloneObj(autosize.ObjValue())
	}
	if width.IsTruthy() && height.IsTruthy() && as.Lookup("type").IsStr() && isFitType(as.Lookup("type").StrValue()) {
		wStep := width.IsStr() && width.StrValue() == "step"
		hStep := height.IsStr() && height.StrValue() == "step"
		switch {
		case wStep && hStep:
			as.Set("type", jsval.Str("pad"))
		case wStep || hStep:
			sizeType := "height"
			if wStep {
				sizeType = "width"
			}
			inverse := "width"
			if sizeType == "width" {
				inverse = "height"
			}
			as.Set("type", jsval.Str(getFitType(inverse)))
		}
	}
	out := jsval.NewObject(6)
	if as.Len() == 1 && as.Lookup("type").IsTruthy() {
		if as.Lookup("type").AsString() != "pad" {
			out.Set("autosize", as.Lookup("type"))
		}
	} else {
		out.Set("autosize", jsval.Obj(as))
	}
	spread(out, jsval.Obj(extractTopLevelProperties(config, false)))
	spread(out, jsval.Obj(extractTopLevelProperties(inputSpec, true)))
	return out
}

func assembleTopLevelModel(model Model, top *Object, datasets, usermeta jsval.Value) jsval.Value {
	cc := model.b().ctx

	b := model.b()
	vgConfig := undef
	if b.config.IsTruthy() {
		vgConfig = stripAndRedirectConfig(cc, b.config)
	}
	rootData := assembleRootData(b.comp.data, datasets)
	var data []Value
	if cc.v5 {
		data = append(model.assembleSelectionData(nil), rootData...)
	} else {
		data = model.assembleSelectionData(rootData)
	}
	projections := b.assembleProjections()
	title := model.assembleTitle()
	style := model.assembleGroupStyle()
	encodeEntry := b.assembleGroupEncodeEntry(true)
	layoutSignals := model.assembleLayoutSignals()
	// width and height signals with a value move to top-level properties.
	var kept []jsval.Value
	for _, s := range layoutSignals {
		n := s.Get("name")
		if n.IsStr() && (n.StrValue() == "width" || n.StrValue() == "height") && !s.Get("value").IsUndefined() {
			top.Set(n.StrValue(), jsval.Num(jsval.ToNumber(s.Get("value"))))
			continue
		}
		kept = append(kept, s)
	}
	params := top.Lookup("params")
	other := omit(jsval.Obj(top), "params")
	schema := "https://vega.github.io/schema/vega/v6.json"
	if cc.v5 {
		schema = "https://vega.github.io/schema/vega/v5.json"
	}
	o := mk("$schema", schema)
	if b.description.IsTruthy() {
		o.Set("description", b.description)
	}
	spread(o, jsval.Obj(other))
	if title.IsTruthy() {
		o.Set("title", title)
	}
	if style.IsTruthy() {
		o.Set("style", style)
	}
	if encodeEntry.IsTruthy() {
		o.Set("encode", mkv("update", encodeEntry))
	}
	o.Set("data", jsval.Arr(data))
	if len(projections) > 0 {
		o.Set("projections", jsval.Arr(projections))
	}
	signals := append([]jsval.Value{}, kept...)
	signals = append(signals, model.assembleSelectionTopLevelSignals(nil)...)
	signals = append(signals, assembleParameterSignals(params)...)
	spread(o, jsval.Obj(assembleGroup(model, signals)))
	if vgConfig.IsTruthy() {
		o.Set("config", vgConfig)
	}
	if usermeta.IsTruthy() {
		o.Set("usermeta", usermeta)
	}
	return jsval.Obj(o)
}
