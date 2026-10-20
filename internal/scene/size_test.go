package scene

import (
	"testing"
	"unsafe"
)

// An Item is allocated once per mark item, so its size class is a cost of
// every chart: the item must stay in the 480-byte class it was in (Bounds
// carries a pointer for string coordinates, and a Paint a flag; both are
// placed so as not to grow it). A field added where padding does not absorb
// it fails here, to be placed or paid for deliberately.
func TestItemStaysInItsSizeClass(t *testing.T) {
	if n := unsafe.Sizeof(Item{}); n > 480 {
		t.Errorf("Item is %d bytes, over its 480-byte class", n)
	}
	if n := unsafe.Sizeof(Paint{}); n != 32 {
		t.Errorf("Paint is %d bytes, want 32", n)
	}
	if n := unsafe.Sizeof(Bounds{}); n != 40 {
		t.Errorf("Bounds is %d bytes, want 40", n)
	}
}
