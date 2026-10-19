package svgpdf

import (
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/raster"
	"github.com/mgilbir/aster/internal/text"
)

// TestNothingReadIsLeftUndrawn is the package contract, "never a silent
// omission" (svgpdf.go), over what the renderer read and then did not draw
// (audit 2026-10-18, C42 and C115's class): each such input is either drawn
// as the PNG draws it (TestReadAndDrawnAsPNG) or refused with an error.
func TestNothingReadIsLeftUndrawn(t *testing.T) {
	for name, svg := range map[string]string{
		// vector-effect values SVG 2 adds, and non-scaling-stroke with a
		// gradient, which only a solid stroke followed.
		"vector-effect non-rotation":     `<path d="M0,0h10v10Z" stroke="red" vector-effect="non-rotation"/>`,
		"vector-effect non-scaling-size": `<path d="M0,0h10v10Z" stroke="red" vector-effect="non-scaling-size"/>`,
		"non-scaling-stroke gradient": `<defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>` +
			`<path d="M0,0h10v10Z" stroke="url(#g)" transform="scale(2,1)" vector-effect="non-scaling-stroke"/>`,
		// A clip path's own attributes, and its children's, were not read.
		"clipPathUnits":          `<defs><clipPath id="c" clipPathUnits="objectBoundingBox"><rect width=".5" height=".5"/></clipPath></defs><rect width="50" height="50" clip-path="url(#c)"/>`,
		"clip child clip-path":   `<defs><clipPath id="d"><rect width="5" height="5"/></clipPath><clipPath id="c"><rect width="50" height="50" clip-path="url(#d)"/></clipPath></defs><rect width="50" height="50" clip-path="url(#c)"/>`,
		"clip child unknown":     `<defs><clipPath id="c"><rect width="50" height="50" rx="4"/></clipPath></defs><rect width="50" height="50" clip-path="url(#c)"/>`,
		"clip path clip-path":    `<defs><clipPath id="d"><rect width="5" height="5"/></clipPath><clipPath id="c" clip-path="url(#d)"><rect width="50" height="50"/></clipPath></defs><rect width="50" height="50" clip-path="url(#c)"/>`,
		"clip-rule two children": `<defs><clipPath id="c"><path d="M0,0h40v40h-40Z M10,10h20v20h-20Z" clip-rule="evenodd"/><rect width="5" height="5"/></clipPath></defs><rect width="50" height="50" clip-path="url(#c)"/>`,
		"defs clip-rule":         `<defs clip-rule="evenodd"><clipPath id="c"><path d="M0,0h40v40h-40Z"/></clipPath></defs><rect width="50" height="50" clip-path="url(#c)"/>`,
		// The root's transform (SVG 2) was read past and never applied.
		"root transform": `@transform="scale(2)"`,
		// A gradient stroke on text.
		"text gradient stroke": `<defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>` +
			`<text x="5" y="20" stroke="url(#g)" font-size="12">Hi</text>`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50">` + svg + `</svg>`
			if svg[0] == '@' {
				doc = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50" ` + svg[1:] + `><rect width="10" height="10"/></svg>`
			}
			if _, err := Convert(doc, testMeasurer(t), Options{}); err == nil {
				t.Errorf("drawn without what it states, rather than refused:\n%s", doc)
			}
		})
	}
}

// TestReadAndDrawnAsPNG: what svgpdf read and left undrawn and now draws,
// drawn as the PNG draws it, read back with poppler.
func TestReadAndDrawnAsPNG(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		if os.Getenv("ASTER_POPPLER") == "require" {
			t.Fatal("pdftoppm (poppler) required (ASTER_POPPLER=require) but not installed")
		}
		t.Skip("pdftoppm (poppler) not installed")
	}
	for name, c := range map[string]struct {
		svg string
		at  []image.Point
	}{
		// The root's opacity, and its fill, inherited.
		"root opacity": {`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50" opacity="0.5" fill="#0000ff"><rect width="60" height="40"/><rect x="30" y="10" width="60" height="40"/></svg>`,
			[]image.Point{{10, 20}, {45, 25}}},
		// clip-rule evenodd of the one child of a clip path: a hole.
		"clip-rule": {`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50"><defs><clipPath id="c"><path d="M0,0h40v40h-40Z M10,10h20v20h-20Z" clip-rule="evenodd"/></clipPath></defs><rect width="50" height="50" fill="red" clip-path="url(#c)"/></svg>`,
			[]image.Point{{5, 5}, {20, 20}}},
		// A clip path child that is not displayed clips nothing in.
		"clip child display none": {`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50"><defs><clipPath id="c"><rect width="20" height="20"/><rect x="30" width="20" height="20" display="none"/></clipPath></defs><rect width="60" height="50" fill="red" clip-path="url(#c)"/></svg>`,
			[]image.Point{{5, 5}, {40, 5}}},
	} {
		t.Run(name, func(t *testing.T) {
			pdf, err := Convert(c.svg, nil, Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := popplerImage(t, pdf)
			want, err := raster.Render([]byte(c.svg), raster.Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range c.at {
				if !sameColour(got.At(p.X, p.Y), want.At(p.X, p.Y)) {
					t.Errorf("at %v: PDF %v, PNG %v", p, got.At(p.X, p.Y), want.At(p.X, p.Y))
				}
			}
		})
	}
}

// popplerImage is pdf drawn by pdftoppm at one pixel a point.
func popplerImage(t *testing.T, pdf []byte) image.Image {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "s.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("pdftoppm", "-r", "72", "-png", "-singlefile", in, filepath.Join(dir, "s")).CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v\n%s", err, out)
	}
	f, err := os.Open(filepath.Join(dir, "s.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// testMeasurer is a Measurer of the embedded fonts.
func testMeasurer(t *testing.T) *text.Measurer {
	t.Helper()
	m, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestCSSNamesFoldASCIIOnly: a style property's name, a unit and a link's
// scheme are ASCII case-insensitive, as CSS and URLs say: U+0130 is no i.
func TestCSSNamesFoldASCIIOnly(t *testing.T) {
	if v, ok := parseLength("1IN", lengthCtx{16, 100}); !ok || v != 96 {
		t.Errorf("1IN = %v %v, want 96", v, ok)
	}
	if _, ok := parseLength("1İN", lengthCtx{16, 100}); ok {
		t.Error("1İN read as inches")
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="5" height="5" style="FILL: red"/></svg>`
	if _, err := Convert(svg, nil, Options{}); err != nil {
		t.Errorf("FILL: %v", err)
	}
	svg = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="5" height="5" style="fİll: red"/></svg>`
	if _, err := Convert(svg, nil, Options{}); err == nil {
		t.Error("fİll read as fill")
	}
	if linkURI("MAILTO:a@b.c") == "" || linkURI("maİlto:a@b.c") != "" {
		t.Error("link schemes fold beyond ASCII, or not ASCII")
	}
}
