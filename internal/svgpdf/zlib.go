package svgpdf

import (
	"bytes"
	"compress/zlib"
	"io"
	"sync"
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

// compress is data, zlib-compressed.
func compress(data []byte) ([]byte, error) {
	var b bytes.Buffer
	err := compressTo(&b, data)
	return b.Bytes(), err
}
