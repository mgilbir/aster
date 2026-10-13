package geo

import (
	"context"

	"github.com/mgilbir/aster/internal/budget"
)

// walkBound bounds the walks of one path generator, or of one call of a
// function that streams geometry: the render's point budget, charged where a
// projection produces points, and the call's work meter, which polls the
// call's context so a timeout or a Close stops a walk wherever it is (ADR
// 0010). A nil *walkBound charges nothing.
//
// Points are charged where they are produced, after the projection's
// resampling (or its planar transform), not where they reach the output: the
// resampler can make 2^16 points of every segment, and a clip extent can then
// drop them all, so counting the output bounded nothing (C17). Every stage
// downstream of that point is linear in the points it receives: the post-clip
// buffers each point once and adds at most four corners per pair of
// intersections, and the rejoin is linear (clipRejoin).
type walkBound struct {
	bud *budget.Budget
	m   budget.Meter
}

func newWalkBound(ctx context.Context) *walkBound {
	if ctx == nil {
		return nil
	}
	bud := budget.From(ctx)
	if bud == nil && budget.LedgerFrom(ctx) == nil && ctx.Done() == nil {
		return nil // nothing to charge, and never done
	}
	return &walkBound{bud: bud, m: budget.NewMeter(ctx)}
}

// point charges one produced point.
func (b *walkBound) point() {
	if b.bud != nil {
		if err := b.bud.Points(1); err != nil {
			panic(&budget.Stop{Err: err})
		}
	}
	b.m.MustWork(1)
}

// work charges n steps of work.
func (b *walkBound) work(n int64) { b.m.MustWork(n) }

// recoverStop turns the *budget.Stop a bounded walk panics with back into an
// error, at an API boundary that returns one.
func recoverStop(err *error) {
	if r := recover(); r != nil {
		s, ok := r.(*budget.Stop)
		if !ok {
			panic(r)
		}
		*err = s.Err
	}
}

// pointCharge is the stage that charges the points a projection produces.
type pointCharge struct {
	b    *walkBound
	sink Stream
}

func (s *pointCharge) Point(x, y float64) {
	s.b.point()
	s.sink.Point(x, y)
}
func (s *pointCharge) LineStart()    { s.sink.LineStart() }
func (s *pointCharge) LineEnd()      { s.sink.LineEnd() }
func (s *pointCharge) PolygonStart() { s.sink.PolygonStart() }
func (s *pointCharge) PolygonEnd()   { s.sink.PolygonEnd() }
func (s *pointCharge) Sphere()       { s.sink.Sphere() }

// workCharge is the stage that meters the points a walk reads, for walks
// that produce none (spherical measures) and in front of a projection, whose
// clipping may drop every input point before it is produced.
type workCharge struct {
	b    *walkBound
	sink Stream
}

func (s *workCharge) Point(x, y float64) {
	s.b.work(1)
	s.sink.Point(x, y)
}
func (s *workCharge) LineStart()    { s.sink.LineStart() }
func (s *workCharge) LineEnd()      { s.sink.LineEnd() }
func (s *workCharge) PolygonStart() { s.sink.PolygonStart() }
func (s *workCharge) PolygonEnd()   { s.sink.PolygonEnd() }
func (s *workCharge) Sphere()       { s.sink.Sphere() }

// meterInput wraps sink in a workCharge stage (b nil: sink itself).
func meterInput(sink Stream, b *walkBound) Stream {
	if b == nil {
		return sink
	}
	return &workCharge{b: b, sink: sink}
}
