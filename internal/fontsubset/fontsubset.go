// Package fontsubset produces sparse subsets of TrueType fonts for PDF
// embedding: the subset keeps the original glyph IDs and glyph count but
// empties the outlines of every glyph outside the requested set, so a PDF
// CIDFontType2 with /CIDToGIDMap /Identity can reference glyphs by their
// original IDs. Keeping IDs sidesteps composite-glyph renumbering entirely;
// the runs of empty loca/hmtx entries this leaves behind cost almost nothing
// once the font stream is Flate-compressed.
//
// Only glyf-flavored (TrueType outline) fonts can be subset; CFF/OTF fonts
// report CanSubset() == false and callers fall back to drawing outlines.
package fontsubset

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/go-text/typesetting/font/opentype"
)

// Font is a parsed TrueType font ready for metric queries and subsetting.
type Font struct {
	numGlyphs uint16
	upem      uint16

	// expanded per-glyph advances (font units), from hmtx/hhea
	advances []uint16

	// raw tables retained for the subset output
	head, hhea, hmtx, maxp, glyf, loca []byte
	cvt, fpgm, prep, os2, cmap         []byte

	longLoca bool

	metrics Metrics
	psName  string
}

// Metrics carries the FontDescriptor fields, in font units (scale by
// 1000/UnitsPerEm for PDF glyph space).
type Metrics struct {
	Ascent      int16
	Descent     int16 // negative below the baseline, as in hhea
	CapHeight   int16
	BBox        [4]int16 // xMin, yMin, xMax, yMax
	ItalicAngle float64
	FixedPitch  bool
}

func tag(s string) opentype.Tag { return opentype.MustNewTag(s) }

// Parse reads the tables needed for subsetting and PDF metrics.
func Parse(ttf []byte) (*Font, error) {
	ld, err := opentype.NewLoader(bytes.NewReader(ttf))
	if err != nil {
		return nil, fmt.Errorf("fontsubset: parsing font: %w", err)
	}
	f := &Font{}

	must := func(name string) ([]byte, error) {
		data, err := ld.RawTable(tag(name))
		if err != nil {
			return nil, fmt.Errorf("fontsubset: missing required table %s: %w", name, err)
		}
		return data, nil
	}
	optional := func(name string) []byte {
		data, err := ld.RawTable(tag(name))
		if err != nil {
			return nil
		}
		return data
	}

	if f.head, err = must("head"); err != nil {
		return nil, err
	}
	if f.maxp, err = must("maxp"); err != nil {
		return nil, err
	}
	if f.hhea, err = must("hhea"); err != nil {
		return nil, err
	}
	if f.hmtx, err = must("hmtx"); err != nil {
		return nil, err
	}
	if len(f.head) < 54 || len(f.maxp) < 6 || len(f.hhea) < 36 {
		return nil, fmt.Errorf("fontsubset: truncated head/maxp/hhea table")
	}

	// glyf/loca are optional at parse time: their absence (CFF fonts) makes
	// the font unsuitable for sparse subsetting but metrics still work.
	f.glyf = optional("glyf")
	f.loca = optional("loca")
	f.cvt = optional("cvt ")
	f.fpgm = optional("fpgm")
	f.prep = optional("prep")
	f.os2 = optional("OS/2")
	// cmap is not needed by PDF CIDFontType2 consumers (glyphs are addressed
	// by ID), but keeping it makes the subset a well-formed standalone font
	// that stricter parsers accept, and it compresses away almost entirely.
	f.cmap = optional("cmap")

	f.upem = binary.BigEndian.Uint16(f.head[18:])
	if f.upem == 0 {
		return nil, fmt.Errorf("fontsubset: unitsPerEm is zero")
	}
	f.longLoca = int16(binary.BigEndian.Uint16(f.head[50:])) == 1
	f.numGlyphs = binary.BigEndian.Uint16(f.maxp[4:])

	// hmtx: numberOfHMetrics (advance, lsb) pairs, then bare lsbs; glyphs
	// past the last pair reuse its advance.
	numH := binary.BigEndian.Uint16(f.hhea[34:])
	if numH == 0 || int(numH) > int(f.numGlyphs) || len(f.hmtx) < int(numH)*4 {
		return nil, fmt.Errorf("fontsubset: malformed hmtx/hhea")
	}
	f.advances = make([]uint16, f.numGlyphs)
	for i := 0; i < int(numH); i++ {
		f.advances[i] = binary.BigEndian.Uint16(f.hmtx[i*4:])
	}
	for i := int(numH); i < int(f.numGlyphs); i++ {
		f.advances[i] = f.advances[numH-1]
	}

	f.metrics = Metrics{
		Ascent:  int16(binary.BigEndian.Uint16(f.hhea[4:])),
		Descent: int16(binary.BigEndian.Uint16(f.hhea[6:])),
		BBox: [4]int16{
			int16(binary.BigEndian.Uint16(f.head[36:])),
			int16(binary.BigEndian.Uint16(f.head[38:])),
			int16(binary.BigEndian.Uint16(f.head[40:])),
			int16(binary.BigEndian.Uint16(f.head[42:])),
		},
	}
	f.metrics.CapHeight = f.metrics.Ascent // fallback when OS/2 lacks it
	if len(f.os2) >= 90 && binary.BigEndian.Uint16(f.os2) >= 2 {
		f.metrics.CapHeight = int16(binary.BigEndian.Uint16(f.os2[88:]))
	}
	if post := optional("post"); len(post) >= 16 {
		// italicAngle is a 16.16 fixed at offset 4; isFixedPitch at 12.
		f.metrics.ItalicAngle = float64(int32(binary.BigEndian.Uint32(post[4:]))) / 65536
		f.metrics.FixedPitch = binary.BigEndian.Uint32(post[12:]) != 0
	}
	f.psName = postScriptName(optional("name"))

	return f, nil
}

// UnitsPerEm returns the font's design grid size.
func (f *Font) UnitsPerEm() uint16 { return f.upem }

// NumGlyphs returns the glyph count.
func (f *Font) NumGlyphs() int { return int(f.numGlyphs) }

// Advance returns a glyph's advance width in font units.
func (f *Font) Advance(gid uint16) uint16 {
	if int(gid) >= len(f.advances) {
		return 0
	}
	return f.advances[gid]
}

// Metrics returns the FontDescriptor metrics in font units.
func (f *Font) Metrics() Metrics { return f.metrics }

// PostScriptName returns the font's PostScript name (name table ID 6), or ""
// when the font does not carry one.
func (f *Font) PostScriptName() string { return f.psName }

// CanSubset reports whether the font has TrueType outlines (glyf/loca) that
// the sparse subsetter can process.
func (f *Font) CanSubset() bool {
	return len(f.glyf) > 0 && len(f.loca) > 0
}

// glyphData returns the raw glyf entry for gid.
func (f *Font) glyphData(gid uint16) ([]byte, error) {
	if gid >= f.numGlyphs {
		return nil, fmt.Errorf("fontsubset: glyph %d out of range", gid)
	}
	var start, end uint32
	if f.longLoca {
		if len(f.loca) < (int(gid)+2)*4 {
			return nil, fmt.Errorf("fontsubset: truncated loca")
		}
		start = binary.BigEndian.Uint32(f.loca[int(gid)*4:])
		end = binary.BigEndian.Uint32(f.loca[(int(gid)+1)*4:])
	} else {
		if len(f.loca) < (int(gid)+2)*2 {
			return nil, fmt.Errorf("fontsubset: truncated loca")
		}
		start = uint32(binary.BigEndian.Uint16(f.loca[int(gid)*2:])) * 2
		end = uint32(binary.BigEndian.Uint16(f.loca[(int(gid)+1)*2:])) * 2
	}
	if start > end || int(end) > len(f.glyf) {
		return nil, fmt.Errorf("fontsubset: glyph %d has invalid loca range [%d,%d)", gid, start, end)
	}
	return f.glyf[start:end], nil
}

// componentFlag bits of composite glyph entries (OpenType spec).
const (
	flagArg1And2AreWords = 0x0001
	flagWeHaveAScale     = 0x0008
	flagMoreComponents   = 0x0020
	flagXAndYScale       = 0x0040
	flagTwoByTwo         = 0x0080
)

// closure expands gids with every component glyph referenced (transitively)
// by composite glyphs in the set, plus .notdef (glyph 0), which viewers may
// touch for any unmapped code.
func (f *Font) closure(gids map[uint16]bool) (map[uint16]bool, error) {
	out := make(map[uint16]bool, len(gids)+1)
	var stack []uint16
	push := func(g uint16) {
		if !out[g] && g < f.numGlyphs {
			out[g] = true
			stack = append(stack, g)
		}
	}
	push(0)
	for g := range gids {
		push(g)
	}
	for len(stack) > 0 {
		g := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		data, err := f.glyphData(g)
		if err != nil {
			return nil, err
		}
		if len(data) < 10 || int16(binary.BigEndian.Uint16(data)) >= 0 {
			continue // empty or simple glyph
		}
		// Composite: walk the component records.
		p := 10
		for {
			if p+4 > len(data) {
				return nil, fmt.Errorf("fontsubset: truncated composite glyph %d", g)
			}
			flags := binary.BigEndian.Uint16(data[p:])
			push(binary.BigEndian.Uint16(data[p+2:]))
			p += 4
			if flags&flagArg1And2AreWords != 0 {
				p += 4
			} else {
				p += 2
			}
			switch {
			case flags&flagWeHaveAScale != 0:
				p += 2
			case flags&flagXAndYScale != 0:
				p += 4
			case flags&flagTwoByTwo != 0:
				p += 8
			}
			if flags&flagMoreComponents == 0 {
				break
			}
		}
	}
	return out, nil
}

// Subset builds a sparse subset containing the outlines of gids (plus their
// composite closure and .notdef). Glyph IDs and count are preserved; every
// other glyph becomes an empty outline.
//
// Beyond dropping unused outlines, the subset sheds everything a PDF
// consumer does not need: hinting is removed (glyph instructions stripped,
// cvt/fpgm/prep dropped — PDF rasterization is unhinted anti-aliasing),
// hmtx is truncated to the highest kept glyph (text layout uses the PDF /W
// array, not hmtx), and cmap is replaced by a minimal valid stub (glyphs are
// addressed by ID via Identity CID mapping, never through character codes).
func (f *Font) Subset(gids map[uint16]bool) ([]byte, error) {
	if !f.CanSubset() {
		return nil, fmt.Errorf("fontsubset: font has no TrueType outlines (CFF fonts are not supported)")
	}
	keep, err := f.closure(gids)
	if err != nil {
		return nil, err
	}
	maxKept := uint16(0)
	for g := range keep {
		if g > maxKept {
			maxKept = g
		}
	}

	// Rebuild glyf/loca: kept glyphs copy their data (hinting instructions
	// stripped), everything else is a zero-length entry. Entries stay
	// 4-byte aligned (some rasterizers read glyf fields as aligned words).
	var glyf bytes.Buffer
	loca := make([]byte, (int(f.numGlyphs)+1)*4)
	for g := 0; g < int(f.numGlyphs); g++ {
		binary.BigEndian.PutUint32(loca[g*4:], uint32(glyf.Len()))
		if keep[uint16(g)] {
			data, err := f.glyphData(uint16(g))
			if err != nil {
				return nil, err
			}
			stripped, err := stripInstructions(data)
			if err != nil {
				return nil, fmt.Errorf("fontsubset: glyph %d: %w", g, err)
			}
			glyf.Write(stripped)
			for glyf.Len()%4 != 0 {
				glyf.WriteByte(0)
			}
		}
	}
	binary.BigEndian.PutUint32(loca[int(f.numGlyphs)*4:], uint32(glyf.Len()))

	// head: force long loca offsets and zero checkSumAdjustment (recomputed
	// over the assembled file below).
	head := append([]byte(nil), f.head...)
	binary.BigEndian.PutUint32(head[8:], 0)
	binary.BigEndian.PutUint16(head[50:], 1)

	// hmtx/hhea: full (advance, lsb) pairs up to the highest kept glyph,
	// bare zero lsbs for the (empty) glyphs after it.
	numH := int(maxKept) + 1
	hmtx := make([]byte, numH*4+(int(f.numGlyphs)-numH)*2)
	for g := 0; g < numH; g++ {
		binary.BigEndian.PutUint16(hmtx[g*4:], f.advances[g])
		binary.BigEndian.PutUint16(hmtx[g*4+2:], uint16(f.lsb(uint16(g))))
	}
	hhea := append([]byte(nil), f.hhea...)
	binary.BigEndian.PutUint16(hhea[34:], uint16(numH))

	// Minimal post table, format 3 (no glyph names): 32 bytes.
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post[0:], 0x00030000)
	binary.BigEndian.PutUint32(post[4:], uint32(int32(f.metrics.ItalicAngle*65536)))
	if f.metrics.FixedPitch {
		binary.BigEndian.PutUint32(post[12:], 1)
	}

	tables := []struct {
		tag  string
		data []byte
	}{
		{"head", head},
		{"hhea", hhea},
		{"maxp", f.maxp},
		{"hmtx", hmtx},
		{"loca", loca},
		{"glyf", glyf.Bytes()},
		{"post", post},
		{"cmap", minimalCmap()},
	}
	if len(f.os2) > 0 {
		tables = append(tables, struct {
			tag  string
			data []byte
		}{"OS/2", f.os2})
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].tag < tables[j].tag })

	return assembleSfnt(tables)
}

// lsb returns a glyph's left side bearing from the original hmtx layout.
func (f *Font) lsb(gid uint16) int16 {
	numH := int(binary.BigEndian.Uint16(f.hhea[34:]))
	if int(gid) < numH {
		return int16(binary.BigEndian.Uint16(f.hmtx[int(gid)*4+2:]))
	}
	off := numH*4 + (int(gid)-numH)*2
	if off+2 > len(f.hmtx) {
		return 0
	}
	return int16(binary.BigEndian.Uint16(f.hmtx[off:]))
}

// composite flag: instructions follow the last component.
const flagWeHaveInstructions = 0x0100

// stripInstructions removes TrueType hinting bytecode from a glyph record.
// PDF rasterizers render unhinted (matching resvg's PNG rasterization), and
// dropping the instructions lets the subset omit the cvt/fpgm/prep hinting
// tables they depend on.
func stripInstructions(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	if len(data) < 10 {
		return nil, fmt.Errorf("truncated glyph header")
	}
	numContours := int16(binary.BigEndian.Uint16(data))

	if numContours < 0 {
		// Composite: clear WE_HAVE_INSTRUCTIONS and truncate the trailing
		// bytecode after the last component record.
		out := append([]byte(nil), data...)
		p := 10
		for {
			if p+4 > len(out) {
				return nil, fmt.Errorf("truncated composite glyph")
			}
			flags := binary.BigEndian.Uint16(out[p:])
			binary.BigEndian.PutUint16(out[p:], flags&^flagWeHaveInstructions)
			p += 4
			if flags&flagArg1And2AreWords != 0 {
				p += 4
			} else {
				p += 2
			}
			switch {
			case flags&flagWeHaveAScale != 0:
				p += 2
			case flags&flagXAndYScale != 0:
				p += 4
			case flags&flagTwoByTwo != 0:
				p += 8
			}
			if flags&flagMoreComponents == 0 {
				break
			}
		}
		if p > len(out) {
			return nil, fmt.Errorf("truncated composite glyph")
		}
		return out[:p], nil
	}

	// Simple glyph: header, endPtsOfContours, instructionLength,
	// instructions, then point flags/coordinates. Zero the length and cut
	// the instruction bytes out.
	instrLenOff := 10 + int(numContours)*2
	if instrLenOff+2 > len(data) {
		return nil, fmt.Errorf("truncated simple glyph")
	}
	instrLen := int(binary.BigEndian.Uint16(data[instrLenOff:]))
	if instrLen == 0 {
		return data, nil
	}
	if instrLenOff+2+instrLen > len(data) {
		return nil, fmt.Errorf("glyph instructions overrun")
	}
	out := make([]byte, 0, len(data)-instrLen)
	out = append(out, data[:instrLenOff]...)
	out = append(out, 0, 0) // instructionLength = 0
	out = append(out, data[instrLenOff+2+instrLen:]...)
	return out, nil
}

// minimalCmap synthesizes the smallest valid cmap: a single Windows Unicode
// (3,1) format-4 subtable containing only the required 0xFFFF terminator
// segment. PDF consumers address glyphs by ID and never consult it; the stub
// keeps the subset a well-formed font for strict sfnt parsers.
func minimalCmap() []byte {
	b := make([]byte, 12+24)
	// header: version 0, one encoding record for (3,1) at offset 12
	binary.BigEndian.PutUint16(b[2:], 1)
	binary.BigEndian.PutUint16(b[4:], 3)
	binary.BigEndian.PutUint16(b[6:], 1)
	binary.BigEndian.PutUint32(b[8:], 12)
	// format 4 subtable with a single terminator segment
	s := b[12:]
	binary.BigEndian.PutUint16(s[0:], 4)       // format
	binary.BigEndian.PutUint16(s[2:], 24)      // length
	binary.BigEndian.PutUint16(s[6:], 2)       // segCountX2
	binary.BigEndian.PutUint16(s[8:], 2)       // searchRange
	binary.BigEndian.PutUint16(s[10:], 0)      // entrySelector
	binary.BigEndian.PutUint16(s[12:], 0)      // rangeShift
	binary.BigEndian.PutUint16(s[14:], 0xFFFF) // endCode[0]
	// reservedPad at 16 is zero
	binary.BigEndian.PutUint16(s[18:], 0xFFFF) // startCode[0]
	binary.BigEndian.PutUint16(s[20:], 1)      // idDelta[0] → maps 0xFFFF to glyph 0
	binary.BigEndian.PutUint16(s[22:], 0)      // idRangeOffset[0]
	return b
}

// assembleSfnt builds a TrueType file from the given tables, computing table
// checksums and the head checkSumAdjustment.
func assembleSfnt(tables []struct {
	tag  string
	data []byte
}) ([]byte, error) {
	n := len(tables)
	// Binary-search fields per the sfnt spec.
	entrySelector := 0
	for 1<<(entrySelector+1) <= n {
		entrySelector++
	}
	searchRange := (1 << entrySelector) * 16

	var out bytes.Buffer
	w32 := func(v uint32) { _ = binary.Write(&out, binary.BigEndian, v) }
	w16 := func(v uint16) { _ = binary.Write(&out, binary.BigEndian, v) }

	w32(0x00010000) // TrueType sfnt version
	w16(uint16(n))
	w16(uint16(searchRange))
	w16(uint16(entrySelector))
	w16(uint16(n*16 - searchRange))

	offset := 12 + n*16
	headOffset := -1
	for _, t := range tables {
		if len(t.tag) != 4 {
			return nil, fmt.Errorf("fontsubset: bad table tag %q", t.tag)
		}
		w32(uint32(opentype.NewTag(t.tag[0], t.tag[1], t.tag[2], t.tag[3])))
		w32(tableChecksum(t.data))
		w32(uint32(offset))
		w32(uint32(len(t.data)))
		if t.tag == "head" {
			headOffset = offset
		}
		offset += (len(t.data) + 3) &^ 3
	}
	for _, t := range tables {
		out.Write(t.data)
		for out.Len()%4 != 0 {
			out.WriteByte(0)
		}
	}

	// head.checkSumAdjustment = 0xB1B0AFBA - checksum(entire font).
	font := out.Bytes()
	adj := 0xB1B0AFBA - tableChecksum(font)
	if headOffset >= 0 {
		binary.BigEndian.PutUint32(font[headOffset+8:], adj)
	}
	return font, nil
}

// tableChecksum sums a table as big-endian uint32s, zero-padded at the end.
func tableChecksum(data []byte) uint32 {
	var sum uint32
	for i := 0; i < len(data); i += 4 {
		var v uint32
		for j := 0; j < 4; j++ {
			v <<= 8
			if i+j < len(data) {
				v |= uint32(data[i+j])
			}
		}
		sum += v
	}
	return sum
}

// postScriptName extracts name ID 6 from a raw name table, preferring the
// Windows (3,1) entry, then Macintosh (1,0).
func postScriptName(name []byte) string {
	if len(name) < 6 {
		return ""
	}
	count := int(binary.BigEndian.Uint16(name[2:]))
	stringOffset := int(binary.BigEndian.Uint16(name[4:]))
	best := -1 // record index; Windows platform wins
	var bestPlatform uint16
	for i := 0; i < count; i++ {
		rec := 6 + i*12
		if rec+12 > len(name) {
			return ""
		}
		platform := binary.BigEndian.Uint16(name[rec:])
		nameID := binary.BigEndian.Uint16(name[rec+6:])
		if nameID != 6 {
			continue
		}
		if platform == 3 || (best < 0 && platform == 1) {
			best = rec
			bestPlatform = platform
		}
	}
	if best < 0 {
		return ""
	}
	length := int(binary.BigEndian.Uint16(name[best+8:]))
	offset := stringOffset + int(binary.BigEndian.Uint16(name[best+10:]))
	if offset+length > len(name) {
		return ""
	}
	raw := name[offset : offset+length]
	if bestPlatform == 3 {
		// UTF-16BE; PostScript names are ASCII, take the low bytes.
		var b []byte
		for i := 1; i < len(raw); i += 2 {
			b = append(b, raw[i])
		}
		return string(b)
	}
	return string(raw)
}
