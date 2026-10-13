package raster

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"sync"
	"testing"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/imageref"
	"github.com/mgilbir/aster/internal/text"
)

// pollCtx counts the polls of its Err and ends, if stop is set, at the
// stop-th: polls are counted, not timed, so no machine's speed changes it.
type pollCtx struct {
	context.Context
	mu    sync.Mutex
	polls int
	stop  int
}

func (c *pollCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if c.stop > 0 && c.polls >= c.stop {
		return context.Canceled
	}
	return nil
}

// bigImageSVG is an SVG of n <image> elements drawing one side x side
// two-colour PNG at 64x64 pixels, so that it is decoded, converted and
// halved four times, as testdata/security/bounded/raster-image-per-element
// is: a PNG of a few hundred bytes, millions of pixels of work.
func bigImageSVG(t *testing.T, side, n int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{color.White, color.Black})
	for y := 0; y < side; y += 2 {
		img.Pix[y*side+y/2] = 1
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	href := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	var svg bytes.Buffer
	svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64">`)
	for i := range n {
		fmt.Fprintf(&svg, `<image id="i%d" x="0" y="0" width="64" height="64" href="%s"/>`, i, href)
	}
	svg.WriteString(`</svg>`)
	return svg.Bytes()
}

// imageWork decodes the first image of doc and makes the level a 64-pixel
// drawing of it samples, under ctx, as renderImage does. It returns the
// renderer and how many halvings were made.
func imageWork(t *testing.T, doc *document, ctx context.Context) (*renderer, int) {
	t.Helper()
	lim := Limits{}.withDefaults()
	lim.MaxPixelOps = math.MaxInt
	r := &renderer{doc: doc, lim: lim, rast: newRasterizer(), ctx: ctx, active: map[*node]bool{}}
	r.initCaches()
	img, _ := r.imageFor(doc.ids["i0"])
	if img == nil {
		return r, 0
	}
	s := float64(img.w) / 64
	sampledLevel(r, img, matrix{s, 0, 0, s, 0, 0})
	return r, len(img.mips)
}

// An image's decoding, its conversion to premultiplied RGBA and its halvings
// poll the render's context a band at a time (the bounded case
// raster-image-per-element ran 4.4 s past a 2 s deadline on CI, as none of
// them polled). Counted, not timed: the work polls at least once per band of
// the conversion and of each halving, and per pollPixels pixels decoded, and
// a context that ends at its middle poll, or late in the halvings, stops it
// there.
func TestImageWorkPolls(t *testing.T) {
	const side = 2048
	doc, err := parseDocument(string(bigImageSVG(t, side, 1)), Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	full := &pollCtx{Context: context.Background()}
	r, levels := imageWork(t, doc, full)
	if r.err != nil || levels != 4 {
		t.Fatalf("made %d levels, err %v; want 4", levels, r.err)
	}
	bands := func(w, h int) int { return (h + imageref.BandRows(w) - 1) / imageref.BandRows(w) }
	conversion := bands(side, side)
	halvings := 0
	for w := side / 2; w >= side/16; w /= 2 {
		halvings += bands(w, w)
	}
	// Decoding polls as imageref.Decode does (its own test holds that to
	// the pixels decoded): counted on its own, so that the stages after it
	// are held to theirs exactly.
	data, err := imageref.DataURI(doc.ids["i0"].str(aHref), r.lim.imageLimits())
	if err != nil {
		t.Fatal(err)
	}
	dec := &pollCtx{Context: context.Background()}
	if _, err := imageref.Decode(dec, data, r.lim.imageLimits()); err != nil {
		t.Fatal(err)
	}
	decoding := dec.polls
	t.Logf("%d polls: at least %d decoding, %d converting, %d halving", full.polls, decoding, conversion, halvings)
	if want := decoding + conversion + halvings; full.polls < want {
		t.Errorf("%d polls, want at least %d: a stretch of the image's work does not poll", full.polls, want)
	}

	// Stopped at the middle poll: the decoding or the conversion, before
	// any halving.
	half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
	r, levels = imageWork(t, doc, half)
	if !errors.Is(r.err, context.Canceled) || levels != 0 {
		t.Errorf("a context ended at poll %d of %d: %d levels made, err %v; want none and context.Canceled", half.stop, full.polls, levels, r.err)
	}
	// Stopped among the halvings' polls: the first halving is most of
	// them, so it stops there, before the last.
	late := &pollCtx{Context: context.Background(), stop: full.polls - halvings/2}
	r, levels = imageWork(t, doc, late)
	if !errors.Is(r.err, context.Canceled) || levels >= 4 {
		t.Errorf("a context ended at poll %d of %d: %d levels made, err %v; want fewer than 4 and context.Canceled", late.stop, full.polls, levels, r.err)
	}
}

// A render of many elements drawing one big image stops where its context
// ends, without reporting the image as one that could not be read.
func TestRenderImageStops(t *testing.T) {
	svg := bigImageSVG(t, 2048, 16)
	full := &pollCtx{Context: context.Background()}
	if _, err := Render(svg, Options{Context: full}); err != nil {
		t.Fatal(err)
	}
	for _, stop := range []int{1, full.polls / 4, full.polls / 2, full.polls - 1} {
		var reported []error
		ctx := &pollCtx{Context: context.Background(), stop: stop}
		_, err := Render(svg, Options{Context: ctx, ImageError: func(_ string, err error) { reported = append(reported, err) }})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a context ended at poll %d of %d: err = %v", stop, full.polls, err)
		}
		if len(reported) > 0 {
			t.Errorf("a context ended at poll %d of %d: reported %v as image errors", stop, full.polls, reported)
		}
	}
}

// The passes over the whole canvas, filling the background and converting
// the result to non-premultiplied pixels, poll once a band each: the canvas
// is as large as the output. Counted, not timed.
func TestCanvasPassesPoll(t *testing.T) {
	const side = 2048
	svg := []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d"/>`, side, side))
	full := &pollCtx{Context: context.Background()}
	if _, err := Render(svg, Options{Context: full, Background: color.White}); err != nil {
		t.Fatal(err)
	}
	bands := side * side / imageref.BandPixels
	if full.polls < 2*bands {
		t.Errorf("%d polls for two passes of %d bands each", full.polls, bands)
	}
	for _, stop := range []int{bands / 2, bands + bands/2} {
		if _, err := Render(svg, Options{Context: &pollCtx{Context: context.Background(), stop: stop}, Background: color.White}); !errors.Is(err, context.Canceled) {
			t.Errorf("a context ended at poll %d of %d: err = %v", stop, full.polls, err)
		}
	}
}

// A colour glyph painted as an image, for the PDF writer, polls the
// context it is given as a render does. Counted, not timed.
func TestColourGlyphImagePolls(t *testing.T) {
	data, err := os.ReadFile("../../testdata/colourfonts/ColourTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	m, err := text.New(text.WithFont("ColourTest", data))
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := m.ShapeText("\U0001F534", "20px ColourTest")
	face, gid := runs[0].Face, runs[0].Glyphs[0].GID
	const ppem = 4000 // a 3200-pixel square
	full := &pollCtx{Context: context.Background()}
	if _, _, err := ColourGlyphImage(full, face, gid, shape.Color{A: 255}, ppem, 0); err != nil {
		t.Fatal(err)
	}
	if bands := 3200 * 3200 / imageref.BandPixels; full.polls < bands {
		t.Errorf("%d polls painting a glyph of %d bands", full.polls, bands)
	}
	half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
	if _, _, err := ColourGlyphImage(half, face, gid, shape.Color{A: 255}, ppem, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("a context ended at poll %d of %d: err = %v", half.stop, full.polls, err)
	}
}
