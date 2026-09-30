package label

// bitmap is a packed occupancy grid addressed as a single bit string of
// w*h bits (row-major), exactly like upstream's Bitmap. Upstream's typed
// arrays silently ignore out-of-range accesses and let an x outside [0,w)
// spill into a neighbouring row; both behaviours are kept (accesses beyond the
// array are dropped) so degenerate marks behave the same way.
type bitmap struct {
	w, h int
	a    []uint32
}

// right1[i] has the low i bits set; right0[i] is its complement.
var right0, right1 [33]uint32

func init() {
	right0[0] = ^uint32(0)
	for i := 1; i <= 32; i++ {
		right1[i] = right1[i-1]<<1 | 1
		right0[i] = ^right1[i]
	}
}

func newBitmap(w, h int) *bitmap {
	return &bitmap{w: w, h: h, a: make([]uint32, (w*h+32)/32)}
}

func (b *bitmap) word(i int) (int, bool) {
	if i < 0 {
		return 0, false
	}
	i >>= 5
	return i, i < len(b.a)
}

func (b *bitmap) get(x, y int) bool {
	index := y*b.w + x
	i, ok := b.word(index)
	return ok && b.a[i]&(1<<uint(index&31)) != 0
}

func (b *bitmap) set(x, y int) {
	index := y*b.w + x
	if i, ok := b.word(index); ok {
		b.a[i] |= 1 << uint(index&31)
	}
}

func (b *bitmap) or(i int, mask uint32) {
	if i >= 0 && i < len(b.a) {
		b.a[i] |= mask
	}
}

// rows clamps a row span to the grid so a wild coordinate cannot make the
// loops below run for billions of iterations.
func (b *bitmap) rows(y, y2 int) (int, int) {
	if y < 0 {
		y = 0
	}
	if y2 > b.h {
		y2 = b.h
	}
	return y, y2
}

// at reads a word, treating anything out of range as zero (typed-array
// semantics).
func (b *bitmap) at(i int) uint32 {
	if i < 0 || i >= len(b.a) {
		return 0
	}
	return b.a[i]
}

func (b *bitmap) getRange(x, y, x2, y2 int) bool {
	y, y2 = b.rows(y, y2)
	for r := y2; r >= y; r-- {
		start, end := r*b.w+x, r*b.w+x2
		is, ie := start>>5, end>>5
		if is == ie {
			if b.at(is)&right0[start&31]&right1[(end&31)+1] != 0 {
				return true
			}
			continue
		}
		if b.at(is)&right0[start&31] != 0 || b.at(ie)&right1[(end&31)+1] != 0 {
			return true
		}
		for i := max(is+1, 0); i < ie && i < len(b.a); i++ {
			if b.a[i] != 0 {
				return true
			}
		}
	}
	return false
}

func (b *bitmap) setRange(x, y, x2, y2 int) {
	y, y2 = b.rows(y, y2)
	for ; y <= y2; y++ {
		start, end := y*b.w+x, y*b.w+x2
		is, ie := start>>5, end>>5
		if is == ie {
			b.or(is, right0[start&31]&right1[(end&31)+1])
			continue
		}
		b.or(is, right0[start&31])
		b.or(ie, right1[(end&31)+1])
		for i := max(is+1, 0); i < ie && i < len(b.a); i++ {
			b.a[i] = ^uint32(0)
		}
	}
}

func (b *bitmap) outOfBounds(x, y, x2, y2 int) bool {
	return x < 0 || y < 0 || y2 >= b.h || x2 >= b.w
}
