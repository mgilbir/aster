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

	asterjs "github.com/mgilbir/aster/internal/js"
	"github.com/mgilbir/aster/internal/quickjs"
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
	rt      *quickjs.Runtime
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
	rt, err := quickjs.New(quickjs.Config{
		MemoryLimit: cfg.MemoryLimit,
		Timeout:     cfg.Timeout,
		Bridge:      bridgeConfig(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("aster/runtime: creating QuickJS runtime: %w", err)
	}

	if cfg.Version == "" {
		def, err := readDefaultVersion()
		if err != nil {
			_ = rt.Close()
			return nil, err
		}
		cfg.Version = def
	}

	r := &Runtime{rt: rt, config: cfg}

	if err := r.installPolyfills(); err != nil {
		_ = rt.Close()
		return nil, err
	}

	if err := r.loadModules(); err != nil {
		_ = rt.Close()
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
		_ = r.rt.Close()
		r.rt = nil
	}
	return nil
}

// bridgeConfig adapts the runtime configuration to the Go callbacks that
// bridge.js reaches through the __aster_* globals. Callbacks execute
// synchronously on the engine thread, mirroring the previous behavior.
func bridgeConfig(cfg Config) quickjs.Bridge {
	var b quickjs.Bridge
	if cfg.Loader != nil {
		b.Load = func(url string) ([]byte, error) {
			loadCtx := context.Background()
			if cfg.Timeout > 0 {
				var cancel context.CancelFunc
				loadCtx, cancel = context.WithTimeout(loadCtx, cfg.Timeout)
				defer cancel()
			}
			return cfg.Loader.Load(loadCtx, url)
		}
		b.Sanitize = func(uri string) (string, error) {
			return cfg.Loader.Sanitize(context.Background(), uri)
		}
	}
	if cfg.TextMeasurer != nil {
		b.MeasureText = cfg.TextMeasurer.MeasureText
	}
	return b
}

// installPolyfills adds missing global APIs that QuickJS doesn't provide
// but that Vega/Vega-Lite expect to exist.
func (r *Runtime) installPolyfills() error {
	polyfills := `
		// structuredClone — Vega-Lite uses this for deep cloning specs.
		if (typeof globalThis.structuredClone === 'undefined') {
			globalThis.structuredClone = function(obj) {
				return JSON.parse(JSON.stringify(obj));
			};
		}

		// setTimeout/clearTimeout — d3-timer and vega-scenegraph reference these.
		// Installed unconditionally: the engine's native timers (QuickJS-NG
		// provides them) would queue callbacks that our synchronous render
		// loop never services, leaving render promises pending forever.
		//
		// Callbacks run as microtasks rather than synchronously: patterns
		// like vega-scenegraph's ready(), which polls a counter via
		// setTimeout while the counter is decremented in a promise .then,
		// need timer callbacks to interleave with promise reactions.
		// Endless timer chains are bounded by the Go-side eval watchdog.
		{
			const _timers = new Map();
			let _nextId = 1;
			globalThis.setTimeout = function(fn, delay) {
				const id = _nextId++;
				_timers.set(id, true);
				Promise.resolve().then(() => {
					if (!_timers.delete(id)) return; // cleared meanwhile
					try { fn(); } catch(e) {}
				});
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

		// requestAnimationFrame — vega-view may reference this. Same
		// microtask scheduling policy as setTimeout above.
		{
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

	if err := r.rt.EvalGlobal("__aster_polyfills__.js", polyfills); err != nil {
		return fmt.Errorf("aster/runtime: installing polyfills: %w", err)
	}

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
		if err := r.rt.EvalGlobal("__aster_tz__.js", utcPolyfill); err != nil {
			return fmt.Errorf("aster/runtime: installing UTC timezone polyfill: %w", err)
		}
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

	// Load each module in topological order.
	for _, mod := range m.Modules {
		src, err := fs.ReadFile(asterjs.Modules, "modules/"+ver+"/"+mod.Filename)
		if err != nil {
			return fmt.Errorf("aster/runtime: reading module %s: %w", mod.Name, err)
		}

		if err := r.rt.LoadModule(mod.Name, string(src)); err != nil {
			return fmt.Errorf("aster/runtime: loading module %s: %w", mod.Name, err)
		}
	}

	// Load the bridge module.
	if err := r.rt.LoadModule("bridge", asterjs.BridgeJS); err != nil {
		return fmt.Errorf("aster/runtime: loading bridge module: %w", err)
	}

	return nil
}

// VegaToSVG renders a Vega spec to SVG.
func (r *Runtime) VegaToSVG(specJSON string) (string, error) {
	spec, err := jsStringLiteral(specJSON)
	if err != nil {
		return "", err
	}
	theme, err := themeLiteral(r.config.Theme)
	if err != nil {
		return "", err
	}

	script := fmt.Sprintf(`
		import { vegaToSvg } from 'bridge';
		export default await vegaToSvg(%s, %s);
	`, spec, theme)

	return r.evalModule(script)
}

// VegaLiteToSVG renders a Vega-Lite spec to SVG.
func (r *Runtime) VegaLiteToSVG(specJSON string) (string, error) {
	spec, err := jsStringLiteral(specJSON)
	if err != nil {
		return "", err
	}
	theme, err := themeLiteral(r.config.Theme)
	if err != nil {
		return "", err
	}

	script := fmt.Sprintf(`
		import { vegaLiteToSvg } from 'bridge';
		export default await vegaLiteToSvg(%s, %s);
	`, spec, theme)

	return r.evalModule(script)
}

// VegaLiteToVega compiles a Vega-Lite spec to a Vega spec.
func (r *Runtime) VegaLiteToVega(specJSON string) (string, error) {
	spec, err := jsStringLiteral(specJSON)
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf(`
		import { vegaLiteToVega } from 'bridge';
		export default vegaLiteToVega(%s);
	`, spec)

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

	result, err = r.rt.EvalModule(script)
	if err != nil {
		return "", fmt.Errorf("aster/runtime: eval: %w", err)
	}
	return result, nil
}

// jsStringLiteral encodes s as a JavaScript string literal (double-quoted).
// It is used to pass spec/theme text into generated module source as inert
// data rather than as code: encoding.json escapes quotes, backslashes,
// control characters, and the U+2028/U+2029 line separators, and HTML-escapes
// <, >, & — so no byte of s (backticks, ${...}, quotes, newlines) can break
// out of the literal or be interpreted as a template interpolation.
func jsStringLiteral(s string) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("aster/runtime: encoding string literal: %w", err)
	}
	return string(b), nil
}

// themeLiteral encodes the configured theme JSON as a JS string literal, or
// returns the JS keyword "undefined" when no theme is set. bridge.js
// JSON.parses the resulting string.
func themeLiteral(theme string) (string, error) {
	if theme == "" {
		return "undefined", nil
	}
	return jsStringLiteral(theme)
}
