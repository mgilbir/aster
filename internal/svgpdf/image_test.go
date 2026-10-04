package svgpdf

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"sync/atomic"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/images"
)

func testImage(t *testing.T, transparent bool, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, color.NRGBA{R: 220, G: 30, B: 30, A: 255})
		}
	}
	if transparent {
		img.Set(0, 0, color.NRGBA{})
	}
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, transparent bool) []byte {
	return testImage(t, transparent, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) })
}

func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func imageSVG(images ...string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="20">` +
		strings.Join(images, "") + `</svg>`
}

func imageEl(href, extra string) string {
	return `<image xlink:href="` + href + `" width="40" height="20"` + extra + `/>`
}

// pdfImages reads back the image XObjects of a converted document.
func pdfImages(t *testing.T, pdf []byte) []images.ExtractedImage {
	t.Helper()
	doc, err := pdf0.Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("pdf0.Read: %v", err)
	}
	return doc.ExtractImages()
}

func TestImagePNG(t *testing.T) {
	pdf, err := Convert(imageSVG(imageEl(dataURL("image/png", pngBytes(t, false)), "")), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	imgs := pdfImages(t, pdf)
	if len(imgs) != 1 || imgs[0].Width != 16 || imgs[0].Height != 16 || imgs[0].Filter != "FlateDecode" {
		t.Fatalf("images = %+v, want one 16x16 Flate image", imgs)
	}
	if !imgs[0].Decoded {
		t.Fatalf("the image did not decode: %s", imgs[0].Note)
	}
	if r, g, b, _ := imgs[0].Image.At(8, 8).RGBA(); r>>8 != 220 || g>>8 != 30 || b>>8 != 30 {
		t.Errorf("pixel = %d %d %d, want 220 30 30", r>>8, g>>8, b>>8)
	}
	if bytes.Contains(pdf, []byte("/SMask")) {
		t.Error("an opaque image has a soft mask")
	}
}

func TestImageTransparentPNGHasSoftMask(t *testing.T) {
	pdf, err := Convert(imageSVG(imageEl(dataURL("image/png", pngBytes(t, true)), "")), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("/SMask")) {
		t.Error("a transparent image has no soft mask")
	}
}

func TestImageJPEGIsEmbeddedAsIs(t *testing.T) {
	jpg := testImage(t, false, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) })
	pdf, err := Convert(imageSVG(imageEl(dataURL("image/jpeg", jpg), "")), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	imgs := pdfImages(t, pdf)
	if len(imgs) != 1 || imgs[0].Filter != "DCTDecode" || imgs[0].ColorSpace != "DeviceRGB" {
		t.Fatalf("images = %+v, want one DCTDecode RGB image", imgs)
	}
	if !bytes.Contains(pdf, jpg) {
		t.Error("the JPEG was not embedded as it is")
	}
}

// renderContent is the uncompressed content stream of svg.
func renderContent(t *testing.T, svg string) (string, error) {
	t.Helper()
	root, err := parseSVG(context.Background(), svg, Limits{}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	content, _, _, _, _, _, err := render(root, nil, nil, Options{})
	return string(bytes.Join(content, nil)), err
}

func TestImagePlacement(t *testing.T) {
	src := dataURL("image/png", pngBytes(t, false))
	for _, tc := range []struct {
		extra, want string
	}{
		// A 16x16 image fits a 40x20 box at 20x20, centred.
		{` preserveAspectRatio="xMidYMid"`, "20 0 0 -20 10 20 cm\n/Im0 Do"},
		{``, "20 0 0 -20 10 20 cm\n/Im0 Do"},
		{` preserveAspectRatio="none"`, "40 0 0 -20 0 20 cm\n/Im0 Do"},
		{` preserveAspectRatio="none" x="5" y="2"`, "40 0 0 -20 5 22 cm\n/Im0 Do"},
	} {
		got, err := renderContent(t, imageSVG(imageEl(src, tc.extra)))
		if err != nil {
			t.Fatalf("%q: %v", tc.extra, err)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q: content\n%s\nwant %q", tc.extra, got, tc.want)
		}
	}
	if _, err := renderContent(t, imageSVG(imageEl(src, ` preserveAspectRatio="xMinYMin slice"`))); err == nil {
		t.Error("an unsupported preserveAspectRatio must be an error")
	}
	// The same image drawn twice is one XObject.
	got, err := renderContent(t, imageSVG(imageEl(src, ""), imageEl(src, "")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "/Im0 Do") != 2 || strings.Contains(got, "/Im1") {
		t.Errorf("an image drawn twice: %s", got)
	}
}

func TestImageFetched(t *testing.T) {
	red := pngBytes(t, false)
	var calls atomic.Int32
	opts := Options{Images: func(ctx context.Context, href string) ([]byte, error) {
		calls.Add(1)
		switch href {
		case "http://example.test/red.png":
			return red, nil
		case "http://example.test/huge.png":
			return nil, ErrLimit
		}
		return nil, errors.New("not found")
	}}
	pdf, err := Convert(imageSVG(imageEl("http://example.test/red.png", ""), imageEl("http://example.test/red.png", ""), imageEl("http://example.test/missing.png", "")), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pdfImages(t, pdf)); n != 1 {
		t.Errorf("%d images, want the fetched one alone", n)
	}
	if calls.Load() != 2 {
		t.Errorf("%d fetches, want one per distinct href", calls.Load())
	}
	if _, err := Convert(imageSVG(imageEl("http://example.test/huge.png", "")), nil, opts); !errors.Is(err, ErrLimit) {
		t.Errorf("err = %v, want a limit", err)
	}
	// Without a fetcher a remote image is left out.
	pdf, err = Convert(imageSVG(imageEl("http://example.test/red.png", "")), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pdfImages(t, pdf)); n != 0 {
		t.Errorf("%d images without a fetcher, want none", n)
	}
}

func TestImageLimits(t *testing.T) {
	src := dataURL("image/png", pngBytes(t, false))
	if _, err := Convert(imageSVG(imageEl(src, "")), nil, Options{Limits: Limits{MaxImagePixels: 100}}); !errors.Is(err, ErrLimit) {
		t.Errorf("pixels: err = %v, want a limit", err)
	}
	if _, err := Convert(imageSVG(imageEl(src, "")), nil, Options{Limits: Limits{MaxImageBytes: 10}}); !errors.Is(err, ErrLimit) {
		t.Errorf("bytes: err = %v, want a limit", err)
	}
	// What does not decode is left out.
	if _, err := Convert(imageSVG(imageEl("data:image/png;base64,QUJD", "")), nil, Options{}); err != nil {
		t.Errorf("a broken image: %v", err)
	}
}
