package svgpdf

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"sync"

	"github.com/mgilbir/aster/internal/imageref"
)

// zlibWriters are zlib writers to reuse: each holds about a megabyte of
// compressor state, which a writer made for every stream of a document (its
// content, fonts, images, forms and meshes) allocated again. A writer Reset
// is the writer NewWriter makes, so its output is the same bytes.
var zlibWriters = sync.Pool{New: func() any { return zlib.NewWriter(nil) }}

// compressTo writes the chunks to w, zlib-compressed at the default level,
// which is deterministic for a given input.
func compressTo(w io.Writer, chunks ...[]byte) error {
	zw := zlibWriters.Get().(*zlib.Writer)
	zw.Reset(w)
	for _, c := range chunks {
		if _, err := zw.Write(c); err != nil {
			return err // a writer that failed is not reused
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	zlibWriters.Put(zw)
	return nil
}

// compressPolled is compress for data a spec sizes (an image's planes, up to
// hundreds of MiB): written to the compressor a band at a time, polling ctx
// before each (nil: never done), so a deadline reaches into the compression.
// DEFLATE's output depends only on the bytes written, not on how they were
// split among writes, so it is compress's, byte for byte.
func compressPolled(ctx context.Context, data []byte) ([]byte, error) {
	const band = 4 * imageref.BandPixels
	var b bytes.Buffer
	zw := zlibWriters.Get().(*zlib.Writer)
	zw.Reset(&b)
	for len(data) > 0 {
		n := min(band, len(data))
		if err := imageref.Poll(ctx, n); err != nil {
			return nil, err // a writer that stopped midway is not reused
		}
		if _, err := zw.Write(data[:n]); err != nil {
			return nil, err
		}
		data = data[n:]
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	zlibWriters.Put(zw)
	return b.Bytes(), nil
}

// compress is data, zlib-compressed.
func compress(data []byte) ([]byte, error) {
	var b bytes.Buffer
	err := compressTo(&b, data)
	return b.Bytes(), err
}
