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
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
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
// Close may be called at any time from any goroutine: it cancels the calls in
// flight (they return an error), waits for them to finish, closes the Loader,
// and makes every later call fail. Closing more than once is harmless.
type Converter struct {
	cfg      *config
	location *time.Location
	theme    jsval.Value // parsed WithTheme config; Undefined when none
	vl       string      // vegalite compiler version (vegalite.Version64, ...)

	// closed is set by Close; mu is read-locked for the duration of every
	// rendering call, so Close can wait for them before releasing the loader.
	closed    atomic.Bool
	mu        sync.RWMutex
	closeOnce sync.Once
	closeErr  error
	base      context.Context // canceled by Close; parent of every call's context
	cancelAll context.CancelFunc

	// Layout text measurement, built on first use.
	measurerOnce sync.Once
	measurer     *text.Measurer
	measurerErr  error

	// PNG text is shaped by one shaper per converter, built on first use.
	shaperOnce sync.Once
	shaper     raster.Shaper
	shaperErr  error

	// PDF output shapes text with the layout measurer when there is one;
	// with text measurement disabled, one is built on first use.
	pdfOnce     sync.Once
	pdfMeasurer *text.Measurer
	pdfErr      error
}

// errConverterClosed is returned by every rendering method after Close.
var errConverterClosed = errors.New("aster: converter is closed")

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

// AvailableVersions reports the Vega-Lite version sets bundled in this build,
// sorted by key. Pass a VegaLiteVersion (e.g. "6.4") to WithVegaLiteVersion.
func AvailableVersions() ([]VersionInfo, error) {
	return append([]VersionInfo(nil), supportedVersions...), nil
}

// New creates a new Converter with the given options.
func New(opts ...Option) (*Converter, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	vlVersion := vegalite.Version64
	if v := cfg.vegaLiteVersion; v != "" {
		key := "vl" + strings.ReplaceAll(v, ".", "_")
		vlVersion = compilerVersion[key]
		found := false
		var names []string
		for _, s := range supportedVersions {
			names = append(names, fmt.Sprintf("%s (Vega-Lite %s)", strings.ReplaceAll(strings.TrimPrefix(s.Key, "vl"), "_", "."), s.VegaLiteVersion))
			if s.Key == key {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("aster: unknown Vega-Lite version %q (available: %s)", v, strings.Join(names, ", "))
		}
	}
	loc := time.UTC
	if tz := cfg.timezone; tz != "" && tz != "UTC" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("aster: unsupported timezone %q: %w", tz, err)
		}
		loc = l
	}
	if cfg.loader == nil {
		cfg.loader = DenyLoader{}
	}
	c := &Converter{cfg: cfg, location: loc, vl: vlVersion}
	c.base, c.cancelAll = context.WithCancel(context.Background())
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
	return c, nil
}

// Close releases all resources held by the Converter. It is safe to call
// multiple times and concurrently with rendering calls: calls in flight are
// canceled and awaited, then the Loader is closed. After Close every rendering
// method returns an error.
func (c *Converter) Close() error {
	c.closed.Store(true)
	if c.cancelAll != nil {
		c.cancelAll()
	}
	c.mu.Lock() // waits for every call in flight
	defer c.mu.Unlock()
	c.closeOnce.Do(func() {
		if closer, ok := c.cfg.loader.(io.Closer); ok {
			c.closeErr = closer.Close()
		}
	})
	err := c.closeErr
	c.closeErr = nil // only the first Close reports it
	return err
}

// enter registers a rendering call; the returned function ends it. It reports
// false once the Converter is closed.
func (c *Converter) enter() (release func(), ok bool) {
	c.mu.RLock()
	if c.closed.Load() {
		c.mu.RUnlock()
		return nil, false
	}
	return c.mu.RUnlock, true
}

// opContext bounds one public call — every stage of it together — by the
// converter's timeout, and by Close. The context carries the call's shaping
// budget, so its text is shaped under the same timeout and within one
// allowance of work.
func (c *Converter) opContext() (context.Context, context.CancelFunc) {
	var ctx context.Context
	var cancel context.CancelFunc
	if c.cfg.timeout > 0 {
		ctx, cancel = context.WithTimeout(c.base, c.cfg.timeout)
	} else {
		ctx, cancel = context.WithCancel(c.base)
	}
	return text.WithShapingBudget(ctx, text.NewShapingBudget(ctx, c.shapingLimits())), cancel
}

// stageErr wraps a stage's error, naming the timeout when the call's context
// expired, so a caller can tell a slow chart from a broken one.
func (c *Converter) stageErr(ctx context.Context, stage string, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		if !errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
		}
		return fmt.Errorf("aster: %s timed out after %v: %w", stage, c.cfg.timeout, err)
	}
	return fmt.Errorf("aster: %s: %w", stage, err)
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

// signalWritesKey carries signal writes in a call's context; only the tests
// set it (export_test.go), to compare a chart after View.signal writes.
type signalWritesKey struct{}

func signalWritesFrom(ctx context.Context) []vega.SignalWrite {
	w, _ := ctx.Value(signalWritesKey{}).([]vega.SignalWrite)
	return w
}

// VegaToSVG renders a Vega spec (JSON) to an SVG string.
func (c *Converter) VegaToSVG(spec []byte) (string, error) {
	release, ok := c.enter()
	if !ok {
		return "", errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	return c.vegaSVG(ctx, spec)
}

// VegaLiteToSVG renders a Vega-Lite spec (JSON) to an SVG string.
func (c *Converter) VegaLiteToSVG(spec []byte) (string, error) {
	release, ok := c.enter()
	if !ok {
		return "", errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	return c.vegaLiteSVG(ctx, spec)
}

func (c *Converter) vegaSVG(ctx context.Context, spec []byte) (string, error) {
	if err := c.checkSpecSize(spec); err != nil {
		return "", err
	}
	v, err := timed(ctx, "json", func() (jsval.Value, error) { return jsval.ParseJSONLimit(spec, c.limits().MaxParseBytes) })
	if err != nil {
		return "", fmt.Errorf("aster: parsing Vega spec: %w", err)
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
	m, err := c.measurerInit()
	if err != nil {
		return "", err
	}
	shaping := text.ShapingBudgetFrom(ctx)
	m = m.Bounded(shaping)
	opts := vega.Options{
		Loader:   c.cfg.loader,
		Location: c.location,
		Now:      c.cfg.now,
		// Set only by the tests' signal sweep (export_test.go).
		SignalWrites: signalWritesFrom(ctx),
		Config:       c.theme,
		Limits:       c.limits(),
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
	if st := stagesFrom(ctx); st != nil {
		opts.Stages = st
		if m != nil {
			opts.TextMeasurer = timedMeasurer{m, st}
		}
	}
	// The label transform paints the marks it avoids, text included; the
	// shaper is only built if it does.
	opts.Shaper = lazyShaper{c, shaping}
	res, err := vega.Render(ctx, spec, opts)
	if err != nil {
		return "", c.stageErr(ctx, "rendering Vega", err)
	}
	so := svg.Options{
		Width:  res.Width,
		Height: res.Height,
		Origin: res.Origin,
		// An SVG past this size is not a chart anybody can use.
		MaxBytes: c.maxSVGBytes(),
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
	so.Href = func(uri string) ([]svg.HrefAttr, bool) {
		if _, ok := svg.SanitizeURL(uri, svg.URLOptions{}); !ok {
			return nil, false
		}
		href, err := c.cfg.loader.Sanitize(ctx, uri)
		if err != nil {
			return nil, false
		}
		if _, ok := svg.SanitizeURL(href, svg.URLOptions{}); !ok {
			return nil, false
		}
		return []svg.HrefAttr{{Name: "xlink:href", Value: href}}, true
	}
	// Image URLs go through the Loader like every other resource (upstream's
	// ResourceLoader.loadImage sanitizes with context "image"); a rejected URL
	// renders what a failed image load does: no source, no size.
	so.Image = func(url string) svg.ImageInfo {
		if url == "" {
			return svg.ImageInfo{}
		}
		u, err := c.cfg.loader.Sanitize(ctx, url)
		if err != nil {
			return svg.ImageInfo{}
		}
		if src, ok := svg.SanitizeURL(u, svg.URLOptions{}); ok {
			return svg.ImageInfo{Src: src}
		}
		return svg.ImageInfo{}
	}
	out, err = timed(ctx, "svg", func() (string, error) { return svg.Render(ctx, res.Scenegraph, so) })
	if err != nil {
		return "", c.stageErr(ctx, "writing SVG", err)
	}
	return out, nil
}

// imageLoader fetches what the <image> elements of a PNG or a PDF refer to through the
// Loader, as every resource of a call is: the href sanitized, then loaded,
// under the call's context. An image is read up to the rasterizer's limit for
// one image, and the images of one call together up to the data allowance (64
// MiB, or half of WithMemoryLimit). A rejected or failed load leaves the image
// undrawn, as a broken image is.
func (c *Converter) imageLoader() func(context.Context, string) ([]byte, error) {
	allowance := c.limits().MaxLoadBytes
	if allowance <= 0 {
		allowance = 64 << 20
	}
	var used atomic.Int64
	return func(ctx context.Context, href string) ([]byte, error) {
		u, err := c.cfg.loader.Sanitize(ctx, href)
		if err != nil {
			return nil, err
		}
		left := allowance - used.Load()
		if left <= 0 {
			return nil, fmt.Errorf("%w: images over %d bytes in total", ErrLimit, allowance)
		}
		b, err := c.cfg.loader.Load(budget.With(ctx, &budget.Budget{MaxLoadBytes: min(left, budget.From(ctx).LoadLeft())}), u)
		if err != nil {
			return nil, err
		}
		if used.Add(int64(len(b))) > allowance {
			return nil, fmt.Errorf("%w: images over %d bytes in total", ErrLimit, allowance)
		}
		return b, nil
	}
}

// rasterLimits turns WithMemoryLimit into the rasterizer's canvas budget:
// the bytes of canvas, layers and masks alive at once, and the output size.
func (c *Converter) rasterLimits() raster.Limits {
	var l raster.Limits
	if n := c.cfg.memoryLimit; n > 0 {
		b := min(max(n, 4<<20), 1<<30)
		l.MaxCanvasBytes = int(b)
		l.MaxPixels = int(b / 8)
	}
	return l
}

// shapingLimits bounds the text shaping of one call: forme's lookup work, and
// with WithMemoryLimit the glyphs of one run to a quarter of the limit, as the
// SVG is.
func (c *Converter) shapingLimits() text.ShapingLimits {
	var l text.ShapingLimits
	if n := c.cfg.memoryLimit; n > 0 {
		l.RunBytes = int64(min(max(n/4, 1<<20), 1<<40))
	}
	return l
}

// maxSVGBytes bounds the SVG a render may produce: 128 MiB by default, a
// quarter of WithMemoryLimit when one is set.
func (c *Converter) maxSVGBytes() int {
	if n := c.cfg.memoryLimit; n > 0 {
		return int(min(max(n/4, 1<<20), 1<<40))
	}
	return 128 << 20
}

// specBytesDivisor sets the specification a memory limit admits: a sixteenth of
// the limit in bytes. The rows and items budgets charge what a specification
// makes the engine build, and none of them sees the text itself, which is all
// there is to an inline geometry; once parsed and compiled it holds about
// nineteen times its size, so a sixteenth of the limit stays within twice it.
const specBytesDivisor = 16

// checkSpecSize refuses a specification too large for WithMemoryLimit. Every
// entry point that takes a specification passes through it, whatever the form
// the caller held it in: they all end up as the JSON bytes parsed here.
func (c *Converter) checkSpecSize(spec []byte) error {
	n := c.cfg.memoryLimit
	if n == 0 {
		return nil
	}
	if limit := max(n/specBytesDivisor, 1<<20); uint64(len(spec)) > limit {
		return fmt.Errorf("aster: %w: the specification is too large for the memory limit (%d bytes, at most %d)", ErrLimit, len(spec), limit)
	}
	return nil
}

// randomSeed seeds Vega's random() and every transform that samples.
const randomSeed = 123456789

// limits turns WithMemoryLimit into the engine's work bounds. The engine has
// no separate heap to cap, so the byte budget is converted into the number of
// rows and scene items a render may create, at a conservative per-object cost.
func (c *Converter) limits() vega.Limits {
	const bytesPerRow, bytesPerItem, bytesPerCell, bytesPerOp = 512, 1536, 16384, 1536
	var l vega.Limits
	if n := c.cfg.memoryLimit; n > 0 {
		l.MaxRows = int(max(n/bytesPerRow, 1000))
		l.MaxItems = int(max(n/bytesPerItem, 1000))
		// A facet cell instantiates its own operators; the default (20,000)
		// is the most that is left alone.
		l.MaxSubflows = int(min(max(n/bytesPerCell, 1000), 20_000))
		l.MaxOperators = int(min(max(n/bytesPerOp, 10_000), 500_000))
		l.MaxLoadBytes = int64(max(n/2, 1<<20))
		l.MaxStringBytes = int64(max(n/4, 1<<20))
		l.MaxCanvasBytes = int64(max(n/2, 1<<20))
		l.MaxParseBytes = int64(max(n/2, 1<<20))
		l.RowBytes = bytesPerRow
	}
	return l
}

// newMeasurer builds a text measurer from the configured fonts.
// newMeasurer builds the layout text measurer.
func (c *Converter) newMeasurer() (*text.Measurer, error) {
	opts := c.fontOptions()
	if !c.cfg.harfBuzzText {
		opts = append(opts, text.WithExactAdvances())
		if c.cfg.pangoText != 0 {
			opts = append(opts, text.WithPangoAdvances(c.cfg.pangoText == 2))
		}
	}
	return text.New(opts...)
}

// fontOptions are the font registrations and generic-family mappings shared
// by layout measurement and PNG text, so both resolve families identically.
func (c *Converter) fontOptions() []text.Option {
	var opts []text.Option
	if c.cfg.systemFonts {
		opts = append(opts, text.WithSystemFonts())
	}
	for _, f := range c.cfg.fonts {
		opts = append(opts, text.WithFont(f.family, f.data))
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

// measurerInit builds the text measurer used for layout on first use; it
// returns nil when text measurement is disabled, which selects Vega's own
// width estimate.
func (c *Converter) measurerInit() (*text.Measurer, error) {
	if !c.cfg.textMeasure {
		return nil, nil
	}
	c.measurerOnce.Do(func() {
		c.measurer, c.measurerErr = c.newMeasurer()
		if c.measurerErr != nil {
			c.measurerErr = fmt.Errorf("aster: initializing text measurer: %w", c.measurerErr)
		}
	})
	return c.measurer, c.measurerErr
}

// VegaLiteToVega compiles a Vega-Lite spec (JSON) to a full Vega spec (JSON).
func (c *Converter) VegaLiteToVega(spec []byte) ([]byte, error) {
	release, ok := c.enter()
	if !ok {
		return nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	vg, err := c.compileVegaLite(ctx, spec)
	if err != nil {
		return nil, err
	}
	return jsval.AppendJSON(nil, vg), nil
}

// compileVegaLite parses a Vega-Lite spec and compiles it to Vega with the
// converter's theme and time zone.
func (c *Converter) compileVegaLite(ctx context.Context, spec []byte) (jsval.Value, error) {
	if err := c.checkSpecSize(spec); err != nil {
		return jsval.Undefined, err
	}
	v, err := timed(ctx, "json", func() (jsval.Value, error) { return jsval.ParseJSONLimit(spec, c.limits().MaxParseBytes) })
	if err != nil {
		return jsval.Undefined, fmt.Errorf("aster: parsing Vega-Lite spec: %w", err)
	}
	vg, err := timed(ctx, "compile", func() (jsval.Value, error) {
		return vegalite.Compile(v, vegalite.Options{Config: c.theme, Location: c.location, Version: c.vl, Context: ctx})
	})
	if err != nil {
		return jsval.Undefined, c.stageErr(ctx, "compiling Vega-Lite", err)
	}
	return vg, nil
}

// VegaToPNG renders a Vega spec (JSON) to a PNG image.
func (c *Converter) VegaToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	release, ok := c.enter()
	if !ok {
		return nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	svg, err := c.vegaSVG(ctx, spec)
	if err != nil {
		return nil, err
	}
	return c.svgToPNG(ctx, svg, opts)
}

// VegaLiteToPNG renders a Vega-Lite spec (JSON) to a PNG image.
func (c *Converter) VegaLiteToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	release, ok := c.enter()
	if !ok {
		return nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	svg, err := c.vegaLiteSVG(ctx, spec)
	if err != nil {
		return nil, err
	}
	return c.svgToPNG(ctx, svg, opts)
}

// SVGToPNG rasterizes an SVG string to a PNG image.
func (c *Converter) SVGToPNG(svg string, opts ...PNGOption) ([]byte, error) {
	release, ok := c.enter()
	if !ok {
		return nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	return c.svgToPNG(ctx, svg, opts)
}

func (c *Converter) svgToPNG(ctx context.Context, svg string, opts []PNGOption) (out []byte, err error) {
	defer recoverInto(&err)
	cfg := defaultPNGConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if !(cfg.scale > 0) || math.IsInf(cfg.scale, 1) {
		return nil, fmt.Errorf("aster: invalid PNG scale %v (must be a positive, finite number)", cfg.scale)
	}
	out, err = c.rasterize(ctx, svg, cfg.scale)
	if err != nil {
		return nil, err
	}
	switch {
	case cfg.quantizeColors > 0:
		out = pngopt.QuantizeOrRecode(out, cfg.quantizeColors)
	case cfg.recode:
		out = pngopt.Recode(out)
	}
	return out, nil
}

// shaperInit builds, once, the text shaper that draws glyphs at their exact
// advances (as resvg draws them), with the same fonts and family mapping as
// layout.
func (c *Converter) shaperInit() (raster.Shaper, error) {
	c.shaperOnce.Do(func() {
		c.shaper, c.shaperErr = raster.NewShaperWithOptions(c.fontOptions()...)
		if c.shaperErr != nil {
			c.shaperErr = fmt.Errorf("aster: initializing PNG text shaper: %w", c.shaperErr)
		}
	})
	return c.shaper, c.shaperErr
}

// lazyShaper defers building the PNG shaper until text is first drawn, and
// shapes under the call's budget.
type lazyShaper struct {
	c *Converter
	b *text.ShapingBudget
}

func (l lazyShaper) Shape(text string, req raster.FontRequest, size float64) []raster.ShapedGlyph {
	sh, err := l.c.shaperInit()
	if err != nil {
		return nil
	}
	return raster.BoundShaper(sh, l.b).Shape(text, req, size)
}

func (c *Converter) rasterize(ctx context.Context, svg string, scale float64) ([]byte, error) {
	sh, err := c.shaperInit()
	if err != nil {
		return nil, err
	}
	sh = raster.BoundShaper(sh, text.ShapingBudgetFrom(ctx))
	out, err := timed(ctx, "png", func() ([]byte, error) {
		return raster.RenderPNG([]byte(svg), raster.Options{Scale: scale, Shaper: sh, Context: ctx, Limits: c.rasterLimits(), Images: c.imageLoader()})
	})
	if err != nil {
		return nil, c.stageErr(ctx, "rendering PNG", err)
	}
	return out, nil
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

// PDFOption configures a single PDF render operation.
type PDFOption func(*pdfConfig)

type pdfConfig struct {
	text PDFTextMode
}

// WithPDFText selects how text is represented in the PDF; see the
// PDFTextMode constants. The default is PDFTextEmbed.
func WithPDFText(mode PDFTextMode) PDFOption {
	return func(c *pdfConfig) {
		c.text = mode
	}
}

// FontUsage reports the source bytes and referenced glyph IDs of one face in a
// rendered PDF; see svgpdf.FontUsage.
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
	out, _, err := c.VegaToPDFUsage(spec, opts...)
	return out, err
}

// VegaLiteToPDF renders a Vega-Lite spec (JSON) to a single-page vector PDF.
func (c *Converter) VegaLiteToPDF(spec []byte, opts ...PDFOption) ([]byte, error) {
	out, _, err := c.VegaLiteToPDFUsage(spec, opts...)
	return out, err
}

// SVGToPDF converts an SVG string (as produced by the Vega SVG renderer) to
// a single-page vector PDF suitable for direct embedding in LaTeX documents
// via \includegraphics. Text handling is controlled by WithPDFText: by
// default the fonts a chart uses are subset and embedded, so the output is
// self-contained and text is selectable.
//
// Only the SVG subset that Vega emits is supported, images included (fetched
// through the Loader, as PNG output fetches them). Unsupported constructs
// (gradients, embedded CSS, ...) return a descriptive error rather than a
// silently incomplete chart; callers can fall back to SVGToPNG.
func (c *Converter) SVGToPDF(svg string, opts ...PDFOption) ([]byte, error) {
	out, _, err := c.SVGToPDFUsage(svg, opts...)
	return out, err
}

// SVGToPDFUsage is SVGToPDF plus the per-face glyph usage of the produced PDF
// (see FontUsage). It is intended with WithPDFText(PDFTextNamed): the returned
// usage is what a caller needs to embed one shared subset across many charts.
func (c *Converter) SVGToPDFUsage(svg string, opts ...PDFOption) ([]byte, []FontUsage, error) {
	release, ok := c.enter()
	if !ok {
		return nil, nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	return c.svgToPDF(ctx, svg, opts)
}

func (c *Converter) svgToPDF(ctx context.Context, svg string, opts []PDFOption) (pdf []byte, uses []FontUsage, err error) {
	defer func() {
		if r := recover(); r != nil {
			pdf, uses, err = nil, nil, fmt.Errorf("aster: internal error: %v", r)
		}
	}()
	cfg := &pdfConfig{text: PDFTextEmbed}
	for _, opt := range opts {
		opt(cfg)
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
	m, err := c.pdfMeasurerInit()
	if err != nil {
		return nil, nil, err
	}
	m = m.Bounded(text.ShapingBudgetFrom(ctx))
	t0 := time.Now()
	pdf, uses, err = svgpdf.ConvertWithUsage(svg, m, svgpdf.Options{Text: mode, Context: ctx, Images: c.imageLoader()})
	if st := stagesFrom(ctx); st != nil {
		st("pdf", time.Since(t0))
	}
	if err != nil {
		return nil, nil, c.stageErr(ctx, "rendering PDF", err)
	}
	return pdf, uses, nil
}

// VegaToPDFUsage renders a Vega spec (JSON) to a vector PDF and reports its
// per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	release, ok := c.enter()
	if !ok {
		return nil, nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	svg, err := c.vegaSVG(ctx, spec)
	if err != nil {
		return nil, nil, err
	}
	return c.svgToPDF(ctx, svg, opts)
}

// VegaLiteToPDFUsage renders a Vega-Lite spec (JSON) to a vector PDF and
// reports its per-face glyph usage; see SVGToPDFUsage.
func (c *Converter) VegaLiteToPDFUsage(spec []byte, opts ...PDFOption) ([]byte, []FontUsage, error) {
	release, ok := c.enter()
	if !ok {
		return nil, nil, errConverterClosed
	}
	defer release()
	ctx, cancel := c.opContext()
	defer cancel()
	svg, err := c.vegaLiteSVG(ctx, spec)
	if err != nil {
		return nil, nil, err
	}
	return c.svgToPDF(ctx, svg, opts)
}

// pdfMeasurerInit returns the measurer PDF text is shaped with: the layout
// measurer when text measurement is enabled, so glyphs come from the faces the
// SVG was laid out against, otherwise one built on first use.
func (c *Converter) pdfMeasurerInit() (*text.Measurer, error) {
	if c.cfg.textMeasure {
		return c.measurerInit()
	}
	c.pdfOnce.Do(func() {
		c.pdfMeasurer, c.pdfErr = c.newMeasurer()
		if c.pdfErr != nil {
			c.pdfErr = fmt.Errorf("aster: initializing PDF text shaper: %w", c.pdfErr)
		}
	})
	return c.pdfMeasurer, c.pdfErr
}

// SubsetFont builds a TrueType subset of source containing gids, preserving the
// source's original glyph numbering. Because numbering is preserved, the subset
// resolves content that references glyphs by original GID through an Identity
// CIDToGIDMap — as PDFTextNamed output does — so it can be embedded once at a
// higher level and shared by every chart that references the same face.
//
// It returns the subset program and the source's PostScript name (matching the
// PostScriptName reported by FontUsage and the /BaseFont written by TextNamed,
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
	if !f.CanSubset() {
		return nil, name, fmt.Errorf("aster: font %q has no TrueType outlines to subset", name)
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
