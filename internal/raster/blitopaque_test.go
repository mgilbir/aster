package raster

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestBlitSolidMatchesPerPixel: blitSolid, which fills runs of covered pixels
// of an opaque colour as a pattern, writes the bytes the per-pixel loop it
// replaced (blitSolidRef) writes: coverage in runs of 0, 255 and partial
// values of every length around fillPixels' cutoff, opaque and translucent
// colours, with and without a mask row, onto random pixels.
func TestBlitSolidMatchesPerPixel(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	for iter := 0; iter < 20000; iter++ {
		n := rng.IntN(200)
		cov := make([]uint8, 0, n)
		for len(cov) < n {
			v := uint8(rng.IntN(256))
			switch rng.IntN(3) {
			case 0:
				v = 255
			case 1:
				v = 0
			}
			for range min(n-len(cov), 1+rng.IntN(40)) {
				cov = append(cov, v)
			}
		}
		var mrow []uint8
		if rng.IntN(3) == 0 {
			mrow = make([]uint8, n)
			for i := range mrow {
				mrow[i] = uint8(rng.IntN(256))
			}
		}
		var b blitter
		b.paint.col = [4]uint8{uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), 255}
		if rng.IntN(3) == 0 {
			b.paint.col[3] = uint8(rng.IntN(256))
		}
		got := make([]uint8, 4*n)
		for i := range got {
			got[i] = uint8(rng.IntN(256))
		}
		want := bytes.Clone(got)
		b.blitSolid(got, cov, mrow)
		b.blitSolidRef(want, cov, mrow)
		if !bytes.Equal(got, want) {
			t.Fatalf("iter %d (n %d, colour %v, mask %v): pixels differ from the per-pixel loop", iter, n, b.paint.col, mrow != nil)
		}
	}
}

// blitSolidRef is blitSolid as it was, a pixel at a time.
func (b *blitter) blitSolidRef(dst, cov, mrow []uint8) {
	cr, cg, cb, ca := uint32(b.paint.col[0]), uint32(b.paint.col[1]), uint32(b.paint.col[2]), uint32(b.paint.col[3])
	if ca == 0 {
		return
	}
	for i, cv := range cov {
		c := uint32(cv)
		if mrow != nil {
			c = mul255(c, uint32(mrow[i]))
		}
		if c == 0 {
			continue
		}
		d := dst[i*4 : i*4+4 : i*4+4]
		if c == 255 && ca == 255 {
			d[0], d[1], d[2], d[3] = uint8(cr), uint8(cg), uint8(cb), 255
			continue
		}
		sr, sg, sb, sa := cr, cg, cb, ca
		if c != 255 {
			sr, sg, sb, sa = mul255(cr, c), mul255(cg, c), mul255(cb, c), mul255(ca, c)
		}
		ia := 255 - sa
		d[0] = uint8(sr + mul255(uint32(d[0]), ia))
		d[1] = uint8(sg + mul255(uint32(d[1]), ia))
		d[2] = uint8(sb + mul255(uint32(d[2]), ia))
		d[3] = uint8(sa + mul255(uint32(d[3]), ia))
	}
}
