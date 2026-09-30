// Package vega turns a Vega specification into a laid-out scenegraph. It is the
// Go counterpart of vega-parser, vega-runtime, vega-dataflow, vega-view,
// vega-encode and the mark machinery of vega-view-transforms.
//
// Rendering follows upstream's two stages. Parsing (vega-parser) turns the
// specification into a description of operators (entries) and their
// dependencies: signals, data pipelines, scales, projections, and for every
// mark the chain data join, mark, encode, layout and bound. The runtime then
// instantiates that description in a dataflow graph and runs it in rank order
// until the view size settles, exactly as `new vega.View(spec).runAsync()`
// does, including the re-evaluation that autosize fitting triggers.
//
// Data transforms are the one-shot functions of package transforms; the
// dataflow here recomputes an operator from its complete input when any input
// changed, rather than propagating incremental add/remove/modify sets. What
// stays faithful is when operators run and in what order, and the
// enter/update/exit lifecycle of visual items.
//
// Specifications are untrusted: group nesting, signal dependency cycles, row
// counts, operator evaluations and layout re-runs are bounded, cancellation of
// the context aborts the run, and data can only come from the Loader.
package vega

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mgilbir/aster/purego/internal/expr"
	"github.com/mgilbir/aster/purego/internal/format"
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scene"
)

// Loader fetches external data. It is satisfied by aster's Loader.
type Loader interface {
	// Load fetches the content at uri.
	Load(ctx context.Context, uri string) ([]byte, error)
	// Sanitize validates uri before loading; it returns the uri to load.
	Sanitize(ctx context.Context, uri string) (string, error)
}

// Limits bound the work a specification can cause. Zero fields select the
// defaults.
type Limits struct {
	// MaxRows is the total number of data rows a specification may load.
	MaxRows int
	// MaxVisits is the number of operator evaluations across all runs.
	MaxVisits int
	// MaxReruns is the number of layout-driven re-evaluations.
	MaxReruns int
	// MaxItems is the number of scenegraph items.
	MaxItems int
	// MaxSubflows is the number of facet cells (group marks over faceted data),
	// each of which instantiates its own operators.
	MaxSubflows int
}

func (l Limits) withDefaults() Limits {
	if l.MaxRows == 0 {
		l.MaxRows = 5_000_000
	}
	if l.MaxVisits == 0 {
		l.MaxVisits = 50_000_000
	}
	if l.MaxReruns == 0 {
		l.MaxReruns = 16
	}
	if l.MaxItems == 0 {
		l.MaxItems = 5_000_000
	}
	if l.MaxSubflows == 0 {
		l.MaxSubflows = 100_000
	}
	return l
}

// Options configure a render.
type Options struct {
	// Loader supplies external data; nil denies all loading.
	Loader Loader
	// TextMeasurer measures text for layout and bounds; nil selects Vega's
	// estimate (0.8 * length * fontSize).
	TextMeasurer scene.TextMeasurer
	// Location is the time zone of local-time scales and functions; nil is UTC.
	Location *time.Location
	// Config is a theme configuration merged under the specification's own
	// config, as vega.parse(spec, config) does.
	Config jsval.Value
	// Locale holds optional number and time locale definitions.
	Locale *format.Locale
	// Now supplies the clock for now(); nil uses the system clock.
	Now func() time.Time
	// Random seeds random(); nil uses a nondeterministic source.
	Random func() float64
	// Limits bound the work of the render.
	Limits Limits
}

// Result is a rendered view: the scenegraph and what the SVG renderer needs
// beside it.
type Result struct {
	Scenegraph *scene.Scenegraph
	// Width and Height are the size of the rendered image including padding.
	Width, Height float64
	// Origin is the translation of the scene inside the image (padding plus
	// the autosize origin).
	Origin [2]float64
	// Background is the view background; HasBackground is false for none.
	Background    string
	HasBackground bool
	// Description is the view's aria description ("" for none).
	Description    string
	HasDescription bool
	// Warnings collects the dataflow warnings (failed loads, unsupported
	// scale properties, ...).
	Warnings []string
}

// runView is a running instance of a specification (vega-view's View).
type runView struct {
	ctx    context.Context
	g      *flowGraph
	sg     *scene.Scenegraph
	loader Loader
	limits Limits
	rows   int
	items  int
	cells  int

	loc     *time.Location
	zone    format.Zone
	locale  *format.Locale
	now     func() float64
	rand    *expr.Random
	bounder *scene.Bounder

	root *rtContext

	// Size state (vega-view): width and height are the signal values, the view
	// sizes include neither padding nor origin.
	width, height         float64
	viewWidth, viewHeight float64
	origin                [2]float64
	resize, autosize      int
	resizeWidth           *opNode
	resizeHeight          *opNode
	widthSig, heightSig   *opNode
	paddingSig            *opNode
	backgroundSig         *opNode
	autosizeSig           *opNode
	bgValue               jsval.Value
	reruns                int

	itemTupleCache map[*scene.Item]*jsval.Object
	viewItem       map[*jsval.Object]*scene.Item
	shapes         []scene.ShapeFunc
	pending        map[*opNode]*pendingChange
	markItemsCache map[*scene.Mark]markItems
	itemIDs        map[*scene.Item]int
	itemByID       []*scene.Item
	marks          []*scene.Mark
	markCtx        map[*scene.Mark]*rtContext
	idCounter      float64
}

// Render parses spec and evaluates it to a laid-out scenegraph.
func Render(ctx context.Context, spec jsval.Value, opts Options) (res *Result, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *parseError:
				err = e
			case *opError:
				err = e.err
			case error:
				err = fmt.Errorf("vega: %w\n%s", e, shortStack())
			default:
				err = fmt.Errorf("vega: internal error: %v\n%s", r, shortStack())
			}
			res = nil
		}
	}()
	if !spec.IsObj() {
		return nil, errors.New("Input Vega specification must be an object.")
	}

	config := mergeConfig(defaultConfig(), opts.Config, spec.Get("config"))
	scope := newScope(config, &parseOptions{})
	parseView(spec, scope)
	flow := scope.toRuntime()

	v := newView(ctx, opts, scope.locale)
	v.build(flow)
	if err := v.run(); err != nil {
		return nil, err
	}
	v.setGuideCaptions()
	return v.result(scope), nil
}

// shortStack is the top of the goroutine stack, for diagnosing unexpected
// panics.
func shortStack() string {
	b := debug.Stack()
	lines := strings.Split(string(b), "\n")
	if len(lines) > 24 {
		lines = lines[:24]
	}
	return strings.Join(lines, "\n")
}

func newView(ctx context.Context, opts Options, locale jsval.Value) *runView {
	limits := opts.Limits.withDefaults()
	v := &runView{
		ctx: ctx, loader: opts.Loader, limits: limits,
		loc: opts.Location, sg: scene.New(),
		bounder: scene.NewBounder(opts.TextMeasurer),
	}
	if v.loc == nil {
		v.loc = time.UTC
	}
	v.zone = format.Local(v.loc)
	switch {
	case opts.Locale != nil:
		v.locale = opts.Locale
	case locale.IsObj():
		// config.locale: {number: ..., time: ...}
		l, err := format.NewLocale(locale.Get("number"), locale.Get("time"), v.zone)
		if err != nil {
			fail("invalid locale: %v", err)
		}
		v.locale = l
	default:
		l := format.DefaultLocale()
		l.Local = v.zone
		v.locale = l
	}
	if opts.Now != nil {
		now := opts.Now
		v.now = func() float64 { return float64(now().UnixMilli()) }
	}
	if opts.Random != nil {
		v.rand = &expr.Random{Source: opts.Random}
	}
	v.g = newGraph(ctx)
	v.g.view = v
	v.g.maxVisits = limits.MaxVisits
	return v
}

// build instantiates the flow: the root context, the scenegraph root and the
// view-level operators that track size and background.
func (v *runView) build(flow *flowSpec) {
	c := newContext(v)
	v.root = c
	c.parse(flow)
	if c.root != nil {
		c.root.value = v.sg.Root
	}
	// The root group item is the sole tuple of the root data set.
	ds := c.dataRoles("root")
	if input := ds["input"]; input != nil {
		it := v.sg.RootItem()
		it.Items = []*scene.Mark{}
		v.g.pulseInput(input, &flowPulse{items: []*scene.Item{it}, add: []*scene.Item{it}, changed: true})
	}

	v.widthSig = c.signal("width")
	v.heightSig = c.signal("height")
	v.paddingSig = c.signal("padding")
	v.backgroundSig = c.signal("background")
	v.autosizeSig = c.signal("autosize")

	v.width = numOf(v.widthSig)
	v.height = numOf(v.heightSig)
	v.viewWidth = v.viewSize(v.width, true)
	v.viewHeight = v.viewSize(v.height, false)
	v.resize = 0
	v.autosize = 1
	v.initResize()
	v.initBackground()
}

func numOf(n *opNode) float64 {
	if n == nil {
		return 0
	}
	return jsval.ToNumber(n.sig())
}

func (v *runView) autosizeContains() string {
	if v.autosizeSig == nil {
		return ""
	}
	return v.autosizeSig.sig().Get("contains").AsString()
}

// padding reads the padding signal as vega-view's padding() does.
func (v *runView) padding() (left, right, top, bottom float64) {
	if v.paddingSig == nil {
		return
	}
	p := v.paddingSig.sig()
	if p.IsObj() {
		return numOr0f(jsval.ToNumber(p.Get("left"))), numOr0f(jsval.ToNumber(p.Get("right"))),
			numOr0f(jsval.ToNumber(p.Get("top"))), numOr0f(jsval.ToNumber(p.Get("bottom")))
	}
	n := numOr0f(jsval.ToNumber(p))
	return n, n, n, n
}

// viewSize is viewWidth/viewHeight: the size minus the padding when autosize
// is measured including it.
func (v *runView) viewSize(size float64, horizontal bool) float64 {
	if v.autosizeContains() == "padding" {
		l, r, t, b := v.padding()
		if horizontal {
			return size - (l + r)
		}
		return size - (t + b)
	}
	return size
}

func (v *runView) initResize() {
	g := v.g
	resetSize := func() { v.autosize, v.resize = 1, 1 }

	if v.widthSig != nil {
		n := g.add("resize-width", nil)
		n.ctx = v.root
		n.update = func(n *opNode, p *opParams) any {
			v.width = jsval.ToNumber(toValue(p.Get("size")))
			v.viewWidth = v.viewSize(v.width, true)
			resetSize()
			return nil
		}
		g.connect(n, n.parameters(map[string]any{"size": v.widthSig}, []string{"size"}, true, false))
		n.rank = v.widthSig.rank + 1
		v.resizeWidth = n
	}
	if v.heightSig != nil {
		n := g.add("resize-height", nil)
		n.ctx = v.root
		n.update = func(n *opNode, p *opParams) any {
			v.height = jsval.ToNumber(toValue(p.Get("size")))
			v.viewHeight = v.viewSize(v.height, false)
			resetSize()
			return nil
		}
		g.connect(n, n.parameters(map[string]any{"size": v.heightSig}, []string{"size"}, true, false))
		n.rank = v.heightSig.rank + 1
		v.resizeHeight = n
	}
	if v.paddingSig != nil {
		n := g.add("resize-padding", nil)
		n.ctx = v.root
		n.update = func(n *opNode, p *opParams) any { resetSize(); return nil }
		g.connect(n, n.parameters(map[string]any{"pad": v.paddingSig}, []string{"pad"}, true, false))
		n.rank = v.paddingSig.rank + 1
	}
}

func (v *runView) initBackground() {
	if v.backgroundSig == nil {
		return
	}
	g := v.g
	n := g.add("background", nil)
	n.ctx = v.root
	n.update = func(n *opNode, p *opParams) any {
		v.bgValue = toValue(p.Get("bg"))
		v.resize = 1
		return v.bgValue
	}
	g.connect(n, n.parameters(map[string]any{"bg": v.backgroundSig}, []string{"bg"}, true, false))
}

// run evaluates the dataflow (View.runAsync).
func (v *runView) run() error {
	return v.g.evaluate("")
}

// resizeView is the view-size adjustment ViewLayout requests (View._resizeView).
// It runs after the current propagation: it stores the new size in the width
// and height signals, skipping their update expressions, and re-runs the
// dataflow with the `enter` encode set when either changed.
func (v *runView) resizeView(viewWidth, viewHeight, width, height float64, origin [2]float64, auto bool) {
	v.g.runAfter(func(g *flowGraph) {
		rerun := false
		v.autosize = 0
		if v.widthSig != nil && numOf(v.widthSig) != width {
			rerun = true
			g.update(v.widthSig, jsval.Num(width), false, true)
			if v.resizeWidth != nil {
				v.resizeWidth.skip = true
			}
		}
		if v.heightSig != nil && numOf(v.heightSig) != height {
			rerun = true
			g.update(v.heightSig, jsval.Num(height), false, true)
			if v.resizeHeight != nil {
				v.resizeHeight.skip = true
			}
		}
		if v.viewWidth != viewWidth {
			v.resize = 1
			v.viewWidth = viewWidth
		}
		if v.viewHeight != viewHeight {
			v.resize = 1
			v.viewHeight = viewHeight
		}
		if v.origin != origin {
			v.resize = 1
			v.origin = origin
		}
		if rerun {
			v.reruns++
			if v.reruns > v.limits.MaxReruns {
				fail("view layout did not converge after %d re-evaluations", v.limits.MaxReruns)
			}
			if err := g.run("enter"); err != nil {
				failErr(err)
			}
		}
		if auto {
			g.runAfter(func(*flowGraph) { v.autosize = 1 }, false, 0)
		}
	}, false, 1)
}

func (v *runView) result(scope *Scope) *Result {
	l, r, t, b := v.padding()
	w := v.viewWidth + l + r
	h := v.viewHeight + t + b
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	res := &Result{
		Scenegraph: v.sg,
		Width:      w, Height: h,
		Origin:   [2]float64{l + v.origin[0], t + v.origin[1]},
		Warnings: v.g.warnings,
	}
	if bg := v.backgroundSig; bg != nil {
		val := bg.sig()
		if val.IsTruthy() {
			res.Background, res.HasBackground = val.AsString(), true
		}
	}
	if d := scope.description; d.IsTruthy() {
		res.Description, res.HasDescription = d.AsString(), true
	}
	return res
}

// countItems accounts for newly created scenegraph items against the limit.
func (v *runView) countItems(n int) {
	v.items += n
	if v.limits.MaxItems > 0 && v.items > v.limits.MaxItems {
		fail("scenegraph exceeds the limit of %d items", v.limits.MaxItems)
	}
}

func (g *flowGraph) warn(msg string) { g.warnings = append(g.warnings, msg) }

// registerMark records a mark for post-run processing.
func (v *runView) registerMark(m *scene.Mark, c *rtContext) {
	v.marks = append(v.marks, m)
	if v.markCtx == nil {
		v.markCtx = map[*scene.Mark]*rtContext{}
	}
	v.markCtx[m] = c
}

// addSignalListener implements onOperator: when the source operator runs, the
// update expression computes the target's new value (Dataflow.on with an
// operator source).
func (v *runView) addSignalListener(c *rtContext, src, tgt *opNode, u *updateSpec) {
	g := v.g
	l := g.add("listener", tgt.value)
	l.ctx = c
	l.modified = u.force
	l.rank = src.rank
	l.skip = true // skip the first invocation
	l.update = func(n *opNode, p *opParams) any {
		var val any
		switch {
		case u.viaSignal != nil:
			if sn := c.get(u.viaSignal); sn != nil {
				val = sn.value
			}
		case u.update != nil:
			ev := jsval.Obj(jsval.NewObject(0))
			val = u.update.eval(c, jsval.Undefined, jsval.Undefined, ev)
		case u.hasVal:
			val = u.value
		}
		if !tgt.skip {
			tgt.skip = !sameValue(val, n.value) || (val == nil) != (n.value == nil)
			tgt.value = val
		}
		return val
	}
	src.addTarget(l)
	l.argval = newParams()
	l.addTarget(tgt)
	g.connect(tgt, []*opNode{l})
}
