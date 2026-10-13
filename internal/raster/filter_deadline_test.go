package raster

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/imageref"
)

// filterSide is the side of the filter region the counted tests run over: a
// pass over it is 16 bands, by rows or by columns.
const filterSide = 1024

// filterSource is a side x side premultiplied sRGB canvas, half covered by
// a translucent orange, as a source graphic.
func filterSource(side int) *canvas {
	cv := newCanvas(side, side)
	for y := range side {
		for x := range side / 2 {
			i := (y*side + x) * 4
			copy(cv.pix[i:i+4], []uint8{140, 70, 0, 179})
		}
	}
	return cv
}

// filterPrimWork runs one primitive over a side x side region of
// filterSource under ctx, as applyFilter does, and returns the renderer: its
// err and pixelOps tell how the primitive ended.
func filterPrimWork(ctx context.Context, side int, p fprim) *renderer {
	lim := Limits{}.withDefaults()
	lim.MaxPixelOps = math.MaxInt
	r := &renderer{lim: lim, rast: newRasterizer(), ctx: ctx, cw: side, ch: side, active: map[*node]bool{}}
	cv := filterSource(side)
	run := r.newFilterRun(cv, identity)
	run.region = irect{0, 0, side, side}
	defer run.end()
	res, _ := run.apply(&p)
	run.free(res)
	return r
}

// filterPrims are the primitives the counted tests run, each with how many
// passes over the region it makes, each pass polled once a band: a
// conversion to linearRGB, SourceAlpha, a copy, a blur's passes (a box
// blur's five steps of two, an IIR blur's per channel: in, the rows'
// sweeps, the columns', the scaling and out) and the primitive's own.
var filterPrims = []struct {
	name   string
	p      fprim
	passes int
}{
	{"flood", fprim{kind: tagFeFlood, color: rgba{255, 0, 0, 255}, alpha: 0.5}, 1},
	{"offset", fprim{kind: tagFeOffset, dx: 3, dy: 4}, 1},
	{"box blur", fprim{kind: tagFeGaussianBlur, stdX: 5, stdY: 5, linear: true}, 1 + 2*boxSteps},
	{"box blur, sRGB", fprim{kind: tagFeGaussianBlur, stdX: 5, stdY: 5}, 1 + 2*boxSteps},
	// No vertical blur: its passes are copies.
	{"box blur, x only", fprim{kind: tagFeGaussianBlur, stdX: 5, linear: true}, 1 + 2*boxSteps},
	{"IIR blur", fprim{kind: tagFeGaussianBlur, stdX: 1, stdY: 1, linear: true}, 1 + 4*5},
	{"drop shadow", fprim{kind: tagFeDropShadow, stdX: 5, stdY: 5, dx: 2, dy: 2, alpha: 1, linear: true}, 1 + 1 + 2*boxSteps + 1 + 1 + 2},
	{"blend normal", fprim{kind: tagFeBlend, in2: finput{kind: inAlpha}, mode: blendNormal, linear: true}, 1 + 1 + 1 + 1},
	{"blend multiply", fprim{kind: tagFeBlend, in2: finput{kind: inAlpha}, mode: blendMultiply, linear: true}, 1 + 1 + 1 + 1},
	{"composite over", fprim{kind: tagFeComposite, in2: finput{kind: inAlpha}, op: opOver, linear: true}, 1 + 1 + 1 + 1},
	{"composite xor", fprim{kind: tagFeComposite, in2: finput{kind: inAlpha}, op: opXor, linear: true}, 1 + 1 + 1 + 1},
	{"composite arithmetic", fprim{kind: tagFeComposite, in2: finput{kind: inAlpha}, op: opArithmetic, k: [4]float64{0.5, 0.5, 0, 0}, linear: true}, 1 + 1 + 1 + 1},
	{"merge", fprim{kind: tagFeMerge, merge: []finput{{kind: inSource}, {kind: inAlpha}}, linear: true}, (1 + 1) + (1 + 1 + 1)},
	{"colour matrix", fprim{kind: tagFeColorMatrix, cmKind: cmMatrix, cmValues: []float64{1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0.5, 0}, linear: true}, 1 + 1},
	{"saturate", fprim{kind: tagFeColorMatrix, cmKind: cmSaturate, cmValues: []float64{0.5}, linear: true}, 1 + 1},
	{"hue rotate", fprim{kind: tagFeColorMatrix, cmKind: cmHueRotate, cmValues: []float64{30}, linear: true}, 1 + 1},
	{"luminance to alpha", fprim{kind: tagFeColorMatrix, cmKind: cmLuminance, linear: true}, 1 + 1},
	{"component transfer", fprim{kind: tagFeComponentTransfer, funcs: [4]transferFn{{kind: 4, a: 1, b: 2}, {kind: 3, a: 0.5}, {kind: 1, table: []float64{0, 1}}, {kind: 2, table: []float64{0, 0.5, 1}}}, linear: true}, 1 + 1},
	{"tile", fprim{kind: tagFeTile}, 1},
	// A conversion, the pass along rows and the pass along columns.
	{"morphology", fprim{kind: tagFeMorphology, dilate: true, rx: 3, ry: 2, linear: true}, 1 + 2},
	{"morphology, sRGB", fprim{kind: tagFeMorphology, rx: 300, ry: 1e6}, 1 + 2},
	{"morphology, radius 0", fprim{kind: tagFeMorphology, linear: true}, 1 + 1},
	{"morphology, size wraps", fprim{kind: tagFeMorphology, rx: 2147483648, ry: 2, linear: true}, 1 + 1},
	{"convolve", fprim{kind: tagFeConvolveMatrix, conv: testKernel(false), linear: true}, 1 + 1},
	{"convolve, preserveAlpha", fprim{kind: tagFeConvolveMatrix, conv: testKernel(true), linear: true}, 1 + 1 + 1},
	// Two conversions (in, and in2 a second time: SourceAlpha's own pass
	// first) and the displacement.
	{"displacement", fprim{kind: tagFeDisplacementMap, in2: finput{kind: inAlpha}, scale: 20, xch: 3, ych: 3, linear: true}, 1 + 1 + 1 + 1},
	{"turbulence", fprim{kind: tagFeTurbulence, turb: &turbSpec{freqX: 0.05, freqY: 0.02, octaves: 2}, linear: true}, 1},
	{"fractal noise, stitched", fprim{kind: tagFeTurbulence, turb: &turbSpec{freqX: 0.05, freqY: 0.02, octaves: 1, stitch: true, fractal: true}}, 1},
	{"diffuse, distant light", fprim{kind: tagFeDiffuseLighting, light: &lightingSpec{surfaceScale: 3, constant: 1, color: [3]uint8{255, 128, 0}, light: lightSource{kind: lightDistant, azimuth: 45, elevation: 30}}, linear: true}, 1},
	{"specular, spot light", fprim{kind: tagFeSpecularLighting, light: &lightingSpec{specular: true, surfaceScale: 3, constant: 1, exponent: 20, color: [3]uint8{255, 255, 255},
		light: lightSource{kind: lightSpot, x: 100, y: 100, z: 50, pointsX: 500, pointsY: 500, spotExp: 2, cone: 40, hasCone: true}}, linear: true}, 1},
}

// testKernel is a 3x3 edge-detecting convolution.
func testKernel(preserveAlpha bool) *convolveSpec {
	return &convolveSpec{
		cols: 3, rows: 3, targetX: 1, targetY: 1, kernel: []float32{-1, -1, -1, -1, 8, -1, -1, -1, -1},
		divisor: 1, edge: edgeWrap, preserveAlpha: preserveAlpha,
	}
}

// Every filter primitive polls the render's context, and charges its pixels
// to MaxPixelOps, a band at a time through each of its passes over the
// region (CI's macOS runner ran a filter render 0.76 s past its deadline,
// as only the boundaries between primitives polled). Counted, not timed:
// the primitive polls at least once a band of each pass, and a context that
// ends at its middle poll stops it there, half its work undone.
func TestFilterPrimitivesPoll(t *testing.T) {
	bands := filterSide / imageref.BandRows(filterSide)
	for _, tc := range filterPrims {
		t.Run(tc.name, func(t *testing.T) {
			full := &pollCtx{Context: context.Background()}
			r := filterPrimWork(full, filterSide, tc.p)
			if r.err != nil {
				t.Fatal(r.err)
			}
			want := tc.passes * bands
			t.Logf("%d polls, %d pixel operations; want at least %d polls (%d passes of %d bands)", full.polls, r.pixelOps, want, tc.passes, bands)
			if full.polls < want {
				t.Errorf("%d polls, want at least %d: a pass of the primitive does not poll a band at a time", full.polls, want)
			}
			if r.pixelOps < tc.passes*filterSide*filterSide {
				t.Errorf("%d pixel operations charged for %d passes of %d pixels", r.pixelOps, tc.passes, filterSide*filterSide)
			}
			half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
			hr := filterPrimWork(half, filterSide, tc.p)
			if !errors.Is(hr.err, context.Canceled) {
				t.Fatalf("a context ended at poll %d of %d: err = %v", half.stop, full.polls, hr.err)
			}
			// What was charged by the stop is about half the work: no pass
			// ran on, uncharged, past it.
			if hr.pixelOps > r.pixelOps*3/4 {
				t.Errorf("a context ended at poll %d of %d: %d of %d pixel operations charged", half.stop, full.polls, hr.pixelOps, r.pixelOps)
			}
		})
	}
}

// The passes applyFilter makes itself, clearing a result outside its
// primitive's sub-region and converting the last result back to sRGB and
// into the canvas, poll a band at a time too.
func TestFilterOutputPassesPoll(t *testing.T) {
	bands := filterSide / imageref.BandRows(filterSide)
	render := func(ctx context.Context) *renderer {
		lim := Limits{}.withDefaults()
		lim.MaxPixelOps = math.MaxInt
		r := &renderer{lim: lim, rast: newRasterizer(), ctx: ctx, cw: filterSide, ch: filterSide, active: map[*node]bool{}}
		whole := rect{0, 0, filterSide, filterSide}
		// A colour matrix in linearRGB over a sub-region: its result is
		// cleared outside the sub-region, then converted back to sRGB and
		// copied into the canvas.
		sp := &filterSpec{rect: whole, prims: []fprim{{
			kind: tagFeColorMatrix, cmKind: cmSaturate, cmValues: []float64{0.5}, linear: true,
			rect: rect{0, 0, filterSide, filterSide / 2},
		}}}
		r.applyFilter(sp, identity, filterSource(filterSide))
		return r
	}
	full := &pollCtx{Context: context.Background()}
	r := render(full)
	if r.err != nil {
		t.Fatal(r.err)
	}
	// The primitive (conversion, matrix), the clearing, the conversion
	// back and the copy.
	want := (2 + 3) * bands
	t.Logf("%d polls, want at least %d", full.polls, want)
	if full.polls < want {
		t.Errorf("%d polls, want at least %d: a pass of applyFilter does not poll a band at a time", full.polls, want)
	}
	// Late, among the output passes.
	late := &pollCtx{Context: context.Background(), stop: full.polls - bands}
	if r := render(late); !errors.Is(r.err, context.Canceled) {
		t.Errorf("a context ended at poll %d of %d: err = %v", late.stop, full.polls, r.err)
	}
}

// A render whose filters are a chain of every primitive stops where its
// context ends, wherever that is.
func TestRenderFilterStops(t *testing.T) {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d"><filter id="f" filterUnits="userSpaceOnUse" x="0" y="0" width="%d" height="%d">`, filterSide, filterSide, filterSide, filterSide)
	b.WriteString(`<feGaussianBlur stdDeviation="5" result="a"/><feGaussianBlur stdDeviation="1" result="b"/><feOffset dx="3" result="c"/>` +
		`<feDropShadow stdDeviation="3" result="d"/><feBlend in="a" in2="b" mode="screen" result="e"/><feComposite in="c" in2="d" operator="arithmetic" k2=".5" k3=".5" result="g"/>` +
		`<feFlood flood-color="blue" flood-opacity=".3" result="h"/><feColorMatrix in="e" type="hueRotate" values="40" result="i"/>` +
		`<feComponentTransfer in="g"><feFuncA type="linear" slope=".8"/></feComponentTransfer><feMerge><feMergeNode in="h"/><feMergeNode in="i"/><feMergeNode/></feMerge>`)
	fmt.Fprintf(&b, `</filter><rect width="%d" height="%d" fill="rgba(200,100,0,.7)" filter="url(#f)"/></svg>`, filterSide/2, filterSide)
	svg := []byte(b.String())
	// The chain spends about 192 pixel operations a pixel, past the default
	// budget at this size: what is tested is where the context stops it.
	lim := Limits{MaxPixelOps: math.MaxInt}
	full := &pollCtx{Context: context.Background()}
	if _, err := Render(svg, Options{Context: full, Limits: lim}); err != nil {
		t.Fatal(err)
	}
	for _, stop := range []int{1, full.polls / 4, full.polls / 2, 3 * full.polls / 4, full.polls - 1} {
		ctx := &pollCtx{Context: context.Background(), stop: stop}
		if _, err := Render(svg, Options{Context: ctx, Limits: lim}); !errors.Is(err, context.Canceled) {
			t.Errorf("a context ended at poll %d of %d: err = %v", stop, full.polls, err)
		}
	}
}

// layerRenderer is a renderer of a side x side canvas under ctx, for the
// passes over layers.
func layerRenderer(ctx context.Context, side int) *renderer {
	lim := Limits{}.withDefaults()
	lim.MaxPixelOps = math.MaxInt
	r := &renderer{lim: lim, rast: newRasterizer(), ctx: ctx, cw: side, ch: side, active: map[*node]bool{}}
	r.cv = r.allocCanvas(side, side)
	return r
}

// paintLayer covers c with a translucent colour and marks it dirty.
func paintLayer(c *canvas) {
	for i := 0; i < len(c.pix); i += 4 {
		copy(c.pix[i:i+4], []uint8{100, 50, 0, 128})
	}
	c.dirty = c.bounds()
}

// The passes over a layer, compositing an opacity group or a COLR group and
// intersecting two clip coverage maps, poll a band at a time: a layer is as
// large as the canvas. Counted, not timed.
func TestLayerPassesPoll(t *testing.T) {
	const side = 2048
	bands := side / imageref.BandRows(side)
	cases := []struct {
		name string
		pass func(r *renderer)
	}{
		{"opacity group", func(r *renderer) {
			prev := r.pushLayer()
			paintLayer(r.cv)
			r.popLayer(prev, 0.5, blendMultiply)
		}},
		{"COLR group", func(r *renderer) {
			p := &colourPainter{r: r, st: &state{}}
			p.PushGroup()
			paintLayer(r.cv)
			p.PopGroup(shape.CompositeSrcIn)
		}},
		{"clip intersection", func(r *renderer) {
			whole := irect{0, 0, side, side}
			a, b := &mask{r: whole, a: make([]uint8, side*side)}, &mask{r: whole, a: make([]uint8, side*side)}
			for i := range a.a {
				a.a[i], b.a[i] = 200, 100
			}
			r.intersectMasks(a, b)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := &pollCtx{Context: context.Background()}
			r := layerRenderer(full, side)
			tc.pass(r)
			if r.err != nil {
				t.Fatal(r.err)
			}
			if full.polls < bands {
				t.Errorf("%d polls for a pass of %d bands", full.polls, bands)
			}
			if r.pixelOps < side*side {
				t.Errorf("%d pixel operations charged for a pass of %d pixels", r.pixelOps, side*side)
			}
			half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
			hr := layerRenderer(half, side)
			tc.pass(hr)
			if !errors.Is(hr.err, context.Canceled) || hr.pixelOps > r.pixelOps*3/4 {
				t.Errorf("a context ended at poll %d of %d: err = %v, %d of %d pixel operations charged", half.stop, full.polls, hr.err, hr.pixelOps, r.pixelOps)
			}
		})
	}
}

// Applying a mask, and compositing a filter's result, poll a band at a time
// of the canvas-sized layer: a render with either polls at least a pass's
// bands more than the same render without it, beyond the passes it makes
// anyway. Counted, not timed.
func TestEffectPassesPoll(t *testing.T) {
	const side = 2048
	bands := side / imageref.BandRows(side)
	polls := func(body string) int {
		svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">%s</svg>`, side, side, body)
		ctx := &pollCtx{Context: context.Background()}
		if _, err := Render([]byte(svg), Options{Context: ctx}); err != nil {
			t.Fatal(err)
		}
		return ctx.polls
	}
	rect := fmt.Sprintf(`<rect width="%d" height="%d" fill="rgba(200,100,0,.7)"`, side, side)
	plain := polls(rect + `/>`)
	for _, tc := range []struct {
		name, body string
		passes     int // beyond the plain render's
	}{
		// The mask's multiplication, then the content layer's composite.
		{"mask", fmt.Sprintf(`<mask id="m" maskUnits="userSpaceOnUse" x="0" y="0" width="%d" height="%d"/>`, side, side) + rect + ` mask="url(#m)"/>`, 2},
		// The result's copy into the layer, then the layer's composite.
		{"filter", fmt.Sprintf(`<filter id="f" filterUnits="userSpaceOnUse" x="0" y="0" width="%d" height="%d"><feOffset/></filter>`, side, side) + rect + ` filter="url(#f)"/>`, 2},
	} {
		got := polls(tc.body) - plain
		t.Logf("%s: %d polls more than without it, want at least %d", tc.name, got, tc.passes*bands)
		if got < tc.passes*bands {
			t.Errorf("%s: %d polls more than without it, want at least %d (%d passes of %d bands)", tc.name, got, tc.passes*bands, tc.passes, bands)
		}
	}
}

// Once its work is refused, a banded pass runs no further band: the passes
// over rows, the box blur's over lines and the layers' over regions.
func TestBandedPassesStop(t *testing.T) {
	const w, h = 1024, 1024 // 16 bands
	refuseAt := func(k int) (func(int) bool, *int) {
		calls := 0
		return func(int) bool { calls++; return calls < k }, &calls
	}
	work, _ := refuseAt(3)
	ran := 0
	if bandRows(w, h, 1, work, func(_, _ int) { ran++ }) || ran != 2 {
		t.Errorf("bandRows refused at its third band: ran %d bands, want 2 and false", ran)
	}
	for _, tc := range []struct {
		name                          string
		radius, step, lineStep, n, nl int
	}{
		{"rows", 3, 4, w * 4, w, h},
		{"columns", 3, w * 4, 4, h, w},
		{"copy", 0, 4, w * 4, w, h},
	} {
		src, dst := make([]uint8, w*h*4), make([]uint8, w*h*4)
		for i := range src {
			src[i] = 200
		}
		work, calls := refuseAt(3)
		if boxPass(dst, src, w, h, tc.radius, tc.step, tc.lineStep, tc.n, tc.nl, work) {
			t.Errorf("box pass (%s) refused at its third band: reported done", tc.name)
		}
		touched := 0
		for i := 3; i < len(dst); i += 4 {
			if dst[i] != 0 {
				touched++
			}
		}
		if want := 2 * imageref.BandRows(tc.n) * tc.n; touched != want || *calls != 3 {
			t.Errorf("box pass (%s) refused at its third band: %d pixels written, %d calls; want %d and 3", tc.name, touched, *calls, want)
		}
	}
	ctx := &pollCtx{Context: context.Background(), stop: 3}
	r := layerRenderer(ctx, w)
	ran = 0
	if r.bands(irect{0, 0, w, h}, func(irect) { ran++ }) || ran != 2 || !errors.Is(r.err, context.Canceled) {
		t.Errorf("a pass over a layer, cancelled at its third band: ran %d bands, err %v; want 2 and context.Canceled", ran, r.err)
	}
}
