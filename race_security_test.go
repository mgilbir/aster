package aster_test

// Separate Converters must share no mutable state: run several at once on
// different specs (Vega, Vega-Lite 6.4 and 5.8, SVG/PNG/PDF) under -race.
//
//	go test -race . -run TestConvertersDoNotShareState

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

func TestConvertersDoNotShareState(t *testing.T) {
	var vg, vl []string
	m, _ := filepath.Glob("testdata/corpus/vega/*.json")
	sort.Strings(m)
	vg = append(vg, m...)
	m, _ = filepath.Glob("testdata/corpus/vegalite/*.json")
	sort.Strings(m)
	vl = append(vl, m...)
	if len(vg) < 20 || len(vl) < 20 {
		t.Skip("no corpus")
	}
	ld := corpusLoader(t)
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			opts := []aster.Option{aster.WithLoader(ld), aster.WithTimeout(20 * time.Second)}
			if w%3 == 2 {
				opts = append(opts, aster.WithVegaLiteVersion("5.8"))
			}
			c, err := aster.New(opts...)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			for i := 0; i < 40; i++ {
				vlp := vl[(w*7+i*3)%len(vl)]
				b, _ := os.ReadFile(vlp)
				if svg, err := c.VegaLiteToSVG(b); err == nil && i%10 == 0 {
					_, _ = c.SVGToPNG(svg)
					_, _ = c.SVGToPDF(svg)
				}
				vgp := vg[(w*5+i*7)%len(vg)]
				b, _ = os.ReadFile(vgp)
				_, _ = c.VegaToSVG(b)
				_, _ = c.VegaLiteToVega(b)
			}
		}(w)
	}
	wg.Wait()
}
