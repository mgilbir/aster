package geo

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// Projection is a cartographic projection as vega-projection exposes it: a d3
// projection augmented with its type name and a geoPath bound to it.
//
// All projections are configured through Set with the property names Vega
// accepts (see ProjectionProperties); properties a given projection type does
// not have are ignored, as Vega ignores them.
type Projection interface {
	// Type is the lower-case registry name ("albersusa", "mercator", ...).
	Type() string
	// Stream wraps sink in the projection pipeline: rotation, pre-clip,
	// resampling, projection and post-clip.
	Stream(sink Stream) Stream
	// Forward projects a [lon, lat] position in degrees. ok is false when the
	// projection has no location for it (the composite albersUsa outside its
	// regions).
	Forward(lon, lat float64) (x, y float64, ok bool)
	// Invert is the inverse of Forward; ok is false when the projection has no
	// inverse.
	Invert(x, y float64) (lon, lat float64, ok bool)
	// Path is the geoPath bound to this projection.
	Path() *Path
	// Set applies a Vega projection property. An error is returned where
	// upstream would throw (a null value for an array-valued property).
	Set(prop string, v jsval.Value) error
	// Scale and Translate read the current scale and translation.
	Scale() float64
	Translate() (x, y float64)
	// Fit* scale and translate the projection so the object fills the box.
	FitExtent(x0, y0, x1, y1 float64, object jsval.Value)
	FitSize(width, height float64, object jsval.Value)
	FitWidth(width float64, object jsval.Value)
	FitHeight(height float64, object jsval.Value)
	// Copy returns an independent projection with the same settings, copied the
	// way vega-projection copies (a fixed list of properties).
	Copy() Projection

	fitTarget
}

// ProjectionProperties are the property names Vega forwards from a projection
// definition to a projection (vega-projection's projectionProperties).
var ProjectionProperties = []string{
	// standard properties in d3-geo
	"clipAngle", "clipExtent", "scale", "translate", "center", "rotate",
	"parallels", "precision", "reflectX", "reflectY",
	// extended properties in d3-geo-projection: no registered projection has
	// them, so setting them does nothing.
	"coefficient", "distance", "fraction", "lobes", "parallel", "radius", "ratio",
	"spacing", "tilt",
}

// projTransform is the composed project+scale/translate map an installed
// projection applies to [lambda, phi]: `compose(project, transform)`.
type projTransform struct {
	raw *raw
	aff affine
}

func (t *projTransform) fwd(lambda, phi float64) (float64, float64) {
	x, y := t.raw.fwd(lambda, phi)
	return t.aff.fwd(x, y)
}

// affine is d3's scaleTranslateRotate: scale k, translate (dx, dy), reflect
// (sx, sy) and an optional post-rotation.
type affine struct {
	k, dx, dy, sx, sy float64
	rotated           bool
	a, b, ai, bi      float64
	ci, fi            float64
}

func newAffine(k, dx, dy, sx, sy, alpha float64) affine {
	t := affine{k: k, dx: dx, dy: dy, sx: sx, sy: sy}
	if truthy(alpha) {
		cosAlpha, sinAlpha := jsmath.Cos(alpha), jsmath.Sin(alpha)
		t.rotated = true
		t.a = float64(cosAlpha * k)
		t.b = float64(sinAlpha * k)
		t.ai = cosAlpha / k
		t.bi = sinAlpha / k
		t.ci = (float64(sinAlpha*dy) - float64(cosAlpha*dx)) / k
		t.fi = (float64(sinAlpha*dx) + float64(cosAlpha*dy)) / k
	}
	return t
}

func (t *affine) fwd(x, y float64) (float64, float64) {
	x = float64(x * t.sx)
	y = float64(y * t.sy)
	if t.rotated {
		return float64(t.a*x) - float64(t.b*y) + t.dx, t.dy - float64(t.b*x) - float64(t.a*y)
	}
	return t.dx + float64(t.k*x), t.dy - float64(t.k*y)
}

func (t *affine) inv(x, y float64) (float64, float64) {
	if t.rotated {
		return t.sx * (float64(t.ai*x) - float64(t.bi*y) + t.ci), t.sy * (t.fi - float64(t.bi*x) - float64(t.ai*y))
	}
	return (x - t.dx) / t.k * t.sx, (t.dy - y) / t.k * t.sy
}

// radiansRotate is d3's transformRadians followed by the rotation stage:
// degrees in, rotated radians out.
type radiansRotate struct {
	rot  rotation
	sink Stream
}

func (s *radiansRotate) Point(x, y float64) {
	l, p := s.rot.forward(x*radians, y*radians)
	s.sink.Point(l, p)
}
func (s *radiansRotate) LineStart()    { s.sink.LineStart() }
func (s *radiansRotate) LineEnd()      { s.sink.LineEnd() }
func (s *radiansRotate) PolygonStart() { s.sink.PolygonStart() }
func (s *radiansRotate) PolygonEnd()   { s.sink.PolygonEnd() }
func (s *radiansRotate) Sphere()       { s.sink.Sphere() }

// streamer builds a stage in front of a sink; postclip stages are streamers.
type streamer interface{ stream(sink Stream) Stream }

// standard is d3's projectionMutator: every projection except identity and the
// composite albersUsa. The mercator, transverse and conic flags select the
// wrapper behaviours d3 builds on top of it (mercatorProjection,
// transverseMercator's center/rotate, conicProjection's parallels).
type standard struct {
	typ       string
	projectAt func(phi0, phi1 float64) *raw
	project   *raw

	conic      bool
	phi0, phi1 float64 // parallels in radians
	mercator   bool
	transverse bool

	k                     float64 // scale
	x, y                  float64 // translate
	lambda                float64 // center
	phi                   float64
	dLambda, dPhi, dGamma float64 // pre-rotation
	rot                   rotation
	alpha                 float64 // post-rotation angle
	sx, sy                float64 // reflect
	theta                 float64 // clip angle in radians; hasTheta false means null
	hasTheta              bool
	thetaUndefined        bool
	preclip               *clipper
	x0, y0, x1, y1        float64 // post-clip extent
	hasClip               bool
	postclip              streamer
	delta2                float64 // precision squared

	pt        *projTransform // projectTransform
	resPt     *projTransform // transform captured by the resampler
	resDelta2 float64

	// mercator wrapper's own clip extent (the user's request, distinct from the
	// extent derived from it).
	mx0, my0, mx1, my1 float64
	mHasClip           bool

	path *Path
	pipe *pipeCache
}

func newStandard(typ string, projectAt func(phi0, phi1 float64) *raw) *standard {
	p := &standard{
		typ: typ, projectAt: projectAt,
		k: 150, x: 480, y: 250,
		sx: 1, sy: 1,
		preclip: clipAntimeridian,
		delta2:  0.5,
	}
	p.setProject()
	return p
}

// newConic is conicProjection: parallels default to [0, 60] degrees.
func newConic(typ string, projectAt func(phi0, phi1 float64) *raw) *standard {
	p := &standard{
		typ: typ, projectAt: projectAt, conic: true,
		phi0: 0, phi1: pi / 3,
		k: 150, x: 480, y: 250,
		sx: 1, sy: 1,
		preclip: clipAntimeridian,
		delta2:  0.5,
	}
	p.setProject()
	return p
}

func (p *standard) setProject() {
	p.project = p.projectAt(p.phi0, p.phi1)
	p.recenter()
}

func (p *standard) recenter() {
	c := newAffine(p.k, 0, 0, p.sx, p.sy, p.alpha)
	cx, cy := p.project.fwd(p.lambda, p.phi)
	cx, cy = c.fwd(cx, cy)
	p.rot = newRotation(p.dLambda, p.dPhi, p.dGamma)
	p.pt = &projTransform{raw: p.project, aff: newAffine(p.k, p.x-cx, p.y-cy, p.sx, p.sy, p.alpha)}
	p.resPt = p.pt
	p.resDelta2 = p.delta2
}

func (p *standard) Type() string { return p.typ }

func (p *standard) Path() *Path {
	if p.path == nil {
		p.path = NewPath(p)
	}
	return p.path
}

// reusableSink marks the sinks a projection may keep a pipeline for: sinks
// that live as long as their owner (a Path's string builder and its limit
// wrapper) rather than one per call.
type reusableSink interface{ reusableSink() }

// pipeKey is everything Stream builds the pipeline from. Every setter that
// changes the pipeline replaces one of these (the clippers and the transform
// are allocated afresh), so equal keys mean an equivalent pipeline.
type pipeKey struct {
	post   streamer
	res    *projTransform
	delta2 float64
	pre    *clipper
	rot    rotation
}

// pipeCache is the last pipeline built for a reusable sink. d3-geo caches the
// same way (projection.stream); the stages reset themselves at every polygon
// and line start, and reusing them keeps their buffers instead of growing them
// again for every feature of a map.
type pipeCache struct {
	key    pipeKey
	sink   Stream
	stream Stream
}

func (p *standard) Stream(sink Stream) Stream {
	_, reusable := sink.(reusableSink)
	if !reusable {
		return p.newStream(sink)
	}
	k := pipeKey{p.postclip, p.resPt, p.resDelta2, p.preclip, p.rot}
	if c := p.pipe; c != nil && c.key == k && c.sink == sink {
		return c.stream
	}
	s := p.newStream(sink)
	p.pipe = &pipeCache{k, sink, s}
	return s
}

// dropStreamCache forgets the cached pipeline; a walk that was cut short may
// have left its stages mid-ring.
func (p *standard) dropStreamCache() { p.pipe = nil }

func (p *standard) newStream(sink Stream) Stream {
	if p.postclip != nil {
		sink = p.postclip.stream(sink)
	}
	sink = newResample(p.resPt, p.resDelta2, sink)
	sink = p.preclip.stream(sink)
	return &radiansRotate{rot: p.rot, sink: sink}
}

func (p *standard) Forward(lon, lat float64) (float64, float64, bool) {
	l, ph := p.rot.forward(lon*radians, lat*radians)
	x, y := p.pt.fwd(l, ph)
	return x, y, true
}

func (p *standard) Invert(x, y float64) (float64, float64, bool) {
	if p.project.inv == nil {
		return 0, 0, false
	}
	x, y = p.pt.aff.inv(x, y)
	x, y = p.project.inv(x, y)
	x, y = p.rot.invert(x, y)
	return x * degrees, y * degrees, true
}

// clipAngle setter: 0 (or NaN) selects antimeridian clipping.
func (p *standard) setClipAngle(a float64) {
	if truthy(a) {
		p.theta = a * radians
		p.hasTheta = true
		p.preclip = newClipCircle(p.theta)
	} else {
		p.theta, p.hasTheta = 0, false
		p.preclip = clipAntimeridian
	}
}

// ClipAngle returns the clip angle in degrees (0 for none).
func (p *standard) clipAngle() float64 {
	return p.theta * degrees
}

func (p *standard) setBaseClipExtent(ext *[4]float64) {
	if ext == nil {
		p.x0, p.y0, p.x1, p.y1 = 0, 0, 0, 0
		p.hasClip = false
		p.postclip = nil
		return
	}
	p.x0, p.y0, p.x1, p.y1 = ext[0], ext[1], ext[2], ext[3]
	p.hasClip = true
	p.postclip = &rectClip{p.x0, p.y0, p.x1, p.y1}
}

// clipExtent returns the extent set by the user (the mercator wrapper hides the
// one it derives).
func (p *standard) clipExtent() (ext [4]float64, ok bool) {
	if p.mercator {
		if !p.mHasClip {
			return ext, false
		}
		return [4]float64{p.mx0, p.my0, p.mx1, p.my1}, true
	}
	if !p.hasClip {
		return ext, false
	}
	return [4]float64{p.x0, p.y0, p.x1, p.y1}, true
}

func (p *standard) setClipExtent(ext *[4]float64) {
	if !p.mercator {
		p.setBaseClipExtent(ext)
		return
	}
	if ext == nil {
		p.mx0, p.my0, p.mx1, p.my1 = 0, 0, 0, 0
		p.mHasClip = false
	} else {
		p.mx0, p.my0, p.mx1, p.my1 = ext[0], ext[1], ext[2], ext[3]
		p.mHasClip = true
	}
	p.reclip()
}

// reclip is mercatorProjection's reclip: the Mercator plane is bounded to
// +-pi*scale around the antimeridian of the rotated frame (and to the user's
// extent if any), so geometry does not wrap.
func (p *standard) reclip() {
	k := float64(pi * p.k)
	rl, rp, rg := float64(p.dLambda*degrees), float64(p.dPhi*degrees), float64(p.dGamma*degrees)
	if p.transverse {
		rg -= 90
	}
	rd := newRotateDegrees(rl, rp, rg)
	il, ip := rd.invert(0, 0)
	tx, ty, _ := p.Forward(il, ip)
	var ext [4]float64
	switch {
	case !p.mHasClip:
		ext = [4]float64{tx - k, ty - k, tx + k, ty + k}
	case p.project.mercator:
		ext = [4]float64{math.Max(tx-k, p.mx0), p.my0, math.Min(tx+k, p.mx1), p.my1}
	default:
		ext = [4]float64{p.mx0, math.Max(ty-k, p.my0), p.mx1, math.Min(ty+k, p.my1)}
	}
	p.setBaseClipExtent(&ext)
}

func (p *standard) Scale() float64 { return p.k }

func (p *standard) SetScale(k float64) {
	p.k = k
	p.recenter()
	if p.mercator {
		p.reclip()
	}
}

func (p *standard) Translate() (float64, float64) { return p.x, p.y }

func (p *standard) SetTranslate(x, y float64) {
	p.x, p.y = x, y
	p.recenter()
	if p.mercator {
		p.reclip()
	}
}

// Center returns [lon, lat] in degrees (transverse Mercator swaps and negates,
// as its frame is rotated a quarter turn).
func (p *standard) Center() (float64, float64) {
	l, ph := p.lambda*degrees, p.phi*degrees
	if p.transverse {
		return ph, -l
	}
	return l, ph
}

func (p *standard) SetCenter(lon, lat float64) {
	if p.transverse {
		lon, lat = -lat, lon
	}
	p.lambda = float64(jsMod(lon, 360) * radians)
	p.phi = jsMod(lat, 360) * radians
	p.recenter()
	if p.mercator {
		p.reclip()
	}
}

// SetRotate takes two or three angles in degrees (a third defaults to 0).
// Transverse Mercator adds its 90 degree gamma. Like d3, rotating does not
// recompute the Mercator clip extent.
func (p *standard) SetRotate(angles []float64) {
	get := func(i int) float64 {
		if i < len(angles) {
			return angles[i]
		}
		return nan
	}
	l, ph := get(0), get(1)
	var g float64
	hasG := len(angles) > 2
	if hasG {
		g = get(2)
	}
	if p.transverse {
		if hasG {
			g += 90
		} else {
			g = 90
		}
		hasG = true
	}
	p.dLambda = jsMod(l, 360) * radians
	p.dPhi = jsMod(ph, 360) * radians
	if hasG {
		p.dGamma = jsMod(g, 360) * radians
	} else {
		p.dGamma = 0
	}
	p.recenter()
}

func (p *standard) SetAngle(a float64) {
	p.alpha = jsMod(a, 360) * radians
	p.recenter()
}

func (p *standard) SetReflectX(v bool) {
	p.sx = 1
	if v {
		p.sx = -1
	}
	p.recenter()
}

func (p *standard) SetReflectY(v bool) {
	p.sy = 1
	if v {
		p.sy = -1
	}
	p.recenter()
}

// Precision returns the resampling threshold in pixels.
func (p *standard) Precision() float64 { return math.Sqrt(p.delta2) }

func (p *standard) SetPrecision(v float64) {
	p.delta2 = v * v
	p.resPt = p.pt
	p.resDelta2 = p.delta2
}

// SetParallels sets the standard parallels of a conic projection, in degrees,
// which rebuilds the raw projection.
func (p *standard) SetParallels(a, b float64) {
	if !p.conic {
		return
	}
	p.phi0, p.phi1 = a*radians, b*radians
	p.setProject()
	if p.mercator {
		p.reclip()
	}
}

func (p *standard) Parallels() (float64, float64) { return p.phi0 * degrees, p.phi1 * degrees }

func (p *standard) clipExtentForFit() (ext [4]float64, ok bool) { return p.clipExtent() }
func (p *standard) setClipExtentForFit(ext *[4]float64)         { p.setClipExtent(ext) }
func (p *standard) setScaleTranslate(k, x, y float64) {
	p.SetScale(k)
	p.SetTranslate(x, y)
}

func (p *standard) FitExtent(x0, y0, x1, y1 float64, object jsval.Value) {
	fitExtent(p, x0, y0, x1, y1, object)
}
func (p *standard) FitSize(w, h float64, object jsval.Value) { fitExtent(p, 0, 0, w, h, object) }
func (p *standard) FitWidth(w float64, object jsval.Value)   { fitWidth(p, w, object) }
func (p *standard) FitHeight(h float64, object jsval.Value)  { fitHeight(p, h, object) }

func (p *standard) Copy() Projection {
	c, _ := NewProjection(p.typ)
	cs := c.(*standard)
	cs.copyFrom(p)
	return cs
}

// copyFrom copies the properties vega-projection's copy() carries over, in its
// order: clipAngle, clipExtent, scale, translate, center, rotate, parallels,
// precision, reflectX, reflectY, and the path's point radius.
func (p *standard) copyFrom(src *standard) {
	p.setClipAngle(src.clipAngle())
	if ext, ok := src.clipExtent(); ok {
		p.setClipExtent(&ext)
	} else {
		p.setClipExtent(nil)
	}
	p.SetScale(src.k)
	p.SetTranslate(src.x, src.y)
	cl, cp := src.Center()
	p.SetCenter(cl, cp)
	r := []float64{float64(src.dLambda * degrees), float64(src.dPhi * degrees), float64(src.dGamma * degrees)}
	if src.transverse {
		r[2] -= 90
	}
	p.SetRotate(r)
	if src.conic {
		a, b := src.Parallels()
		p.SetParallels(a, b)
	}
	p.SetPrecision(src.Precision())
	p.SetReflectX(src.sx < 0)
	p.SetReflectY(src.sy < 0)
	p.Path().radius = src.Path().radius
	p.Path().radiusFn = src.Path().radiusFn
}
