package raster

import "encoding/binary"

// FaceMetrics are the vertical metrics resvg (via ttf-parser) reads from a
// font, in font units.
type FaceMetrics struct {
	UnitsPerEm         float64
	Ascender           float64 // hhea
	Descender          float64 // hhea, negative below the baseline
	XHeight            float64 // OS/2 sxHeight, or 0.45 of the height when absent
	UnderlinePosition  float64 // post; negative below the baseline
	UnderlineThickness float64
	StrikePosition     float64 // OS/2 yStrikeoutPosition, or half the x-height
	SubscriptOffset    float64 // OS/2 ySubscriptYOffset
	SuperscriptOffset  float64 // OS/2 ySuperscriptYOffset
}

// MetricsFace is implemented by faces that can report FaceMetrics; text
// decorations and baseline shifts use them and fall back to fixed fractions of
// the em otherwise.
type MetricsFace interface {
	Metrics() (FaceMetrics, bool)
}

// parseFaceMetrics reads the metrics from an sfnt font program. It is tolerant
// of truncated or hostile tables: anything out of range reads as absent.
func parseFaceMetrics(prog []byte, upem float64) (FaceMetrics, bool) {
	tables := sfntTables(prog)
	if tables == nil {
		return FaceMetrics{}, false
	}
	m := FaceMetrics{UnitsPerEm: upem}
	i16 := func(t []byte, off int) (float64, bool) {
		if off < 0 || off+2 > len(t) {
			return 0, false
		}
		return float64(int16(binary.BigEndian.Uint16(t[off:]))), true
	}
	hhea := tables["hhea"]
	var ok bool
	if m.Ascender, ok = i16(hhea, 4); !ok {
		return FaceMetrics{}, false
	}
	if m.Descender, ok = i16(hhea, 6); !ok {
		return FaceMetrics{}, false
	}
	os2 := tables["OS/2"]
	xh, hasXH := 0.0, false
	if len(os2) >= 90 && binary.BigEndian.Uint16(os2) >= 2 {
		if v, good := i16(os2, 86); good && v > 0 {
			xh, hasXH = v, true
		}
	}
	if !hasXH {
		xh = float64(int((m.Ascender - m.Descender) * 0.45))
	}
	m.XHeight = xh
	if v, good := i16(os2, 28); good && len(os2) >= 30 {
		m.StrikePosition = v
	} else {
		m.StrikePosition = float64(int16(xh) / 2)
	}
	m.SubscriptOffset = upem / 0.2
	m.SuperscriptOffset = upem / 0.4
	if v, good := i16(os2, 16); good && len(os2) >= 30 {
		m.SubscriptOffset = v
	}
	if v, good := i16(os2, 24); good && len(os2) >= 30 {
		m.SuperscriptOffset = v
	}
	post := tables["post"]
	pos, hasPos := i16(post, 8)
	th, hasTh := i16(post, 10)
	if hasPos && hasTh {
		m.UnderlinePosition = pos
		m.UnderlineThickness = th
		if th <= 0 {
			m.UnderlineThickness = float64(int(upem) / 12)
		}
	} else {
		m.UnderlinePosition = float64(-int(upem) / 9)
		m.UnderlineThickness = float64(int(upem) / 12)
	}
	return m, true
}

// sfntTables returns the table directory of a single-font sfnt as name ->
// bytes. Tables reaching past the data are dropped.
func sfntTables(b []byte) map[string][]byte {
	if len(b) < 12 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[4:]))
	if n > 512 || 12+16*n > len(b) {
		return nil
	}
	out := make(map[string][]byte, 8)
	for i := 0; i < n; i++ {
		rec := b[12+16*i : 12+16*i+16]
		tag := string(rec[:4])
		off := uint64(binary.BigEndian.Uint32(rec[8:]))
		ln := uint64(binary.BigEndian.Uint32(rec[12:]))
		if off+ln > uint64(len(b)) {
			continue
		}
		switch tag {
		case "hhea", "OS/2", "post":
			out[tag] = b[off : off+ln]
		}
	}
	return out
}
