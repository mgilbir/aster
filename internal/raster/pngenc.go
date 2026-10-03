package raster

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"io"
	"sync"
)

// This file is image/png's encoder for an 8-bit NRGBA image at the default
// compression level, byte for byte: the same chunks, the same adaptive filter
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
	hdr [8]byte
	tmp [13]byte
}

var pngEncoders sync.Pool

var errPNGSize = errors.New("invalid image size")

// encodePNG writes img to out as a PNG: RGB if it is fully opaque, else RGBA.
func encodePNG(out *bytes.Buffer, img *image.NRGBA) error {
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
	defer func() { e.out = nil }()

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
	err := e.writeRows(img, b, bpp, opaque)
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
	m := s >> 63
	return (s ^ m) - m
}

func absInt(x int) int {
	m := x >> 63
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
