package raster

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/fuzzutil"
)

// coverageDoc is a document and the options to draw it with.
type coverageDoc struct {
	svg  string
	opts Options
}

// coverageDocs are the synthetic documents of the tests, the SVG of real
// charts and random documents made to draw through every compositing path
// (genDocs), some with colour fonts.
func coverageDocs(t *testing.T) []coverageDoc {
	var docs []coverageDoc
	for _, list := range [][]synth{synths, featureSynths, extraSynths, filterParity} {
		for _, s := range list {
			docs = append(docs, coverageDoc{svg: s.svg})
		}
	}
	files := []string{"../../testdata/vl-convert/expected/v5_8/*.svg", "../../testdata/security/*.svg"}
	n := 400
	if testing.Short() {
		// The security documents draw up to the bounds, slowly.
		files, n = files[:1], 60
	}
	for _, d := range fuzzutil.Files(t, files...) {
		docs = append(docs, coverageDoc{svg: d})
	}
	for _, d := range genDocs(11, n, false) {
		docs = append(docs, coverageDoc{svg: d})
	}
	fonts := colourFonts(t)
	for _, d := range genDocs(12, n/4, true) {
		docs = append(docs, coverageDoc{svg: d, opts: Options{Fonts: fonts}})
	}
	return docs
}

// layerDocs draw layers that reuse one another's buffers: one that holds
// drawing grows while a larger buffer is idle in the pool, an empty one
// takes an idle one, and idle ones are dropped when the canvas memory runs
// short (layerDocLimits).
var layerDocs = []string{
	// A large layer given back, then a small one that grows piece by piece.
	`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="150"><g opacity="0.5"><rect width="200" height="150" fill="#08f"/></g>` +
		`<g opacity="0.6"><rect x="10" y="10" width="20" height="20" fill="#f00"/><rect x="150" y="100" width="30" height="30" fill="#0a0" fill-opacity="0.5"/><circle cx="100" cy="20" r="15" fill="#00f"/></g>` +
		`<g opacity="0.7"><circle cx="40" cy="120" r="10" fill="#f0f" stroke="#000" stroke-width="3"/><rect x="170" y="5" width="25" height="25" fill="#ff0"/></g></svg>`,
	// Nested layers, each composited into the next, after a layer is given
	// back elsewhere.
	`<svg xmlns="http://www.w3.org/2000/svg" width="160" height="120"><g opacity="0.5"><rect x="80" width="80" height="120" fill="#333"/></g>` +
		`<g opacity="0.9"><g opacity="0.8"><g opacity="0.7"><rect width="60" height="40" fill="#e33"/><rect x="100" y="80" width="50" height="30" fill="#3e3"/></g><circle cx="80" cy="60" r="30" fill="#33e" fill-opacity="0.4"/></g><rect y="100" width="160" height="20" fill="#999"/></g></svg>`,
	// Three nested layers: the outermost, empty, takes the buffer the
	// innermost gave back.
	`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="80"><g opacity="0.9"><g opacity="0.8"><g opacity="0.7"><rect x="10" y="10" width="90" height="70" fill="#c40" fill-opacity="0.6"/></g></g></g></svg>`,
	// A layer holding a square takes, to grow, the buffer of a nested
	// layer given back; the buffer it gives in return is drawn into, small,
	// by the last of three nested layers.
	`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="80"><g opacity="0.9"><rect x="5" y="5" width="8" height="8" fill="#048" fill-opacity="0.7"/>` +
		`<g opacity="0.8"><g opacity="0.7"><rect width="100" height="80" fill="#c40" fill-opacity="0.6"/></g></g></g>` +
		`<g opacity="0.5"><g opacity="0.5"><g opacity="0.5"><rect x="60" y="50" width="6" height="6" fill="#0c4" fill-opacity="0.5"/><rect x="62" y="51" width="3" height="3" fill="#00f" fill-opacity="0.5"/></g></g></g></svg>`,
	// Layers of fill and stroke, one after another, of growing size.
	`<svg xmlns="http://www.w3.org/2000/svg" width="120" height="120"><circle cx="20" cy="20" r="5" fill="#f80" stroke="#000" opacity="0.5"/><circle cx="60" cy="60" r="40" fill="#08f" stroke="#000" stroke-width="6" opacity="0.5"/>` +
		`<g opacity="0.5"><circle cx="100" cy="100" r="8" fill="#0f8" stroke="#000"/><circle cx="15" cy="100" r="12" fill="#80f" stroke="#000"/></g></svg>`,
}

// layerDocLimits are canvas memory bounds that leave layerDocs short of
// room for idle layers.
var layerDocLimits = []int{0, 200 * 150 * 4 * 3, 160 * 120 * 4 * 3}

// isPixelMemory reports whether err is the canvas memory bound, which a
// render that holds fewer pixels may stay within where another does not.
func isPixelMemory(err error) bool {
	return errors.Is(err, errLimit) && strings.Contains(err.Error(), "pixel memory")
}

// TestLayersHoldWhatIsDrawn checks that layers holding only the pixels drawn
// into them render every document exactly as layers holding the whole
// canvas do.
func TestLayersHoldWhatIsDrawn(t *testing.T) {
	scales := []float64{1, 2.5}
	if testing.Short() {
		scales = scales[:1]
	}
	for i, doc := range coverageDocs(t) {
		for _, scale := range scales {
			opts := doc.opts
			opts.Scale = scale
			got, gerr := Render([]byte(doc.svg), opts)
			opts.wholeLayers = true
			want, werr := Render([]byte(doc.svg), opts)
			if werr != nil || gerr != nil {
				if werr != nil && isPixelMemory(werr) && gerr == nil {
					continue // whole layers take more memory
				}
				if (werr == nil) != (gerr == nil) || werr.Error() != gerr.Error() {
					t.Fatalf("doc %d at %v: error %v, with whole layers %v", i, scale, gerr, werr)
				}
				continue
			}
			if got.Rect != want.Rect || string(got.Pix) != string(want.Pix) {
				t.Fatalf("doc %d at %v: pixels differ from whole layers'\n%s", i, scale, doc.svg)
			}
		}
	}
	for i, doc := range layerDocs {
		for _, scale := range []float64{1, 2.5} {
			for _, lim := range layerDocLimits {
				opts := Options{Scale: scale, Limits: Limits{MaxCanvasBytes: lim * int(scale*scale+1)}}
				got, gerr := Render([]byte(doc), opts)
				opts.Limits, opts.wholeLayers = Limits{}, true
				want, werr := Render([]byte(doc), opts)
				if gerr != nil || werr != nil {
					t.Fatalf("layer doc %d at %v, limit %d: error %v, with whole layers %v", i, scale, lim, gerr, werr)
				}
				if string(got.Pix) != string(want.Pix) {
					t.Fatalf("layer doc %d at %v, limit %d: pixels differ from whole layers'", i, scale, lim)
				}
			}
		}
	}
	if noPixel != [4]uint8{} {
		t.Fatalf("the transparent pixel was written: %v", noPixel)
	}
}

// TestRenderCoverageMatchesRender checks RenderCoverage against the alpha of
// RenderRGBA's canvas, cell by cell, at several scales and cell sizes, with
// and without a background.
func TestRenderCoverageMatchesRender(t *testing.T) {
	type run struct {
		scale float64
		cell  int
		bg    color.Color
	}
	// The label transform's samples are the 4 x 4 run's.
	runs := []run{{1, 1, nil}, {4, 4, nil}, {1, 5, color.NRGBA{9, 9, 9, 1}}, {2.5, 3, nil}, {1, 2, color.NRGBA{0, 0, 0, 0}}}
	if testing.Short() {
		runs = []run{{1, 1, nil}, {2, 2, nil}, {1, 5, color.NRGBA{9, 9, 9, 1}}}
	}
	inked, partial, total, bare := 0, 0, 0, 0
	for i, doc := range coverageDocs(t) {
		for _, rn := range runs {
			opts := doc.opts
			opts.Scale, opts.Background = rn.scale, rn.bg
			img, _, werr := RenderRGBA([]byte(doc.svg), opts)
			cov, gerr := RenderCoverage([]byte(doc.svg), rn.cell, opts)
			if werr != nil || gerr != nil {
				if werr != nil && isPixelMemory(werr) && gerr == nil {
					continue // the canvas takes more memory than the coverage
				}
				if (werr == nil) != (gerr == nil) || werr.Error() != gerr.Error() {
					t.Fatalf("doc %d %+v: RenderCoverage error %v, RenderRGBA's %v", i, rn, gerr, werr)
				}
				continue
			}
			total++
			want, part := cellsOf(img, rn.cell)
			if rn.bg == nil && rn.cell > 1 {
				bare++
				if part {
					partial++
				}
			}
			if cov.W != want.W || cov.H != want.H || cov.Cell != rn.cell {
				t.Fatalf("doc %d %+v: coverage %dx%d of %d, want %dx%d", i, rn, cov.W, cov.H, cov.Cell, want.W, want.H)
			}
			inkedRun := false
			for y := 0; y < want.H; y++ {
				for x := 0; x < want.W; x++ {
					if g, w := cov.Has(x, y), want.Has(x, y); g != w {
						t.Fatalf("doc %d %+v: cell (%d, %d) has ink %v, the canvas's %v\n%s", i, rn, x, y, g, w, doc.svg)
					}
					inkedRun = inkedRun || want.Has(x, y)
				}
			}
			if inkedRun {
				inked++
			}
			var each []image.Point
			for y := range cov.H {
				cov.Row(y, func(x int) { each = append(each, image.Pt(x, y)) })
			}
			k := 0
			for y := 0; y < want.H; y++ {
				for x := 0; x < want.W; x++ {
					if want.Has(x, y) {
						if k >= len(each) || each[k] != image.Pt(x, y) {
							t.Fatalf("doc %d %+v: Row does not list the cells with ink in order", i, rn)
						}
						k++
					}
				}
			}
			if k != len(each) {
				t.Fatalf("doc %d %+v: Row lists %d cells, %d have ink", i, rn, len(each), k)
			}
		}
	}
	// The documents must draw, and draw cells only partly covered (where a
	// stray sample decides; a background covers every cell, and a cell of
	// one pixel is never partly inked), for the comparison to show anything.
	if inked < total*3/4 || partial < bare/3 {
		t.Fatalf("of %d renders %d have ink; of %d of cells larger than a pixel without a background %d have a cell only partly inked", total, inked, bare, partial)
	}
	if noPixel != [4]uint8{} {
		t.Fatalf("the transparent pixel was written: %v", noPixel)
	}
	t.Logf("%d renders, %d with ink; %d of cells larger than a pixel without a background, %d of them with a cell only partly inked", total, inked, bare, partial)
}

// cellsOf is the coverage of img's alpha in cells of cell x cell pixels, and
// whether a cell has ink in only some of its pixels.
func cellsOf(img *image.RGBA, cell int) (*Coverage, bool) {
	b := img.Rect
	c := newCoverage(b.Dx(), b.Dy(), cell)
	n := make([]int, c.W*c.H)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if img.Pix[img.PixOffset(b.Min.X+x, b.Min.Y+y)+3] != 0 {
				i := (y/cell)*c.W + x/cell
				c.set(i)
				n[i]++
			}
		}
	}
	for i, k := range n {
		cw := min(cell, b.Dx()-(i%c.W)*cell)
		ch := min(cell, b.Dy()-(i/c.W)*cell)
		if k > 0 && k < cw*ch {
			return c, true
		}
	}
	return c, false
}

func TestCoverageCells(t *testing.T) {
	c := newCoverage(10, 7, 4) // 3 x 2 cells, the last column and row short
	if c.W != 3 || c.H != 2 {
		t.Fatalf("%d x %d cells", c.W, c.H)
	}
	c.set(5)
	if !c.Has(2, 1) || c.Has(1, 1) || c.Has(-1, 0) || c.Has(3, 0) || c.Has(0, 2) {
		t.Fatal("Has")
	}
	if n := coverageBytes(10, 7, 4); n != 8 {
		t.Fatalf("coverageBytes %d", n)
	}
	// Rows that share words with their neighbours, and rows of more than a
	// word, each listing its own cells only.
	for _, w := range []int{1, 3, 63, 64, 65, 130} {
		c := newCoverage(w, 5, 1)
		for i := range w * 5 {
			if (i*7)%5 < 2 {
				c.set(i)
			}
		}
		c.set(w*5 - 1)
		for y := -1; y <= 5; y++ {
			var got []int
			c.Row(y, func(x int) { got = append(got, x) })
			var want []int
			for x := 0; x < w && y >= 0 && y < 5; x++ {
				if c.Has(x, y) {
					want = append(want, x)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("width %d, row %d: %v, want %v", w, y, got, want)
			}
		}
	}
}
