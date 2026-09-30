package contour

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestReadBack(t *testing.T) {
	cases := []struct{ in, want [4]uint8 }{
		{[4]uint8{136, 136, 136, 255}, [4]uint8{136, 136, 136, 255}},
		{[4]uint8{200, 10, 99, 0}, [4]uint8{0, 0, 0, 0}},
		// 136 * (63/255) truncates to 33; 33 un-premultiplies to (33*255+31)/63.
		{[4]uint8{136, 136, 136, 63}, [4]uint8{134, 134, 134, 63}},
		{[4]uint8{255, 0, 1, 1}, [4]uint8{255, 0, 0, 1}},
	}
	for _, c := range cases {
		r, g, b, a := readBack(c.in[0], c.in[1], c.in[2], c.in[3])
		if got := [4]uint8{r, g, b, a}; got != c.want {
			t.Errorf("readBack(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestImageDataURL(t *testing.T) {
	img := &Image{Width: 2, Height: 1, Pix: []uint8{136, 136, 136, 63, 1, 2, 3, 255}}
	u := img.DataURL()
	raw, ok := strings.CutPrefix(u, "data:image/png;base64,")
	if !ok {
		t.Fatalf("not a PNG data URL: %.40s", u)
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(dec.At(0, 0)).(color.NRGBA); got != (color.NRGBA{134, 134, 134, 63}) {
		t.Errorf("pixel 0 = %v", got)
	}
	if got := color.NRGBAModel.Convert(dec.At(1, 0)).(color.NRGBA); got != (color.NRGBA{1, 2, 3, 255}) {
		t.Errorf("pixel 1 = %v", got)
	}
	if u := (&Image{}).DataURL(); u != "data:," {
		t.Errorf("empty canvas = %q", u)
	}
}
