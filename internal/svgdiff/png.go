package svgdiff

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/png" // registers the decoder for embedded PNG images
)

// samePNG reports whether two base64 PNG payloads decode to images of the same
// size with the same pixels. Encoders differ in compression, filters and
// chunk layout without the picture differing, so the bytes are not compared.
//
// Pixels are compared as non-premultiplied 8-bit RGBA (image/color.NRGBA,
// which is how PNG stores them): a decoder that yields premultiplied or
// 16-bit samples is converted to it first. A partially transparent pixel is
// compared exactly, including its color channels: a producer that premultiplies
// internally (cairo) loses precision in those channels, and that loss is part
// of what it draws, so it must be reproduced rather than forgiven. The color
// channels of a fully transparent pixel are invisible and the PNG format
// leaves them to the encoder, so they are not compared. An image that does not
// decode is never equal to anything but a byte-identical one.
func samePNG(a, b string) bool {
	ia, err := decodePNG(a)
	if err != nil {
		return false
	}
	ib, err := decodePNG(b)
	if err != nil {
		return false
	}
	ba, bb := ia.Bounds(), ib.Bounds()
	if ba.Dx() != bb.Dx() || ba.Dy() != bb.Dy() {
		return false
	}
	for y := 0; y < ba.Dy(); y++ {
		for x := 0; x < ba.Dx(); x++ {
			p := color.NRGBAModel.Convert(ia.At(ba.Min.X+x, ba.Min.Y+y)).(color.NRGBA)
			q := color.NRGBAModel.Convert(ib.At(bb.Min.X+x, bb.Min.Y+y)).(color.NRGBA)
			if p.A == 0 && q.A == 0 {
				continue
			}
			if p != q {
				return false
			}
		}
	}
	return true
}

func decodePNG(b64 string) (image.Image, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}
