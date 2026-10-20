package geo

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
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

	// bctx and wb bound the walks (see Bind).
	bctx context.Context
	wb   *walkBound
	lim  *pointCharge // reused wrapper around str or cs for an unprojected path
	cs   *pathContextStream

	// walking is set while a string walk is in progress; still set at the next
	// call means the last walk was cut short (a limit panic) and left the
	// cached pipeline in a half-finished state.
	walking bool
}

// Bind bounds the path's walks by ctx: every point the projection produces
// (after resampling, before clipping), or every point an unprojected path
// emits, is charged to the budget ctx carries, and the walk meters its work
// to the call's ledger, so it stops when ctx is done. A walk past a bound
// panics with a *budget.Stop, which the caller's API boundary recovers.
// Binding the context the path is already bound to changes nothing.
func (p *Path) Bind(ctx context.Context) {
	if ctx == p.bctx {
		return
	}
	p.bctx = ctx
	p.wb = newWalkBound(ctx)
	p.lim = nil
}

func (*pathString) reusableSink() {}

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
	switch {
	case p.proj != nil:
		if d, ok := p.proj.(interface{ dropStreamCache() }); ok && p.walking {
			d.dropStreamCache()
		}
		sink = p.proj.streamBound(sink, p.wb)
	case p.wb != nil:
		if _, ok := sink.(reusableSink); ok {
			if p.lim == nil || p.lim.sink != sink {
				p.lim = &pointCharge{b: p.wb, sink: sink}
			}
			sink = p.lim
		} else {
			sink = &pointCharge{b: p.wb, sink: sink}
		}
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
	if p.str == nil || p.walking {
		// A walk that threw (upstream's exception, which fails the render)
		// left its text in the buffer: start anew.
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
