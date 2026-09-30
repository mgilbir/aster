// Package wordcloud implements vega-wordcloud: Jason Davies' d3-cloud layout,
// which packs words into a bitmap board using per-word sprites and a spiral
// search.
//
// Upstream measures and draws text with a canvas and consumes Math.random.
// Both are injected here (TextRenderer and Params.Random; CanvasRenderer is the
// one that matches node-canvas), so a layout is
// reproducible for a given renderer and random sequence.
package wordcloud

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// Accessor reads a value from a tuple.
type Accessor func(jsval.Value) jsval.Value

// Const returns an accessor that ignores its tuple.
func Const(v jsval.Value) Accessor { return func(jsval.Value) jsval.Value { return v } }

// DefaultFontSizeRange is the transform's fontSizeRange default.
var DefaultFontSizeRange = []float64{10, 50}

// Params mirrors the Wordcloud transform's parameters. Nil accessors take the
// upstream defaults. Upstream applies `value || default` to constant
// parameters, so a caller holding a constant padding of 0 must pass 1.
type Params struct {
	Size       *[2]float64 // nil means 500x500; both must be non-zero
	Text       Accessor
	Font       Accessor // default "sans-serif"
	FontStyle  Accessor // default "normal"
	FontWeight Accessor // default "normal"
	FontSize   Accessor // default constant 14
	// FontSizeIsField reports that FontSize varies per tuple (a field or
	// expression upstream); only then is FontSizeRange applied, through a
	// sqrt scale over the data extent. A nil FontSizeRange disables scaling.
	FontSizeIsField bool
	FontSizeRange   []float64
	Rotate          Accessor // degrees, default 0
	Padding         Accessor // default 1
	Spiral          Spiral   // default Archimedean
	As              []string // seven output names; default DefaultOutput

	// Random supplies uniform values in [0, 1) (upstream: Math.random). Nil
	// uses a fixed-seed LCG so results stay reproducible.
	Random   func() float64
	Renderer TextRenderer // nil means BoxRenderer
}

// DefaultOutput lists the output fields: x, y, font, fontSize, fontStyle,
// fontWeight, angle.
var DefaultOutput = []string{"x", "y", "font", "fontSize", "fontStyle", "fontWeight", "angle"}

// LCG returns a deterministic uniform generator, the fallback for Params.Random.
func LCG(seed uint32) func() float64 {
	s := seed
	return func() float64 {
		s = s*1664525 + 1013904223
		return float64(s) / 4294967296
	}
}

// Transform lays out the words of data, writing x, y, font, fontSize,
// fontStyle, fontWeight and angle onto each tuple. Tuples that do not fit keep
// x = y = NaN and fontSize 0. It honours ctx cancellation.
func Transform(ctx context.Context, data []jsval.Value, p Params) error {
	size := [2]float64{500, 500}
	if p.Size != nil {
		size = *p.Size
		if !(size[0] != 0 && size[1] != 0) {
			return errors.New("Wordcloud size dimensions must be non-zero.")
		}
	}
	if !(size[0] > 0 && size[1] > 0) || math.IsInf(size[0], 0) || math.IsInf(size[1], 0) ||
		size[0]*size[1] > maxArea {
		return fmt.Errorf("wordcloud: unsupported size %v x %v", size[0], size[1])
	}
	if len(data) > MaxWords {
		return fmt.Errorf("wordcloud: %d words exceeds the limit of %d", len(data), MaxWords)
	}
	as := p.As
	if len(as) != 7 {
		as = DefaultOutput
	}
	for _, t := range data {
		if !t.IsObj() {
			return errors.New("wordcloud: tuples must be objects")
		}
	}
	text := orDefault(p.Text, jsval.Str(""))
	font := orDefault(p.Font, jsval.Str("sans-serif"))
	style := orDefault(p.FontStyle, jsval.Str("normal"))
	weight := orDefault(p.FontWeight, jsval.Str("normal"))
	rotate := orDefault(p.Rotate, jsval.Num(0))
	padding := orDefault(p.Padding, jsval.Num(1))
	fontSize := orDefault(p.FontSize, jsval.Num(14))
	if p.FontSizeIsField && p.FontSizeRange != nil && len(p.FontSizeRange) >= 2 {
		fontSize = sqrtScaled(data, fontSize, p.FontSizeRange[0], p.FontSizeRange[1])
	}
	spiral := p.Spiral
	if spiral != Rectangular {
		spiral = Archimedean
	}
	random := p.Random
	if random == nil {
		random = LCG(1)
	}
	renderer := p.Renderer
	if renderer == nil {
		renderer = BoxRenderer{}
	}

	for _, t := range data {
		o := t.ObjValue()
		o.Set(as[0], jsval.Num(math.NaN()))
		o.Set(as[1], jsval.Num(math.NaN()))
		o.Set(as[3], jsval.Num(0))
	}

	words := make([]*word, len(data))
	for i, d := range data {
		w := &word{datum: d}
		w.text = text(d).AsString()
		w.font, w.style, w.weight = font(d), style(d), weight(d)
		w.family, w.styleS, w.wtS = w.font.AsString(), w.style.AsString(), w.weight.AsString()
		w.rotate = rotate(d)
		w.rotDeg = jsval.ToNumber(w.rotate)
		w.size = toInt32(jsval.ToNumber(fontSize(d)) + 1e-14)
		w.padding = jsval.ToNumber(padding(d))
		words[i] = w
	}
	sortWords(words)

	l := &layout{ctx: ctx, size: size, spiral: spiral, random: random, renderer: renderer}
	tags, err := l.run(words)
	if err != nil {
		return err
	}
	dx, dy := toInt32(size[0])>>1, toInt32(size[1])>>1
	for _, w := range tags {
		o := w.datum.ObjValue()
		o.Set(as[0], jsval.Num(float64(w.x+dx)))
		o.Set(as[1], jsval.Num(float64(w.y+dy)))
		o.Set(as[2], w.font)
		o.Set(as[3], jsval.Num(float64(w.size)))
		o.Set(as[4], w.style)
		o.Set(as[5], w.weight)
		o.Set(as[6], w.rotate)
	}
	return nil
}

func orDefault(a Accessor, def jsval.Value) Accessor {
	if a != nil {
		return a
	}
	return Const(def)
}

// sqrtScaled builds `d3.scaleSqrt().domain(extent(data, f)).range([r0, r1])`
// composed with f. The extent skips null/NaN values as vega-util's does.
func sqrtScaled(data []jsval.Value, f Accessor, r0, r1 float64) Accessor {
	lo, hi := math.Inf(1), math.Inf(-1)
	found := false
	for _, d := range data {
		v := f(d)
		if v.IsNullish() {
			continue
		}
		n := jsval.ToNumber(v)
		if !(n >= n) {
			continue
		}
		if !found || n < lo {
			lo = n
		}
		if !found || n > hi {
			hi = n
		}
		found = true
	}
	sq := func(x float64) float64 {
		if x < 0 {
			return -math.Sqrt(-x)
		}
		return math.Sqrt(x)
	}
	a, b := sq(lo), sq(hi)
	span := b - a
	return func(d jsval.Value) jsval.Value {
		x := jsval.ToNumber(f(d))
		if math.IsNaN(x) || !found {
			return jsval.Num(math.NaN())
		}
		// d3 normalize: a degenerate domain maps everything to 0.5.
		u := 0.5
		if span != 0 {
			u = (sq(x) - a) / span
		}
		return jsval.Num(float64(r0*(1-u)) + float64(r1*u))
	}
}
