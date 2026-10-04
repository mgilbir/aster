package svgpdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/mgilbir/aster/internal/imageref"
	pdf0 "github.com/mgilbir/pdf0"
)

// pdfImage is one image XObject: its stream, the soft mask of its alpha, and
// its size in pixels.
type pdfImage struct {
	res    string // resource name (Im0, Im1, ...)
	w, h   int
	data   []byte
	filter string // DCTDecode (a JPEG as it is) or FlateDecode
	space  string // DeviceRGB or DeviceGray
	smask  []byte // Flate-compressed alpha; nil when the image is opaque
}

// imageCatalog holds the images a document draws, each href once, in the
// order they are first drawn.
type imageCatalog struct {
	lim     imageref.Limits
	fetched map[string][]byte    // bytes by href, from Options.Images
	byHref  map[string]*pdfImage // nil: the image cannot be drawn
	list    []*pdfImage
}

func newImageCatalog(fetched map[string][]byte, lim Limits) *imageCatalog {
	return &imageCatalog{lim: lim.imageLimits(), fetched: fetched, byHref: map[string]*pdfImage{}}
}

// get returns the image href refers to, or nil when there is none to draw: an
// image that was not fetched or does not decode is left out, as a broken image
// is. An image over the limits is an error.
func (c *imageCatalog) get(href string) (*pdfImage, error) {
	href = strings.TrimSpace(href)
	if img, ok := c.byHref[href]; ok {
		return img, nil
	}
	img, err := c.load(href)
	if err != nil {
		if errors.Is(err, ErrLimit) {
			return nil, err
		}
		img = nil
	}
	if img != nil {
		img.res = fmt.Sprintf("Im%d", len(c.list))
		c.list = append(c.list, img)
	}
	c.byHref[href] = img
	return img, nil
}

func (c *imageCatalog) load(href string) (*pdfImage, error) {
	data, ok := c.fetched[href]
	if !ok {
		if !imageref.IsData(href) {
			return nil, errors.New("svgpdf: image not fetched")
		}
		var err error
		if data, err = imageref.DataURI(href, c.lim); err != nil {
			return nil, err
		}
	}
	cfg, format, err := imageref.Config(data, c.lim)
	if err != nil {
		return nil, err
	}
	// A JPEG in RGB or grey goes in as it is: PDF decodes it (DCTDecode). A
	// CMYK one is decoded below, as Adobe's inverted CMYK would need a Decode
	// array to be read right.
	if format == "jpeg" && (cfg.ColorModel == color.YCbCrModel || cfg.ColorModel == color.GrayModel) {
		space := "DeviceRGB"
		if cfg.ColorModel == color.GrayModel {
			space = "DeviceGray"
		}
		return &pdfImage{w: cfg.Width, h: cfg.Height, data: data, filter: "DCTDecode", space: space}, nil
	}
	src, err := imageref.Decode(data, c.lim)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), src, b.Min, draw.Src)
	n := b.Dx() * b.Dy()
	rgb := make([]byte, 0, 3*n)
	alpha := make([]byte, 0, n)
	opaque := true
	for i := 0; i < len(nrgba.Pix); i += 4 {
		rgb = append(rgb, nrgba.Pix[i], nrgba.Pix[i+1], nrgba.Pix[i+2])
		alpha = append(alpha, nrgba.Pix[i+3])
		opaque = opaque && nrgba.Pix[i+3] == 0xff
	}
	img := &pdfImage{w: b.Dx(), h: b.Dy(), filter: "FlateDecode", space: "DeviceRGB"}
	if img.data, err = deflate(rgb); err != nil {
		return nil, err
	}
	if !opaque {
		if img.smask, err = deflate(alpha); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// deflate is zlib at the default level, which is deterministic for a given
// input.
func deflate(b []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(b); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing image: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("svgpdf: compressing image: %w", err)
	}
	return out.Bytes(), nil
}

// drawImage draws an <image> into its box (x, y, width, height), placed as
// preserveAspectRatio says: "none" stretches it, and the default (what Vega
// writes, "xMidYMid") fits it whole, centred. A box missing a side takes it
// from the image's own proportions, and a box missing both is the image's
// size.
func (r *renderer) drawImage(e *element, st gstate) error {
	img, err := r.images.get(e.attrVal("href"))
	if err != nil || img == nil {
		return err
	}
	num := func(name string) (float64, bool, error) {
		v, ok := e.attr(name)
		if !ok || strings.TrimSpace(v) == "" {
			return 0, false, nil
		}
		f, err := parseLength(v)
		if err != nil {
			return 0, false, fmt.Errorf("svgpdf: <image> %s: %w", name, err)
		}
		return f, true, nil
	}
	x, _, err := num("x")
	if err != nil {
		return err
	}
	y, _, err := num("y")
	if err != nil {
		return err
	}
	w, hasW, err := num("width")
	if err != nil {
		return err
	}
	h, hasH, err := num("height")
	if err != nil {
		return err
	}
	iw, ih := float64(img.w), float64(img.h)
	switch {
	case !hasW && !hasH:
		w, h = iw, ih
	case !hasW:
		w = h * iw / ih
	case !hasH:
		h = w * ih / iw
	}
	if !(w > 0 && h > 0) {
		return nil
	}
	dw, dh := w, h
	switch par := strings.Join(strings.Fields(e.attrVal("preserveAspectRatio")), " "); par {
	case "none":
	case "", "xMidYMid", "xMidYMid meet":
		s := min(w/iw, h/ih)
		dw, dh = iw*s, ih*s
	default:
		return fmt.Errorf("svgpdf: unsupported preserveAspectRatio %q on <image>", par)
	}
	ox, oy := x+(w-dw)/2, y+(h-dh)/2

	r.w.save()
	r.w.setAlpha(st.opacity, st.opacity)
	// The image fills the unit square, its first row at the top: in this
	// y-down space, the square's top edge goes to oy.
	r.w.concat(Matrix{A: dw, D: -dh, E: ox, F: oy + dh})
	r.w.drawXObject(img.res)
	r.w.restore()
	return nil
}

// buildImageObjects returns the image XObjects of the catalog, numbered from
// next, and the XObject resource dictionary naming them.
func buildImageObjects(c *imageCatalog, next int) (map[int]*pdf0.IndirectObject, *pdf0.Dictionary) {
	objects := map[int]*pdf0.IndirectObject{}
	res := &pdf0.Dictionary{}
	add := func(v pdf0.Object) pdf0.IndirectRef {
		n := next
		next++
		objects[n] = &pdf0.IndirectObject{Number: n, Value: v}
		return pdf0.IndirectRef{Number: n}
	}
	stream := func(data []byte, w, h int, space, filter string) *pdf0.Stream {
		s := &pdf0.Stream{Data: data}
		s.Dict.Set("Type", pdf0.Name("XObject"))
		s.Dict.Set("Subtype", pdf0.Name("Image"))
		s.Dict.Set("Width", pdf0.Integer(w))
		s.Dict.Set("Height", pdf0.Integer(h))
		s.Dict.Set("ColorSpace", pdf0.Name(space))
		s.Dict.Set("BitsPerComponent", pdf0.Integer(8))
		s.Dict.Set("Filter", pdf0.Name(filter))
		s.Dict.Set("Length", pdf0.Integer(len(data)))
		return s
	}
	for _, img := range c.list {
		s := stream(img.data, img.w, img.h, img.space, img.filter)
		if img.smask != nil {
			s.Dict.Set("SMask", add(stream(img.smask, img.w, img.h, "DeviceGray", "FlateDecode")))
		}
		res.Set(pdf0.Name(img.res), add(s))
	}
	return objects, res
}

// hrefsToFetch lists the hrefs of the <image> elements under root that are
// not data: URIs, each once, in document order.
func hrefsToFetch(root *element) []string {
	var hrefs []string
	seen := map[string]bool{}
	stack := []*element{root}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for i := len(e.children) - 1; i >= 0; i-- {
			stack = append(stack, e.children[i])
		}
		if e.name != "image" {
			continue
		}
		href := strings.TrimSpace(e.attrVal("href"))
		if href != "" && !imageref.IsData(href) && !strings.HasPrefix(href, "#") && !seen[href] {
			seen[href] = true
			hrefs = append(hrefs, href)
		}
	}
	return hrefs
}
