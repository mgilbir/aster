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
	n := binary.BigEndian.Uint32(b[8:12])
	if index >= maxCollectionFonts || uint32(index) >= n || 12+4*(index+1) > len(b) {
		return 0, false
	}
	// Within b, so that dir+12 cannot overflow a 32-bit int.
	dir := binary.BigEndian.Uint32(b[12+4*index:])
	if uint64(dir) > uint64(len(b)) {
		return 0, false
	}
	return int(dir), true
}

// collectionSize is the number of faces in a font file: those of a
// collection, or 1.
func collectionSize(b []byte) int {
	if len(b) < 12 || string(b[:4]) != "ttcf" {
		return 1
	}
	return int(min(binary.BigEndian.Uint32(b[8:12]), maxCollectionFonts))
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

// sfntRecord is a table of a face: its tag and where its bytes are in the
// font file.
type sfntRecord struct {
	tag     string
	off, nb int
}

// sfntRecords reads the table directory of face index of b, the one
// discipline every reader of a whole face here follows, since a font is
// untrusted (C31): a tag named twice is the table its first record names, as
// sfntTable and fontsubset take it, and a record whose bytes lie outside the
// file is no table. ok is false for a face with no directory, and for one
// whose tables add up to more than the file: tables lie apart in a sound
// font, and ones that overlap would make whatever is written from them
// larger than the font itself, many times over.
func sfntRecords(b []byte, index int) (dir int, recs []sfntRecord, ok bool) {
	dir, ok = sfntDir(b, index)
	if !ok || dir < 0 || dir+12 > len(b) {
		return 0, nil, false
	}
	n := int(binary.BigEndian.Uint16(b[dir+4:]))
	if n > 512 || dir+12+16*n > len(b) {
		return 0, nil, false
	}
	recs = make([]sfntRecord, 0, n)
	seen := make(map[string]bool, n)
	total := uint64(0)
	for i := range n {
		rec := b[dir+12+16*i:][:16]
		tag := string(rec[:4])
		if seen[tag] {
			continue
		}
		seen[tag] = true
		off := uint64(binary.BigEndian.Uint32(rec[8:]))
		l := uint64(binary.BigEndian.Uint32(rec[12:]))
		if off+l > uint64(len(b)) {
			continue
		}
		if total += l; total > uint64(len(b)) {
			return 0, nil, false
		}
		recs = append(recs, sfntRecord{tag, int(off), int(l)})
	}
	return dir, recs, true
}

// sfntSize is the size of the font face index of b would be on its own: its
// directory and its tables, each padded to four bytes (to within the padding
// of the last). It is never more than b holds, padding aside: a face whose
// directory cannot be read says it is the size of b.
func sfntSize(b []byte, index int) int {
	_, recs, ok := sfntRecords(b, index)
	if !ok {
		return len(b)
	}
	size := 12 + 16*len(recs)
	for _, r := range recs {
		size += (r.nb + 3) &^ 3 // padded to 4 bytes
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

// outlineProgramSize is the size of what outlineProgram writes, or 0 when it
// writes nothing.
func outlineProgramSize(b []byte, index int) int {
	_, recs, ok := sfntRecords(b, index)
	if !ok {
		return 0
	}
	n, size := 0, 0
	for _, r := range recs {
		if outlineTables[r.tag] {
			n++
			size += (r.nb + 3) &^ 3
		}
	}
	if n == 0 {
		return 0
	}
	return 12 + 16*n + size
}

// outlineProgram writes the outline tables of face index of b as a font of
// its own, or nil when b has no such face.
func outlineProgram(b []byte, index int) []byte {
	dir, recs, ok := sfntRecords(b, index)
	if !ok {
		return nil
	}
	type table struct {
		tag  string
		data []byte
	}
	var tables []table
	for _, r := range recs {
		if outlineTables[r.tag] {
			tables = append(tables, table{r.tag, b[r.off : r.off+r.nb]})
		}
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
