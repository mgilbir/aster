// Package aster converts Vega and Vega-Lite visualization specs to SVG, PNG
// and vector PDF with an engine written entirely in Go: no JavaScript
// runtime, no WebAssembly and no CGO.
//
// Basic usage:
//
//	c, err := aster.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer c.Close()
//
//	svg, err := c.VegaLiteToSVG(specJSON)
//	png, err := c.VegaLiteToPNG(specJSON)
//	pdf, err := c.VegaLiteToPDF(specJSON)
package aster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fontsubset"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/pngopt"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/svg"
	"github.com/mgilbir/aster/internal/svgpdf"
	"github.com/mgilbir/aster/internal/text"
	"github.com/mgilbir/aster/internal/transforms"
	"github.com/mgilbir/aster/internal/transforms/wordcloud"
	"github.com/mgilbir/aster/internal/vega"
	"github.com/mgilbir/aster/internal/vegalite"
)

// Converter renders Vega/Vega-Lite specs to SVG, PNG and PDF.
//
// A Converter is safe for concurrent use by multiple goroutines: any number of
// rendering calls (VegaToSVG, VegaLiteToPNG, SVGToPDF, ...) may run at once on
// one Converter, and each call behaves exactly as it would alone. Its
// configuration is immutable after New, every render owns its own state
// (dataflow, scenegraph, random generator, budgets, deadline), and the state
// the calls share (font shaping caches, locale and colour-scheme tables,
// compiled-expression caches) is either immutable or synchronized. Sharing one
// Converter is also cheaper than creating one per goroutine, because the fonts
// are loaded and their caches filled once. A Loader passed to WithLoader is
// called from several goroutines at once, so it must be safe for concurrent
// use as well; the loaders in this package are.
//
// Close may be called at any time from any goroutine, an error handler or a
// Loader of a call included: it cancels the calls in flight (they fail with
// context.Canceled), waits for them to finish, closes the Loader if it
// implements io.Closer, and makes every later call fail with ErrClosed.
// Closing more than once is harmless. To cancel one call, give it
// WithContext.
type Converter struct {
	cfg      *config
	lim      callLimits // every bound of a call, from cfg.memoryLimit
	location *time.Location
	theme    jsval.Value // parsed WithTheme config; Undefined when none
	vl       string      // vegalite compiler version (vegalite.Version64, ...)
	// loader is cfg.loader as every stage calls it: as a callback of the
	// call (see lifecycle).
	loader Loader

	life      lifecycle          // the calls in flight, for Close
	base      context.Context    // canceled by Close; parent of every call's context
	cancelAll context.CancelFunc // cancels base

	// The fonts, one set for every stage (C71): the layout measurer, the
	// PNG shaper (an exact-advance view of it) and the PDF writer's
	// measurer. Built in New when fonts are configured, else on first use.
	fontsOnce sync.Once
	fonts     *text.Measurer
	shaper    raster.Shaper
	fontsErr  error
}

// supportedVersions are the Vega-Lite versions this engine compiles, sorted
// by key. Every version renders with the Vega 6.4
// runtime; VegaVersion says whose behaviour rendering follows.
var supportedVersions = []VersionInfo{
	{Key: "vl5_8", VegaVersion: "6.4.0", VegaLiteVersion: "5.8.0"},
	{Key: "vl6_4", VegaVersion: "6.4.0", VegaLiteVersion: "6.4.3"},
}

// compilerVersion maps a version set key to the vegalite compiler's version.
var compilerVersion = map[string]string{
	"vl5_8": vegalite.Version58,
	"vl6_4": vegalite.Version64,
}

// VersionInfo describes an available Vega-Lite version set.
type VersionInfo struct {
	Key             string // version set key, e.g. "vl6_4"
	VegaVersion     string // Vega version whose behaviour the engine follows
	VegaLiteVersion string // Vega-Lite version the compiler follows
}

// AvailableVersions reports the Vega-Lite compilers bundled in this build,
// sorted by key. WithVegaLiteVersion takes a VegaLiteVersion ("6.4.3") or
// its major.minor ("6.4"). The error is always nil: the list is compiled in.
// It is kept in the signature so that existing callers compile.
func AvailableVersions() ([]VersionInfo, error) {
	return append([]VersionInfo(nil), supportedVersions...), nil
}

// New creates a Converter with the given options. It fails for an option
// that is not valid: an unknown Vega-Lite version (WithVegaLiteVersion), an
// unknown time zone or "Local" (WithTimezone), a theme that is not a JSON
// object (WithTheme), a nil pointer as the Loader (WithLoader), a negative
// face index (WithFontFace), a zero WithPixelWorkLimit, a font that does
// not load (WithFont, WithFontFace, WithFontInstance), and a family with no
// font given to WithFontFallback or WithDefault*Family. The fonts are loaded here when any
// font option is given; with the embedded fonts alone, New loads nothing and
// they load on first use.
func New(opts ...Option) (*Converter, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if err := errors.Join(cfg.errs...); err != nil {
		return nil, err
	}
	vlVersion, err := compilerFor(cfg.vegaLiteVersion)
	if err != nil {
		return nil, err
	}
	loc := time.UTC
	switch tz := cfg.timezone; tz {
	case "", "UTC":
	case "Local":
		// The host's zone would make the output depend on the machine it
		// renders on (C79).
		return nil, errors.New(`aster: unsupported timezone "Local": output would depend on the host's zone; name the zone, such as "Europe/Madrid"`)
	default:
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("aster: unsupported timezone %q: %w", tz, err)
		}
		loc = l
	}
	if cfg.loader == nil {
		cfg.loader = DenyLoader{}
	} else if isNilPointer(cfg.loader) {
		return nil, fmt.Errorf("aster: WithLoader: a nil %T (pass nil for the default, DenyLoader)", cfg.loader)
	}
	c := &Converter{cfg: cfg, lim: deriveLimits(cfg.memoryLimit, cfg.pixelWork), location: loc, vl: vlVersion}
	c.loader = trackedLoader{c, cfg.loader}
	if cfg.theme != "" {
		theme, err := jsval.ParseJSONString(cfg.theme)
		if err != nil {
			return nil, fmt.Errorf("aster: invalid theme config: %w", err)
		}
		if !theme.IsObj() {
			return nil, errors.New("aster: invalid theme config: not a JSON object")
		}
		c.theme = theme
	}
	if cfg.fontsGiven() {
		// A font that does not load, or a family with none, fails New rather
		// than every render; the set built is the one every render uses.
		if _, _, err := c.fontSet(); err != nil {
			return nil, err
		}
	}
	c.base, c.cancelAll = context.WithCancel(context.Background())
	return c, nil
}

// compilerFor is the Vega-Lite compiler WithVegaLiteVersion(v) selects: v is
// a set's major.minor ("6.4") or the full Vega-Lite version AvailableVersions
// reports ("6.4.3"); "" is the default.
func compilerFor(v string) (string, error) {
	if v == "" {
		return vegalite.Version64, nil
	}
	var names []string
	for _, s := range supportedVersions {
		minor := strings.ReplaceAll(strings.TrimPrefix(s.Key, "vl"), "_", ".")
		if v == minor || v == s.VegaLiteVersion {
			return compilerVersion[s.Key], nil
		}
		names = append(names, fmt.Sprintf("%s (Vega-Lite %s)", minor, s.VegaLiteVersion))
	}
	return "", fmt.Errorf("aster: unknown Vega-Lite version %q (available: %s)", v, strings.Join(names, ", "))
}

// isNilPointer reports whether l is a typed nil: an interface holding a nil
// pointer, map, slice, channel or function, which is not the nil that
// selects the default and panics at first use (C76).
func isNilPointer(l Loader) bool {
	v := reflect.ValueOf(l)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// recoverInto turns a panic into an error. Every layer returns errors for bad
// input, so a panic is a bug — but it must not take the host process down.
func recoverInto(err *error) {
	if r := recover(); r != nil {
		// A budget that stops work deep inside a stage (the geographic path
		// walk, text shaping) panics with its error: it is a limit or the
		// call's context, not a bug.
		if s, ok := r.(*budget.Stop); ok {
			*err = fmt.Errorf("aster: %w", s.Err)
			return
		}
		if e, ok := r.(error); ok && errors.Is(e, ErrLimit) {
			*err = fmt.Errorf("aster: %w", e)
			return
		}
		*err = fmt.Errorf("aster: internal error: %v", r)
	}
}

// stagesKey carries a stage timer in a call's context; only the tests set it
// (export_test.go), to time each stage of a render.
type stagesKey struct{}

func stagesFrom(ctx context.Context) func(string, time.Duration) {
	f, _ := ctx.Value(stagesKey{}).(func(string, time.Duration))
	return f
}

// timed runs f and reports its duration as stage, when a timer is set.
func timed[T any](ctx context.Context, stage string, f func() (T, error)) (T, error) {
	st := stagesFrom(ctx)
	if st == nil {
		return f()
	}
	t0 := time.Now()
	v, err := f()
	st(stage, time.Since(t0))
	return v, err
}

// timedMeasurer reports the time spent measuring text as the "text" stage.
type timedMeasurer struct {
	m  scene.TextMeasurer
	st func(string, time.Duration)
}

func (t timedMeasurer) MeasureText(text, font string) float64 {
	t0 := time.Now()
	w := t.m.MeasureText(text, font)
	t.st("text", time.Since(t0))
	return w
}

// signalWritesKey carries a call's signal writes (WithSignal) in its context,
// to the render.
type signalWritesKey struct{}

func signalWritesFrom(ctx context.Context) []vega.SignalWrite {
	w, _ := ctx.Value(signalWritesKey{}).([]vega.SignalWrite)
	return w
}

// withRender returns ctx carrying the writes of the signals rc sets, their
// values turned into the JSON values they encode to, and installs rc's error
// handler in the call's report.
func withRender(ctx context.Context, rc renderConfig) (context.Context, error) {
	if rc.handler != nil {
		reportFrom(ctx).handler = rc.handler
	}
	if len(rc.signals) == 0 {
		return ctx, nil
	}
	ws := make([]vega.SignalWrite, len(rc.signals))
	for i, s := range rc.signals {
		b, err := json.Marshal(s.value)
		if err != nil {
			return nil, fmt.Errorf("aster: signal %q: %w", s.name, err)
		}
		v, err := jsval.ParseJSON(b)
		if err != nil {
			return nil, fmt.Errorf("aster: signal %q: %w", s.name, err)
		}
		ws[i] = vega.SignalWrite{Name: s.name, Value: v}
	}
	return context.WithValue(ctx, signalWritesKey{}, ws), nil
}

func renderOptions(opts []RenderOption) renderConfig {
	var rc renderConfig
	for _, o := range opts {
		o.applyRender(&rc)
	}
	return rc
}

func pngRenderOptions(opts []PNGOption) renderConfig {
	cfg := defaultPNGConfig()
	for _, o := range opts {
		o.applyPNG(cfg)
	}
	return cfg.render
}

func pdfRenderOptions(opts []PDFOption) renderConfig {
	var cfg pdfConfig
	for _, o := range opts {
		o.applyPDF(&cfg)
	}
	return cfg.render
}

// VegaToSVG renders a Vega spec (JSON) to an SVG string.
func (c *Converter) VegaToSVG(spec []byte, opts ...RenderOption) (string, error) {
	return call(c, renderOptions(opts), func(ctx context.Context) (string, error) { return c.vegaSVG(ctx, spec) })
}

// VegaLiteToSVG renders a Vega-Lite spec (JSON) to an SVG string.
func (c *Converter) VegaLiteToSVG(spec []byte, opts ...RenderOption) (string, error) {
	return call(c, renderOptions(opts), func(ctx context.Context) (string, error) { return c.vegaLiteSVG(ctx, spec) })
}

func (c *Converter) vegaSVG(ctx context.Context, spec []byte) (string, error) {
	if err := c.checkSpecSize(spec); err != nil {
		return "", err
	}
	v, err := timed(ctx, "json", func() (jsval.Value, error) { return jsval.ParseJSONContext(ctx, spec, c.lim.vega.MaxParseBytes) })
	if err != nil {
		return "", c.stageErr(ctx, "parsing Vega spec", err)
	}
	return c.renderSVG(ctx, v)
}

func (c *Converter) vegaLiteSVG(ctx context.Context, spec []byte) (string, error) {
	vg, err := c.compileVegaLite(ctx, spec)
	if err != nil {
		return "", err
	}
	return c.renderSVG(ctx, vg)
}

// renderSVG runs a Vega spec and serializes the resulting scenegraph.
func (c *Converter) renderSVG(ctx context.Context, spec jsval.Value) (out string, err error) {
	defer recoverInto(&err)
	fonts, shaper, err := c.fontSet()
	if err != nil {
		return "", err
	}
	var m *text.Measurer
	if c.cfg.textMeasure {
		m = fonts
	}
	shaping := text.ShapingBudgetFrom(ctx)
	m = m.Bounded(shaping)
	opts := vega.Options{
		Loader:      c.loader,
		Location:    c.location,
		Projections: c.cfg.projections,
		Now:         c.cfg.now,
		// Set only by the tests' signal sweep (export_test.go).
		SignalWrites: signalWritesFrom(ctx),
		Config:       c.theme,
		Limits:       c.lim.vega,
		// Seeded per render so sample, jitter and
		// bootstrap confidence intervals are reproducible. 123456789 is the
		// seed Vega-Lite's own example renders use (vg2svg --seed).
		Random: transforms.LCG(randomSeed),
	}
	// One canvas context measures for the layout and the SVG writer, as
	// vega-scenegraph's single context does.
	var canvas *text.CanvasContext
	if m != nil {
		canvas = text.NewCanvasContext(m)
		opts.TextMeasurer = canvas
		opts.WordcloudText = wordcloud.NewCanvasRenderer(m)
	}
	if c.cfg.wordcloudBoxForTest {
		opts.WordcloudText = wordcloud.BoxRenderer{}
	}
	if st := stagesFrom(ctx); st != nil {
		opts.Stages = st
		if m != nil {
			opts.TextMeasurer = timedMeasurer{m, st}
		}
	}
	// The label transform paints the marks it avoids, text included, with
	// the PNG shaper.
	opts.Shaper = raster.BoundShaper(shaper, shaping)
	rep := reportFrom(ctx)
	opts.Report = rep.vega
	res, err := vega.Render(ctx, spec, opts)
	if err != nil {
		return "", c.stageErr(ctx, "rendering Vega", err)
	}
	images := imagesFrom(ctx)
	// The images draw from what the data left of the call's allowance.
	images.chargeData(res.LoadedBytes)
	so := svg.Options{
		Width:  res.Width,
		Height: res.Height,
		Origin: res.Origin,
		// An SVG past this size is not a chart anybody can use.
		MaxBytes: c.lim.svgBytes,
	}
	if res.HasBackground {
		so.Background = res.Background
	}
	if canvas != nil {
		so.Measurer = canvas
	}
	// Vega sanitizes every href through the view's loader; the Loader decides
	// which links a chart may carry, and a rejected URL renders no link.
	// Vega's vega-loader allow-list (no javascript: and the like) applies as
	// well: a permissive Loader cannot make a script URL a link.
	// A dropped link is a Warning: the chart is drawn whole without it.
	// Each URL is sanitized once for the call, however many items link it.
	var hookErr error
	dropLink := func(uri, why string) ([]svg.HrefAttr, bool) {
		if e := rep.warn(fmt.Sprintf("link %q dropped: %s", uri, why)); e != nil && hookErr == nil {
			hookErr = e
		}
		return nil, false
	}
	so.Href = func(uri string) ([]svg.HrefAttr, bool) {
		if _, ok := svg.SanitizeURL(uri, svg.URLOptions{}); !ok {
			return dropLink(uri, "its scheme is not allowed")
		}
		href, err := images.link(ctx, uri)
		if errors.Is(err, ErrLimit) {
			if hookErr == nil {
				hookErr = err // a limit is not a Warning
			}
			return nil, false
		}
		if err != nil {
			return dropLink(uri, err.Error())
		}
		if _, ok := svg.SanitizeURL(href, svg.URLOptions{}); !ok {
			return dropLink(uri, "its scheme is not allowed")
		}
		return []svg.HrefAttr{{Name: "xlink:href", Value: href}}, true
	}
	// Image URLs go through the Loader like every other resource (upstream's
	// ResourceLoader.loadImage sanitizes with context "image"); a rejected URL
	// renders what a failed image load does: no source, no size.
	// An item missing a width or a height takes it from the image, as
	// upstream's SVG renderer does once the image has loaded; the image is
	// fetched for that through the call's table (callImages), which PNG and
	// PDF output then draw from, by the href written here.
	// A data: URI of an image the engine draws (PNG, JPEG, GIF) is part of
	// the specification, not a resource to fetch, so it does not go through
	// the Loader; the rasterizer and the PDF writer decode it within the image
	// limits. Any other data: URL is the Loader's to allow, as before.
	so.Image = func(url string, size bool) svg.ImageInfo {
		if url == "" {
			return svg.ImageInfo{}
		}
		r, err := images.resolve(ctx, url)
		if err == nil && r.err != nil {
			err = r.err
		}
		if err != nil {
			rep.image(url, err)
			return svg.ImageInfo{}
		}
		src, ok := svg.SanitizeURL(r.u, svg.URLOptions{})
		if !ok {
			rep.image(url, errors.New("its URL scheme is not allowed"))
			return svg.ImageInfo{}
		}
		if err := images.written(ctx, src, r); err != nil {
			rep.image(url, err)
			return svg.ImageInfo{}
		}
		info := svg.ImageInfo{Src: src}
		if size {
			info.Width, info.Height, _ = images.size(ctx, r)
		}
		return info
	}
	out, err = timed(ctx, "svg", func() (string, error) { return svg.Render(ctx, res.Scenegraph, so) })
	if err == nil {
		err = hookErr
	}
	if err == nil {
		err = rep.flushImages()
	}
	if err != nil {
		return "", c.stageErr(ctx, "writing SVG", err)
	}
	return out, nil
}

// checkSpecSize refuses a specification too large for WithMemoryLimit. Every
// entry point that takes a specification passes through it, whatever the form
// the caller held it in: they all end up as the JSON bytes parsed here.
func (c *Converter) checkSpecSize(spec []byte) error {
	limit := c.lim.specBytes
	if limit == 0 {
		return nil
	}
	if uint64(len(spec)) > limit {
		return fmt.Errorf("aster: %w: the specification is too large for the memory limit (%d bytes, at most %d)", ErrLimit, len(spec), limit)
	}
	return nil
}

// randomSeed seeds Vega's random() and every transform that samples.
const randomSeed = 123456789

// fontSet is the Converter's fonts, built once (C71), and the PNG shaper over
// them: the measurer lays text out (unless WithTextMeasurement(false)) and
// shapes PDF text with the metric model layout uses; the shaper is a view of
// the same fonts at their exact advances, as resvg places glyphs. No stage
// parses a font or scans the system's again.
func (c *Converter) fontSet() (*text.Measurer, raster.Shaper, error) {
	c.fontsOnce.Do(func() {
		if c.cfg.fontFailForTest {
			c.fontsErr = errors.New("aster: loading fonts: font set failure for test")
			return
		}
		opts := c.fontOptions()
		if !c.cfg.harfBuzzText {
			opts = append(opts, text.WithExactAdvances())
			if c.cfg.pangoText != 0 {
				opts = append(opts, text.WithPangoAdvances(c.cfg.pangoText == 2))
			}
		}
		m, err := text.New(opts...)
		if err != nil {
			c.fontsErr = fmt.Errorf("aster: loading fonts: %w", err)
			return
		}
		c.fonts, c.shaper = m, raster.NewShaperFromMeasurer(m.Exact())
	})
	return c.fonts, c.shaper, c.fontsErr
}

// fontOptions are the font registrations and generic-family mappings shared
// by layout measurement and PNG text, so both resolve families identically.
func (c *Converter) fontOptions() []text.Option {
	var opts []text.Option
	if c.cfg.systemFonts {
		opts = append(opts, text.WithSystemFonts())
	}
	for _, f := range c.cfg.fonts {
		switch {
		case f.axes != nil:
			opts = append(opts, text.WithFontInstance(f.family, f.data, f.axes))
		case f.face >= 0:
			opts = append(opts, text.WithFontFace(f.family, f.data, f.face))
		default:
			opts = append(opts, text.WithFont(f.family, f.data))
		}
	}
	for _, p := range c.cfg.palettes {
		opts = append(opts, text.WithFontPalette(p.family, p.index))
	}
	if c.cfg.fallbackSet {
		opts = append(opts, text.WithFallbackFamilies(c.cfg.fallbacks...))
	}
	if f := c.cfg.defaultFontFamily; f != "" {
		opts = append(opts, text.WithDefaultFontFamily(f))
	}
	if f := c.cfg.defaultSerifFamily; f != "" {
		opts = append(opts, text.WithDefaultSerifFamily(f))
	}
	if f := c.cfg.defaultMonospaceFamily; f != "" {
		opts = append(opts, text.WithDefaultMonospaceFamily(f))
	}
	return opts
}

// VegaLiteToVega compiles a Vega-Lite spec (JSON) to a full Vega spec (JSON).
//
// Of the RenderOptions, WithContext and WithErrorHandler apply (signals are
// set when a chart renders): the handler is told of each warning the
// Vega-Lite compiler logs, as an aster.Warning, and may fail the call for it.
// Without a handler, warnings are dropped.
func (c *Converter) VegaLiteToVega(spec []byte, opts ...RenderOption) ([]byte, error) {
	rc := renderOptions(opts)
	rc.signals = nil
	return call(c, rc, func(ctx context.Context) ([]byte, error) {
		vg, err := c.compileVegaLite(ctx, spec)
		if err != nil {
			return nil, err
		}
		return jsval.AppendJSON(nil, vg), nil
	})
}

// compileVegaLite parses a Vega-Lite spec and compiles it to Vega with the
// converter's theme and time zone.
func (c *Converter) compileVegaLite(ctx context.Context, spec []byte) (jsval.Value, error) {
	if err := c.checkSpecSize(spec); err != nil {
		return jsval.Undefined, err
	}
	v, err := timed(ctx, "json", func() (jsval.Value, error) { return jsval.ParseJSONContext(ctx, spec, c.lim.vega.MaxParseBytes) })
	if err != nil {
		return jsval.Undefined, c.stageErr(ctx, "parsing Vega-Lite spec", err)
	}
	vg, err := timed(ctx, "compile", func() (jsval.Value, error) {
		opts := vegalite.Options{Config: c.theme, Location: c.location, Version: c.vl, Context: ctx, MaxViews: c.lim.vlViews}
		// The compiler's warnings go to the call's report, which tells
		// WithErrorHandler of them as Warnings.
		if rep := reportFrom(ctx); rep != nil {
			opts.Warn = rep.warn
		}
		return vegalite.Compile(v, opts)
	})
	if err != nil {
		return jsval.Undefined, c.stageErr(ctx, "compiling Vega-Lite", err)
	}
	return vg, nil
}

// VegaToPNG renders a Vega spec (JSON) to a PNG image.
func (c *Converter) VegaToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	return call(c, pngRenderOptions(opts), func(ctx context.Context) ([]byte, error) {
		svg, err := c.vegaSVG(ctx, spec)
		if err != nil {
			return nil, err
		}
		return c.svgToPNG(ctx, svg, opts)
	})
}

// VegaLiteToPNG renders a Vega-Lite spec (JSON) to a PNG image.
func (c *Converter) VegaLiteToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	return call(c, pngRenderOptions(opts), func(ctx context.Context) ([]byte, error) {
		svg, err := c.vegaLiteSVG(ctx, spec)
		if err != nil {
			return nil, err
		}
		return c.svgToPNG(ctx, svg, opts)
	})
}

// SVGToPNG rasterizes an SVG string to a PNG image.
func (c *Converter) SVGToPNG(svg string, opts ...PNGOption) ([]byte, error) {
	return call(c, svgInput(pngRenderOptions(opts)), func(ctx context.Context) ([]byte, error) {
		return c.svgToPNG(ctx, svg, opts)
	})
}

func (c *Converter) svgToPNG(ctx context.Context, svg string, opts []PNGOption) (out []byte, err error) {
	defer recoverInto(&err)
	cfg := defaultPNGConfig()
	for _, opt := range opts {
		opt.applyPNG(cfg)
	}
	if cfg.render.handler != nil {
		reportFrom(ctx).handler = cfg.render.handler
	}
	if !(cfg.scale > 0) || math.IsInf(cfg.scale, 1) {
		return nil, fmt.Errorf("aster: invalid PNG scale %v (must be a positive, finite number)", cfg.scale)
	}
	img, out, err := c.rasterize(ctx, svg, cfg.scale)
	if err != nil {
		return nil, err
	}
	// Post-processing works on the image the rasterizer drew, not on its
	// PNG decoded again, under the call's context and ledger like every
	// stage (C29).
	switch {
	case cfg.quantizeColors > 0:
		out, err = pngopt.QuantizeOrRecodeImage(ctx, img, out, cfg.quantizeColors)
	case cfg.recode:
		out, err = pngopt.RecodeImage(ctx, img, out)
	}
	if err != nil {
		return nil, c.stageErr(ctx, "optimizing PNG", err)
	}
	return out, nil
}

// rasterize draws svg and returns the image and its PNG encoding.
func (c *Converter) rasterize(ctx context.Context, svg string, scale float64) (*image.NRGBA, []byte, error) {
	_, sh, err := c.fontSet()
	if err != nil {
		return nil, nil, err
	}
	sh = raster.BoundShaper(sh, text.ShapingBudgetFrom(ctx))
	var img *image.NRGBA
	out, err := timed(ctx, "png", func() ([]byte, error) {
		var err error
		img, err = raster.Render([]byte(svg), raster.Options{Scale: scale, Shaper: sh, Context: ctx, Limits: c.lim.rasterAt(scale), Images: imagesFrom(ctx).load, ImageError: imagesFrom(ctx).imageError(ctx)})
		if err != nil {
			return nil, err
		}
		// Encoded under ctx, polled per row, as every stage is.
		var buf bytes.Buffer
		buf.Grow(len(img.Pix) / 8)
		if err := pngopt.Encode(ctx, &buf, img); err != nil {
			return nil, fmt.Errorf("encoding: %w", err) // stageErr names the stage
		}
		return buf.Bytes(), nil
	})
	if err == nil {
		err = reportFrom(ctx).flushImages()
	}
	if err != nil {
		return nil, nil, c.stageErr(ctx, "rendering PNG", err)
	}
	return img, out, nil
}

// PDFTextMode selects how text is represented in PDF output.
type PDFTextMode int

const (
	// PDFTextEmbed (the default) emits real PDF text with subset TrueType
	// fonts embedded: only the glyphs a chart uses ship, once, in the font
	// program, and each occurrence costs two bytes. Output is self-contained
	// and text is selectable and searchable. Text whose font cannot be
	// embedded (CFF/OTF outlines, unloadable system fonts) falls back to
	// glyph outlines automatically.
	PDFTextEmbed PDFTextMode = iota
	// PDFTextNamed emits the same PDF text structure without embedding the
	// font program: fonts are referenced by name only, so the output is as
	// small as it gets. Glyphs are addressed by the IDs of the exact font
	// file used at generation time — the consuming pipeline must embed that
	// same font file when assembling the final document, or viewers will
	// substitute a different font and may draw the wrong glyphs. Use this
	// when generating many charts whose fonts are embedded once at assembly
	// time.
	PDFTextNamed
	// PDFTextOutlines converts every glyph occurrence to filled path
	// outlines. No fonts are referenced or embedded at all; output is much
	// larger and text is not selectable, but nothing can go wrong with font
	// handling downstream.
	PDFTextOutlines
)

// PDFOption configures a single PDF render operation: WithPDFText,
// WithContext, WithErrorHandler, and WithSignal for the Vega and Vega-Lite
// methods.
type PDFOption interface{ applyPDF(*pdfConfig) }

type pdfOption func(*pdfConfig)

func (f pdfOption) applyPDF(c *pdfConfig) { f(c) }

type pdfConfig struct {
	text   PDFTextMode
	render renderConfig
}

// WithPDFText selects how text is represented in the PDF; see the
// PDFTextMode constants. The default is PDFTextEmbed.
func WithPDFText(mode PDFTextMode) PDFOption {
	return pdfOption(func(c *pdfConfig) {
		c.text = mode
	})
}

// FontUsage reports one font face a rendered PDF uses: PostScriptName string
// (the /BaseFont PDFTextNamed output references), Source []byte (a copy of the
// font program it was shaped with, which may be a registered, system or
// embedded font) and GIDs []uint16 (the glyph IDs of Source it references,
// sorted and deduplicated).
//
// It enables higher-level, file-level font embedding: render many charts with
// WithPDFText(PDFTextNamed) (which does not embed fonts), union the reported
// GIDs per face across all of them, build one shared subset with SubsetFont,
// and embed that single subset into the composed document. Compared with
// PDFTextEmbed (a subset per chart), this stores each font's glyphs once no
// matter how many charts share them.
type FontUsage = svgpdf.FontUsage

// VegaToPDF renders a Vega spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	out, err := c.toPDF(opts, false, func(ctx context.Context) (string, error) { return c.vegaSVG(ctx, spec) })
	return out.pdf, err
}

// VegaLiteToPDF renders a Vega-Lite spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaLiteToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	out, err := c.toPDF(opts, false, func(ctx context.Context) (string, error) { return c.vegaLiteSVG(ctx, spec) })
	return out.pdf, err
}

// pdfOut is a PDF and, when asked for, its font usage.
type pdfOut struct {
	pdf  []byte
	uses []FontUsage
}

// toPDF is one PDF call: the SVG svg makes (from a specification, or given),
// converted.
func (c *Converter) toPDF(opts []PDFOption, usage bool, svg func(ctx context.Context) (string, error)) (pdfOut, error) {
	return call(c, pdfRenderOptions(opts), func(ctx context.Context) (pdfOut, error) {
		s, err := svg(ctx)
		if err != nil {
			return pdfOut{}, err
		}
		pdf, uses, err := c.svgToPDF(ctx, s, opts, usage)
		return pdfOut{pdf, uses}, err
	})
}

// SVGToPDF converts an SVG string (as produced by the Vega SVG renderer) to
// a single-page vector PDF suitable for direct embedding in LaTeX documents
// via \includegraphics. Text handling is controlled by WithPDFText: by
// default the fonts a chart uses are subset and embedded, so the output is
// self-contained and text is selectable.
//
// Only the SVG subset that Vega emits is supported, images (fetched through
// the Loader, as PNG output fetches them) and gradients included. Unsupported
// constructs (embedded CSS, patterns, ...) return a descriptive error rather
// than a silently incomplete chart; callers can fall back to SVGToPNG.
func (c *Converter) SVGToPDF(svg string, opts ...PDFOption) ([]byte, error) {
	out, err := c.svgPDF(svg, opts, false)
	return out.pdf, err
}

// svgPDF is one call converting SVG input to PDF.
func (c *Converter) svgPDF(svg string, opts []PDFOption, usage bool) (pdfOut, error) {
	return call(c, svgInput(pdfRenderOptions(opts)), func(ctx context.Context) (pdfOut, error) {
		pdf, uses, err := c.svgToPDF(ctx, svg, opts, usage)
		return pdfOut{pdf, uses}, err
	})
}

// SVGToPDFUsage is SVGToPDF plus the per-face glyph usage of the produced PDF
// (see FontUsage). It is intended with WithPDFText(PDFTextNamed): the returned
// usage is what a caller needs to embed one shared subset across many charts.
func (c *Converter) SVGToPDFUsage(svg string, opts ...PDFOption) ([]byte, []FontUsage, error) {
	out, err := c.svgPDF(svg, opts, true)
	return out.pdf, out.uses, err
}

// svgToPDF converts svg, reporting its font usage when usage is set: usage
// copies the font programs it reports.
func (c *Converter) svgToPDF(ctx context.Context, svg string, opts []PDFOption, usage bool) (pdf []byte, uses []FontUsage, err error) {
	defer func() {
		if err != nil {
			pdf, uses = nil, nil
		}
	}()
	defer recoverInto(&err)
	cfg := &pdfConfig{text: PDFTextEmbed}
	for _, opt := range opts {
		opt.applyPDF(cfg)
	}
	if cfg.render.handler != nil {
		reportFrom(ctx).handler = cfg.render.handler
	}
	var mode svgpdf.TextMode
	switch cfg.text {
	case PDFTextEmbed:
		mode = svgpdf.TextEmbed
	case PDFTextNamed:
		mode = svgpdf.TextNamed
	case PDFTextOutlines:
		mode = svgpdf.TextOutlines
	default:
		return nil, nil, fmt.Errorf("aster: unknown PDF text mode %d", cfg.text)
	}
	m, _, err := c.fontSet()
	if err != nil {
		return nil, nil, err
	}
	m = m.Bounded(text.ShapingBudgetFrom(ctx))
	t0 := time.Now()
	popts := svgpdf.Options{Text: mode, Context: ctx, Images: imagesFrom(ctx).load, ImageError: imagesFrom(ctx).imageError(ctx), Limits: svgpdf.Limits{MaxImagePixels: c.imagePixels(), MaxPixelOps: c.lim.pixelOps}}
	if usage {
		pdf, uses, err = svgpdf.ConvertWithUsage(svg, m, popts)
	} else {
		pdf, err = svgpdf.Convert(svg, m, popts)
	}
	if st := stagesFrom(ctx); st != nil {
		st("pdf", time.Since(t0))
	}
	if err == nil {
		err = reportFrom(ctx).flushImages()
	}
	if err != nil {
		return nil, nil, c.stageErr(ctx, "rendering PDF", err)
	}
	return pdf, uses, nil
}

// VegaToPDFUsage renders a Vega spec (JSON) to a vector PDF and reports its
// per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	out, err := c.toPDF(opts, true, func(ctx context.Context) (string, error) { return c.vegaSVG(ctx, spec) })
	return out.pdf, out.uses, err
}

// VegaLiteToPDFUsage renders a Vega-Lite spec (JSON) to a vector PDF and
// reports its per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaLiteToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	out, err := c.toPDF(opts, true, func(ctx context.Context) (string, error) { return c.vegaLiteSVG(ctx, spec) })
	return out.pdf, out.uses, err
}

// SubsetFont builds a TrueType subset of source containing gids, preserving the
// source's original glyph numbering. Because numbering is preserved, the subset
// resolves content that references glyphs by original GID through an Identity
// CIDToGIDMap — as PDFTextNamed output does — so it can be embedded once at a
// higher level and shared by every chart that references the same face.
//
// It returns the subset program and the source's PostScript name (matching the
// PostScriptName reported by FontUsage and the /BaseFont PDFTextNamed writes,
// including the shared fallback for fonts that carry no PostScript name).
// Only fonts with TrueType glyph outlines can be subset; others return an
// error. Glyph IDs outside the font's range are ignored — GIDs sourced from
// FontUsage are always in range, but hand-built inputs are not validated.
func SubsetFont(source []byte, gids []uint16) (subset []byte, postScriptName string, err error) {
	f, err := fontsubset.Parse(source)
	if err != nil {
		return nil, "", fmt.Errorf("aster: parse font: %w", err)
	}
	name := svgpdf.PostScriptNameOrFallback(f.PostScriptName())
	if err := f.SubsetErr(); err != nil {
		return nil, name, fmt.Errorf("aster: font %q cannot be subset: %w", name, err)
	}
	set := make(map[uint16]bool, len(gids))
	for _, g := range gids {
		set[g] = true
	}
	sub, err := f.Subset(set)
	if err != nil {
		return nil, name, fmt.Errorf("aster: subset font: %w", err)
	}
	return sub, name, nil
}
