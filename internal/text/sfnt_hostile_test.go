package text

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fonts/liberation"
)

// hostileRecords rewrites the table directory of font b: its own records,
// then extra records, each a tag pointing at the bytes of an existing table
// (by its tag). The table data is not copied: the records alias it.
func hostileRecords(t testing.TB, b []byte, extra [][2]string) []byte {
	t.Helper()
	n := int(binary.BigEndian.Uint16(b[4:]))
	type rec struct {
		tag       string
		off, size uint32
	}
	var recs []rec
	byTag := map[string]rec{}
	for i := range n {
		r := b[12+16*i:]
		x := rec{string(r[:4]), binary.BigEndian.Uint32(r[8:]), binary.BigEndian.Uint32(r[12:])}
		recs = append(recs, x)
		byTag[x.tag] = x
	}
	for _, e := range extra {
		src, ok := byTag[e[1]]
		if !ok {
			t.Fatalf("no table %q", e[1])
		}
		recs = append(recs, rec{e[0], src.off, src.size})
	}
	// The new directory is longer: the tables move by the growth.
	shift := uint32(16 * (len(recs) - n))
	out := make([]byte, 12+16*len(recs))
	copy(out, b[:12])
	binary.BigEndian.PutUint16(out[4:], uint16(len(recs)))
	for i, r := range recs {
		w := out[12+16*i:]
		copy(w, r.tag)
		binary.BigEndian.PutUint32(w[8:], r.off+shift)
		binary.BigEndian.PutUint32(w[12:], r.size)
	}
	return append(out, b[12+16*n:]...)
}

// TestHostileTableRecords is C31's first half: a font whose directory names
// one table many times, or names many tables over the same bytes, must not
// make a font program larger than itself. The first record of a tag is the
// table, as every reader takes it.
func TestHostileTableRecords(t *testing.T) {
	src := liberation.SansRegular
	var dups [][2]string
	for range 400 {
		dups = append(dups, [2]string{"glyf", "glyf"})
	}
	var overlaps [][2]string
	for _, tag := range []string{"CFF ", "CFF2", "VORG", "vhea", "vmtx", "gasp", "fpgm", "prep", "cvt "} {
		if sfntTable(src, 0, tag) == nil {
			overlaps = append(overlaps, [2]string{tag, "glyf"})
		}
	}
	for name, ttf := range map[string][]byte{
		"duplicate glyf records": hostileRecords(t, src, dups),
		"tables over glyf":       hostileRecords(t, src, overlaps),
	} {
		t.Run(name, func(t *testing.T) {
			if p := outlineProgram(ttf, 0); len(p) > len(ttf) {
				t.Errorf("outlineProgram: %d bytes from a %d byte font", len(p), len(ttf))
			}
			if s := sfntSize(ttf, 0); s > len(ttf) {
				t.Errorf("sfntSize: %d bytes for a %d byte font", s, len(ttf))
			}
			m, err := New(WithFont("Evil", ttf))
			if err != nil {
				t.Skipf("the font does not load: %v", err)
			}
			runs, _ := m.ShapeText("Hello", "12px Evil")
			if len(runs) == 0 || runs[0].Face.Family != "Evil" {
				t.Fatalf("not shaped in the font: %+v", runs)
			}
			data := fontData(t, m, runs[0].Face)
			if len(data) > len(ttf) {
				t.Errorf("FontData: %d bytes from a %d byte font", len(data), len(ttf))
			}
		})
	}
}

// fontData is m.FontData without a ledger, failing t on an error.
func fontData(t testing.TB, m *Measurer, f *Face) []byte {
	t.Helper()
	p, err := m.FontData(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSystemFacesBounded: however many system font files a specification's
// family names reach, a Measurer keeps at most maxSystemFaces of them loaded
// (X5); a dropped one loads again when it is named again.
func TestSystemFacesBounded(t *testing.T) {
	dir := t.TempDir()
	var sys []sysFont
	for i := range maxSystemFaces + 10 {
		p := filepath.Join(dir, fmt.Sprintf("f%d.ttf", i))
		if err := os.WriteFile(p, liberation.SansRegular, 0o600); err != nil {
			t.Fatal(err)
		}
		sys = append(sys, sysFont{family: fmt.Sprintf("Sys %d", i), weight: 400, path: p})
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	es := m.systemEntries(sys)
	for _, e := range es {
		if e.load(m) == nil {
			t.Fatal("not loaded")
		}
	}
	loaded := 0
	for _, e := range es {
		if e.face != nil {
			loaded++
		}
	}
	if loaded > maxSystemFaces {
		t.Errorf("%d system faces loaded, bound %d", loaded, maxSystemFaces)
	}
	if es[0].face != nil || es[0].load(m) == nil {
		t.Error("the oldest face was not dropped, or does not load again")
	}
}

// TestFontDataChargesLedger: a font program FontData cuts out of a system
// font for embedding is charged to the call's ledger before it is made, and
// kept in the Measurer's bounded cache (X5); a font in memory is its own
// program and charges nothing.
func TestFontDataChargesLedger(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sys.ttf")
	if err := os.WriteFile(p, liberation.SansRegular, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	f := m.systemEntries([]sysFont{{family: "Sys", weight: 400, path: p}})[0].load(m)
	m.mu.Unlock()
	if f == nil || !f.mapped {
		t.Fatal("not a mapped system face")
	}
	small := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 10})
	if _, err := m.FontData(budget.WithLedger(context.Background(), small), f); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("FontData under a 1 KiB ledger: %v", err)
	}
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	data, err := m.FontData(budget.WithLedger(context.Background(), led), f)
	if err != nil || len(data) == 0 || led.Bytes() < int64(len(data)) {
		t.Errorf("FontData: %d bytes, err %v, %d charged", len(data), err, led.Bytes())
	}
	again := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	if d, _ := m.FontData(budget.WithLedger(context.Background(), again), f); &d[0] != &data[0] || again.Bytes() != 0 {
		t.Error("a cut-out program is not cached, or a cached one is charged")
	}
	runs, _ := m.ShapeText("A", "12px sans-serif")
	mem := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	if d := fontData(t, m, runs[0].Face); len(d) != len(liberation.SansRegular) || mem.Bytes() != 0 {
		t.Error("an embedded font is not its own program")
	}
}
