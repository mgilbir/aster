package aster_test

// SVG mutation sweep for SVGToPNG and SVGToPDF (both accept an arbitrary
// caller-supplied SVG). Corpus specs are rendered to SVG, then attribute
// values and path data are replaced by hostile values. Neither entry point
// may panic (the PDF path has no recover of its own), return an
// "internal error", or run for more than a few seconds.
//
//	go test . -run TestSVGMutations -v -args -sec.svgn=2000

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

var secSVGN = flag.Int("sec.svgn", 200, "SVG mutants")
var secSVGSeed = flag.Int64("sec.svgseed", 1, "seed")

var attrRe = regexp.MustCompile(`([a-zA-Z:-]+)="([^"]*)"`)
var hostileAttr = []string{"", "NaN", "Infinity", "-Infinity", "1e999", "-1e999", "1e308", "-1e308", "0", "-1", "1e-320", "99999999999", "nan", "M", "M0", "M0,0L", "M0,0A", "M1e308,1e308L-1e308,-1e308", "M0,0C1e308,1e308,-1e308,1e308,0,0", "M0,0A0,0,0,0,0,1,1", "M0,0A1e-300,1e-300,0,1,1,1e300,1e300", "translate(", "translate(1e308,1e308)", "scale(0)", "scale(1e308)", "rotate(1e308)", "matrix(0,0,0,0,0,0)", "matrix(1e308,1e308,1e308,1e308,1e308,1e308)", "rgb(", "rgba(1,2,3,NaN)", "#", "#12", "url(#", "url(#nope)", "url(#a", "1,2,3", "0,0", "-5", "100%", "1e9px", "px", "\x00", "\xff\xfe", "&#0;", "&amp", "<", "M0,0h1e308v1e308h-1e308z", "1e9,1e9", "0 0 0 0", "0 0 -1 -1", "0 0 1e308 1e308"}

func TestSVGMutations(t *testing.T) {
	var files []string
	for _, d := range []string{"testdata/corpus/vega", "testdata/corpus/vegalite"} {
		m, _ := filepath.Glob(d + "/*.json")
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Skip("no corpus")
	}
	sort.Strings(files)
	rng := rand.New(rand.NewSource(*secSVGSeed))
	ld := corpusLoader(t)
	c, err := aster.New(aster.WithLoader(ld), aster.WithTimeout(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var svgs []string
	for len(svgs) < 40 {
		f := files[rng.Intn(len(files))]
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s string
		if strings.Contains(f, "vegalite") {
			s, err = c.VegaLiteToSVG(b)
		} else {
			s, err = c.VegaToSVG(b)
		}
		if err == nil && len(s) < 200_000 {
			svgs = append(svgs, s)
		}
	}
	bad := 0
	for i := 0; i < *secSVGN && bad < 15; i++ {
		s := svgs[rng.Intn(len(svgs))]
		locs := attrRe.FindAllStringSubmatchIndex(s, -1)
		if len(locs) == 0 {
			continue
		}
		mutated := s
		for k := 0; k < 1+rng.Intn(3); k++ {
			locs = attrRe.FindAllStringSubmatchIndex(mutated, -1)
			l := locs[rng.Intn(len(locs))]
			val := hostileAttr[rng.Intn(len(hostileAttr))]
			if rng.Intn(4) == 0 { // duplicate / drop an element instead
				mutated = mutated[:l[2]] + mutated[l[2]:l[3]] + "-x" + mutated[l[3]:]
				continue
			}
			mutated = mutated[:l[4]] + val + mutated[l[5]:]
		}
		for _, target := range []string{"png", "pdf"} {
			done := make(chan string, 1)
			start := time.Now()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						buf := make([]byte, 2048)
						buf = buf[:runtime.Stack(buf, false)]
						done <- fmt.Sprintf("PANIC %v\n%s", r, buf)
					}
				}()
				var err error
				if target == "png" {
					_, err = c.SVGToPNG(mutated)
				} else {
					_, err = c.SVGToPDF(mutated)
				}
				if err != nil && strings.Contains(err.Error(), "internal error") {
					done <- "INTERNAL " + err.Error()
					return
				}
				done <- ""
			}()
			var msg string
			select {
			case msg = <-done:
			case <-time.After(60 * time.Second * aster.RaceSlowdownForTest):
				msg = fmt.Sprintf("HANG > %v", 60*time.Second*aster.RaceSlowdownForTest)
			}
			if msg == "" && time.Since(start) > 15*time.Second*aster.RaceSlowdownForTest {
				msg = fmt.Sprintf("SLOW %v", time.Since(start))
			}
			if msg != "" {
				bad++
				p := fmt.Sprintf("testdata/security/svg-mutant-%d-%d-%s.svg", *secSVGSeed, i, target)
				_ = os.MkdirAll("testdata/security", 0o755)
				_ = os.WriteFile(p, []byte(mutated), 0o644)
				t.Errorf("%s -> %s: %s", p, target, msg)
			}
		}
	}
}
