package geo

// lineClipper cuts a stream of lines into visible segments. clean reports, once
// a ring has been streamed through it, a bit set: bit 0 set means there were no
// intersections, bit 1 set means there were intersections and the first and
// last segments should be rejoined; 0 means there were intersections or the
// line was empty.
type lineClipper interface {
	Point(x, y float64)
	LineStart()
	LineEnd()
	clean() int
}

// clipper is d3-geo's clip factory: a set of primitives (point visibility, a
// line clipper, edge interpolation, a start point known to be outside) from
// which the polygon-aware clip stream is assembled.
type clipper struct {
	visible     func(lambda, phi float64) bool
	line        func(sink Stream) lineClipper
	interpolate interpolateFunc
	startLambda float64
	startPhi    float64
}

// compareIntersection sorts intersections along the clip edge. Antimeridian
// cutting and circle clipping share the comparison.
func compareIntersection(a, b *intersection) float64 {
	var da, db float64
	if a.x.x < 0 {
		da = a.x.y - halfPi - epsilon
	} else {
		da = halfPi - a.x.y
	}
	if b.x.x < 0 {
		db = b.x.y - halfPi - epsilon
	} else {
		db = halfPi - b.x.y
	}
	return da - db
}

type clipStream struct {
	c    *clipper
	sink Stream

	line       lineClipper
	ringBuffer *clipBuffer
	ringSink   lineClipper

	polygonStarted bool // the sink has been sent PolygonStart
	inPolygon      bool
	inLine         bool
	hasRing        bool

	// The polygon's rings, kept for the containment test at polygonEnd, live in
	// one arena: ringStarts are offsets of each ring's first point.
	polyPts    []cpoint
	ringStarts []int
	polygon    [][]cpoint // windows of polyPts, built at polygonEnd
	// segments are the visible pieces of all rings, flattened.
	segments [][]cpoint
}

func (c *clipper) stream(sink Stream) Stream {
	cs := &clipStream{c: c, sink: sink, ringBuffer: &clipBuffer{}}
	cs.line = c.line(sink)
	cs.ringSink = c.line(cs.ringBuffer)
	return cs
}

func (s *clipStream) Point(lambda, phi float64) {
	switch {
	case s.inPolygon:
		s.pointRing(lambda, phi)
	case s.inLine:
		s.line.Point(lambda, phi)
	default:
		if s.c.visible(lambda, phi) {
			s.sink.Point(lambda, phi)
		}
	}
}

func (s *clipStream) LineStart() {
	if s.inPolygon {
		s.ringSink.LineStart()
		s.ringStarts = append(s.ringStarts, len(s.polyPts))
		s.hasRing = true
		return
	}
	s.inLine = true
	s.line.LineStart()
}

func (s *clipStream) LineEnd() {
	if s.inPolygon {
		s.ringEnd()
		return
	}
	s.inLine = false
	s.line.LineEnd()
}

func (s *clipStream) pointRing(lambda, phi float64) {
	if !s.hasRing {
		return
	}
	s.polyPts = append(s.polyPts, cpoint{lambda, phi, 0})
	s.ringSink.Point(lambda, phi)
}

func (s *clipStream) PolygonStart() {
	s.inPolygon = true
	s.segments = s.segments[:0]
	s.polyPts = s.polyPts[:0]
	s.ringStarts = s.ringStarts[:0]
	s.ringBuffer.reset()
}

func (s *clipStream) startPolygon() {
	if !s.polygonStarted {
		s.sink.PolygonStart()
		s.polygonStarted = true
	}
}

func (s *clipStream) PolygonEnd() {
	s.inPolygon = false
	s.polygon = s.polygon[:0]
	for i, start := range s.ringStarts {
		end := len(s.polyPts)
		if i+1 < len(s.ringStarts) {
			end = s.ringStarts[i+1]
		}
		s.polygon = append(s.polygon, s.polyPts[start:end:end])
	}
	startInside := polygonContains(s.polygon, s.c.startLambda, s.c.startPhi)
	if len(s.segments) > 0 {
		s.startPolygon()
		clipRejoin(s.segments, compareIntersection, startInside, s.c.interpolate, s.sink)
	} else if startInside {
		s.startPolygon()
		s.sink.LineStart()
		s.c.interpolate(nil, nil, 1, s.sink)
		s.sink.LineEnd()
	}
	if s.polygonStarted {
		s.sink.PolygonEnd()
		s.polygonStarted = false
	}
	s.segments = s.segments[:0]
	s.polygon = s.polygon[:0]
}

func (s *clipStream) Sphere() {
	s.sink.PolygonStart()
	s.sink.LineStart()
	s.c.interpolate(nil, nil, 1, s.sink)
	s.sink.LineEnd()
	s.sink.PolygonEnd()
}

func (s *clipStream) ringEnd() {
	if len(s.ringStarts) == 0 {
		return
	}
	start := s.ringStarts[len(s.ringStarts)-1]
	if !s.hasRing || len(s.polyPts) == start {
		// A ring with no points: upstream indexes ring[0] and would throw.
		s.ringSink.LineEnd()
		s.ringBuffer.result()
		s.ringBuffer.next()
		s.hasRing = false
		return
	}
	head := s.polyPts[start]
	s.pointRing(head.x, head.y)
	s.ringSink.LineEnd()

	clean := s.ringSink.clean()
	ringSegments := s.ringBuffer.result()
	n := len(ringSegments)

	// The closing point is not part of the polygon's ring.
	s.polyPts = s.polyPts[:len(s.polyPts)-1]
	s.hasRing = false

	if n == 0 {
		s.ringBuffer.next()
		return
	}

	// No intersections.
	if clean&1 != 0 {
		segment := ringSegments[0]
		if m := len(segment) - 1; m > 0 {
			s.startPolygon()
			s.sink.LineStart()
			for i := 0; i < m; i++ {
				s.sink.Point(segment[i].x, segment[i].y)
			}
			s.sink.LineEnd()
		}
		s.ringBuffer.next()
		return
	}

	// Rejoin connected segments.
	if n > 1 && clean&2 != 0 {
		// [middle..., last+first]
		last, firstSeg := ringSegments[n-1], ringSegments[0]
		joined := make([]cpoint, 0, len(last)+len(firstSeg))
		joined = append(append(joined, last...), firstSeg...)
		ringSegments = append(ringSegments[1:n-1:n-1], joined)
	}

	for _, seg := range ringSegments {
		if len(seg) > 1 {
			s.segments = append(s.segments, seg)
		}
	}
	s.ringBuffer.next()
}
