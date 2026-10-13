package imageref

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"io"

	"github.com/mgilbir/aster/internal/budget"
)

// BandPixels is about how many pixels a loop over a whole image handles
// between two polls of its context: a band of rows of that many pixels is a
// few hundred microseconds of conversion, a few milliseconds under the race
// detector, whatever the image's size. An image of up to the pixel limit (64
// Mi) is a thousand bands.
const BandPixels = 1 << 16

// BandRows is how many rows of an image w pixels wide make a band of about
// BandPixels pixels: at least one.
func BandRows(w int) int {
	return max(1, BandPixels/max(w, 1))
}

// Poll charges n pixels of work to the ledger ctx carries (budget.LedgerFrom)
// and returns ctx's error once ctx is done: the poll of a loop over an image,
// once a band. A nil ctx is never done.
func Poll(ctx context.Context, n int) error {
	if ctx == nil {
		return nil
	}
	if err := budget.LedgerFrom(ctx).Work(int64(n)); err != nil {
		return err
	}
	return ctx.Err()
}

// DrawSrc is draw.Draw(dst, r, src, sp, draw.Src), drawn in bands of
// BandRows rows and polling ctx before each band (see Poll): a decoded image
// converted to the pixels a writer holds is up to 64 Mi pixels, which the
// generic conversion of a paletted or 16-bit image takes seconds over under
// the race detector, and a deadline must reach into it. draw.Src sets each
// pixel of dst from the pixel of src under it alone, so the bands make the
// same pixels a single draw.Draw does, byte for byte. It returns ctx's error
// once ctx is done, dst then drawn in part. dst and src must not share
// pixels.
func DrawSrc(ctx context.Context, dst draw.Image, r image.Rectangle, src image.Image, sp image.Point) error {
	// Clipped to dst first, as draw.Draw clips it, so that the bands cover
	// only rows it draws.
	clipped := r.Intersect(dst.Bounds())
	sp = sp.Add(clipped.Min.Sub(r.Min))
	r = clipped
	if r.Empty() {
		return Poll(ctx, 0)
	}
	rows := BandRows(r.Dx())
	for y := r.Min.Y; y < r.Max.Y; y += rows {
		y1 := min(y+rows, r.Max.Y)
		if err := Poll(ctx, (y1-y)*r.Dx()); err != nil {
			return err
		}
		draw.Draw(dst, image.Rect(r.Min.X, y, r.Max.X, y1), src, image.Pt(sp.X, sp.Y+y-r.Min.Y), draw.Src)
	}
	return nil
}

// pollPixels is about the most pixels a decoder makes between two polls of
// its reader (see pollReader).
const pollPixels = 1 << 18

// pollReader reads data, polling ctx before each read of at most max bytes.
// The standard decoders read their input through a buffer (none of them
// reads a pollReader byte by byte: it is no io.ByteReader), which takes a
// short read as it comes. A read is bounded by how many pixels it can
// become, not by its own size: a highly compressed image is a few hundred
// bytes of input for MiBs of pixels (the bounded corpus has one), which a
// buffer's worth of input between polls would not bound. See readBytes.
type pollReader struct {
	ctx context.Context
	max int
	r   bytes.Reader
}

func (p *pollReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	return p.r.Read(b[:min(len(b), p.max)])
}

// readBytes is how many bytes of an image in format a pollReader reads at
// once. A PNG's DEFLATE expands a byte to at most about 1,032 bytes of
// scanlines, which are 8/bpp pixels a byte at bpp bits a pixel (its IHDR's
// bit depth times its channels), so reading pollPixels/8192*bpp (32*bpp)
// bytes at a time keeps a read to about pollPixels pixels whatever the
// image. Reads much shorter
// than a few hundred bytes cost the decoder a measurable share of its time,
// which only the densest images (1 bit a pixel) pay. JPEG and GIF read 256
// bytes at a time; their expansion has no such bound (a progressive JPEG's
// end-of-band runs, a GIF's LZW strings), and a progressive JPEG is
// reconstructed after its last read: those stretches are bounded only by
// the pixel limit Config checks.
func readBytes(data []byte, format string) int {
	if format != "png" || len(data) < 26 {
		return 256
	}
	channels := 1     // grey (0) and palette (3)
	switch data[25] { // the colour type
	case 2:
		channels = 3
	case 4:
		channels = 2
	case 6:
		channels = 4
	}
	bpp := int(data[24]) * channels
	return min(max(pollPixels/(8*1024)*bpp, 32), 4096)
}

// reader is a reader of data, an image in format, that polls ctx (nil:
// never done) as it is read.
func reader(ctx context.Context, data []byte, format string) io.Reader {
	if ctx == nil {
		return bytes.NewReader(data)
	}
	p := &pollReader{ctx: ctx, max: readBytes(data, format)}
	p.r.Reset(data)
	return p
}
