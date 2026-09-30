package wordcloud

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

// Canvas geometry of the shared sprite sheet. Upstream draws every word of a
// batch onto one 2048x2048 canvas and reads the pixels back, so the sheet size
// decides how many words fit in a batch and where sprites are cut.
const (
	sheetW = 1 << 11
	sheetH = 1 << 11
)

// Font is the CSS font a word is measured and drawn with. Px is the integer
// pixel size upstream passes to the canvas (its `size + 1`, truncated).
type Font struct {
	Style, Weight, Family string
	Px                    int
}

// Mask is the sheet a TextRenderer paints on. Only "any ink here?" matters to
// the layout, so it is a bitset rather than an RGBA buffer.
type Mask struct {
	bits []uint64
}

func newMask() *Mask { return &Mask{bits: make([]uint64, sheetW*sheetH/64)} }

func (m *Mask) clear() { clear(m.bits) }

// Width and Height are the sheet dimensions in pixels.
func (m *Mask) Width() int  { return sheetW }
func (m *Mask) Height() int { return sheetH }

// Set marks the pixel (x, y) as inked. Coordinates outside the sheet are
// clipped, as canvas drawing is.
func (m *Mask) Set(x, y int) {
	if x < 0 || y < 0 || x >= sheetW || y >= sheetH {
		return
	}
	i := y*sheetW + x
	m.bits[i>>6] |= 1 << (uint(i) & 63)
}

// get reads by linear index. Upstream indexes the flat pixel array directly,
// so a sprite wider than a row reads into the next row, and indexes past the
// end read as "no ink".
func (m *Mask) get(i int) bool {
	if i < 0 || i >= sheetW*sheetH {
		return false
	}
	return m.bits[i>>6]&(1<<(uint(i)&63)) != 0
}

// TextRenderer measures and rasterises text the way the canvas 2D context
// upstream uses does. Implementations must be deterministic for a layout to be
// reproducible.
type TextRenderer interface {
	// Measure returns the advance width of text in pixels (measureText().width).
	Measure(f Font, text string) float64
	// Draw paints text centred horizontally on the point (tx, ty) after
	// rotating by angle radians about that point, with the alphabetic
	// baseline at ty. When strokeWidth > 0 the glyph outline is also stroked
	// with that line width, which is how padding is realised.
	Draw(m *Mask, f Font, text string, tx, ty, angle, strokeWidth float64)
}

// BoxRenderer is a font-free TextRenderer: every rune advances 0.6 em and a
// word is a solid box spanning 0.8 em above and 0.2 em below the baseline.
// It is used when no renderer is supplied and by the tests, whose golden
// vectors run upstream against an identical canvas shim.
type BoxRenderer struct{}

// Measure implements TextRenderer.
func (BoxRenderer) Measure(f Font, text string) float64 {
	n := 0
	for range text {
		n++
	}
	return float64(n) * 0.6 * float64(f.Px)
}

// Draw implements TextRenderer.
func (BoxRenderer) Draw(m *Mask, f Font, text string, tx, ty, angle, strokeWidth float64) {
	n := 0
	for range text {
		n++
	}
	px := float64(f.Px)
	half := float64(n) * 0.6 * px / 2
	top, bottom := float64(-0.8*px), float64(0.2*px)
	grow := strokeWidth / 2
	if grow < 0 {
		grow = 0
	}
	x0, x1 := -half-grow, half+grow
	y0, y1 := top-grow, bottom+grow
	s, c := jsmath.Sin(angle), jsmath.Cos(angle)
	// Bounding box of the rotated rectangle limits the scan.
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range [4][2]float64{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
		gx := float64(tx + float64(c*p[0]) - float64(s*p[1]))
		gy := float64(ty + float64(s*p[0]) + float64(c*p[1]))
		minX, maxX = math.Min(minX, gx), math.Max(maxX, gx)
		minY, maxY = math.Min(minY, gy), math.Max(maxY, gy)
	}
	ix0, ix1 := int(math.Max(math.Floor(minX)-1, 0)), int(math.Min(math.Ceil(maxX)+1, sheetW-1))
	iy0, iy1 := int(math.Max(math.Floor(minY)-1, 0)), int(math.Min(math.Ceil(maxY)+1, sheetH-1))
	for y := iy0; y <= iy1; y++ {
		for x := ix0; x <= ix1; x++ {
			dx, dy := float64(x)+0.5-tx, float64(y)+0.5-ty
			// Inverse rotation into glyph space.
			lx := float64(c*dx) + float64(s*dy)
			ly := float64(c*dy) - float64(s*dx)
			if lx >= x0 && lx < x1 && ly >= y0 && ly < y1 {
				m.Set(x, y)
			}
		}
	}
}
