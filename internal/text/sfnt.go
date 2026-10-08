package text

import (
	"encoding/binary"
	"math/bits"
	"slices"
	"strings"
)

// maxCollectionFonts bounds the faces read from one font collection.
const maxCollectionFonts = 256

// sfntDir returns the offset of the table directory of face index of a font
// file: 0 for a single font (index 0), or the index-th of a collection's.
// ok is false for anything else.
func sfntDir(b []byte, index int) (off int, ok bool) {
	if len(b) < 12 || index < 0 {
		return 0, false
	}
	if string(b[:4]) != "ttcf" {
		return 0, index == 0
	}
	n := int(binary.BigEndian.Uint32(b[8:12]))
	if index >= n || index >= maxCollectionFonts || 12+4*(index+1) > len(b) {
		return 0, false
	}
	return int(binary.BigEndian.Uint32(b[12+4*index:])), true
}

// sfntTable returns table tag of face index of font file b, as a slice of b,
// or nil.
func sfntTable(b []byte, index int, tag string) []byte {
	dir, ok := sfntDir(b, index)
	if !ok || dir < 0 || dir+12 > len(b) {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[dir+4:]))
	if n > 512 || dir+12+16*n > len(b) {
		return nil
	}
	for i := range n {
		rec := b[dir+12+16*i:][:16]
		if string(rec[:4]) != tag {
			continue
		}
		off := uint64(binary.BigEndian.Uint32(rec[8:]))
		l := uint64(binary.BigEndian.Uint32(rec[12:]))
		if off+l > uint64(len(b)) {
			return nil
		}
		return b[off : off+l]
	}
	return nil
}

// sfntSize is the size of the font face index of b would be on its own: its
// directory and its tables, each padded to four bytes (to within the padding
// of the last).
func sfntSize(b []byte, index int) int {
	dir, ok := sfntDir(b, index)
	if !ok || dir < 0 || dir+12 > len(b) {
		return len(b)
	}
	n := int(binary.BigEndian.Uint16(b[dir+4:]))
	if n > 512 || dir+12+16*n > len(b) {
		return len(b)
	}
	size := 12 + 16*n
	for i := range n {
		size += (int(binary.BigEndian.Uint32(b[dir+12+16*i+12:])) + 3) &^ 3 // padded to 4 bytes
	}
	return size
}

// outlineTables are the tables of a font an embedder of its outlines reads:
// what a PDF font subset is cut from. The rest, the colour and bitmap tables
// above all, stay behind: Apple Color Emoji's sbix is 191 of its 192 MB.
var outlineTables = map[string]bool{
	"cmap": true, "head": true, "hhea": true, "hmtx": true, "maxp": true, "name": true, "post": true, "OS/2": true,
	"glyf": true, "loca": true, "cvt ": true, "fpgm": true, "prep": true, "gasp": true,
	"CFF ": true, "CFF2": true, "VORG": true, "vhea": true, "vmtx": true,
}

// outlineProgram writes the outline tables of face index of b as a font of
// its own, or nil when b has no such face.
func outlineProgram(b []byte, index int) []byte {
	dir, ok := sfntDir(b, index)
	if !ok || dir < 0 || dir+12 > len(b) {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[dir+4:]))
	if n > 512 || dir+12+16*n > len(b) {
		return nil
	}
	type table struct {
		tag  string
		data []byte
	}
	var tables []table
	for i := range n {
		rec := b[dir+12+16*i:][:16]
		tag := string(rec[:4])
		off := uint64(binary.BigEndian.Uint32(rec[8:]))
		l := uint64(binary.BigEndian.Uint32(rec[12:]))
		if !outlineTables[tag] || off+l > uint64(len(b)) {
			continue
		}
		tables = append(tables, table{tag, b[off : off+l]})
	}
	if len(tables) == 0 {
		return nil
	}
	slices.SortFunc(tables, func(x, y table) int { return strings.Compare(x.tag, y.tag) })
	// The sfnt header: the face's own version, the table count and the
	// binary search fields.
	out := make([]byte, 12+16*len(tables))
	copy(out, b[dir:dir+4])
	binary.BigEndian.PutUint16(out[4:], uint16(len(tables)))
	es := bits.Len(uint(len(tables))) - 1
	binary.BigEndian.PutUint16(out[6:], uint16(16<<es))
	binary.BigEndian.PutUint16(out[8:], uint16(es))
	binary.BigEndian.PutUint16(out[10:], uint16(16*len(tables)-16<<es))
	headAt := -1
	for i, t := range tables {
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		start := len(out)
		out = append(out, t.data...)
		if t.tag == "head" && len(t.data) >= 12 {
			headAt = start
			clear(out[start+8 : start+12]) // checkSumAdjustment, set below
		}
		// The record is written after the append, which may move out.
		rec := out[12+16*i:]
		copy(rec, t.tag)
		binary.BigEndian.PutUint32(rec[4:], sfntChecksum(out[start:]))
		binary.BigEndian.PutUint32(rec[8:], uint32(start))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(t.data)))
	}
	for len(out)%4 != 0 {
		out = append(out, 0)
	}
	if headAt >= 0 {
		binary.BigEndian.PutUint32(out[headAt+8:], 0xB1B0AFBA-sfntChecksum(out))
	}
	return out
}

// sfntChecksum is the sum of b as big-endian 32-bit words, padded with zeros.
func sfntChecksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}
