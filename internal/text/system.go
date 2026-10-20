package text

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"

	"github.com/mgilbir/aster/internal/csscolor"
)

// System fonts (WithSystemFonts).
//
// forme parses fonts but does not enumerate them, so this package walks the
// platform's font directories itself, once per process, and indexes each plain
// .ttf/.otf file by family, weight and style from its name, OS/2 and head
// tables. A file is only read in full, and handed to forme, when a query
// names its family.
//
// Each face of a TrueType or OpenType collection (.ttc, .otc) is indexed as
// a font of its own. A file is mapped into memory read-only rather than read
// (see mapSystemFont), so a large one such as Apple Color Emoji costs only the
// pages its faces read.
//
// Limits compared with a full font database: bare .woff files are not
// indexed, and there is no fallback to system fonts at all: a system font
// draws text only when the CSS font-family names it. That is deliberate for
// colour emoji fonts too: Apple Color Emoji's licence, for one, does not
// allow drawing it into documents for distribution, so it is only used for a
// spec that asks for it by name.
const (
	maxSystemFiles = 20000
	maxSystemDepth = 6
	maxNameTable   = 1 << 20
)

type sysFont struct {
	family string
	names  []string // every family name the face states (family among them)
	weight int
	italic bool
	path   string
	index  int // the face's index in its collection; 0 for a single font
}

type systemIndex struct {
	byFam map[string][]sysFont // by normalised family
}

var (
	systemOnce   sync.Once
	systemIndexV *systemIndex
)

// loadSystemIndex scans the system font directories once per process.
func loadSystemIndex() *systemIndex {
	systemOnce.Do(func() {
		idx := &systemIndex{byFam: make(map[string][]sysFont)}
		files := 0
		for _, dir := range systemFontDirs() {
			scanFontDir(dir, 0, &files, idx)
		}
		for _, list := range idx.byFam {
			// By file, then face: the order a family's equally good faces
			// are chosen in does not depend on the scan's.
			sort.Slice(list, func(i, j int) bool {
				if list[i].path != list[j].path {
					return list[i].path < list[j].path
				}
				return list[i].index < list[j].index
			})
		}
		systemIndexV = idx
	})
	return systemIndexV
}

func systemFontDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		d := []string{"/System/Library/Fonts", "/System/Library/Fonts/Supplemental", "/Library/Fonts"}
		if home != "" {
			d = append(d, filepath.Join(home, "Library/Fonts"))
		}
		return d
	case "windows":
		if root := os.Getenv("SystemRoot"); root != "" {
			return []string{filepath.Join(root, "Fonts")}
		}
		return nil
	default:
		d := []string{"/usr/share/fonts", "/usr/local/share/fonts"}
		if home != "" {
			d = append(d, filepath.Join(home, ".fonts"), filepath.Join(home, ".local/share/fonts"))
		}
		return d
	}
}

func scanFontDir(dir string, depth int, files *int, idx *systemIndex) {
	if depth > maxSystemDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		if *files >= maxSystemFiles {
			return
		}
		path := filepath.Join(dir, de.Name())
		if de.IsDir() {
			scanFontDir(path, depth+1, files, idx)
			continue
		}
		switch csscolor.ASCIILower(filepath.Ext(de.Name())) { // the font extensions are ASCII
		case ".ttf", ".otf", ".ttc", ".otc":
		default:
			continue
		}
		if info, err := de.Info(); err != nil || !info.Mode().IsRegular() || info.Size() > maxSystemFontBytes {
			continue
		}
		*files++
		idx.add(readSysFonts(path))
	}
}

// add indexes faces under each of their family names.
func (idx *systemIndex) add(faces []sysFont) {
	for _, sf := range faces {
		for _, name := range sf.names {
			n := normFamily(name)
			idx.byFam[n] = append(idx.byFam[n], sf)
		}
	}
}

// readSysFonts reads the family, weight and style of each face of a font
// file, a single font or a collection, from its tables without loading the
// whole file.
func readSysFonts(path string) (out []sysFont) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var hdr [12]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return nil
	}
	if string(hdr[:4]) != "ttcf" {
		if sf, ok := readSysFace(f, path, 0, 0); ok {
			out = append(out, sf)
		}
		return out
	}
	n := int(min(binary.BigEndian.Uint32(hdr[8:12]), maxCollectionFonts))
	offs := make([]byte, 4*n)
	if _, err := f.ReadAt(offs, 12); err != nil {
		return nil
	}
	for i := range n {
		if sf, ok := readSysFace(f, path, i, int64(binary.BigEndian.Uint32(offs[4*i:]))); ok {
			out = append(out, sf)
		}
	}
	return out
}

// readSysFace reads the face whose table directory is at base in f.
func readSysFace(f *os.File, path string, index int, base int64) (sf sysFont, ok bool) {
	var hdr [12]byte
	if _, err := f.ReadAt(hdr[:], base); err != nil {
		return sf, false
	}
	switch binary.BigEndian.Uint32(hdr[:4]) {
	case 0x00010000, 0x4F54544F, 0x74727565: // 1.0, OTTO, true
	default:
		return sf, false
	}
	n := int(binary.BigEndian.Uint16(hdr[4:6]))
	if n == 0 || n > 512 {
		return sf, false
	}
	dir := make([]byte, 16*n)
	if _, err := f.ReadAt(dir, base+12); err != nil {
		return sf, false
	}
	table := func(tag string, max int) []byte {
		for i := 0; i < n; i++ {
			rec := dir[16*i : 16*i+16]
			if string(rec[:4]) != tag {
				continue
			}
			off := int64(binary.BigEndian.Uint32(rec[8:12]))
			l := int(min(int64(binary.BigEndian.Uint32(rec[12:16])), int64(max)))
			b := make([]byte, l)
			if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
				return nil
			}
			return b
		}
		return nil
	}

	names, family := familyNames(table("name", maxNameTable))
	if family == "" {
		return sf, false
	}
	sf = sysFont{family: family, names: names, weight: weightNormal, path: path, index: index}
	if os2 := table("OS/2", 64); len(os2) >= 64 {
		if w := int(binary.BigEndian.Uint16(os2[4:6])); w >= 1 && w <= 1000 {
			sf.weight = w
		}
		sel := binary.BigEndian.Uint16(os2[62:64])
		sf.italic = sel&1 != 0 || sel&(1<<9) != 0
	} else if head := table("head", 54); len(head) >= 46 {
		mac := binary.BigEndian.Uint16(head[44:46])
		if mac&1 != 0 {
			sf.weight = weightBold
		}
		sf.italic = mac&2 != 0
	}
	return sf, true
}

// maxFamilyNames bounds the family names one face is indexed under: a font
// states a few, one per language it is localised in.
const maxFamilyNames = 32

// familyNames reads the family names a face states in its name table: its
// family (name ID 1, which for a face beyond the four of a style-linked
// family names the face itself, "Calibri Light"), its typographic family
// (16, "Calibri") and its WWS family (21), in every language, as fontconfig
// and browsers match a family by any of them (C45). display is the one the
// face is known by: the English (United States) typographic family, else
// its family, else any.
//
// A Windows record is UTF-16 when its encoding is Unicode (1, 10) or symbol
// (0), a Unicode platform record always; a Macintosh one is read when it is
// Mac Roman. Others (Windows code pages, Mac Japanese, ...) are skipped
// rather than misread.
func familyNames(t []byte) (names []string, display string) {
	if len(t) < 6 {
		return nil, ""
	}
	count := int(binary.BigEndian.Uint16(t[2:4]))
	strOff := int(binary.BigEndian.Uint16(t[4:6]))
	best := -1
	seen := map[string]bool{}
	for i := 0; i < count; i++ {
		r := 6 + 12*i
		if r+12 > len(t) {
			break
		}
		plat := binary.BigEndian.Uint16(t[r:])
		enc := binary.BigEndian.Uint16(t[r+2:])
		lang := binary.BigEndian.Uint16(t[r+4:])
		nid := binary.BigEndian.Uint16(t[r+6:])
		l := int(binary.BigEndian.Uint16(t[r+8:]))
		o := strOff + int(binary.BigEndian.Uint16(t[r+10:]))
		if (nid != 1 && nid != 16 && nid != 21) || o+l > len(t) {
			continue
		}
		raw := t[o : o+l]
		var name string
		switch {
		case plat == 0, plat == 3 && (enc == 0 || enc == 1 || enc == 10):
			u := make([]uint16, len(raw)/2)
			for j := range u {
				u[j] = binary.BigEndian.Uint16(raw[2*j:])
			}
			name = string(utf16.Decode(u))
		case plat == 1 && enc == 0:
			name = macRoman(raw)
		default:
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if n := normFamily(name); !seen[n] && len(names) < maxFamilyNames {
			seen[n] = true
			names = append(names, name)
		}
		// The display family: typographic before legacy (21 last), Windows
		// or Unicode before Macintosh, en-US first among them.
		score := 0
		switch nid {
		case 16:
			score = 4
		case 1:
			score = 2
		}
		if plat != 1 {
			score += 8
		}
		if plat == 3 && lang == 0x409 {
			score += 16
		}
		if score > best {
			best, display = score, name
		}
	}
	return names, display
}

// macRoman decodes Mac OS Roman text.
func macRoman(b []byte) string {
	s, err := charmap.Macintosh.NewDecoder().Bytes(b)
	if err != nil {
		return ""
	}
	return string(s)
}

// systemEntries adapts indexed system fonts to this Measurer's entries,
// creating each entry (and so, later, each Face) once per Measurer: faces are
// not shareable between Measurers.
func (m *Measurer) systemEntries(sys []sysFont) []*entry {
	out := make([]*entry, len(sys))
	for i, sf := range sys {
		key := sysKey{sf.path, sf.index}
		e, ok := m.sysEntries[key]
		if !ok {
			e = &entry{family: sf.family, norm: normFamily(sf.family), names: sf.names, weight: sf.weight, italic: sf.italic, path: sf.path, index: sf.index}
			if m.sysEntries == nil {
				m.sysEntries = make(map[sysKey]*entry)
			}
			m.sysEntries[key] = e
		}
		out[i] = e
	}
	return out
}

// sysKey is a face of a system font file.
type sysKey struct {
	path  string
	index int
}

// loadSystemFace maps a system font file and loads the entry's face of it.
func loadSystemFace(m *Measurer, e *entry) (*Face, error) {
	data, err := mapSystemFont(e.path)
	if err != nil {
		return nil, err
	}
	m.nextID++
	id := fmt.Sprintf("system-%d-%s-%d", m.nextID, filepath.Base(e.path), e.index)
	f, err := newFaceAt(id, e.family, e.weight, e.italic, data, e.index, true)
	if err != nil {
		return nil, err
	}
	f.source = "system\x00" + e.path + "\x00" + strconv.Itoa(e.index)
	return f, nil
}
