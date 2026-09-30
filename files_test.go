package aster_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// Debugging aids, each skipped unless its variable lists spec files
// (comma-separated paths; *.vl.json is Vega-Lite).

// TestCompareFiles compares the engine with the node oracle on the specs in
// ASTER_COMPARE and prints the first difference in full (the whole engine
// error with ASTER_FULL set). ASTER_COMPARE_DUMP=dir writes both SVGs there.
func TestCompareFiles(t *testing.T) {
	list := os.Getenv("ASTER_COMPARE")
	if list == "" {
		t.Skip("set ASTER_COMPARE")
	}
	o := oracle.For(t, oracle.VL6)
	c := oracleConverter(t)
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lite := strings.HasSuffix(f, ".vl.json")
		var got string
		var gerr error
		if lite {
			got, gerr = c.VegaLiteToSVG(spec)
		} else {
			got, gerr = c.VegaToSVG(spec)
		}
		want, err := o.SVG(lite, spec)
		if err != nil {
			t.Fatalf("oracle: %v", err)
		}
		if dir := os.Getenv("ASTER_COMPARE_DUMP"); dir != "" {
			base := filepath.Join(dir, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
			_ = os.WriteFile(base+".got.svg", []byte(got), 0o644)
			_ = os.WriteFile(base+".want.svg", []byte(want.SVG), 0o644)
		}
		msg := func(err error) string {
			if os.Getenv("ASTER_FULL") != "" {
				return err.Error()
			}
			return firstLine(err.Error())
		}
		switch {
		case gerr != nil && want.Err != "":
			t.Logf("%s: both error\n engine: %s\n node: %s", f, msg(gerr), firstLine(want.Err))
		case gerr != nil:
			t.Logf("%s: only the engine errors: %s", f, msg(gerr))
		case want.Err != "":
			t.Logf("%s: only node errors: %s", f, firstLine(want.Err))
		default:
			d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
			switch {
			case err != nil:
				t.Logf("%s: compare: %v", f, err)
			case d.Identical || d.Equal:
				t.Logf("%s: matches node", f)
			default:
				diff := d.Diff
				if len(diff) > 1500 {
					diff = diff[:1500]
				}
				t.Logf("%s: DIFFERS from node (%d): %s", f, d.DiffCount, diff)
			}
		}
	}
}

// TestTimeFiles renders the specs in ASTER_TIME and logs the duration and
// outcome of each.
func TestTimeFiles(t *testing.T) {
	list := os.Getenv("ASTER_TIME")
	if list == "" {
		t.Skip("set ASTER_TIME")
	}
	c, err := aster.New(aster.WithLoader(corpusLoader(t)), aster.WithTimeout(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		var rerr error
		if strings.HasSuffix(f, ".vl.json") {
			_, rerr = c.VegaLiteToSVG(spec)
		} else {
			_, rerr = c.VegaToSVG(spec)
		}
		msg := "ok"
		if rerr != nil {
			msg = firstLine(rerr.Error())
		}
		t.Logf("%s: %v %s", filepath.Base(f), time.Since(t0).Round(time.Millisecond), msg)
	}
}

// TestCompileFile prints the Vega each Vega-Lite spec in ASTER_VEGA compiles
// to.
func TestCompileFile(t *testing.T) {
	list := os.Getenv("ASTER_VEGA")
	if list == "" {
		t.Skip("set ASTER_VEGA")
	}
	c, err := aster.New(aster.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out, err := c.VegaLiteToVega(spec)
		t.Logf("%s: err=%v\n%s", filepath.Base(f), err, out)
	}
}
