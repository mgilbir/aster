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
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
)

// feImageSVG is a 200x150 document whose rectangle is filtered by a filter
// of the whole canvas made of prims, with defs beside it.
func feImageSVG(prims, defs string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs>` +
		`<filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse">` + prims + `</filter>` + defs +
		`</defs><rect x="40" y="30" width="60" height="50" fill="#c33" filter="url(#f)"/></svg>`)
}

// pngBytes is a w x h PNG of a gradient.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 200, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// An feImage of another href than a data: URI or a fragment draws what
// Options.Images fetched for it, as an <image> does: the same pixels as the
// data: URI of the same bytes. Unfetched, it is not drawn, and reported.
func TestFeImageFetched(t *testing.T) {
	data := pngBytes(t, 12, 9)
	prims := func(href string) string {
		return `<feImage xlink:href="` + href + `" x="20" y="10" width="120" height="90" preserveAspectRatio="xMidYMid slice"/>`
	}
	want, err := Render(feImageSVG(prims("data:image/png;base64,"+base64.StdEncoding.EncodeToString(data)), ""), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	got, err := Render(feImageSVG(prims("https://example.com/a.png"), ""), Options{Images: func(_ context.Context, href string) ([]byte, error) {
		asked = append(asked, href)
		return data, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(asked) != "[https://example.com/a.png]" {
		t.Errorf("fetched %v, want the feImage's href once", asked)
	}
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Error("a fetched feImage differs from the same image as a data: URI")
	}
	var reported []string
	none, err := Render(feImageSVG(prims("https://example.com/a.png"), ""), Options{ImageError: func(href string, _ error) { reported = append(reported, href) }})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(reported) != "[https://example.com/a.png]" {
		t.Errorf("reported %v, want the unfetched href", reported)
	}
	for i, v := range none.Pix {
		if v != 0 {
			t.Fatalf("an unfetched feImage drew byte %d = %d: want a transparent result", i, v)
		}
	}
	// Past the image limits, as an <image>: the render fails.
	_, err = Render(feImageSVG(prims("https://example.com/a.png"), ""), Options{
		Images: func(context.Context, string) ([]byte, error) { return data, nil },
		Limits: Limits{MaxImagePixels: 50},
	})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("an feImage over MaxImagePixels: err = %v, want ErrLimit", err)
	}
}

// pixelAt is the NRGBA pixel at (x, y).
func pixelAt(img *image.NRGBA, x, y int) [4]uint8 {
	i := y*img.Stride + x*4
	return [4]uint8(img.Pix[i : i+4])
}

// An feImage that draws an element whose filter is being applied (a cycle
// through a group) ends: the element is not drawn inside its own filter's
// feImage. usvg's own tree recurses until its stack overflows.
func TestFeImageCycle(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs>
<filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#g"/><feOffset dx="50" dy="40"/></filter></defs>
<g id="g"><rect x="10" y="10" width="30" height="30" fill="lime"/><rect x="100" y="80" width="20" height="20" fill="red" filter="url(#f)"/></g></svg>`)
	img, err := Render(svg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The group's lime square, drawn by the feImage and moved by (50, 40);
	// the filtered red square itself is not drawn inside it (it would be
	// moved to (150, 120)), and the filter's result replaces it.
	for _, tc := range []struct {
		x, y int
		want [4]uint8
	}{
		{20, 20, [4]uint8{0, 255, 0, 255}}, // the group's own lime square
		{70, 60, [4]uint8{0, 255, 0, 255}}, // the feImage's copy, moved
		{110, 90, [4]uint8{0, 0, 0, 0}},    // the filtered square: its result
		{160, 130, [4]uint8{0, 0, 0, 0}},   // nor drawn moved inside it
		{150, 120, [4]uint8{0, 0, 0, 0}},
	} {
		if got := pixelAt(img, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d, %d) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
}

// chainSVG is a chain of n filters, each an feImage drawing an element that
// has the next filter.
func chainSVG(n int) []byte {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs>`)
	for i := range n {
		fmt.Fprintf(&b, `<filter id="f%d" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e%d"/><feOffset dx="1"/></filter>`, i, i)
		next := ""
		if i+1 < n {
			next = fmt.Sprintf(` filter="url(#f%d)"`, i+1)
		}
		fmt.Fprintf(&b, `<rect id="e%d" x="%d" y="20" width="10" height="10" fill="blue"%s/>`, i, 10+i, next)
	}
	b.WriteString(`</defs><rect width="20" height="20" fill="red" filter="url(#f0)"/></svg>`)
	return []byte(b.String())
}

// A chain of feImages each drawing an element filtered by the next is as
// deep as its length (a filter layer and the feImage's drawing a link), and
// fails with ErrLimit past MaxLayerDepth; a short one renders.
func TestFeImageDepth(t *testing.T) {
	depth := Limits{}.withDefaults().MaxLayerDepth
	ok := depth/2 - 1 // each link is two layers: the filter's and the feImage's
	if _, err := Render(chainSVG(ok), Options{}); err != nil {
		t.Errorf("a chain of %d feImages: %v", ok, err)
	}
	for _, n := range []int{depth/2 + 1, 100} {
		if _, err := Render(chainSVG(n), Options{}); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("a chain of %d feImages: err = %v, want ErrLimit", n, err)
		}
	}
}

// What an feImage draws polls the render's context as any drawing does, a
// band at a time of the canvas-sized element it draws: a render with it
// polls at least a pass's bands more than one without it, and a context
// that ends partway stops it. Counted, not timed.
func TestFeImagePolls(t *testing.T) {
	const side = 2048
	bands := side / imageref.BandRows(side)
	doc := func(href string) []byte {
		return []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%d" height="%d"><defs>`+
			`<filter id="f" x="0" y="0" width="%d" height="%d" filterUnits="userSpaceOnUse"><feImage xlink:href="%s"/></filter>`+
			`<rect id="big" width="%d" height="%d" fill="rgba(0,90,200,.6)"/></defs>`+
			`<rect width="10" height="10" fill="red" filter="url(#f)"/></svg>`, side, side, side, side, href, side, side))
	}
	polls := func(svg []byte) int {
		ctx := &pollCtx{Context: context.Background()}
		if _, err := Render(svg, Options{Context: ctx}); err != nil {
			t.Fatal(err)
		}
		return ctx.polls
	}
	without, with := polls(doc("#none")), polls(doc("#big"))
	t.Logf("%d polls drawing the element, %d without; want at least %d more", with, without, bands)
	if with-without < bands {
		t.Errorf("%d polls more drawing a %dx%d element in an feImage, want at least %d", with-without, side, side, bands)
	}
	stop := &pollCtx{Context: context.Background(), stop: without + (with-without)/2}
	if _, err := Render(doc("#big"), Options{Context: stop}); !errors.Is(err, context.Canceled) {
		t.Errorf("a context ended while the feImage draws: err = %v", err)
	}
}
