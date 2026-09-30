package geo

import (
	"context"

	"github.com/mgilbir/aster/purego/internal/budget"
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

	// bctx carries the render's budget and cancellation (see Bind).
	bctx context.Context
	bud  *budget.Budget
	lim  *limitSink // reused wrapper around str or cs, so the projection's pipeline is too
	cs   *pathContextStream

	// walking is set while a string walk is in progress; still set at the next
	// call means the last walk was cut short (a limit panic) and left the
	// cached pipeline in a half-finished state.
	walking bool
}

// LimitError is the panic value that stops a path walk whose point budget is
// spent or whose context is done; it wraps the cause. The Path methods cannot
// return errors (they are scenegraph callbacks), so callers that can (GeoPath)
// recover it, and the renderer's own recover reports the rest.
type LimitError struct{ Err error }

func (e *LimitError) Error() string { return e.Err.Error() }
func (e *LimitError) Unwrap() error { return e.Err }

// Bind charges every point the path emits to the budget carried by ctx and
// stops the walk when ctx is done. Projection resampling can multiply the
// points of a segment by 2^16, so this is what bounds geographic output.
func (p *Path) Bind(ctx context.Context) {
	p.bctx = ctx
	p.bud = budget.From(ctx)
}

// limitSink counts the points reaching the output.
type limitSink struct {
	Stream
	p *Path
	n int
}

func (*limitSink) reusableSink()  {}
func (*pathString) reusableSink() {}

func (s *limitSink) Point(x, y float64) {
	s.n++
	if s.p.bud != nil {
		if err := s.p.bud.Points(1); err != nil {
			panic(&LimitError{err})
		}
	}
	if s.n&4095 == 0 {
		if err := s.p.bctx.Err(); err != nil {
			panic(&LimitError{err})
		}
	}
	s.Stream.Point(x, y)
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
	if p.bctx != nil {
		if _, ok := sink.(reusableSink); ok {
			// The same wrapper every call, so the projection can reuse its pipeline.
			if p.lim == nil || p.lim.Stream != sink {
				p.lim = &limitSink{Stream: sink, p: p}
			}
			sink = p.lim
		} else {
			sink = &limitSink{Stream: sink, p: p}
		}
	}
	if p.proj != nil {
		if d, ok := p.proj.(interface{ dropStreamCache() }); ok && p.walking {
			d.dropStreamCache()
		}
		sink = p.proj.Stream(sink)
	}
	p.walking = true
	StreamObject(object, sink)
	p.walking = false
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
	s := p.contextStream()
	if p.radiusFn != nil {
		s.pointRadius(p.radiusFn(object))
	} else {
		s.pointRadius(p.radius)
	}
	p.stream(object, s)
}

// contextStream is the stream drawing onto the path's context. Contexts that
// declare themselves reusable (they are pointers that outlive one call) keep
// their stream, and with it the projection's pipeline, from one object to the next.
func (p *Path) contextStream() *pathContextStream {
	if _, ok := p.ctx.(ReusableContext); !ok {
		return newPathContextStream(p.ctx)
	}
	if p.cs == nil || p.cs.ctx != p.ctx {
		p.cs = newPathContextStream(p.ctx)
	}
	return p.cs
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
