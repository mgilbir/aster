package geo

import "math"

// PathContext receives drawing commands, the subset of CanvasRenderingContext2D
// (and d3-path) that d3.geoPath draws with. Its method set is a subset of the
// scenegraph's path context, so that one satisfies it directly.
type PathContext interface {
	MoveTo(x, y float64)
	LineTo(x, y float64)
	// Arc draws a circular arc; angles in radians, ccw flips the direction.
	Arc(x, y, r, startAngle, endAngle float64, ccw bool)
	ClosePath()
}

// ReusableContext is implemented by PathContexts that are comparable pointers
// living across calls, so a Path may keep a stream for them.
type ReusableContext interface{ ReusableContext() }

func (*pathContextStream) reusableSink() {}

// pathContextStream is d3-geo's PathContext stream: it draws projected geometry
// onto a PathContext.
type pathContextStream struct {
	ctx    PathContext
	radius float64
	line   float64
	point  int
}

func newPathContextStream(ctx PathContext) *pathContextStream {
	return &pathContextStream{ctx: ctx, radius: 4.5, line: nan, point: 2}
}

func (s *pathContextStream) pointRadius(r float64) { s.radius = r }
func (s *pathContextStream) PolygonStart()         { s.line = 0 }
func (s *pathContextStream) PolygonEnd()           { s.line = nan }
func (s *pathContextStream) LineStart()            { s.point = 0 }
func (s *pathContextStream) Sphere()               {}
func (s *pathContextStream) LineEnd() {
	if s.line == 0 {
		s.ctx.ClosePath()
	}
	s.point = 2
}
func (s *pathContextStream) Point(x, y float64) {
	switch s.point {
	case 0:
		s.ctx.MoveTo(x, y)
		s.point = 1
	case 1:
		s.ctx.LineTo(x, y)
	default:
		s.ctx.MoveTo(x+s.radius, y)
		s.ctx.Arc(x, y, s.radius, 0, tau, false)
	}
}

// areaSink is d3's planar path area: the sum of absolute ring areas (shoelace),
// halved. Exact summation keeps tiny rings from cancelling.
type areaSink struct {
	sum, ringSum adder
	inPolygon    bool
	mode         int // 0 ignore points, 1 first point of a ring, 2 following points
	x00, y00     float64
	x0, y0       float64
}

func (s *areaSink) Sphere()       {}
func (s *areaSink) PolygonStart() { s.inPolygon = true }
func (s *areaSink) PolygonEnd() {
	s.inPolygon = false
	s.mode = 0
	s.sum.add(abs(s.ringSum.value()))
	s.ringSum = adder{}
}
func (s *areaSink) LineStart() {
	if s.inPolygon {
		s.mode = 1
	}
}
func (s *areaSink) LineEnd() {
	if s.inPolygon {
		s.areaPoint(s.x00, s.y00)
	}
}
func (s *areaSink) Point(x, y float64) {
	switch s.mode {
	case 1:
		s.mode = 2
		s.x00, s.x0 = x, x
		s.y00, s.y0 = y, y
	case 2:
		s.areaPoint(x, y)
	}
}
func (s *areaSink) areaPoint(x, y float64) {
	s.ringSum.add(float64(s.y0*x) - float64(s.x0*y))
	s.x0, s.y0 = x, y
}
func (s *areaSink) result() float64 { return s.sum.value() / 2 }

// boundsSink accumulates the planar bounding box.
type boundsSink struct{ x0, y0, x1, y1 float64 }

func newBoundsSink() *boundsSink {
	return &boundsSink{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
}
func (s *boundsSink) Sphere()       {}
func (s *boundsSink) LineStart()    {}
func (s *boundsSink) LineEnd()      {}
func (s *boundsSink) PolygonStart() {}
func (s *boundsSink) PolygonEnd()   {}
func (s *boundsSink) Point(x, y float64) {
	if x < s.x0 {
		s.x0 = x
	}
	if x > s.x1 {
		s.x1 = x
	}
	if y < s.y0 {
		s.y0 = y
	}
	if y > s.y1 {
		s.y1 = y
	}
}
func (s *boundsSink) result() [2][2]float64 {
	return [2][2]float64{{s.x0, s.y0}, {s.x1, s.y1}}
}

// centroidSink is d3's planar centroid: the area-weighted centroid of polygons,
// falling back to the length-weighted centroid of lines and then the mean of
// points, as the higher-dimensional geometry dominates.
type centroidSink struct {
	X0, Y0, Z0, X1, Y1, Z1, X2, Y2, Z2 float64
	x00, y00, x0, y0                   float64
	inPolygon                          bool
	mode                               int
}

const (
	cPoint = iota
	cFirstLine
	cLine
	cFirstRing
	cRing
)

func (s *centroidSink) Sphere() {}
func (s *centroidSink) centroidPoint(x, y float64) {
	s.X0 += x
	s.Y0 += y
	s.Z0++
}
func (s *centroidSink) PolygonStart() { s.inPolygon = true }
func (s *centroidSink) PolygonEnd() {
	s.inPolygon = false
	s.mode = cPoint
}
func (s *centroidSink) LineStart() {
	if s.inPolygon {
		s.mode = cFirstRing
	} else {
		s.mode = cFirstLine
	}
}
func (s *centroidSink) LineEnd() {
	if s.inPolygon {
		s.pointRing(s.x00, s.y00)
	} else {
		s.mode = cPoint
	}
}
func (s *centroidSink) Point(x, y float64) {
	switch s.mode {
	case cPoint:
		s.centroidPoint(x, y)
	case cFirstLine:
		s.mode = cLine
		s.x0, s.y0 = x, y
		s.centroidPoint(x, y)
	case cLine:
		dx, dy := x-s.x0, y-s.y0
		z := math.Sqrt(float64(dx*dx) + float64(dy*dy))
		s.X1 += float64(z*(s.x0+x)) / 2
		s.Y1 += float64(z*(s.y0+y)) / 2
		s.Z1 += z
		s.x0, s.y0 = x, y
		s.centroidPoint(x, y)
	case cFirstRing:
		s.mode = cRing
		s.x00, s.x0 = x, x
		s.y00, s.y0 = y, y
		s.centroidPoint(x, y)
	case cRing:
		s.pointRing(x, y)
	}
}
func (s *centroidSink) pointRing(x, y float64) {
	dx, dy := x-s.x0, y-s.y0
	z := math.Sqrt(float64(dx*dx) + float64(dy*dy))

	s.X1 += float64(z*(s.x0+x)) / 2
	s.Y1 += float64(z*(s.y0+y)) / 2
	s.Z1 += z

	z = float64(s.y0*x) - float64(s.x0*y)
	s.X2 += float64(z * (s.x0 + x))
	s.Y2 += float64(z * (s.y0 + y))
	s.Z2 += float64(z * 3)
	s.x0, s.y0 = x, y
	s.centroidPoint(x, y)
}
func (s *centroidSink) result() [2]float64 {
	switch {
	case truthy(s.Z2):
		return [2]float64{s.X2 / s.Z2, s.Y2 / s.Z2}
	case truthy(s.Z1):
		return [2]float64{s.X1 / s.Z1, s.Y1 / s.Z1}
	case truthy(s.Z0):
		return [2]float64{s.X0 / s.Z0, s.Y0 / s.Z0}
	}
	return [2]float64{nan, nan}
}

// measureSink is d3's planar path length; polygon rings count their closing
// edge.
type measureSink struct {
	sum      adder
	ring     bool
	mode     int // 0 ignore, 1 first point, 2 following
	x00, y00 float64
	x0, y0   float64
}

func (s *measureSink) Sphere()       {}
func (s *measureSink) PolygonStart() { s.ring = true }
func (s *measureSink) PolygonEnd()   { s.ring = false }
func (s *measureSink) LineStart()    { s.mode = 1 }
func (s *measureSink) LineEnd() {
	if s.ring {
		s.lengthPoint(s.x00, s.y00)
	}
	s.mode = 0
}
func (s *measureSink) Point(x, y float64) {
	switch s.mode {
	case 1:
		s.mode = 2
		s.x00, s.x0 = x, x
		s.y00, s.y0 = y, y
	case 2:
		s.lengthPoint(x, y)
	}
}
func (s *measureSink) lengthPoint(x, y float64) {
	s.x0 -= x
	s.y0 -= y
	s.sum.add(math.Sqrt(float64(s.x0*s.x0) + float64(s.y0*s.y0)))
	s.x0, s.y0 = x, y
}
func (s *measureSink) result() float64 { return s.sum.value() }
