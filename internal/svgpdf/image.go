package svgpdf

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
	pdf0 "github.com/mgilbir/pdf0"
)

// pdfImage is one image XObject: its stream, the soft mask of its alpha, and
// its size in pixels.
type pdfImage struct {
	// The resource names (Im0, Im1, ...) of the image smoothed and not:
	// interpolation is a property of the image's stream, so an image drawn
	// both ways is embedded twice, once each way.
	smoothRes, sharpRes string
	w, h                int
	cat                 *imageCatalog
	data                []byte
	filter              string // DCTDecode (a JPEG as it is) or FlateDecode
	space               string // DeviceRGB or DeviceGray
	smask               []byte // Flate-compressed alpha; nil when the image is opaque
}

// imageCatalog holds the images a document draws, each href once, in the
// order they are first drawn.
type imageCatalog struct {
	lim     imageref.Limits
	fetched map[string][]byte            // bytes by href, from Options.Images
	byHref  map[string]*pdfImage         // nil: the image cannot be drawn
	onErr   func(href string, err error) // Options.ImageError
	uses    []imageUse                   // the XObjects drawn, in first-use order
	// byData are the images by a digest of their bytes: one drawn under
	// several hrefs is decoded and written once.
	byData map[[sha256.Size]byte]*pdfImage
	// ledger is the call's: an image's pixels and what is made of them are
	// charged before they are made.
	ledger *budget.Ledger
}

// imageUse is one XObject: an image, smoothed or not.
type imageUse struct {
	img    *pdfImage
	smooth bool
	res    string
}

func newImageCatalog(fetched map[string][]byte, lim Limits, ledger *budget.Ledger) *imageCatalog {
	return &imageCatalog{lim: lim.imageLimits(), fetched: fetched, byHref: map[string]*pdfImage{}, ledger: ledger}
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
		if errors.Is(err, budget.ErrLimit) {
			return nil, err
		}
		img = nil
		if c.onErr != nil {
			c.onErr(href, err)
		}
	}
	if img != nil && img.cat == nil {
		img.cat = c
	}
	c.byHref[href] = img
	return img, nil
}

// put returns the image drawn from the pixels make returns, under key, made
// once: what is drawn that is not an <image>, such as a colour glyph. It is
// nil when make returns no image.
func (c *imageCatalog) put(key string, make func() (*image.NRGBA, error)) (*pdfImage, error) {
	if img, ok := c.byHref[key]; ok {
		return img, nil
	}
	px, err := make()
	if err != nil {
		return nil, err
	}
	var img *pdfImage
	if px != nil && px.Rect.Dx() > 0 && px.Rect.Dy() > 0 {
		if img, err = encodeNRGBA(px); err != nil {
			return nil, err
		}
		img.cat = c
	}
	c.byHref[key] = img
	return img, nil
}

// res is the resource name of the image, smoothed or not, naming it on first
// use.
func (img *pdfImage) res(smooth bool) string {
	name := &img.sharpRes
	if smooth {
		name = &img.smoothRes
	}
	if *name == "" {
		*name = fmt.Sprintf("Im%d", len(img.cat.uses))
		img.cat.uses = append(img.cat.uses, imageUse{img, smooth, *name})
	}
	return *name
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
	key := sha256.Sum256(data)
	if img, ok := c.byData[key]; ok {
		return img, nil
	}
	img, err := c.decode(data)
	if err != nil {
		return nil, err
	}
	if c.byData == nil {
		c.byData = map[[sha256.Size]byte]*pdfImage{}
	}
	c.byData[key] = img
	return img, nil
}

// decodedBytes is about what decoding an image of the colour model takes, a
// pixel.
func decodedBytes(m color.Model) int64 {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// decode is the image XObject of an image's bytes. What it makes is
// charged to the call's ledger before it is made (C7's class): the decoded
// pixels, their NRGBA copy where the decoder made another kind, the colour
// and alpha planes, and the compressed streams it keeps; all but these are
// given back once made.
func (c *imageCatalog) decode(data []byte) (img *pdfImage, err error) {
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
	px := int64(cfg.Width) * int64(cfg.Height)
	var held int64
	charge := func(n int64) error {
		if err := c.ledger.Alloc(n); err != nil {
			return err
		}
		held += n
		return nil
	}
	defer func() { c.ledger.Free(held) }()
	if err := charge(px * decodedBytes(cfg.ColorModel)); err != nil {
		return nil, err
	}
	src, err := imageref.Decode(data, c.lim)
	if err != nil {
		return nil, err
	}
	// The colour and alpha planes, and the streams compressed from them,
	// which are no larger than the planes but for a few bytes.
	if err := charge(4*px + 4*px); err != nil {
		return nil, err
	}
	// The planes are read straight from the decoder's pixels where they are
	// 8-bit RGBA, premultiplied or not (a PNG's usual decoding); any other
	// kind is first drawn into NRGBA, as it always was.
	switch m := src.(type) {
	case *image.NRGBA:
		img, err = encodePlanes(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy(), false)
	case *image.RGBA:
		img, err = encodePlanes(m.Pix, m.Stride, m.Rect.Dx(), m.Rect.Dy(), true)
	default:
		if err := charge(4 * px); err != nil {
			return nil, err
		}
		b := src.Bounds()
		nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(nrgba, nrgba.Bounds(), src, b.Min, draw.Src)
		img, err = encodeNRGBA(nrgba)
	}
	if err != nil {
		return nil, err
	}
	// The streams stay for the document: charged as kept.
	kept := int64(len(img.data) + len(img.smask))
	if err := c.ledger.Alloc(kept); err != nil {
		return nil, err
	}
	return img, nil
}

// encodeNRGBA is an image XObject of decoded pixels: their colour, and their
// alpha as a soft mask when they are not all opaque.
func encodeNRGBA(nrgba *image.NRGBA) (*pdfImage, error) {
	return encodePlanes(nrgba.Pix, nrgba.Stride, nrgba.Rect.Dx(), nrgba.Rect.Dy(), false)
}

// encodePlanes is encodeNRGBA of w×h 8-bit RGBA pixels, rows stride bytes
// apart, premultiplied when premul is set: a translucent premultiplied pixel
// is converted as draw.Draw converts it to NRGBA (color.NRGBAModel).
func encodePlanes(pix []byte, stride, w, h int, premul bool) (*pdfImage, error) {
	n := w * h
	rgb := make([]byte, 0, 3*n)
	alpha := make([]byte, 0, n)
	opaque := true
	for y := range h {
		row := pix[y*stride : y*stride+4*w]
		for i := 0; i < len(row); i += 4 {
			r, g, b, a := row[i], row[i+1], row[i+2], row[i+3]
			if premul && a != 0xff {
				c := color.NRGBAModel.Convert(color.RGBA{r, g, b, a}).(color.NRGBA)
				r, g, b = c.R, c.G, c.B
			}
			rgb = append(rgb, r, g, b)
			alpha = append(alpha, a)
			opaque = opaque && a == 0xff
		}
	}
	img := &pdfImage{w: w, h: h, filter: "FlateDecode", space: "DeviceRGB"}
	var err error
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
	out, err := compress(b)
	if err != nil {
		return nil, fmt.Errorf("svgpdf: compressing image: %w", err)
	}
	return out, nil
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
	// A side that is absent, or does not parse, takes the image's own.
	num := func(name string, c lengthCtx) (float64, bool) {
		v, ok := e.attr(name)
		if !ok {
			return 0, false
		}
		return parseLength(v, c)
	}
	x, _ := num("x", st.lenX())
	y, _ := num("y", st.lenY())
	w, hasW := num("width", st.lenX())
	h, hasH := num("height", st.lenY())
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
		return fmt.Errorf("svgpdf: unsupported preserveAspectRatio %s on <image>", quoted(par))
	}
	ox, oy := x+(w-dw)/2, y+(h-dh)/2

	r.w.markBox(ox, oy, ox+dw, oy+dh)
	r.w.save()
	r.w.setAlpha(st.opacity, st.opacity)
	// The image fills the unit square, its first row at the top: in this
	// y-down space, the square's top edge goes to oy.
	r.w.concat(Matrix{A: dw, D: -dh, E: ox, F: oy + dh})
	r.w.drawXObject(img.res(st.smooth))
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
	for _, u := range c.uses {
		img := u.img
		s := stream(img.data, img.w, img.h, img.space, img.filter)
		if u.smooth {
			s.Dict.Set("Interpolate", pdf0.Boolean(true))
		}
		if img.smask != nil {
			s.Dict.Set("SMask", add(stream(img.smask, img.w, img.h, "DeviceGray", "FlateDecode")))
		}
		res.Set(pdf0.Name(u.res), add(s))
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
