package testutil

import (
	"encoding/binary"
	"unicode/utf16"
)

// NameRecord is one record of a font's name table. A Windows (3) or Unicode
// (0) record's Value is written in UTF-16BE, a Macintosh (1) one as it is.
type NameRecord struct {
	Platform, Encoding, Language, ID uint16
	Value                            string
}

// FontWithNames returns base, a single TrueType or OpenType font, with its
// name table replaced by one of recs: a font that calls itself what a test
// needs. Its other tables are base's.
func FontWithNames(base []byte, recs []NameRecord) []byte {
	return FontCollection(base, [][]NameRecord{recs})[4*3+4:]
}

// FontCollection returns a font collection (.ttc) of len(faces) faces of
// base, a single font, each with its own name table, of faces[i], and every
// other table shared with the others: many differently named faces in a file
// little larger than base. With one face the result, past its 16-byte
// collection header, is a single font whose offsets count from the header
// (FontWithNames moves them).
func FontCollection(base []byte, faces [][]NameRecord) []byte {
	n := int(binary.BigEndian.Uint16(base[4:]))
	single := len(faces) == 1
	head := 12 + 4*len(faces)
	dirLen := 12 + 16*n
	blob := head + len(faces)*dirLen // where base's bytes are copied
	out := make([]byte, blob, blob+len(base))
	copy(out, "ttcf")
	binary.BigEndian.PutUint32(out[4:], 0x00010000)
	binary.BigEndian.PutUint32(out[8:], uint32(len(faces)))
	out = append(out, base...)
	shift := func(off int) int {
		if single {
			// FontWithNames drops the collection header: offsets count
			// from the font's own start.
			return off - head
		}
		return off
	}
	for i, recs := range faces {
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		name := nameTable(recs)
		nameOff := len(out)
		out = append(out, name...)
		dir := head + i*dirLen
		binary.BigEndian.PutUint32(out[12+4*i:], uint32(dir))
		copy(out[dir:dir+12], base[:12])
		for k := range n {
			r := base[12+16*k : 12+16*k+16]
			w := out[dir+12+16*k : dir+12+16*k+16]
			copy(w, r)
			if string(r[:4]) == "name" {
				binary.BigEndian.PutUint32(w[4:], checksum(name))
				binary.BigEndian.PutUint32(w[8:], uint32(shift(nameOff)))
				binary.BigEndian.PutUint32(w[12:], uint32(len(name)))
				continue
			}
			binary.BigEndian.PutUint32(w[8:], uint32(shift(blob+int(binary.BigEndian.Uint32(r[8:])))))
		}
	}
	return out
}

// nameTable is a format 0 name table of recs.
func nameTable(recs []NameRecord) []byte {
	var strs []byte
	t := make([]byte, 6+12*len(recs))
	binary.BigEndian.PutUint16(t[2:], uint16(len(recs)))
	binary.BigEndian.PutUint16(t[4:], uint16(len(t)))
	for i, r := range recs {
		var v []byte
		if r.Platform == 1 {
			v = []byte(r.Value)
		} else {
			for _, u := range utf16.Encode([]rune(r.Value)) {
				v = append(v, byte(u>>8), byte(u))
			}
		}
		w := t[6+12*i:]
		binary.BigEndian.PutUint16(w[0:], r.Platform)
		binary.BigEndian.PutUint16(w[2:], r.Encoding)
		binary.BigEndian.PutUint16(w[4:], r.Language)
		binary.BigEndian.PutUint16(w[6:], r.ID)
		binary.BigEndian.PutUint16(w[8:], uint16(len(v)))
		binary.BigEndian.PutUint16(w[10:], uint16(len(strs)))
		strs = append(strs, v...)
	}
	return append(t, strs...)
}

func checksum(b []byte) uint32 {
	var s uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		s += binary.BigEndian.Uint32(w[:])
	}
	return s
}
