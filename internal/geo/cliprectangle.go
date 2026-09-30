package geo

import "math"

const (
	clipMax = 1e9
	clipMin = -clipMax
)

// rectClip is d3-geo's clipRectangle: planar clipping of lines and polygons
// (already projected) against the box [x0,y0]-[x1,y1].
type rectClip struct{ x0, y0, x1, y1 float64 }

func (r *rectClip) visible(x, y float64) bool {
	return r.x0 <= x && x <= r.x1 && r.y0 <= y && y <= r.y1
}

func (r *rectClip) interpolate(from, to *cpoint, direction float64, s Stream) {
	a, a1 := 0, 0
	if from == nil ||
		func() bool {
			a = r.corner(from, direction)
			a1 = r.corner(to, direction)
			return a != a1
		}() ||
		(r.comparePoint(from, to) < 0) != (direction > 0) {
		dir := int(direction)
		for {
			x, y := r.x1, r.y0
			if a == 0 || a == 3 {
				x = r.x0
			}
			if a > 1 {
				y = r.y1
			}
			s.Point(x, y)
			a = ((a+dir)%4 + 4) % 4
			if a == a1 {
				break
			}
		}
	} else {
		s.Point(to.x, to.y)
	}
}

func (r *rectClip) corner(p *cpoint, direction float64) int {
	switch {
	case abs(p.x-r.x0) < epsilon:
		if direction > 0 {
			return 0
		}
		return 3
	case abs(p.x-r.x1) < epsilon:
		if direction > 0 {
			return 2
		}
		return 1
	case abs(p.y-r.y0) < epsilon:
		if direction > 0 {
			return 1
		}
		return 0
	}
	if direction > 0 {
		return 3
	}
	return 2 // abs(p.y - y1) < epsilon
}

func (r *rectClip) compareIntersection(a, b *intersection) float64 {
	return r.comparePoint(a.x, b.x)
}

func (r *rectClip) comparePoint(a, b *cpoint) float64 {
	ca, cb := r.corner(a, 1), r.corner(b, 1)
	switch {
	case ca != cb:
		return float64(ca - cb)
	case ca == 0:
		return b.y - a.y
	case ca == 1:
		return a.x - b.x
	case ca == 2:
		return a.y - b.y
	}
	return b.x - a.x
}

type rectStream struct {
	r      *rectClip
	stream Stream
	active Stream
	buffer *clipBuffer

	inPolygon bool
	inLine    bool       // point is linePoint
	segments  [][]cpoint // visible pieces of every ring, flattened
	// The polygon's rings live in one arena for the winding test at polygonEnd.
	polyPts    []cpoint
	ringStarts []int
	polygon    [][]cpoint
	hasRing    bool

	x__, y__ float64
	v__      bool // first point
	x_, y_   float64
	v_       bool // previous point
	first    bool
	isClean  bool
}

func (r *rectClip) stream(sink Stream) Stream {
	s := &rectStream{r: r, stream: sink, active: sink, buffer: &clipBuffer{}}
	return s
}

func (s *rectStream) Sphere() {}

func (s *rectStream) Point(x, y float64) {
	if s.inLine {
		s.linePoint(x, y)
		return
	}
	if s.r.visible(x, y) {
		s.active.Point(x, y)
	}
}

// polygonInside returns the winding number of the polygon around the
// rectangle's top-left start, the rectangle's own inside test.
func (s *rectStream) polygonInside() int {
	r := s.r
	winding := 0
	for _, ring := range s.polygon {
		if len(ring) == 0 {
			continue
		}
		b0, b1 := ring[0].x, ring[0].y
		for j := 1; j < len(ring); j++ {
			a0, a1 := b0, b1
			b0, b1 = ring[j].x, ring[j].y
			if a1 <= r.y1 {
				if b1 > r.y1 && (b0-a0)*(r.y1-a1) > (b1-a1)*(r.x0-a0) {
					winding++
				}
			} else if b1 <= r.y1 && (b0-a0)*(r.y1-a1) < (b1-a1)*(r.x0-a0) {
				winding--
			}
		}
	}
	return winding
}

// PolygonStart buffers geometry within a polygon so it can be clipped en masse.
func (s *rectStream) PolygonStart() {
	s.active = s.buffer
	s.inPolygon = true
	s.segments = s.segments[:0]
	s.polyPts = s.polyPts[:0]
	s.ringStarts = s.ringStarts[:0]
	s.buffer.reset()
	s.isClean = true
}

func (s *rectStream) PolygonEnd() {
	s.polygon = s.polygon[:0]
	for i, start := range s.ringStarts {
		end := len(s.polyPts)
		if i+1 < len(s.ringStarts) {
			end = s.ringStarts[i+1]
		}
		s.polygon = append(s.polygon, s.polyPts[start:end:end])
	}
	startInside := s.polygonInside() != 0
	cleanInside := s.isClean && startInside
	merged := s.segments
	visible := len(merged) > 0
	if cleanInside || visible {
		s.stream.PolygonStart()
		if cleanInside {
			s.stream.LineStart()
			s.r.interpolate(nil, nil, 1, s.stream)
			s.stream.LineEnd()
		}
		if visible {
			clipRejoin(merged, s.r.compareIntersection, startInside, s.r.interpolate, s.stream)
		}
		s.stream.PolygonEnd()
	}
	s.active = s.stream
	s.inPolygon = false
	s.segments = s.segments[:0]
	s.polygon = s.polygon[:0]
	s.hasRing = false
}

func (s *rectStream) LineStart() {
	s.inLine = true
	if s.inPolygon {
		s.ringStarts = append(s.ringStarts, len(s.polyPts))
		s.hasRing = true
	}
	s.first = true
	s.v_ = false
	s.x_, s.y_ = nan, nan
}

// LineEnd: polygons are special-cased rather than handled separately, as
// upstream does.
func (s *rectStream) LineEnd() {
	if s.inPolygon {
		s.linePoint(s.x__, s.y__)
		if s.v__ && s.v_ {
			s.buffer.rejoin()
		}
		s.segments = append(s.segments, s.buffer.result()...)
		s.buffer.next()
		s.hasRing = false
	}
	s.inLine = false
	if s.v_ {
		s.active.LineEnd()
	}
}

func (s *rectStream) linePoint(x, y float64) {
	r := s.r
	v := r.visible(x, y)
	if s.hasRing {
		s.polyPts = append(s.polyPts, cpoint{x, y, 0})
	}
	if s.first {
		s.x__, s.y__, s.v__ = x, y, v
		s.first = false
		if v {
			s.active.LineStart()
			s.active.Point(x, y)
		}
	} else if v && s.v_ {
		s.active.Point(x, y)
	} else {
		s.x_ = math.Max(clipMin, math.Min(clipMax, s.x_))
		s.y_ = math.Max(clipMin, math.Min(clipMax, s.y_))
		a := [2]float64{s.x_, s.y_}
		x = math.Max(clipMin, math.Min(clipMax, x))
		y = math.Max(clipMin, math.Min(clipMax, y))
		b := [2]float64{x, y}
		if clipSegment(&a, &b, r.x0, r.y0, r.x1, r.y1) {
			if !s.v_ {
				s.active.LineStart()
				s.active.Point(a[0], a[1])
			}
			s.active.Point(b[0], b[1])
			if !v {
				s.active.LineEnd()
			}
			s.isClean = false
		} else if v {
			s.active.LineStart()
			s.active.Point(x, y)
			s.isClean = false
		}
	}
	s.x_, s.y_, s.v_ = x, y, v
}
