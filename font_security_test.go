package aster

// Hostile fonts (WithFont, SubsetFont). A font file is mutated at the byte
// and table-field level; each mutant must not panic out of the public API,
// hang, or allocate much more than its size would suggest.
//
//	go test . -run TestHostileFonts -v -args -sec.fontn=2000

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

var secFontN = flag.Int("sec.fontn", 300, "hostile font mutants")
var secFontSeed = flag.Int64("sec.fontseed", 1, "seed")

type sfntTable struct {
	tag         string
	off, length int
}

func sfntTables(b []byte) []sfntTable {
	n := int(binary.BigEndian.Uint16(b[4:]))
	var out []sfntTable
	for i := 0; i < n; i++ {
		r := b[12+i*16:]
		out = append(out, sfntTable{string(r[:4]), int(binary.BigEndian.Uint32(r[8:])), int(binary.BigEndian.Uint32(r[12:]))})
	}
	return out
}

var interesting16 = []uint16{0, 1, 2, 0x7fff, 0x8000, 0xffff, 0xfffe, 255, 256}
var interesting32 = []uint32{0, 1, 0x7fffffff, 0x80000000, 0xffffffff, 0xfffffff0, 65536}

func mutateFont(rng *rand.Rand, src []byte) []byte {
	b := append([]byte(nil), src...)
	tabs := sfntTables(b)
	nmut := 1 + rng.Intn(4)
	for i := 0; i < nmut; i++ {
		switch rng.Intn(6) {
		case 0: // table directory offset/length
			k := rng.Intn(len(tabs))
			rec := 12 + k*16
			if rng.Intn(2) == 0 {
				binary.BigEndian.PutUint32(b[rec+8:], interesting32[rng.Intn(len(interesting32))]%uint32(len(b)+1))
			} else {
				binary.BigEndian.PutUint32(b[rec+12:], uint32(rng.Intn(len(b)+1)))
			}
		case 1: // 16-bit field in a small table (head, hhea, maxp, OS/2, post, cmap header, name header)
			for tries := 0; tries < 20; tries++ {
				t := tabs[rng.Intn(len(tabs))]
				switch t.tag {
				case "head", "hhea", "maxp", "OS/2", "post", "cmap", "name", "hmtx", "loca", "GDEF", "GSUB", "GPOS", "kern":
					lim := t.length
					if lim > 512 {
						lim = 512
					}
					if lim < 2 || t.off+lim > len(b) {
						continue
					}
					p := t.off + rng.Intn(lim-1)
					binary.BigEndian.PutUint16(b[p:], interesting16[rng.Intn(len(interesting16))])
					tries = 99
				}
			}
		case 2: // 32-bit field in the same tables
			for tries := 0; tries < 20; tries++ {
				t := tabs[rng.Intn(len(tabs))]
				lim := t.length
				if lim > 1024 {
					lim = 1024
				}
				if lim < 4 || t.off+lim > len(b) {
					continue
				}
				p := t.off + rng.Intn(lim-3)
				binary.BigEndian.PutUint32(b[p:], interesting32[rng.Intn(len(interesting32))])
				break
			}
		case 3: // random byte flips anywhere
			for j := 0; j < 1+rng.Intn(16); j++ {
				b[rng.Intn(len(b))] = byte(rng.Intn(256))
			}
		case 4: // glyf/loca region flips
			for _, t := range tabs {
				if (t.tag == "glyf" || t.tag == "loca" || t.tag == "GSUB" || t.tag == "GPOS") && rng.Intn(2) == 0 && t.off+t.length <= len(b) && t.length > 0 {
					for j := 0; j < 1+rng.Intn(32); j++ {
						b[t.off+rng.Intn(t.length)] = byte(rng.Intn(256))
					}
				}
			}
		case 5: // truncate
			if len(b) > 100 {
				b = b[:len(b)-rng.Intn(len(b)/2)]
			}
		}
	}
	return b
}

func TestHostileFonts(t *testing.T) {
	src, err := os.ReadFile("internal/fonts/liberation/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skip(err)
	}
	rng := rand.New(rand.NewSource(*secFontSeed))
	svgIn := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="60"><text x="5" y="30" font-family="Evil" font-size="20">Hello wOrld fi ffl 123</text></svg>`
	spec := `{"width":100,"height":50,"title":"Hello World","marks":[{"type":"text","encode":{"update":{"text":{"value":"Evil Text"},"font":{"value":"Evil"},"x":{"value":5},"y":{"value":20}}}}]}`
	var bad []string
	for i := 0; i < *secFontN; i++ {
		f := mutateFont(rng, src)
		var msg string
		start := time.Now()
		done := make(chan string, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					buf := make([]byte, 4096)
					buf = buf[:runtime.Stack(buf, false)]
					done <- fmt.Sprintf("PANIC %v\n%s", r, buf)
				}
			}()
			// SubsetFont is public and takes the font straight from the caller.
			if _, _, err := SubsetFont(f, []uint16{0, 1, 2, 3, 36, 37, 100, 500, 3000, 65535}); err != nil && strings.Contains(err.Error(), "internal") {
				done <- "internal: " + err.Error()
				return
			}
			c, err := New(WithFont("Evil", f), WithTimeout(5*time.Second))
			if err != nil {
				done <- ""
				return
			}
			defer c.Close()
			if _, err := c.VegaToSVG([]byte(spec)); err != nil && strings.Contains(err.Error(), "internal error") {
				done <- "internal: " + err.Error()
				return
			}
			if _, err := c.SVGToPNG(svgIn); err != nil && strings.Contains(err.Error(), "internal error") {
				done <- "internal: " + err.Error()
				return
			}
			for _, m := range []PDFTextMode{PDFTextEmbed, PDFTextOutlines, PDFTextNamed} {
				if _, err := c.SVGToPDF(svgIn, WithPDFText(m)); err != nil && strings.Contains(err.Error(), "internal") {
					done <- "internal: " + err.Error()
					return
				}
			}
			done <- ""
		}()
		select {
		case msg = <-done:
		case <-time.After(30 * time.Second):
			msg = "HANG > 30s"
		}
		if msg == "" && time.Since(start) > 10*time.Second {
			msg = fmt.Sprintf("SLOW %v", time.Since(start))
		}
		if msg != "" {
			path := fmt.Sprintf("testdata/security/font-mutant-%d-%d.ttf", *secFontSeed, i)
			_ = os.MkdirAll("testdata/security", 0o755)
			_ = os.WriteFile(path, f, 0o644)
			bad = append(bad, path+": "+msg)
			t.Errorf("%s: %s", path, msg)
			if len(bad) > 10 {
				break
			}
		}
	}
}
