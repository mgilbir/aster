package scene

import (
	"testing"
	"unsafe"
)

// An Item is allocated once per mark item, so its size class is a cost of
// every chart: the item must stay in the 480-byte class it was in on 64-bit
// platforms (Bounds carries a pointer for string coordinates, and a Paint a
// flag; both are placed so as not to grow it). A field added where padding
// does not absorb it fails here, to be placed or paid for deliberately. The
// layouts are compared with their fields', so that it holds on 32-bit
// platforms too.
func TestItemStaysInItsSizeClass(t *testing.T) {
	if n := unsafe.Sizeof(Item{}); n > 480 {
		t.Errorf("Item is %d bytes, over its 480-byte class", n)
	}
	// raw sits in kind's padding: a Paint is the size it would be without it.
	if got, want := unsafe.Sizeof(Paint{}), unsafe.Sizeof(struct {
		kind paintKind
		s    string
		g    *Gradient
	}{}); got != want {
		t.Errorf("Paint is %d bytes, want %d", got, want)
	}
	// Four coordinates and the pointer, nothing more.
	if got, want := unsafe.Sizeof(Bounds{}), unsafe.Sizeof(struct {
		x [4]float64
		s *boundStrs
	}{}); got != want {
		t.Errorf("Bounds is %d bytes, want %d", got, want)
	}
}
