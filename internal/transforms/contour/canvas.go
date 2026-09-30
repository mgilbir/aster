package contour

import (
	"encoding/base64"
	"image"
	"image/png"
	"strings"
)

// Size is the width and height of the canvas the image was painted into.
func (img *Image) Size() (w, h int) { return img.Width, img.Height }

// DataURL is canvas.toDataURL() of a canvas that received the image through
// putImageData, as node-canvas (cairo) produces it: a PNG in a data URL.
//
// The canvas holds premultiplied pixels, so what reads back is not always what
// was put in. putImageData premultiplies with a float32 factor and truncates
// (a fully transparent pixel becomes 0,0,0,0, an opaque one is stored as is),
// and cairo's PNG writer un-premultiplies with rounding. For a partially
// transparent pixel the two steps lose information, and the loss is visible in
// the output, so it is reproduced here. A zero-area canvas gives "data:,".
func (img *Image) DataURL() string {
	if img.url != "" {
		return img.url
	}
	if img.Width <= 0 || img.Height <= 0 {
		img.url = "data:,"
		return img.url
	}
	out := image.NewNRGBA(image.Rect(0, 0, img.Width, img.Height))
	for i := 0; i+3 < len(img.Pix) && i+3 < len(out.Pix); i += 4 {
		r, g, b, a := readBack(img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3])
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = r, g, b, a
	}
	var sb strings.Builder
	sb.WriteString("data:image/png;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, &sb)
	if err := png.Encode(enc, out); err != nil {
		img.url = "data:,"
		return img.url
	}
	_ = enc.Close()
	img.url = sb.String()
	return img.url
}

// readBack is the round trip of one pixel through a premultiplied cairo
// surface: node-canvas putImageData, then cairo's PNG writer.
func readBack(r, g, b, a uint8) (uint8, uint8, uint8, uint8) {
	if a == 0 {
		return 0, 0, 0, 0
	}
	if a == 255 {
		return r, g, b, a
	}
	alpha := float32(a) / 255
	pre := func(c uint8) uint8 { return uint8(float32(c) * alpha) }
	un := func(c uint8) uint8 { return uint8((uint32(c)*255 + uint32(a)/2) / uint32(a)) }
	return un(pre(r)), un(pre(g)), un(pre(b)), a
}
