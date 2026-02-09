// Package runtime manages the QuickJS engine lifecycle, loads vendored
// Vega/Vega-Lite modules, and bridges Go callbacks into JavaScript.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/fastschema/qjs"

	asterjs "github.com/mgilbir/aster/internal/js"
)

// Loader is the subset of the aster.Loader interface needed by the runtime.
type Loader interface {
	Load(ctx context.Context, uri string) ([]byte, error)
	Sanitize(ctx context.Context, uri string) (string, error)
}

// TextMeasurer measures text width given a string and CSS font descriptor.
type TextMeasurer interface {
	MeasureText(text, cssFont string) float64
}

// Config holds runtime configuration.
type Config struct {
	Loader       Loader
	TextMeasurer TextMeasurer
	Theme        string
	MemoryLimit  int
	Timeout      time.Duration
	Version      string // version set key, e.g. "vl6_4" (default)
	Timezone     string // IANA timezone name or "UTC" (default: "UTC")
}

// Runtime wraps a QuickJS engine with Vega/Vega-Lite loaded.
type Runtime struct {
	rt      *qjs.Runtime
	config  Config
	crashed bool // set after a WASM panic; further calls return errors
}

// versionIndex matches the top-level versions.json from the vendoring tool.
type versionIndex struct {
	Default  string                    `json:"default"`
	Versions map[string]versionIndexDef `json:"versions"`
}

type versionIndexDef struct {
	VegaVersion     string `json:"vegaVersion"`
	VegaLiteVersion string `json:"vegaLiteVersion"`
}

// manifest matches the JSON structure from the vendoring tool.
type manifest struct {
	VegaVersion     string           `json:"vegaVersion"`
	VegaLiteVersion string           `json:"vegaLiteVersion"`
	Modules         []manifestModule `json:"modules"`
}

type manifestModule struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Filename string `json:"filename"`
}

// New creates a new Runtime, loading all vendored JS modules and registering
// Go bridge functions.
func New(cfg Config) (*Runtime, error) {
	opts := qjs.Option{}
	if cfg.MemoryLimit > 0 {
		opts.MemoryLimit = cfg.MemoryLimit
	}
	if cfg.Timeout > 0 {
		opts.MaxExecutionTime = int(cfg.Timeout / time.Millisecond)
	}

	rt, err := qjs.New(opts)
	if err != nil {
		return nil, fmt.Errorf("aster/runtime: creating QuickJS runtime: %w", err)
	}

	if cfg.Version == "" {
		def, err := readDefaultVersion()
		if err != nil {
			rt.Close()
			return nil, err
		}
		cfg.Version = def
	}

	r := &Runtime{rt: rt, config: cfg}

	if err := r.registerBridgeFunctions(); err != nil {
		rt.Close()
		return nil, err
	}

	if err := r.installPolyfills(); err != nil {
		rt.Close()
		return nil, err
	}

	if err := r.loadModules(); err != nil {
		rt.Close()
		return nil, err
	}

	return r, nil
}

// Close releases the QuickJS runtime.
// If the WASM runtime has crashed, Close silently skips cleanup to avoid
// secondary panics.
func (r *Runtime) Close() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("aster/runtime: panic during close: %v", p)
		}
	}()

	if r.rt != nil && !r.crashed {
		r.rt.Close()
		r.rt = nil
	}
	return nil
}

// registerBridgeFunctions registers Go callbacks that bridge.js will use.
func (r *Runtime) registerBridgeFunctions() error {
	ctx := r.rt.Context()

	// __aster_load(url) → async, returns string data
	if r.config.Loader != nil {
		ctx.SetAsyncFunc("__aster_load", func(this *qjs.This) {
			args := this.Args()
			if len(args) == 0 {
				this.Promise().Reject(this.Context().NewError(errors.New("__aster_load: missing url argument")))
				return
			}
			url := args[0].String()

			// Resolve synchronously — the WASM runtime is not thread-safe,
			// so we cannot call back from a goroutine.
			loadCtx := context.Background()
			if r.config.Timeout > 0 {
				var cancel context.CancelFunc
				loadCtx, cancel = context.WithTimeout(loadCtx, r.config.Timeout)
				defer cancel()
			}
			data, err := r.config.Loader.Load(loadCtx, url)
			if err != nil {
				this.Promise().Reject(this.Context().NewError(err))
				return
			}
			this.Promise().Resolve(this.Context().NewString(string(data)))
		})

		// __aster_sanitize(uri) → sync, returns sanitized string
		ctx.SetFunc("__aster_sanitize", func(this *qjs.This) (*qjs.Value, error) {
			args := this.Args()
			if len(args) == 0 {
				return nil, fmt.Errorf("__aster_sanitize: missing uri argument")
			}
			uri := args[0].String()

			sanitizeCtx := context.Background()
			sanitized, err := r.config.Loader.Sanitize(sanitizeCtx, uri)
			if err != nil {
				return nil, err
			}
			return this.Context().NewString(sanitized), nil
		})
	}

	// __aster_measure_text(text, cssFont) → sync, returns number
	if r.config.TextMeasurer != nil {
		ctx.SetFunc("__aster_measure_text", func(this *qjs.This) (*qjs.Value, error) {
			args := this.Args()
			if len(args) < 2 {
				return nil, fmt.Errorf("__aster_measure_text: expected 2 arguments")
			}
			text := args[0].String()
			cssFont := args[1].String()

			width := r.config.TextMeasurer.MeasureText(text, cssFont)
			return this.Context().NewFloat64(width), nil
		})
	}

	return nil
}

// installPolyfills adds missing global APIs that QuickJS doesn't provide
// but that Vega/Vega-Lite expect to exist.
func (r *Runtime) installPolyfills() error {
	ctx := r.rt.Context()

	polyfills := `
		// structuredClone — Vega-Lite uses this for deep cloning specs.
		if (typeof globalThis.structuredClone === 'undefined') {
			globalThis.structuredClone = function(obj) {
				return JSON.parse(JSON.stringify(obj));
			};
		}

		// setTimeout/clearTimeout — d3-timer and vega-scenegraph reference these.
		// In our headless SVG rendering context, we provide minimal stubs.
		if (typeof globalThis.setTimeout === 'undefined') {
			const _timers = new Map();
			let _nextId = 1;
			globalThis.setTimeout = function(fn, delay) {
				const id = _nextId++;
				// Execute immediately since we're in a synchronous rendering context.
				try { fn(); } catch(e) {}
				return id;
			};
			globalThis.clearTimeout = function(id) {
				_timers.delete(id);
			};
			globalThis.setInterval = function(fn, delay) {
				return globalThis.setTimeout(fn, delay);
			};
			globalThis.clearInterval = function(id) {
				globalThis.clearTimeout(id);
			};
		}

		// requestAnimationFrame — vega-view may reference this.
		if (typeof globalThis.requestAnimationFrame === 'undefined') {
			globalThis.requestAnimationFrame = function(fn) {
				return globalThis.setTimeout(fn, 0);
			};
			globalThis.cancelAnimationFrame = function(id) {
				globalThis.clearTimeout(id);
			};
		}

		// performance.now — some modules may reference this.
		if (typeof globalThis.performance === 'undefined') {
			globalThis.performance = { now: function() { return Date.now(); } };
		}
	`

	val, err := ctx.Eval("__aster_polyfills__.js", qjs.Code(polyfills))
	if err != nil {
		return fmt.Errorf("aster/runtime: installing polyfills: %w", err)
	}
	val.Free()

	// Force UTC timezone by redirecting local Date methods to UTC equivalents.
	// QuickJS in WASM has no timezone configuration, so we polyfill it.
	tz := r.config.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if tz == "UTC" {
		utcPolyfill := `
			Date.prototype.getTimezoneOffset = function() { return 0; };
			Date.prototype.getFullYear = Date.prototype.getUTCFullYear;
			Date.prototype.getMonth = Date.prototype.getUTCMonth;
			Date.prototype.getDate = Date.prototype.getUTCDate;
			Date.prototype.getDay = Date.prototype.getUTCDay;
			Date.prototype.getHours = Date.prototype.getUTCHours;
			Date.prototype.getMinutes = Date.prototype.getUTCMinutes;
			Date.prototype.getSeconds = Date.prototype.getUTCSeconds;
			Date.prototype.getMilliseconds = Date.prototype.getUTCMilliseconds;
			Date.prototype.setFullYear = Date.prototype.setUTCFullYear;
			Date.prototype.setMonth = Date.prototype.setUTCMonth;
			Date.prototype.setDate = Date.prototype.setUTCDate;
			Date.prototype.setHours = Date.prototype.setUTCHours;
			Date.prototype.setMinutes = Date.prototype.setUTCMinutes;
			Date.prototype.setSeconds = Date.prototype.setUTCSeconds;
			Date.prototype.setMilliseconds = Date.prototype.setUTCMilliseconds;
		`
		val, err := ctx.Eval("__aster_tz__.js", qjs.Code(utcPolyfill))
		if err != nil {
			return fmt.Errorf("aster/runtime: installing UTC timezone polyfill: %w", err)
		}
		val.Free()
	}

	return nil
}

// readDefaultVersion reads the default version key from versions.json.
func readDefaultVersion() (string, error) {
	idx, err := readVersionIndex()
	if err != nil {
		return "", err
	}
	return idx.Default, nil
}

// readVersionIndex reads and parses the versions.json index.
func readVersionIndex() (*versionIndex, error) {
	data, err := fs.ReadFile(asterjs.Modules, "modules/versions.json")
	if err != nil {
		return nil, fmt.Errorf("aster/runtime: reading versions index: %w", err)
	}
	var idx versionIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("aster/runtime: parsing versions index: %w", err)
	}
	return &idx, nil
}

// AvailableVersions returns the available version set keys and their metadata.
func AvailableVersions() (map[string]struct{ VegaVersion, VegaLiteVersion string }, error) {
	idx, err := readVersionIndex()
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{ VegaVersion, VegaLiteVersion string }, len(idx.Versions))
	for k, v := range idx.Versions {
		result[k] = struct{ VegaVersion, VegaLiteVersion string }{v.VegaVersion, v.VegaLiteVersion}
	}
	return result, nil
}

// loadModules reads the manifest and loads all vendored JS modules in order.
func (r *Runtime) loadModules() error {
	// Read manifest from the versioned subdirectory.
	ver := r.config.Version
	manifestPath := "modules/" + ver + "/manifest.json"
	manifestData, err := fs.ReadFile(asterjs.Modules, manifestPath)
	if err != nil {
		return fmt.Errorf("aster/runtime: reading manifest for %s: %w", ver, err)
	}

	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return fmt.Errorf("aster/runtime: parsing manifest: %w", err)
	}

	ctx := r.rt.Context()

	// Load each module in topological order.
	for _, mod := range m.Modules {
		src, err := fs.ReadFile(asterjs.Modules, "modules/"+ver+"/"+mod.Filename)
		if err != nil {
			return fmt.Errorf("aster/runtime: reading module %s: %w", mod.Name, err)
		}

		val, err := ctx.Load(mod.Name, qjs.Code(string(src)))
		if err != nil {
			return fmt.Errorf("aster/runtime: loading module %s: %w", mod.Name, err)
		}
		val.Free()
	}

	// Load the bridge module.
	val, err := ctx.Load("bridge", qjs.Code(asterjs.BridgeJS))
	if err != nil {
		return fmt.Errorf("aster/runtime: loading bridge module: %w", err)
	}
	val.Free()

	return nil
}

// VegaToSVG renders a Vega spec to SVG.
func (r *Runtime) VegaToSVG(specJSON string) (string, error) {
	theme := "undefined"
	if r.config.Theme != "" {
		theme = "`" + r.config.Theme + "`"
	}

	script := fmt.Sprintf(`
		import { vegaToSvg } from 'bridge';
		export default await vegaToSvg(%s, %s);
	`, "`"+escapeBackticks(specJSON)+"`", theme)

	return r.evalModule(script)
}

// VegaLiteToSVG renders a Vega-Lite spec to SVG.
func (r *Runtime) VegaLiteToSVG(specJSON string) (string, error) {
	theme := "undefined"
	if r.config.Theme != "" {
		theme = "`" + r.config.Theme + "`"
	}

	script := fmt.Sprintf(`
		import { vegaLiteToSvg } from 'bridge';
		export default await vegaLiteToSvg(%s, %s);
	`, "`"+escapeBackticks(specJSON)+"`", theme)

	return r.evalModule(script)
}

// VegaLiteToVega compiles a Vega-Lite spec to a Vega spec.
func (r *Runtime) VegaLiteToVega(specJSON string) (string, error) {
	script := fmt.Sprintf(`
		import { vegaLiteToVega } from 'bridge';
		export default vegaLiteToVega(%s);
	`, "`"+escapeBackticks(specJSON)+"`")

	return r.evalModule(script)
}

var errRuntimeCrashed = errors.New("aster/runtime: WASM runtime has crashed; create a new Converter")

// evalModule evaluates an inline ES module and returns its default export as a string.
// It recovers from panics in the WASM runtime and converts them to errors.
func (r *Runtime) evalModule(script string) (result string, err error) {
	if r.crashed {
		return "", errRuntimeCrashed
	}

	defer func() {
		if p := recover(); p != nil {
			r.crashed = true
			err = fmt.Errorf("aster/runtime: WASM panic: %v", p)
		}
	}()

	ctx := r.rt.Context()
	val, err := ctx.Eval("__aster_eval__.js", qjs.Code(script), qjs.TypeModule())
	if err != nil {
		return "", fmt.Errorf("aster/runtime: eval: %w", err)
	}
	defer val.Free()

	return val.String(), nil
}

// escapeBackticks escapes backtick characters in a string for use inside
// JS template literals.
func escapeBackticks(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			result = append(result, '\\')
		}
		if s[i] == '\\' {
			result = append(result, '\\')
		}
		result = append(result, s[i])
	}
	return string(result)
}
