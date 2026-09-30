package geo

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// DefaultDigits is d3-geo v3's default number of decimals in generated path
// strings.
const DefaultDigits = 3

// Path is d3.geoPath: it projects GeoJSON and renders it as SVG path data (or
// onto a PathContext), and measures it in projected coordinates. A Path is not
// safe for concurrent use.
type Path struct {
	proj     Projection // nil: coordinates are used as they are
	digits   int        // negative: no rounding
	radius   float64
	radiusFn func(jsval.Value) float64
	ctx      PathContext
	str      *pathString
}

// NewPath returns a path generator for the projection (nil for none), with
// digits DefaultDigits, point radius 4.5 and string output.
func NewPath(proj Projection) *Path {
	return &Path{proj: proj, digits: DefaultDigits, radius: 4.5}
}

// Projection returns the projection of the path (nil if none).
func (p *Path) Projection() Projection { return p.proj }

// SetProjection sets the projection (nil for none).
func (p *Path) SetProjection(proj Projection) { p.proj = proj }

// SetContext sets the drawing context; nil selects string output.
func (p *Path) SetContext(ctx PathContext) { p.ctx = ctx }

// Digits returns the decimals used in path strings (negative: full precision).
func (p *Path) Digits() int { return p.digits }

// SetDigits sets the decimals used in path strings; negative selects full
// precision (d3's null).
func (p *Path) SetDigits(d int) {
	p.digits = d
	p.str = nil
}

// PointRadius returns the constant radius used for point geometries.
func (p *Path) PointRadius() float64 { return p.radius }

// SetPointRadius sets a constant point radius.
func (p *Path) SetPointRadius(r float64) {
	p.radius = r
	p.radiusFn = nil
}

// SetPointRadiusFunc sets a point radius computed from each object rendered.
func (p *Path) SetPointRadiusFunc(fn func(jsval.Value) float64) { p.radiusFn = fn }

type radiusState struct {
	r  float64
	fn func(jsval.Value) float64
}

func (p *Path) saveRadius() radiusState { return radiusState{p.radius, p.radiusFn} }
func (p *Path) restoreRadius(s radiusState) {
	p.radius, p.radiusFn = s.r, s.fn
}

func (p *Path) stream(object jsval.Value, sink Stream) {
	if p.proj != nil {
		sink = p.proj.Stream(sink)
	}
	StreamObject(object, sink)
}

// String renders the object as SVG path data. ok is false when nothing was
// drawn (upstream's null), including for a falsy object or when the path has a
// drawing context.
func (p *Path) String(object jsval.Value) (string, bool) {
	if p.ctx != nil {
		p.Draw(object)
		return "", false
	}
	if p.str == nil {
		p.str = newPathString(p.digits)
	}
	if object.IsTruthy() {
		r := p.radius
		if p.radiusFn != nil {
			r = p.radiusFn(object)
		}
		p.str.pointRadius(r)
		p.stream(object, p.str)
	}
	return p.str.result()
}

// Draw renders the object onto the path's PathContext (a no-op without one).
func (p *Path) Draw(object jsval.Value) {
	if p.ctx == nil || !object.IsTruthy() {
		return
	}
	s := newPathContextStream(p.ctx)
	if p.radiusFn != nil {
		s.pointRadius(p.radiusFn(object))
	} else {
		s.pointRadius(p.radius)
	}
	p.stream(object, s)
}

// Area is the planar area of the projected object.
func (p *Path) Area(object jsval.Value) float64 {
	s := &areaSink{}
	p.stream(object, s)
	return s.result()
}

// Measure is the planar length of the projected object (perimeter for
// polygons).
func (p *Path) Measure(object jsval.Value) float64 {
	s := &measureSink{}
	p.stream(object, s)
	return s.result()
}

// Bounds is the planar bounding box [[x0, y0], [x1, y1]] of the projected
// object.
func (p *Path) Bounds(object jsval.Value) [2][2]float64 {
	s := newBoundsSink()
	p.stream(object, s)
	return s.result()
}

// Centroid is the planar centroid of the projected object.
func (p *Path) Centroid(object jsval.Value) [2]float64 {
	s := &centroidSink{}
	p.stream(object, s)
	return s.result()
}

// Render draws the object onto ctx and returns "" or, when ctx is nil, returns
// the SVG path data (empty when nothing is drawn): the calling convention of the
// scenegraph's path and shape generators.
func (p *Path) Render(ctx PathContext, object jsval.Value) string {
	saved := p.ctx
	p.ctx = ctx
	defer func() { p.ctx = saved }()
	if ctx != nil {
		p.Draw(object)
		return ""
	}
	s, _ := p.String(object)
	return s
}
