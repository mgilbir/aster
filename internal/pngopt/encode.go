package pngopt

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"io"
	"math/bits"
	"sync"

	"github.com/mgilbir/aster/internal/budget"
)

// This file is image/png's encoder for an 8-bit NRGBA or paletted image at
// the default compression level, byte for byte, polling a context as it
// goes: a row is filtered and deflated at a time, so the work between two
// polls is a few rows whatever the image (image/png runs a whole image
// without one, a second for a large one on a slow machine). For NRGBA it is: the same chunks, the same adaptive filter
// choice (libpng's minimum sum of absolute differences, in the same order,
// with the same early exits) and the same deflate stream. It exists because
// the filter search is a fifth of the encoding time and image/png's version
// has a data-dependent branch in its innermost loop and no shortcut for rows
// that repeat the one above, which in a chart is most of them.
// TestEncodePNGMatchesStdlib holds it to image/png's output.

const (
	ftNone = iota
	ftSub
	ftUp
	ftAverage
	ftPaeth
	nFilter
)

type pngEncoder struct {
	cr  [nFilter][]byte // cr[0] is the row as is, the others are it filtered
	pr  []byte          // the previous row
	zw  *zlib.Writer
	bw  *bufio.Writer
	out *bytes.Buffer
	m   budget.Meter
	hdr [8]byte
	tmp [3*256 + 256]byte
}

var pngEncoders sync.Pool

var errPNGSize = errors.New("invalid image size")

// Encode writes img to out as a PNG, RGB if it is fully opaque, else RGBA,
// image/png's bytes, polling ctx (and charging the call's ledger for the
// work) per row: it returns ctx's error once ctx is done.
func Encode(ctx context.Context, out *bytes.Buffer, img *image.NRGBA) error {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return errPNGSize
	}
	e, _ := pngEncoders.Get().(*pngEncoder)
	if e == nil {
		e = &pngEncoder{}
	}
	defer pngEncoders.Put(e)
	e.out = out
	e.m = budget.NewMeter(ctx)
	defer func() { e.out, e.m = nil, budget.Meter{} }()

	opaque := img.Opaque()
	bpp, colorType := 4, byte(6)
	if opaque {
		bpp, colorType = 3, 2
	}

	out.WriteString("\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(e.tmp[0:4], uint32(w))
	binary.BigEndian.PutUint32(e.tmp[4:8], uint32(h))
	e.tmp[8], e.tmp[9], e.tmp[10], e.tmp[11], e.tmp[12] = 8, colorType, 0, 0, 0
	e.chunk(e.tmp[:13], "IHDR")

	e.startIDAT()
	err := e.writeRows(img, b, bpp, opaque)
	return e.finish(err)
}

// startIDAT points the deflater at a fresh run of IDAT chunks.
func (e *pngEncoder) startIDAT() {
	// IDAT chunks are whatever the 32 KiB buffer in front of the deflater
	// flushes, as in image/png.
	if e.bw == nil {
		e.bw = bufio.NewWriterSize((*idatWriter)(e), 1<<15)
	} else {
		e.bw.Reset((*idatWriter)(e))
	}
	if e.zw == nil {
		e.zw, _ = zlib.NewWriterLevel(e.bw, zlib.DefaultCompression)
	} else {
		e.zw.Reset(e.bw)
	}
}

// finish ends the IDAT run (err is the rows' error) and writes IEND.
func (e *pngEncoder) finish(err error) error {
	if cerr := e.zw.Close(); err == nil {
		err = cerr
	}
	if ferr := e.bw.Flush(); err == nil {
		err = ferr
	}
	if err != nil {
		return err
	}
	e.chunk(nil, "IEND")
	return nil
}

func (e *pngEncoder) writeRows(img *image.NRGBA, b image.Rectangle, bpp int, opaque bool) error {
	w, h := b.Dx(), b.Dy()
	sz := 1 + w*bpp
	for i := range e.cr {
		if cap(e.cr[i]) < sz {
			e.cr[i] = make([]byte, sz)
		} else {
			e.cr[i] = e.cr[i][:sz]
		}
		e.cr[i][0] = byte(i)
	}
	if cap(e.pr) < sz {
		e.pr = make([]byte, sz)
	} else {
		e.pr = e.pr[:sz]
		clear(e.pr)
	}
	for y := 0; y < h; y++ {
		o := img.PixOffset(b.Min.X, b.Min.Y+y)
		src := img.Pix[o : o+w*4]
		row := e.cr[0][1:]
		if opaque {
			for i, j := 0, 0; j < len(src); i, j = i+3, j+4 {
				row[i], row[i+1], row[i+2] = src[j], src[j+1], src[j+2]
			}
		} else {
			copy(row, src)
		}
		if err := e.m.Work(int64(w)); err != nil {
			return err
		}
		f := filterRow(&e.cr, e.pr, bpp)
		if _, err := e.zw.Write(e.cr[f]); err != nil {
			return err
		}
		e.pr, e.cr[0] = e.cr[0], e.pr
	}
	return nil
}

// abs8 is the absolute value of d read as a signed byte (128 for 0x80),
// without a branch.
func abs8(d uint8) int {
	s := int(int8(d))
	m := s >> (bits.UintSize - 1)
	return (s ^ m) - m
}

func absInt(x int) int {
	m := x >> (bits.UintSize - 1)
	return (x ^ m) - m
}

func paethPredictor(a, b, c uint8) uint8 {
	pc := int(c)
	pa := int(b) - pc
	pb := int(a) - pc
	pc = absInt(pa + pb)
	pa = absInt(pa)
	pb = absInt(pb)
	if pa <= pb && pa <= pc {
		return a
	} else if pb <= pc {
		return b
	}
	return c
}

// filterRow picks the PNG filter for the row in cr[0] given the previous row
// pr, leaves the filtered row in cr[f] and returns f. Exactly image/png's
// choice: the filter with the smallest sum of absolute differences, the
// candidates tried in the order up, Paeth, none, sub, average, each ending
// early once it cannot beat the best, ties going to the earlier.
func filterRow(cr *[nFilter][]byte, pr []byte, bpp int) int {
	cdat0 := cr[ftNone][1:]
	cdat1 := cr[ftSub][1:]
	cdat2 := cr[ftUp][1:]
	cdat3 := cr[ftAverage][1:]
	cdat4 := cr[ftPaeth][1:]
	pdat := pr[1:]
	n := len(cdat0)
	pdat = pdat[:n]
	cdat1, cdat2, cdat3, cdat4 = cdat1[:n], cdat2[:n], cdat3[:n], cdat4[:n]

	// A row equal to the one above filters to zeros under Up, the smallest
	// possible sum, and nothing is smaller than that.
	if bytes.Equal(cdat0, pdat) {
		clear(cdat2)
		return ftUp
	}

	sum := 0
	for i := 0; i < n; i++ {
		d := cdat0[i] - pdat[i]
		cdat2[i] = d
		sum += abs8(d)
	}
	best := sum
	filter := ftUp

	sum = 0
	for i := 0; i < bpp; i++ {
		cdat4[i] = cdat0[i] - pdat[i]
		sum += abs8(cdat4[i])
	}
	for i := bpp; i < n; i++ {
		cdat4[i] = cdat0[i] - paethPredictor(cdat0[i-bpp], pdat[i], pdat[i-bpp])
		sum += abs8(cdat4[i])
		if sum >= best {
			break
		}
	}
	if sum < best {
		best = sum
		filter = ftPaeth
	}

	sum = 0
	for i := 0; i < n; i++ {
		sum += abs8(cdat0[i])
		if sum >= best {
			break
		}
	}
	if sum < best {
		best = sum
		filter = ftNone
	}

	sum = 0
	for i := 0; i < bpp; i++ {
		cdat1[i] = cdat0[i]
		sum += abs8(cdat1[i])
	}
	for i := bpp; i < n; i++ {
		cdat1[i] = cdat0[i] - cdat0[i-bpp]
		sum += abs8(cdat1[i])
		if sum >= best {
			break
		}
	}
	if sum < best {
		best = sum
		filter = ftSub
	}

	sum = 0
	for i := 0; i < bpp; i++ {
		cdat3[i] = cdat0[i] - pdat[i]/2
		sum += abs8(cdat3[i])
	}
	for i := bpp; i < n; i++ {
		cdat3[i] = cdat0[i] - uint8((int(cdat0[i-bpp])+int(pdat[i]))/2)
		sum += abs8(cdat3[i])
		if sum >= best {
			break
		}
	}
	if sum < best {
		filter = ftAverage
	}
	return filter
}

// chunk writes one PNG chunk.
func (e *pngEncoder) chunk(b []byte, name string) {
	binary.BigEndian.PutUint32(e.hdr[:4], uint32(len(b)))
	copy(e.hdr[4:], name)
	crc := crc32.NewIEEE()
	crc.Write(e.hdr[4:8])
	crc.Write(b)
	e.out.Write(e.hdr[:8])
	e.out.Write(b)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	e.out.Write(sum[:])
}

// idatWriter turns each write it receives into an IDAT chunk.
type idatWriter pngEncoder

func (w *idatWriter) Write(b []byte) (int, error) {
	(*pngEncoder)(w).chunk(b, "IDAT")
	return len(b), nil
}

var _ io.Writer = (*idatWriter)(nil)

// EncodePaletted writes img to out as an indexed PNG, image/png's bytes:
// 1, 2, 4 or 8 bits a pixel by the palette's size, its PLTE and (when a
// colour is not opaque) tRNS chunks, and rows unfiltered, as image/png
// leaves a paletted image's. It polls ctx per row as Encode does.
func EncodePaletted(ctx context.Context, out *bytes.Buffer, img *image.Paletted) error {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return errPNGSize
	}
	pal := img.Palette
	if len(pal) < 1 || len(pal) > 256 {
		return errors.New("png: bad palette length")
	}
	depth := 8
	switch {
	case len(pal) <= 2:
		depth = 1
	case len(pal) <= 4:
		depth = 2
	case len(pal) <= 16:
		depth = 4
	}
	e, _ := pngEncoders.Get().(*pngEncoder)
	if e == nil {
		e = &pngEncoder{}
	}
	defer pngEncoders.Put(e)
	e.out = out
	e.m = budget.NewMeter(ctx)
	defer func() { e.out, e.m = nil, budget.Meter{} }()

	out.WriteString("\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(e.tmp[0:4], uint32(w))
	binary.BigEndian.PutUint32(e.tmp[4:8], uint32(h))
	e.tmp[8], e.tmp[9], e.tmp[10], e.tmp[11], e.tmp[12] = byte(depth), 3, 0, 0, 0
	e.chunk(e.tmp[:13], "IHDR")
	last := -1
	for i, c := range pal {
		c1 := color.NRGBAModel.Convert(c).(color.NRGBA)
		e.tmp[3*i], e.tmp[3*i+1], e.tmp[3*i+2] = c1.R, c1.G, c1.B
		if c1.A != 0xff {
			last = i
		}
		e.tmp[3*256+i] = c1.A
	}
	e.chunk(e.tmp[:3*len(pal)], "PLTE")
	if last != -1 {
		e.chunk(e.tmp[3*256:3*256+1+last], "tRNS")
	}

	e.startIDAT()
	sz := 1 + (w*depth+7)/8
	if cap(e.pr) < sz {
		e.pr = make([]byte, sz)
	}
	row := e.pr[:sz]
	row[0] = ftNone
	var err error
	for y := 0; y < h && err == nil; y++ {
		if err = e.m.Work(int64(w)); err != nil {
			break
		}
		o := img.PixOffset(b.Min.X, b.Min.Y+y)
		src := img.Pix[o : o+w]
		if depth == 8 {
			copy(row[1:], src)
		} else {
			// Packed most significant first, the last byte padded with
			// zero bits, as image/png packs them.
			per := 8 / depth
			var a uint8
			c, i := 0, 1
			for _, v := range src {
				a = a<<uint(depth) | v
				if c++; c == per {
					row[i] = a
					i++
					a, c = 0, 0
				}
			}
			if c != 0 {
				row[i] = a << uint(depth*(per-c))
			}
		}
		_, err = e.zw.Write(row)
	}
	return e.finish(err)
}
