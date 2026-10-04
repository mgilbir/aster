package aster_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mgilbir/aster"
)

// These tests pin down raster-image (<image>) support: a correctly-formatted
// embedded raster renders, while malformed or unsupported payloads are handled
// gracefully (no panic) rather than rendering garbage. This guards the
// rasterizer's image support and its bounds.

// redPNG is a small solid-red PNG.
func redPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, color.RGBA{R: 220, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

// validPNGDataURL returns a data: URL for a small solid-red PNG.
func validPNGDataURL(t *testing.T) string {
	t.Helper()
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(redPNG(t))
}

// renderImage rasterizes a 40x40 SVG containing a single <image> with href, and
// returns the decoded output plus any render error.
func renderImage(t *testing.T, href string, opts ...aster.Option) (image.Image, error) {
	t.Helper()
	c, err := aster.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()

	svg := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="40">` +
		`<image xlink:href="` + href + `" x="0" y="0" width="40" height="40"/></svg>`
	out, err := c.SVGToPNG(svg)
	if err != nil {
		return nil, err
	}
	img, derr := png.Decode(bytes.NewReader(out))
	if derr != nil {
		t.Fatalf("output is not a valid PNG: %v", derr)
	}
	return img, nil
}

func countReddish(img image.Image) int {
	b := img.Bounds()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a>>8 > 200 && r>>8 > 150 && g>>8 < 110 && bl>>8 < 110 {
				n++
			}
		}
	}
	return n
}

// A correctly-formatted embedded PNG must actually rasterize.
func TestEmbeddedPNGRenders(t *testing.T) {
	img, err := renderImage(t, validPNGDataURL(t))
	if err != nil {
		t.Fatalf("SVGToPNG with a valid embedded PNG: %v", err)
	}
	if n := countReddish(img); n < 200 {
		t.Fatalf("expected the embedded PNG to render (only %d red pixels)", n)
	}
}

// Malformed image bytes under a raster MIME must not crash or render garbage:
// resvg drops the broken image and still produces a valid (blank) PNG.
func TestMalformedImageRendersBlank(t *testing.T) {
	img, err := renderImage(t, "data:image/png;base64,QUJDREVGRw==") // "ABCDEFG", not a PNG
	if err != nil {
		// Rejecting it outright is also acceptable; the point is no panic.
		return
	}
	if n := countReddish(img); n != 0 {
		t.Fatalf("malformed image should not render pixels, got %d", n)
	}
}

// An unsupported declared format is ignored gracefully (no panic, valid output).
func TestUnsupportedImageFormatIgnored(t *testing.T) {
	if _, err := renderImage(t, "data:image/tiff;base64,SUkqAAgAAAA="); err != nil {
		return // graceful rejection is fine
	}
}

// A data: image is bounded before it is decoded: by its size, base64 or not,
// and by the dimensions in its header.
func TestDataURIImageLimits(t *testing.T) {
	ihdr := func(w, h uint32) string {
		chunk := make([]byte, 0, 17)
		chunk = append(chunk, "IHDR"...)
		chunk = binary.BigEndian.AppendUint32(chunk, w)
		chunk = binary.BigEndian.AppendUint32(chunk, h)
		chunk = append(chunk, 8, 6, 0, 0, 0) // 8-bit RGBA
		b := []byte("\x89PNG\r\n\x1a\n")
		b = binary.BigEndian.AppendUint32(b, 13)
		b = append(b, chunk...)
		b = binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(chunk))
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
	}
	for name, href := range map[string]string{
		"base64 payload":  "data:image/png;base64," + strings.Repeat("A", 48<<20),
		"escaped payload": "data:image/png," + strings.Repeat("a", 33<<20),
		"pixel count":     ihdr(60000, 60000),
		"dimension":       ihdr(70000, 1),
	} {
		if _, err := renderImage(t, href); !errors.Is(err, aster.ErrLimit) {
			t.Errorf("%s: err = %v, want a limit", name, err)
		}
	}
	for name, href := range map[string]string{
		"no comma":          "data:image/png;base64",
		"empty":             "data:",
		"bad base64":        "data:image/png;base64,@@@@",
		"truncated base64":  "data:image/png;base64,iVBORw0KGgo",
		"bad percent":       "data:image/png,%zz",
		"not an image":      "data:text/html,<script>alert(1)</script>",
		"remote":            "http://127.0.0.1:1/x.png",
		"file":              "file:///etc/passwd",
		"relative":          "../../etc/passwd",
		"empty after comma": "data:image/png;base64,",
	} {
		if _, err := renderImage(t, href); err != nil && errors.Is(err, aster.ErrLimit) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// imageServer serves /red.png, and counts the requests for it.
func imageServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	body := redPNG(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/red.png" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// An image the Loader can fetch is drawn in PNG output, as it is in a browser.
func TestRemoteImageRenders(t *testing.T) {
	srv, hits := imageServer(t)
	loader := aster.WithLoader(&aster.HTTPLoader{AllowPrivateNetworks: true})
	img, err := renderImage(t, srv.URL+"/red.png", loader)
	if err != nil {
		t.Fatal(err)
	}
	if n := countReddish(img); n < 1500 {
		t.Fatalf("the fetched PNG did not render (only %d red pixels)", n)
	}

	// Through Vega: the image mark's URL is sanitized into the SVG, then
	// fetched for the PNG. Two marks with the same URL fetch it once.
	c, err := aster.New(loader)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	hits.Store(0)
	spec := `{"width":40,"height":20,"marks":[
	  {"type":"image","encode":{"enter":{"url":{"value":"` + srv.URL + `/red.png"},"x":{"value":0},"width":{"value":20},"height":{"value":20}}}},
	  {"type":"image","encode":{"enter":{"url":{"value":"` + srv.URL + `/red.png"},"x":{"value":20},"width":{"value":20},"height":{"value":20}}}}]}`
	out, err := c.VegaToPNG([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	vimg, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if n := countReddish(vimg); n < 600 {
		t.Fatalf("the image marks did not render (only %d red pixels)", n)
	}
	if hits.Load() != 1 {
		t.Errorf("the image was fetched %d times, want once", hits.Load())
	}
}

// A relative image path is read through a FileLoader.
func TestRelativeImageRenders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "red.png"), redPNG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	img, err := renderImage(t, "red.png", aster.WithLoader(&aster.FileLoader{BaseDir: dir}))
	if err != nil {
		t.Fatal(err)
	}
	if n := countReddish(img); n < 1500 {
		t.Fatalf("the file did not render (only %d red pixels)", n)
	}
}

// What the Loader refuses or cannot fetch is drawn as a broken image: not at
// all, and the render goes on.
func TestRefusedImageRendersBlank(t *testing.T) {
	srv, hits := imageServer(t)
	for name, tc := range map[string]struct {
		href string
		opts []aster.Option
	}{
		"default loader":         {srv.URL + "/red.png", nil},
		"private network denied": {srv.URL + "/red.png", []aster.Option{aster.WithLoader(aster.NewHTTPLoader(nil))}},
		"not found":              {srv.URL + "/missing.png", []aster.Option{aster.WithLoader(&aster.HTTPLoader{AllowPrivateNetworks: true})}},
		"outside the directory":  {"../red.png", []aster.Option{aster.WithLoader(&aster.FileLoader{BaseDir: t.TempDir()})}},
	} {
		img, err := renderImage(t, tc.href, tc.opts...)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if n := countReddish(img); n != 0 {
			t.Errorf("%s: %d red pixels, want none", name, n)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("a refused image was fetched %d times", hits.Load())
	}
}

// An image over the loader's size limit fails the render as a limit, as an
// oversized data: image does.
func TestRemoteImageLimit(t *testing.T) {
	srv, _ := imageServer(t)
	_, err := renderImage(t, srv.URL+"/red.png", aster.WithLoader(&aster.HTTPLoader{AllowPrivateNetworks: true, MaxResponseBytes: 16}))
	if !errors.Is(err, aster.ErrLimit) {
		t.Fatalf("err = %v, want a limit", err)
	}
}

// PDF output embeds the images the PNG would draw, fetched the same way, and a
// chart with an image mark converts.
func TestImagesInPDF(t *testing.T) {
	srv, hits := imageServer(t)
	c, err := aster.New(aster.WithLoader(&aster.HTTPLoader{AllowPrivateNetworks: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="40">` +
		`<image xlink:href="` + srv.URL + `/red.png" width="40" height="40"/></svg>`
	pdf, err := c.SVGToPDF(svg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("/Subtype /Image")) || hits.Load() != 1 {
		t.Errorf("the image was not embedded (%d fetches)", hits.Load())
	}

	spec := `{"width":40,"height":20,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"` + srv.URL + `/red.png"},"width":{"value":20},"height":{"value":20}}}}]}`
	pdf, err = c.VegaToPDF([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("/Subtype /Image")) {
		t.Error("the image mark was not embedded")
	}

	// The default loader leaves the image out, and the chart still converts.
	d, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	pdf, err = d.SVGToPDF(svg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pdf, []byte("/Subtype /Image")) {
		t.Error("the default loader fetched an image")
	}
}
