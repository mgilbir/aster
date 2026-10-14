package raster

import (
	"image/color"
	"math/bits"
)

// Coverage is where a render leaves ink: a grid of cells, each Cell x Cell
// device pixels of the canvas (the last row and column may hold fewer), a
// cell set when any of its pixels ends the render with non-zero alpha.
type Coverage struct {
	W, H int // cells
	Cell int
	bits []uint64
}

func newCoverage(cw, ch, cell int) *Coverage {
	w, h := (cw+cell-1)/cell, (ch+cell-1)/cell
	return &Coverage{W: w, H: h, Cell: cell, bits: make([]uint64, (w*h+63)/64)}
}

// coverageBytes is the memory of the coverage of a cw x ch canvas.
func coverageBytes(cw, ch, cell int) int {
	w, h := (cw+cell-1)/cell, (ch+cell-1)/cell
	return (w*h + 63) / 64 * 8
}

// Has reports whether cell (x, y) has ink; cells outside the grid have none.
func (c *Coverage) Has(x, y int) bool {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return false
	}
	i := y*c.W + x
	return c.bits[i>>6]&(1<<uint(i&63)) != 0
}

// Row calls fn for every cell of row y with ink, from left to right.
func (c *Coverage) Row(y int, fn func(x int)) {
	if y < 0 || y >= c.H {
		return
	}
	lo, hi := y*c.W, (y+1)*c.W
	for wi := lo >> 6; wi<<6 < hi; wi++ {
		w := c.bits[wi]
		if s := lo - wi<<6; s > 0 {
			w &^= 1<<uint(s) - 1 // cells of the rows above
		}
		if e := hi - wi<<6; e < 64 {
			w &= 1<<uint(e) - 1 // cells of the rows below
		}
		for w != 0 {
			fn(wi<<6 + bits.TrailingZeros64(w) - lo)
			w &= w - 1
		}
	}
}

func (c *Coverage) set(i int) { c.bits[i>>6] |= 1 << uint(i&63) }

func (c *Coverage) has(i int) bool { return c.bits[i>>6]&(1<<uint(i&63)) != 0 }

func (c *Coverage) fill() {
	for i := range c.W * c.H {
		c.set(i)
	}
}

// Drawing onto a coverage canvas gives the alpha drawing onto a pixel canvas
// would be non-zero exactly where this does, without the pixels. Every way
// pixels reach a canvas keeps a pixel's alpha non-zero once it is: a blit or
// a composite with source alpha sa leaves sa + d(255-sa)/255 (rounded, so
// sa + d when sa is 0 and at least sa otherwise), a blend mode
// round(255(as + ab - as ab)), at least round(255 max(as, ab)). So a pixel
// ends with alpha exactly when some drawing onto it had a non-zero source
// alpha, which depends on the source alone: the paint's, the layer's pixel's,
// times the coverage, the clip mask and the opacity, rounded as the pixel
// paths round them. The pixel paths that read or rewrite what is beneath
// (masks, filters, COLR's composite modes) apply to layers, which hold pixels,
// and never to a coverage canvas (canvas.mustHold).

// markRow marks the cells of sample row y holding a sample x0+i (0 <= i < n)
// for which hit(i) holds, testing no sample of a cell already marked.
func (c *Coverage) markRow(y, x0, n int, hit func(i int) bool) {
	row := (y / c.Cell) * c.W
	for i := 0; i < n; {
		x := x0 + i
		cell := row + x/c.Cell
		next := (x/c.Cell+1)*c.Cell - x0 // the first sample of the next cell
		if !c.has(cell) {
			for ; i < n && i < next; i++ {
				if hit(i) {
					c.set(cell)
					break
				}
			}
		}
		i = next
	}
}

// blitCoverage is blitRow's drawing onto a coverage canvas: cov and mrow
// (nil: no clip mask) are already clipped to the canvas and the mask, and
// sample i is drawn where its source alpha (blitSolid's and blitRow's sa) is
// not zero.
func (b *blitter) blitCoverage(y, x0 int, cov, mrow []uint8) {
	cv := b.cv.cov
	n := len(cov)
	if b.paint.solid {
		ca := uint32(b.paint.col[3])
		if ca == 0 {
			return
		}
		cv.markRow(y, x0, n, func(i int) bool {
			c := uint32(cov[i])
			if mrow != nil {
				c = mul255(c, uint32(mrow[i]))
			}
			if c == 0 {
				return false
			}
			return c == 255 || mul255(ca, c) != 0
		})
		return
	}
	if cap(b.tmp) < n*4 {
		b.tmp = make([]uint8, n*4)
	}
	src := b.tmp[:n*4]
	b.paint.sh.shadeRow(y, x0, src)
	cv.markRow(y, x0, n, func(i int) bool {
		c := uint32(cov[i])
		if mrow != nil {
			c = mul255(c, uint32(mrow[i]))
		}
		if c == 0 {
			return false
		}
		sa := uint32(src[i*4+3])
		if c != 255 {
			sa = mul255(sa, c)
		}
		return sa != 0
	})
}

// compositeRow is compositeLayer's drawing of the layer pixels s (the row
// from x0 on of sample row y) onto a coverage canvas with opacity op and the
// clip mask m: a pixel is drawn where compositePixel's source alpha is not
// zero, whatever the blend mode.
func (c *Coverage) compositeRow(s []uint8, x0, y int, op uint32, m *mask) {
	c.markRow(y, x0, len(s)/4, func(i int) bool {
		sa := uint32(s[i*4+3])
		if sa == 0 {
			return false
		}
		k := op
		if m != nil {
			k = mul255(k, uint32(m.at(x0+i, y)))
		}
		if k == 0 {
			return false
		}
		if k != 255 {
			sa = mul255(sa, k)
		}
		return sa != 0
	})
}

// RenderCoverage rasterizes svg as Render does, at opts.Scale, and reports
// which cells of cell x cell canvas pixels end with non-zero alpha, without
// the canvas: it holds the pixels of the offscreen layers drawing needs only,
// as far as they are drawn. A cell has ink exactly where Render's image has a
// pixel with non-zero alpha within it. The canvas's limits apply as to
// Render's, the coverage counting as canvas memory.
func RenderCoverage(svg []byte, cell int, opts Options) (*Coverage, error) {
	if cell < 1 {
		cell = 1
	}
	var cov *Coverage
	err := renderDocInto(svg, opts, &coverageTarget{cell: cell}, func(r *renderer) error {
		cov = r.cv.cov
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cov, nil
}

// coverageTarget makes the root canvas of a render a coverage canvas.
type coverageTarget struct{ cell int }

func (t *coverageTarget) root(r *renderer, cw, ch int, bg color.Color) *canvas {
	n := coverageBytes(cw, ch, t.cell)
	if !r.chargeCanvas(n) {
		return nil
	}
	c := &canvas{w: cw, h: ch, cov: newCoverage(cw, ch, t.cell)}
	if bg != nil {
		if color.NRGBAModel.Convert(bg).(color.NRGBA).A != 0 {
			// Every pixel starts with the background's alpha.
			c.cov.fill()
			c.dirty = c.bounds()
		}
	}
	return c
}
