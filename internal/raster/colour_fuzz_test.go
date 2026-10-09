package raster

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/mgilbir/forme/shape"

	"github.com/mgilbir/aster/internal/fuzzutil"
)

// FuzzColourPainter plays op strings through the PNG writer's colour glyph
// painter and the bounds pass: neither may panic, nor fail the render.
func FuzzColourPainter(f *testing.F) {
	for _, s := range fuzzutil.PaintSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		fuzzutil.Within(t, 10*time.Second, "painting", func() {
			img, err := Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><text x="4" y="50" font-size="48" fill="#08f" fill-opacity="0.7">x</text></svg>`),
				Options{Shaper: fakeShaper{&fakeColourFace{paint: func(p shape.Painter) error {
					fuzzutil.PlayPaint(ops, p)
					return nil
				}}}})
			if err != nil || img == nil {
				t.Errorf("render failed: %v", err)
			}
			fuzzutil.PlayPaint(ops, &colourBounds{face: &fakeColourFace{}})
		})
	})
}

// FuzzColourFont renders every glyph of a mutated colour font.
func FuzzColourFont(f *testing.F) {
	for _, name := range []string{"ColourTest.ttf", "SbixTest.ttf", "SvgTest.ttf"} {
		data, err := os.ReadFile("../../testdata/colourfonts/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// A font is bounded work: one that is not is a failure to record.
		fuzzutil.Within(t, 10*time.Second, "drawing a colour font", func() {
			sh, err := NewShaper(FontData{Family: "F", Data: data})
			if err != nil {
				return
			}
			_, _ = Render([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><text x="0" y="30" font-family="F" font-size="30" fill-opacity="0.5">&#x1F534;&#x1F7E2;&#x1F308;&#x1F31E;&#x1F300;&#x1F600;&#x1F3A8;&#x1F4A0;&#x1F4A1;&#x1F52E;&#x1F4A7;A</text></svg>`),
				Options{Shaper: sh, Limits: fuzzLimits})
		})
	})
}

// leafClips wraps a painter and notes whether every fill of a painting is
// inside a clip, only then bounded by its clips, and how far out the
// painting reaches: a coordinate the rasterizer clamps (coordLimit) or one
// past 1e9 pixels, which doubles cannot place near the canvas, is drawn
// only as exactly as that, and the bounds pass, which clamps nothing, may
// disagree.
type leafClips struct {
	shape.Painter
	clips     int
	unclipped bool
	scale     []float64 // the transforms' largest coefficients
	far       bool
}

// reach notes coordinates of the current space.
func (l *leafClips) reach(vs ...float64) {
	k := 0.064 // the test's pixels a font unit
	for _, s := range l.scale {
		k *= s
	}
	for _, v := range vs {
		if !(math.Abs(v) < coordLimit) || !(math.Abs(v)*k < 1e9) {
			l.far = true
		}
	}
}

func (l *leafClips) PushTransform(t shape.Transform) {
	l.scale = append(l.scale, max(1, math.Abs(t.XX), math.Abs(t.YX), math.Abs(t.XY), math.Abs(t.YY)))
	l.reach(t.X0, t.Y0)
	l.Painter.PushTransform(t)
}

func (l *leafClips) PopTransform() {
	if len(l.scale) > 0 {
		l.scale = l.scale[:len(l.scale)-1]
	}
	l.Painter.PopTransform()
}

func (l *leafClips) PushClipGlyph(gid int) {
	l.clips++
	l.reach(1000)
	l.Painter.PushClipGlyph(gid)
}

func (l *leafClips) PushClipRect(r shape.Rect) {
	l.clips++
	l.reach(r.XMin, r.YMin, r.XMax, r.YMax)
	l.Painter.PushClipRect(r)
}

func (l *leafClips) PopClip()                    { l.clips--; l.Painter.PopClip() }
func (l *leafClips) leaf()                       { l.unclipped = l.unclipped || l.clips <= 0 }
func (l *leafClips) Solid(c shape.Color, f bool) { l.leaf(); l.Painter.Solid(c, f) }
func (l *leafClips) LinearGradient(g shape.LinearGradient) {
	l.leaf()
	l.Painter.LinearGradient(g)
}
func (l *leafClips) RadialGradient(g shape.RadialGradient) {
	l.leaf()
	l.Painter.RadialGradient(g)
}
func (l *leafClips) SweepGradient(g shape.SweepGradient) {
	l.leaf()
	l.Painter.SweepGradient(g)
}
func (l *leafClips) Image(img shape.Image) {
	l.leaf()
	l.reach(img.Box.XMin, img.Box.YMin, img.Box.XMax, img.Box.YMax)
	l.Painter.Image(img)
}

// FuzzColourPaintTree paints trees of the shape forme paints COLR glyphs
// in, every push matched by its pop, through the PNG writer's painter, and
// checks what it drew against the bounds pass: a glyph whose fills are all
// clipped paints nothing outside the box the bounds pass finds, which is
// what a PDF's image of a glyph is cropped to.
func FuzzColourPaintTree(f *testing.F) {
	for _, s := range fuzzutil.TreeSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		fuzzutil.Within(t, 10*time.Second, "painting", func() {
			face := &fakeColourFace{}
			b := &colourBounds{face: face}
			check := &leafClips{Painter: b}
			fuzzutil.PlayTree(ops, check)

			const side = 128
			lim := Limits{}.withDefaults()
			lim.MaxPixelOps = defaultMaxPixelOps
			r := &renderer{lim: lim, rast: newRasterizer(), cw: side, ch: side, active: map[*node]bool{}}
			r.cv = r.allocCanvas(side, side)
			face.paint = func(p shape.Painter) error {
				fuzzutil.PlayTree(ops, p)
				return nil
			}
			st := initialState(0, 0)
			// Font units, y up, to pixels: a 1000-unit em is 64 pixels,
			// its origin at (32, 96).
			m := matrix{0.064, 0, 0, -0.064, 32, 96}
			r.paintColour(face, 1, m, &st, shape.Color{A: 255}, 64, 1)
			if r.err != nil {
				t.Fatalf("painting failed the render: %v", r.err)
			}
			if check.unclipped || check.far || !b.ok {
				return
			}
			box := transformRect(b.r, m)
			if math.IsNaN(box.x0+box.y0+box.x1+box.y1) || math.IsInf(box.x0+box.y0+box.x1+box.y1, 0) {
				return
			}
			for y := range side {
				for x := range side {
					fx, fy := float64(x), float64(y)
					if r.cv.pix[(y*side+x)*4+3] != 0 && (fx+1 < box.x0-1 || fx > box.x1+1 || fy+1 < box.y0-1 || fy > box.y1+1) {
						t.Fatalf("painted at (%d, %d), outside the bounds %v", x, y, box)
					}
				}
			}
		})
	})
}
