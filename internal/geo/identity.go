package geo

import (
	"context"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// identityProj is d3.geoIdentity: a planar scale/translate/reflect/rotate of
// coordinates that are already projected. It has no clipping stage other than
// an optional clip extent, no rotation of the sphere and no resampling.
type identityProj struct {
	typ            string
	k, tx, ty      float64
	sx, sy         float64
	alpha, ca, sa  float64
	x0, y0, x1, y1 float64
	hasClip        bool
	postclip       streamer
	kx, ky         float64
	path           *Path
}

func newIdentity(typ string) *identityProj {
	return &identityProj{typ: typ, k: 1, sx: 1, sy: 1, kx: 1, ky: 1}
}

func (p *identityProj) reset() {
	p.kx = p.k * p.sx
	p.ky = p.k * p.sy
}

func (p *identityProj) Type() string { return p.typ }

func (p *identityProj) Path() *Path {
	if p.path == nil {
		p.path = NewPath(p)
	}
	return p.path
}

func (p *identityProj) project(x, y float64) (float64, float64) {
	x = float64(x * p.kx)
	y = float64(y * p.ky)
	if truthy(p.alpha) {
		t := float64(y*p.ca) - float64(x*p.sa)
		x = float64(x*p.ca) + float64(y*p.sa)
		y = t
	}
	return x + p.tx, y + p.ty
}

func (p *identityProj) Forward(x, y float64) (float64, float64, bool) {
	x, y = p.project(x, y)
	return x, y, true
}

func (p *identityProj) Invert(x, y float64) (float64, float64, bool) {
	x -= p.tx
	y -= p.ty
	if truthy(p.alpha) {
		t := float64(y*p.ca) + float64(x*p.sa)
		x = float64(x*p.ca) - float64(y*p.sa)
		y = t
	}
	return x / p.kx, y / p.ky, true
}

type identityStage struct {
	p    *identityProj
	sink Stream
}

func (s *identityStage) Point(x, y float64) {
	x, y = s.p.project(x, y)
	s.sink.Point(x, y)
}
func (s *identityStage) LineStart()    { s.sink.LineStart() }
func (s *identityStage) LineEnd()      { s.sink.LineEnd() }
func (s *identityStage) PolygonStart() { s.sink.PolygonStart() }
func (s *identityStage) PolygonEnd()   { s.sink.PolygonEnd() }
func (s *identityStage) Sphere()       { s.sink.Sphere() }

func (p *identityProj) Stream(sink Stream) Stream { return p.streamBound(sink, nil) }

// streamBound charges the points of the planar transform to b: an identity
// projection produces exactly the points it reads.
func (p *identityProj) streamBound(sink Stream, b *walkBound) Stream {
	if p.postclip != nil {
		sink = p.postclip.stream(sink)
	}
	if b != nil {
		sink = &pointCharge{b: b, sink: sink}
	}
	return &identityStage{p: p, sink: sink}
}

func (p *identityProj) Scale() float64                { return p.k }
func (p *identityProj) Translate() (float64, float64) { return p.tx, p.ty }
func (p *identityProj) SetScale(k float64)            { p.k = k; p.reset() }
func (p *identityProj) SetTranslate(x, y float64)     { p.tx, p.ty = x, y; p.reset() }
func (p *identityProj) SetReflectX(v bool)            { p.sx = boolSign(v); p.reset() }
func (p *identityProj) SetReflectY(v bool)            { p.sy = boolSign(v); p.reset() }
func (p *identityProj) SetAngle(a float64) {
	p.alpha = jsMod(a, 360) * radians
	p.sa, p.ca = jsmath.Sin(p.alpha), jsmath.Cos(p.alpha)
	p.reset()
}

func boolSign(v bool) float64 {
	if v {
		return -1
	}
	return 1
}

func (p *identityProj) clipExtentForFit() ([4]float64, bool) {
	if !p.hasClip {
		return [4]float64{}, false
	}
	return [4]float64{p.x0, p.y0, p.x1, p.y1}, true
}

func (p *identityProj) setClipExtentForFit(ext *[4]float64) {
	if ext == nil {
		p.x0, p.y0, p.x1, p.y1 = 0, 0, 0, 0
		p.hasClip = false
		p.postclip = nil
	} else {
		p.x0, p.y0, p.x1, p.y1 = ext[0], ext[1], ext[2], ext[3]
		p.hasClip = true
		p.postclip = &rectClip{p.x0, p.y0, p.x1, p.y1}
	}
	p.reset()
}

func (p *identityProj) setScaleTranslate(k, x, y float64) {
	p.SetScale(k)
	p.SetTranslate(x, y)
}

func (p *identityProj) FitExtent(ctx context.Context, x0, y0, x1, y1 float64, object jsval.Value) error {
	return fitExtent(ctx, p, x0, y0, x1, y1, object)
}
func (p *identityProj) FitSize(ctx context.Context, w, h float64, object jsval.Value) error {
	return fitExtent(ctx, p, 0, 0, w, h, object)
}
func (p *identityProj) FitWidth(ctx context.Context, w float64, object jsval.Value) error {
	return fitWidth(ctx, p, w, object)
}
func (p *identityProj) FitHeight(ctx context.Context, h float64, object jsval.Value) error {
	return fitHeight(ctx, p, h, object)
}

func (p *identityProj) Copy() Projection {
	c := newIdentity(p.typ)
	if ext, ok := p.clipExtentForFit(); ok {
		c.setClipExtentForFit(&ext)
	} else {
		c.setClipExtentForFit(nil)
	}
	c.SetScale(p.k)
	c.SetTranslate(p.tx, p.ty)
	c.SetReflectX(p.sx < 0)
	c.SetReflectY(p.sy < 0)
	c.Path().radius = p.Path().radius
	c.Path().radiusFn = p.Path().radiusFn
	return c
}

// bareProj is what upstream's Projection operator makes of the type
// "constructor": vega-projection looks the type up with `projections[type]`
// on a plain object, which finds Object, and `Object()` is an empty object.
// It has no projection function and no path (geoshape draws with the
// default, unprojected path), and no property to set: only its identity
// stream, and the errors of calling what it lacks.
type bareProj struct{ *identityProj }

func newBare() *bareProj { return &bareProj{newIdentity("constructor")} }

// Bare marks the projection as the empty object.
func (b *bareProj) Bare() bool { return true }

// Set is `if (isFunction(proj[key])) proj[key](value)`: the object has no
// such function.
func (b *bareProj) Set(string, jsval.Value) error { return nil }

// IsBare reports whether proj is the empty object a projection of the type
// "constructor" is.
func IsBare(proj Projection) bool {
	b, ok := proj.(interface{ Bare() bool })
	return ok && b.Bare()
}
