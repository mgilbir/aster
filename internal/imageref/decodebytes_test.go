package imageref

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"compress/zlib"

	"github.com/mgilbir/aster/internal/testutil"
)

// adam7PNG is a w x h RGBA PNG, interlaced (Adam7), which the standard
// encoder never writes: the decoder makes every pass an image of its own
// before merging it into the whole one.
func adam7PNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var raw bytes.Buffer
	for _, p := range [7][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}} {
		if p[0] >= w {
			continue
		}
		for y := p[1]; y < h; y += p[3] {
			raw.WriteByte(0)
			for x := p[0]; x < w; x += p[2] {
				raw.Write([]byte{uint8(x), uint8(y), uint8(x ^ y), 200})
			}
		}
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(typ string, d []byte) {
		binary.Write(&out, binary.BigEndian, uint32(len(d)))
		out.WriteString(typ)
		out.Write(d)
		binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), d...)))
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9], ihdr[12] = 8, 6, 1
	chunk("IHDR", ihdr)
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

// DecodeBytes is at least what the standard decoders allocate, whatever
// the encoding: a progressive JPEG keeps its coefficients (64 int32 a block
// a component) besides its pixels, a JPEG of RGB or CMYK is converted into
// another image, an interlaced PNG makes an image for each pass (C20). And
// it is not grossly more.
func TestDecodeBytesCoversTheDecoder(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocations are not measured under -race")
	}
	inputs := map[string][]byte{"adam7.png": adam7PNG(t, 384, 384)}
	for _, name := range []string{"base444.jpg", "prog444.jpg", "prog420.jpg", "proggray.jpg", "progrgb.jpg"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		inputs[name] = b
	}
	enc := func(name string, img image.Image) {
		var b bytes.Buffer
		var err error
		switch filepath.Ext(name) {
		case ".png":
			err = png.Encode(&b, img)
		case ".jpg":
			err = jpeg.Encode(&b, img, nil)
		default:
			err = gif.Encode(&b, img, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		inputs[name] = b.Bytes()
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for i := range nrgba.Pix {
		nrgba.Pix[i] = uint8(i * 7)
	}
	enc("nrgba.png", nrgba)
	g16 := image.NewGray16(image.Rect(0, 0, 300, 200))
	for i := range g16.Pix {
		g16.Pix[i] = uint8(i * 3)
	}
	enc("gray16.png", g16)
	pal := image.NewPaletted(image.Rect(0, 0, 300, 200), color.Palette{color.Black, color.White})
	enc("paletted.png", pal)
	enc("paletted.gif", pal)
	enc("ycbcr.jpg", nrgba)
	lim := Limits{MaxBytes: 32 << 20, MaxPixels: 64 << 20, Err: testLimits.Err}
	for name, data := range inputs {
		cfg, format, err := Config(data, lim)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := DecodeBytes(data, cfg, format)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		img, err := Decode(context.Background(), data, lim)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		runtime.KeepAlive(img)
		got := int64(after.TotalAlloc - before.TotalAlloc)
		// The decoder's tables and reader buffers, whatever the size.
		const slack = 64 << 10
		if got > want+slack {
			t.Errorf("%s (%dx%d %s): decoding allocated %d bytes, DecodeBytes says %d", name, cfg.Width, cfg.Height, format, got, want)
		}
		if want > 2*got+slack {
			t.Errorf("%s (%dx%d %s): DecodeBytes says %d, decoding allocated only %d", name, cfg.Width, cfg.Height, format, want, got)
		}
		t.Logf("%-13s %dx%d: allocated %8d, charged %8d", name, cfg.Width, cfg.Height, got, want)
	}
}
