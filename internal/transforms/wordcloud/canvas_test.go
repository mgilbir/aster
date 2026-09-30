package wordcloud

import (
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/fonts/dejavu"
	"github.com/mgilbir/aster/internal/text"
)

func dejavuShaper(t testing.TB) *text.Measurer {
	t.Helper()
	m, err := text.New(
		text.WithFont("DejaVu Sans", dejavu.SansRegular), text.WithFont("DejaVu Sans", dejavu.SansBold),
		text.WithFont("DejaVu Sans", dejavu.SansOblique), text.WithFont("DejaVu Sans", dejavu.SansBoldOblique),
		text.WithDefaultFontFamily("DejaVu Sans"), text.WithExactAdvances(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// rows renders the inked pixels of the sheet as text: one line per row that
// has ink, left-trimmed to the leftmost inked column.
func rows(m *Mask) (first, left int, lines []string) {
	left = sheetW
	var all []string
	for y := 0; y < 512; y++ {
		var sb strings.Builder
		for x := 0; x < 512; x++ {
			if m.get(y*sheetW + x) {
				sb.WriteByte('#')
				left = min(left, x)
			} else {
				sb.WriteByte('.')
			}
		}
		all = append(all, sb.String())
	}
	first = -1
	for y, r := range all {
		if !strings.Contains(r, "#") {
			continue
		}
		if first < 0 {
			first = y
		}
		lines = append(lines, strings.TrimRight(r[left:], "."))
	}
	return first, left, lines
}

// TestCanvasRendererMatchesCanvas compares a word's mask with the pixels
// node-canvas (Pango and Cairo) gives `fillText("Hello gy")` in a 17px weight
// 300 DejaVu Sans, centred on (128, 128): the rows below are its pixels with
// non-zero alpha.
func TestCanvasRendererMatchesCanvas(t *testing.T) {
	want := []string{
		"###.....###............###..###",
		"###.....###............###..###",
		"###.....###............###..###",
		"###.....###....#####...###..###...#####...........#######.###.....##",
		"###.....###..########..###..###..########........########.###....###",
		"###########..###..###..###..###..###..###.......####.####..###...###",
		"###########.###....###.###..###.###...###.......###...###..###..###",
		"###########.##########.###..###.###....###......###....##...###.###",
		"###.....###.##########.###..###.###....###......###....##...###.###",
		"###.....###.###........###..###.###....###......###...###....#####",
		"###.....###.####.......###..###.###...###.......###...###....#####",
		"###.....###..########..###..###..########........########....####",
		"###.....###...#######..###..###..#######.........########.....###",
		"................####...............####...............###.....###",
		".................................................##..####....###",
		".................................................#######...#####",
		".................................................######....####",
	}
	r := NewCanvasRenderer(dejavuShaper(t))
	f := Font{Style: "normal", Weight: "300", Family: "DejaVu Sans", Px: 17}
	mask := newMask()
	r.Draw(mask, f, "Hello gy", 128, 128, 0, 0)
	first, left, got := rows(mask)
	if first != 115 || left != 94 {
		t.Errorf("ink starts at row %d column %d, want row 115 column 94", first, left)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("mask differs from canvas:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCanvasRendererStrokeAndRotation(t *testing.T) {
	r := NewCanvasRenderer(dejavuShaper(t))
	f := Font{Style: "normal", Weight: "600", Family: "DejaVu Sans", Px: 20}
	plain, padded := newMask(), newMask()
	r.Draw(plain, f, "DATA", 128, 128, 45*math.Pi/180, 0)
	r.Draw(padded, f, "DATA", 128, 128, 45*math.Pi/180, 4)
	count := func(m *Mask) (n int) {
		for y := 0; y < 512; y++ {
			for x := 0; x < 512; x++ {
				if m.get(y*sheetW + x) {
					n++
				}
			}
		}
		return n
	}
	if p, q := count(plain), count(padded); p == 0 || q <= p {
		t.Errorf("stroking must grow the ink: %d pixels plain, %d padded", p, q)
	}
}

// A font the canvas rejects leaves its default, 10px sans-serif.
func TestCanvasFontFallback(t *testing.T) {
	def := Font{Style: "normal", Weight: "normal", Family: "sans-serif", Px: 10}
	for _, f := range []Font{
		{Style: "normal", Weight: "normal", Family: "x", Px: 0},
		{Style: "normal", Weight: "normal", Family: "x", Px: -3},
		{Style: "bogus", Weight: "normal", Family: "x", Px: 12},
		{Style: "normal", Weight: "heavy", Family: "x", Px: 12},
		{Style: "normal", Weight: "normal", Family: " ", Px: 12},
	} {
		if got := canvasFont(f); got != def {
			t.Errorf("canvasFont(%+v) = %+v, want the default", f, got)
		}
	}
	ok := Font{Style: "italic", Weight: "600", Family: "x", Px: 12}
	if canvasFont(ok) != ok {
		t.Errorf("canvasFont rejected a valid font %+v", ok)
	}
}

// Words far off the sheet cost nothing, and an absurd word fails instead of
// consuming the machine.
func TestCanvasRendererBounds(t *testing.T) {
	r := NewCanvasRenderer(dejavuShaper(t))
	f := Font{Style: "normal", Weight: "normal", Family: "DejaVu Sans", Px: 20}
	mask := newMask()
	r.Draw(mask, f, strings.Repeat("w", 20000), 128, 128, 0, 4)
	r.Draw(mask, f, "x", math.Inf(1), math.NaN(), math.Inf(1), 4)
}
