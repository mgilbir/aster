package svgpdf

import (
	"bytes"
	"compress/zlib"
	"math/rand"
	"testing"
)

// TestContentChunksAreInvisible checks that spilling the content stream into
// chunks changes neither the stream nor its compressed form: the PDF bytes must
// not depend on where a chunk boundary fell.
func TestContentChunksAreInvisible(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	var chunked, plain contentWriter
	plain.cur, chunked.cur = defaultStreamState(), defaultStreamState()
	for range 400_000 {
		x, y := rng.Float64()*600, rng.Float64()*400
		op := func(w *contentWriter) {
			w.op("m", x, y)
			w.op("c", x+1, y+2, x+3, y-4, x+5, y)
			w.op("f")
		}
		op(&chunked)
		chunked.spill()
		op(&plain)
	}
	if len(chunked.chunks) == 0 {
		t.Fatal("test never spilled")
	}
	var joined []byte
	for _, c := range chunked.stream() {
		joined = append(joined, c...)
	}
	if !bytes.Equal(joined, plain.buf) {
		t.Fatal("chunked stream differs from the plain one")
	}

	deflate := func(chunks [][]byte) []byte {
		var b bytes.Buffer
		zw := zlib.NewWriter(&b)
		for _, c := range chunks {
			zw.Write(c)
		}
		zw.Close()
		return b.Bytes()
	}
	if !bytes.Equal(deflate(chunked.stream()), deflate([][]byte{plain.buf})) {
		t.Fatal("compressed chunked stream differs from compressing it whole")
	}
}
