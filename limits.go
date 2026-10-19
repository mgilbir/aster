package aster

import (
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/text"
	"github.com/mgilbir/aster/internal/vega"
	"github.com/mgilbir/aster/internal/vegalite"
)

// callLimits are every bound of one public call. They are derived from
// WithMemoryLimit in one place, deriveLimits, so that no two of them can
// follow different formulas; docs/limits.md documents each one, and
// TestLimitsDocMatchesCode checks it against this function.
type callLimits struct {
	// vega bounds the Vega run: rows, items, operators, loaded and parsed
	// bytes, expression strings, transform bitmaps.
	vega vega.Limits
	// raster bounds the rasterizer's canvas memory and pixels (PNG).
	raster raster.Limits
	// shaping bounds the glyphs of one shaped run of text.
	shaping text.ShapingLimits
	// svgBytes bounds the SVG a render may write.
	svgBytes int
	// specBytes bounds the specification's JSON (0: no bound).
	specBytes uint64
	// vlViews bounds the views a Vega-Lite compilation creates (repeats
	// multiply them).
	vlViews int
	// ledger bounds the memory charged to the call's ledger: what the
	// specification makes the engine allocate that no other bound counts.
	ledger budget.LedgerLimits
	// pixelOps and pixelOpsPerPixel are the rasterizer's work budget (PNG):
	// pixelOps operations at scale 1, times the scale squared above it, and
	// at least pixelOpsPerPixel per output pixel (rasterAt); pixelOps is
	// WithPixelWorkLimit's when given.
	pixelOps, pixelOpsPerPixel int
}

// Without WithMemoryLimit: the bounds that are not the zero value's defaults
// of the stage they belong to. The heaviest chart of the corpus
// (vl-examples/geo_circle) peaks at 130 MiB of heap rendered to SVG, PNG and
// PDF, and allocates 230 MiB in all; the ledger's default is about fifteen
// times the peak, and still a bound.
const (
	defaultSVGBytes    = 128 << 20
	defaultLedgerBytes = 2 << 30
)

// The rasterizer's work budget bounds time, not memory: it is the same
// whatever WithMemoryLimit says. The heaviest chart of the corpora
// (wild/vl_0123) spends about 140 operations per output pixel at every
// scale, and 128 Mi operations is 5.5 times what it spends at scale 1 (5.6
// at 2, 5.7 at 3; TestPixelOpsMarginOverCorpus holds every chart to half the
// budget). A document built to spend it all (2^12 nested <use> copies of a
// canvas-sized rectangle) takes a quarter of the 15 s CI took on the
// previous budget, 512 Mi. Above scale 1 the budget grows as the pixels do,
// with the scale squared, as the work does; the floor of 16 operations per
// output pixel admits a large canvas with little drawn on it.
const (
	defaultPixelOps         = 128 << 20
	defaultPixelOpsPerPixel = 16
)

// Per-object costs that turn a byte limit into counts: a data row, a scene
// item, the operators of a facet cell, a dataflow operator. RowBytes is also
// what a counted row stands for, so heavier rows are charged the difference.
const bytesPerRow, bytesPerItem, bytesPerCell, bytesPerOp = 512, 1536, 16384, 1536

// bytesPerView turns a byte limit into the Vega-Lite views a compilation may
// create: a plain unit view's compiled Vega retains about 24 KiB and the
// compilation allocates ten times that as it goes. The compiler weighs a
// view by the same measure (a view that compiles more text weighs more).
const bytesPerView = vegalite.ViewBytes

// specBytesDivisor sets the specification a memory limit admits: a sixteenth of
// the limit in bytes. The rows and items budgets charge what a specification
// makes the engine build, and none of them sees the text itself, which is all
// there is to an inline geometry; once parsed and compiled it holds about
// nineteen times its size, so a sixteenth of the limit stays within twice it.
const specBytesDivisor = 16

// deriveLimits turns WithMemoryLimit(n) into every bound of a call (n == 0:
// the defaults), and WithPixelWorkLimit(ops) into the rasterizer's work
// budget at scale 1 (ops == 0: the default). A bound derived from n replaces
// its default, so a large n raises it as a small one lowers it.
func deriveLimits(n, ops uint64) callLimits {
	// The Vega run's defaults are applied here, not left to the run: the
	// call sites read them too (the specification's parse bound, the load
	// allowance images share), and a zero read there is no bound (C40).
	l := callLimits{vega: vega.Limits{}.WithDefaults(), svgBytes: defaultSVGBytes, ledger: budget.LedgerLimits{MaxBytes: defaultLedgerBytes}, vlViews: vegalite.DefaultMaxViews,
		pixelOps: defaultPixelOps, pixelOpsPerPixel: defaultPixelOpsPerPixel}
	if ops > 0 {
		l.pixelOps = toInt(ops)
	}
	// The label transform's bitmaps have it too, at scale 1.
	l.vega.RasterPixelOps = l.pixelOps
	if n == 0 {
		return l
	}
	l.vega = vega.Limits{
		MaxRows:  toInt(max(n/bytesPerRow, 1000)),
		MaxItems: toInt(max(n/bytesPerItem, 1000)),
		// A facet cell instantiates its own operators; the default (20,000)
		// is the most that is left alone.
		MaxSubflows:    int(min(max(n/bytesPerCell, 1000), 20_000)),
		MaxOperators:   int(min(max(n/bytesPerOp, 10_000), 500_000)),
		MaxLoadBytes:   toInt64(max(n/2, 1<<20)),
		MaxStringBytes: toInt64(max(n/4, 1<<20)),
		MaxCanvasBytes: toInt64(max(n/2, 1<<20)),
		MaxParseBytes:  toInt64(max(n/2, 1<<20)),
		RowBytes:       bytesPerRow,
		RasterPixelOps: l.pixelOps,
	}
	// The canvas, layers and masks alive at once, and the pixels of one.
	canvas := min(max(n, 4<<20), 1<<30)
	// The path geometry a render holds at once, in 16-byte points, and the
	// pixels of one image it decodes (about 16 bytes each with its decoded
	// form and halvings): each a sixteenth of the limit.
	l.raster = raster.Limits{
		MaxCanvasBytes:  int(canvas),
		MaxPixels:       int(canvas / 8),
		MaxPathSegments: toInt(max(n/16, 64<<10)),
		MaxImagePixels:  int(min(max(n/16, 64<<10), 64<<20)),
	}
	// The label transform's bitmaps hold their pixels as a PNG does.
	l.vega.RasterCanvasBytes = l.raster.MaxCanvasBytes
	// The glyphs of one run of text, and the SVG, each a quarter of the limit.
	l.shaping = text.ShapingLimits{RunBytes: int64(min(max(n/4, 1<<20), 1<<40))}
	l.svgBytes = toInt(min(max(n/4, 1<<20), 1<<40))
	l.specBytes = max(n/specBytesDivisor, 1<<20)
	l.ledger.MaxBytes = toInt64(max(n, 1<<20))
	l.vlViews = toInt(max(n/bytesPerView, 64))
	return l
}

// rasterAt is the rasterizer's limits for a render at scale s: l.raster,
// with the work budget derived for s. Every caller has a scale, SVGToPNG's
// input included (WithScale's default is 1).
func (l callLimits) rasterAt(s float64) raster.Limits {
	r := l.raster
	r.MaxPixelOps = raster.ScaledPixelOps(l.pixelOps, s)
	r.PixelOpsPerPixel = l.pixelOpsPerPixel
	return r
}

// toInt converts a bound derived from WithMemoryLimit to an int, saturating
// where int is 32 bits: a wrapped bound would read as unlimited (<= 0) or as
// a spurious small one.
func toInt(n uint64) int { return int(min(n, math.MaxInt)) }

// toInt64 is toInt for an int64 bound.
func toInt64(n uint64) int64 { return int64(min(n, math.MaxInt64)) }
