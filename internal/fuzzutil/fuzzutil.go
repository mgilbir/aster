// Package fuzzutil holds what the coverage-guided fuzz targets (go test -fuzz)
// share: seeds read from the checked-in test data, and a per-input time bound.
// The nightly workflow (.github/workflows/fuzz.yml) runs the targets listed in
// scripts/fuzz-targets.txt.
package fuzzutil

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mgilbir/forme/shape"
)

// MaxSeedBytes is the largest file Files returns: a big seed makes every
// mutation of it slow, and the fuzzer finds the depth by itself.
const MaxSeedBytes = 32 << 10

// Files returns the contents of the files matching the glob patterns
// (relative to the calling package's directory), skipping files over
// MaxSeedBytes, for the caller to f.Add. A pattern that matches nothing fails
// the target, so a moved corpus is noticed.
func Files(tb testing.TB, patterns ...string) []string {
	tb.Helper()
	var out []string
	for _, pat := range patterns {
		files, err := filepath.Glob(pat)
		if err != nil || len(files) == 0 {
			tb.Fatalf("no seed files match %q (%v)", pat, err)
		}
		for _, file := range files {
			if fi, err := os.Stat(file); err != nil || fi.Size() > MaxSeedBytes {
				continue
			}
			b, err := os.ReadFile(file)
			if err != nil {
				tb.Fatal(err)
			}
			out = append(out, string(b))
		}
	}
	return out
}

// Within runs fn and fails the test if it takes longer than d: a fuzzer
// reports a hang as nothing at all, so an input that makes fn run away has to
// become a failure here. what names the input in the message.
func Within(t testing.TB, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s: did not finish in %v", what, d)
	}
}

// PlayPaint plays a byte string through p as a glyph's painting, for the
// fuzz targets of the colour glyph painters: each op a byte, its
// arguments the bytes after it, floats as float32 bits (NaN and infinities
// included), and pushes and pops in any order, which forme never makes but a
// painter must survive.
func PlayPaint(ops []byte, p shape.Painter) { (&paintOps{ops}).play(p) }

type paintOps struct{ b []byte }

func (o *paintOps) u8() uint8 {
	if len(o.b) == 0 {
		return 0
	}
	v := o.b[0]
	o.b = o.b[1:]
	return v
}

func (o *paintOps) f() float64 {
	if len(o.b) < 4 {
		o.b = nil
		return 0
	}
	v := math.Float32frombits(binary.LittleEndian.Uint32(o.b))
	o.b = o.b[4:]
	return float64(v)
}

func (o *paintOps) color() shape.Color {
	return shape.Color{R: o.u8(), G: o.u8(), B: o.u8(), A: o.u8()}
}

func (o *paintOps) line() shape.ColorLine {
	l := shape.ColorLine{Extend: shape.Extend(o.u8() % 4)}
	for range o.u8() % 6 {
		l.Stops = append(l.Stops, shape.ColorStop{Offset: o.f(), Color: o.color(), Foreground: o.u8()&1 != 0})
	}
	return l
}

func (o *paintOps) point() shape.Point { return shape.Point{X: o.f(), Y: o.f()} }

func (o *paintOps) rect() shape.Rect {
	return shape.Rect{XMin: o.f(), YMin: o.f(), XMax: o.f(), YMax: o.f()}
}

// a 1x1 PNG, for the images the ops paint
var onePixelPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\xa5\x9b\xcd\x1f\x00\x00\x00\x00IEND\xaeB`\x82")

func (o *paintOps) play(p shape.Painter) {
	for n := 0; len(o.b) > 0 && n < 512; n++ {
		switch o.u8() % 14 {
		case 0:
			p.PushTransform(shape.Transform{XX: o.f(), YX: o.f(), XY: o.f(), YY: o.f(), X0: o.f(), Y0: o.f()})
		case 1:
			p.PopTransform()
		case 2:
			p.PushClipGlyph(int(o.u8()))
		case 3:
			p.PushClipRect(o.rect())
		case 4:
			p.PopClip()
		case 5:
			p.PushGroup()
		case 6:
			p.PopGroup(shape.CompositeMode(o.u8() % 32))
		case 7:
			p.Solid(o.color(), o.u8()&1 != 0)
		case 8:
			p.LinearGradient(shape.LinearGradient{Line: o.line(), P0: o.point(), P1: o.point(), P2: o.point()})
		case 9:
			p.RadialGradient(shape.RadialGradient{Line: o.line(), C0: o.point(), R0: o.f(), C1: o.point(), R1: o.f()})
		case 10:
			p.SweepGradient(shape.SweepGradient{Line: o.line(), Center: o.point(), StartAngle: o.f(), EndAngle: o.f()})
		case 11:
			p.Image(shape.Image{Format: shape.ImagePNG, Data: onePixelPNG, Width: 1, Height: 1, Box: o.rect()})
		case 12:
			w, h := int(o.u8()%16), int(o.u8()%16)
			data := make([]byte, w*h)
			copy(data, o.b)
			p.Image(shape.Image{Format: shape.ImageMask, Data: data, Width: w, Height: h, Box: o.rect(), Color: o.color()})
		case 13:
			n, format, w, h, box := int(o.u8()), shape.ImageFormat(o.u8()%4), int(o.u8()), int(o.u8()), o.rect()
			p.Image(shape.Image{Format: format, Data: o.b[:min(n, len(o.b))], Width: w, Height: h, Box: box})
		}
	}
}

// PaintSeeds are op strings for PlayPaint: a SrcIn composite of a solid and
// a gradient, and two of what forme never paints.
func PaintSeeds() [][]byte {
	le := func(vs ...float32) []byte {
		var b []byte
		for _, v := range vs {
			b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
		}
		return b
	}
	var b []byte
	// A composite: backdrop, source, SrcIn.
	b = append(b, 5, 3)
	b = append(b, le(0, 0, 800, 800)...)
	b = append(b, 7, 255, 0, 0, 255, 0, 4, 5, 2, 1, 8, 2)
	b = append(b, 2, 0, 0, 255)
	b = append(b, le(0, 0.5, 1, 1, 0)...)
	b = append(b, le(100, 0, 900, 0, 100, 800)...)
	b = append(b, 4, 6, 5, 6, 3)
	return [][]byte{b,
		{10, 1, 2, 9, 0xff, 0xff, 0xc0, 0x7f, 6, 6, 6, 1, 1, 4},
		{0, 0, 0, 0x80, 0x7f, 11, 12, 3, 3, 1}}
}
