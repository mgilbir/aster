package purego_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/fonts/dejavu"
	"github.com/mgilbir/aster/purego"
	"github.com/mgilbir/aster/purego/internal/svgdiff"
)

// TestCompareFiles renders the specs listed in PUREGO_COMPARE (comma-separated
// paths; *.vl.json is Vega-Lite) with both engines and prints the first
// difference in full. It is a debugging aid and skips when the variable is
// unset.
func TestCompareFiles(t *testing.T) {
	list := os.Getenv("PUREGO_COMPARE")
	if list == "" {
		t.Skip("set PUREGO_COMPARE")
	}
	ld := corpusLoader(t)
	pg, err := purego.New(purego.WithLoader(ld), purego.WithTimeout(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	ref, err := aster.New(aster.WithLoader(ld), aster.WithTimeout(120*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lite := strings.HasSuffix(f, ".vl.json")
		render := func(vl func([]byte) (string, error), vg func([]byte) (string, error)) (string, error) {
			if lite {
				return vl(spec)
			}
			return vg(spec)
		}
		got, gerr := render(pg.VegaLiteToSVG, pg.VegaToSVG)
		want, werr := render(ref.VegaLiteToSVG, ref.VegaToSVG)
		switch {
		case gerr != nil && werr != nil:
			msg := firstLine(gerr.Error())
			if os.Getenv("PUREGO_FULL") != "" {
				msg = gerr.Error()
			}
			t.Logf("%s: both error\n purego: %s\n ref: %s", f, msg, firstLine(werr.Error()))
		case gerr != nil:
			msg := firstLine(gerr.Error())
			if os.Getenv("PUREGO_FULL") != "" {
				msg = gerr.Error()
			}
			t.Logf("%s: only purego errors: %s", f, msg)
		case werr != nil:
			t.Logf("%s: only reference errors: %s", f, firstLine(werr.Error()))
		default:
			d, err := svgdiff.Compare([]byte(got), []byte(want), svgdiff.DefaultOptions)
			switch {
			case err != nil:
				t.Logf("%s: compare: %v", f, err)
			case d.Identical || d.Equal:
				t.Logf("%s: equal", f)
			default:
				diff := d.Diff
				if len(diff) > 1500 {
					diff = diff[:1500]
				}
				t.Logf("%s: DIFFER (%d): %s", f, d.DiffCount, diff)
			}
		}
	}
}

// TestCompareNode renders the specs in PUREGO_COMPARE with purego (DejaVu
// text, as nodesvg.mjs measures) and compares them within half a pixel with
// the SVGs nodesvg.mjs wrote to PUREGO_NODEDIR. Skips when unset.
func TestCompareNode(t *testing.T) {
	list, dir := os.Getenv("PUREGO_COMPARE"), os.Getenv("PUREGO_NODEDIR")
	if list == "" || dir == "" {
		t.Skip("set PUREGO_COMPARE and PUREGO_NODEDIR")
	}
	c, err := purego.New(
		purego.WithFont("DejaVu Sans", dejavu.SansRegular), purego.WithFont("DejaVu Sans", dejavu.SansBold),
		purego.WithFont("DejaVu Sans", dejavu.SansOblique), purego.WithFont("DejaVu Sans", dejavu.SansBoldOblique),
		purego.WithFont("DejaVu Sans Mono", dejavu.MonoRegular), purego.WithFont("DejaVu Sans Mono", dejavu.MonoBold),
		purego.WithFont("DejaVu Sans Mono", dejavu.MonoOblique), purego.WithFont("DejaVu Sans Mono", dejavu.MonoBoldOblique),
		purego.WithDefaultFontFamily("DejaVu Sans"), purego.WithDefaultMonospaceFamily("DejaVu Sans Mono"),
		purego.WithLoader(corpusLoader(t)), purego.WithTimeout(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var refSVG func([]byte, bool) (string, error)
	if os.Getenv("PUREGO_REF") != "" {
		ref, err := aster.New(aster.WithLoader(corpusLoader(t)), aster.WithTimeout(60*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		defer ref.Close()
		refSVG = func(spec []byte, lite bool) (string, error) {
			if lite {
				return ref.VegaLiteToSVG(spec)
			}
			return ref.VegaToSVG(spec)
		}
	}
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		name = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".vl"), ".vg")
		want, werr := os.ReadFile(filepath.Join(dir, name+".svg"))
		var got string
		var gerr error
		if strings.HasSuffix(f, ".vl.json") {
			got, gerr = c.VegaLiteToSVG(spec)
		} else {
			got, gerr = c.VegaToSVG(spec)
		}
		switch {
		case gerr != nil && werr != nil:
			t.Logf("%s: node errored too (%s); purego: %s", name, firstLine(string(mustRead(filepath.Join(dir, name+".err")))), firstLine(gerr.Error()))
		case gerr != nil:
			t.Logf("%s: only purego errors: %s", name, firstLine(gerr.Error()))
		case werr != nil:
			t.Logf("%s: node errored: %s", name, firstLine(string(mustRead(filepath.Join(dir, name+".err")))))
		default:
			d, err := svgdiff.Compare([]byte(got), want, svgdiff.Options{Abs: 0.5, Rel: 1e-6})
			switch {
			case err != nil:
				t.Logf("%s: compare: %v", name, err)
			case d.Identical || d.Equal:
				t.Logf("%s: matches node", name)
			default:
				diff := d.Diff
				if len(diff) > 1500 {
					diff = diff[:1500]
				}
				t.Logf("%s: DIFFERS from node (%d): %s", name, d.DiffCount, diff)
				if refSVG != nil {
					rs, rerr := refSVG(spec, strings.HasSuffix(f, ".vl.json"))
					if rerr != nil {
						t.Logf("%s:   reference errored: %s", name, firstLine(rerr.Error()))
					} else if rd, err := svgdiff.Compare([]byte(rs), want, svgdiff.Options{Abs: 0.5, Rel: 1e-6}); err == nil && rd.Equal {
						t.Logf("%s:   the reference matches node (purego is wrong)", name)
					} else {
						t.Logf("%s:   the reference differs from node too", name)
					}
				}
			}
		}
	}
}

func mustRead(p string) []byte {
	b, _ := os.ReadFile(p)
	return b
}

// TestTimeFiles renders the specs in PUREGO_TIME (comma-separated paths) with
// purego alone and logs the duration and outcome of each. Debugging aid.
func TestTimeFiles(t *testing.T) {
	list := os.Getenv("PUREGO_TIME")
	if list == "" {
		t.Skip("set PUREGO_TIME")
	}
	pg, err := purego.New(purego.WithLoader(corpusLoader(t)), purego.WithTimeout(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		var rerr error
		if strings.HasSuffix(f, ".vl.json") {
			_, rerr = pg.VegaLiteToSVG(spec)
		} else {
			_, rerr = pg.VegaToSVG(spec)
		}
		msg := "ok"
		if rerr != nil {
			msg = firstLine(rerr.Error())
		}
		t.Logf("%s: %v %s", filepath.Base(f), time.Since(t0).Round(time.Millisecond), msg)
	}
}

// TestCompileFile prints the Vega that purego compiles each Vega-Lite spec in
// PUREGO_VEGA to (comma-separated paths). Debugging aid.
func TestCompileFile(t *testing.T) {
	list := os.Getenv("PUREGO_VEGA")
	if list == "" {
		t.Skip("set PUREGO_VEGA")
	}
	pg, err := purego.New(purego.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pg.VegaLiteToVega(spec)
		t.Logf("%s: err=%v\n%s", filepath.Base(f), err, out)
	}
}
