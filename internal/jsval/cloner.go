package jsval

import "unsafe"

// Cloner makes shallow copies of objects as Object.Clone does, carving the
// small ones out of chunks, one allocation per chunk: a data set of a hundred
// thousand rows is copied once on ingest and once more by every derived
// stream. A chunk lives as long as any copy made from it does. The zero Cloner
// is ready; it is for one goroutine at a time.
type Cloner struct {
	sl slabs
}

// chunkBytes sizes a Cloner's chunks once they have grown: 16 KB of objects,
// under the size Go allocates apart.
const chunkBytes = 16 << 10

func limitFor(size uintptr) int { return max(4, min(256, chunkBytes/int(size))) }

// Clone is o.Clone().
func (c *Cloner) Clone(o *Object) *Object {
	n := o.Len()
	if o == nil || n+1 > 12 {
		return o.Clone()
	}
	var x *Object
	var kb []string
	var vb []Value
	switch n + 1 {
	case 1:
		s := slabNext(&c.sl.po1, limitFor(unsafe.Sizeof(objectN1{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 2:
		s := slabNext(&c.sl.po2, limitFor(unsafe.Sizeof(objectN2{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 3:
		s := slabNext(&c.sl.po3, limitFor(unsafe.Sizeof(objectN3{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 4:
		s := slabNext(&c.sl.po4, limitFor(unsafe.Sizeof(objectN4{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 5:
		s := slabNext(&c.sl.po5, limitFor(unsafe.Sizeof(objectN5{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 6:
		s := slabNext(&c.sl.po6, limitFor(unsafe.Sizeof(objectN6{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 7:
		s := slabNext(&c.sl.po7, limitFor(unsafe.Sizeof(objectN7{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 8:
		s := slabNext(&c.sl.po8, limitFor(unsafe.Sizeof(objectN8{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 9:
		s := slabNext(&c.sl.po9, limitFor(unsafe.Sizeof(objectN9{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 10:
		s := slabNext(&c.sl.po10, limitFor(unsafe.Sizeof(objectN10{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	case 11:
		s := slabNext(&c.sl.po11, limitFor(unsafe.Sizeof(objectN11{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	default:
		s := slabNext(&c.sl.po12, limitFor(unsafe.Sizeof(objectN12{})))
		x, kb, vb = &s.Object, s.kbuf[:0], s.vbuf[:0]
	}
	x.keys, x.vals = append(kb, o.keys...), append(vb, o.vals...)
	x.str, x.host, x.tid = o.str, o.host, o.tid
	return x
}
