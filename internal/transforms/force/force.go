// Package force implements the vega-force "force" transform: a d3-force
// simulation (center, collide, nbody, link, x, y forces) run over data tuples.
//
// The simulation is deterministic. d3-force draws its jiggle from a linear
// congruential generator seeded with 1, and places nodes without a position on
// a phyllotaxis spiral; both are reproduced exactly, so layouts match
// upstream to floating-point precision.
package force

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
)

// Limits on spec-driven work. They are far above any real chart.
const (
	MaxIterations      = 100_000 // simulation ticks in static mode
	MaxForceIterations = 1_000   // per-force "iterations" (collide, link)
)

// Accessor reads a value from a tuple, e.g. a field accessor or a compiled
// expression.
type Accessor func(jsval.Value) jsval.Value

// Param is a numeric force parameter that is either a constant or evaluated
// per tuple (Vega's "number or expression" parameters). The result is
// coerced with Number(), as d3 does with unary plus.
type Param struct {
	Fn    Accessor
	Const float64
}

// Constant returns a constant Param.
func Constant(v float64) Param { return Param{Const: v} }

func (p Param) eval(d jsval.Value) float64 {
	if p.Fn != nil {
		return jsval.ToNumber(p.Fn(d))
	}
	return p.Const
}

// Params configures the transform. Use DefaultParams for Vega's defaults; the
// zero value is not the default configuration.
type Params struct {
	// Static runs Iterations ticks synchronously. Otherwise only the first
	// tick runs: Vega's remaining ticks are driven by a wall-clock timer
	// after the initial render, so a rendered spec shows a single tick.
	Static        bool
	Iterations    int // 0 means 300, as upstream's `_.iterations || 300`
	Alpha         float64
	AlphaMin      float64
	AlphaTarget   float64
	VelocityDecay float64
	Forces        []Force
	// Bound lists the simulation parameters that Vega applies to the
	// simulation. Vega copies alpha, alphaMin, alphaTarget and velocityDecay
	// onto the simulation only when it sees them as modified, which never
	// happens on the first pass (literals and signals alike already hold their
	// value before the first pulse). The zero value therefore leaves d3's own
	// defaults in place; alpha still raises the starting alpha through
	// max(alpha, _.alpha || 1). Set bits only to model a later update.
	Bound Bound
}

// Bound is a set of simulation parameters applied to the simulation; see Params.Bound.
type Bound uint8

// Parameters that may be signal-driven.
const (
	BoundAlpha Bound = 1 << iota
	BoundAlphaMin
	BoundAlphaTarget
	BoundVelocityDecay
)

// DefaultParams returns Vega's defaults (alpha 1, alphaMin 0.001,
// alphaTarget 0, velocityDecay 0.4, 300 iterations, no forces).
func DefaultParams() Params {
	return Params{Iterations: 300, Alpha: 1, AlphaMin: 0.001, VelocityDecay: 0.4}
}

// Force is one entry of the "forces" parameter: Center, Collide, NBody, Link,
// X or Y.
type Force interface{ build() force }

// force is an initialised d3 force.
type force interface {
	initialize(s *sim) error
	apply(alpha float64)
}

// Products that feed an addition are wrapped in float64(...) throughout this
// package: Go may otherwise fuse them into an FMA on some architectures, and
// JavaScript never does, which would make layouts drift from upstream.
type node struct {
	x, y, vx, vy float64
	fx, fy       float64
	hasFx, hasFy bool
}

// sim is the d3 simulation state.
type sim struct {
	nodes         []node
	objs          []*jsval.Object
	vals          []jsval.Value
	alpha         float64
	alphaMin      float64
	alphaDecay    float64
	alphaTarget   float64
	velocityDecay float64 // d3 stores 1 - velocityDecay
	rng           lcg
	forces        []force
	ctx           context.Context
	err           error // the context's error once it was seen cancelled
}

// halt reports, polling the context every 256 nodes, whether the simulation
// must stop: one tick of a collide or many-body force is quadratic in the
// worst case, so cancellation cannot wait for the tick to end.
func (s *sim) halt(i int) bool {
	if s.err != nil {
		return true
	}
	if i&255 == 0 {
		s.err = s.ctx.Err()
	}
	return s.err != nil
}

// lcg is d3-force's generator (Numerical Recipes constants, seed 1).
type lcg struct{ s uint32 }

func (l *lcg) next() float64 {
	l.s = 1664525*l.s + 1013904223 // arithmetic modulo 2^32
	return float64(l.s) / 4294967296
}

func (l *lcg) jiggle() float64 { return float64((l.next() - 0.5) * 1e-6) }

// Run executes the simulation over nodes (object tuples) and writes x, y, vx,
// vy and index onto each, as vega-force does. Link forces additionally write
// index and resolved source/target node objects onto their link tuples.
func Run(ctx context.Context, nodes []jsval.Value, p Params) error {
	iters := p.Iterations
	if iters == 0 {
		iters = 300
	}
	if iters > MaxIterations {
		return fmt.Errorf("%w: force: iterations %d exceeds limit %d", budget.ErrLimit, iters, MaxIterations)
	}
	m, err := NewSimulation(ctx, nodes, p)
	if err != nil {
		return err
	}
	if !p.Static {
		m.Tick()
		m.WriteBack()
		return firstErr(m.s.err, ctx.Err())
	}
	// Vega raises alpha to at least _.alpha (or 1) and spreads the decay so the
	// simulation cools to alphaMin in exactly `iterations` ticks.
	alpha := p.Alpha
	if alpha == 0 || math.IsNaN(alpha) {
		alpha = 1
	}
	m.SetAlpha(math.Max(m.Alpha(), alpha))
	m.SetAlphaDecay(1 - jsmath.Pow(m.AlphaMin(), 1/float64(iters)))
	for ; iters > 0; iters-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.Tick()
		if m.s.err != nil {
			return m.s.err
		}
	}
	m.WriteBack()
	return nil
}

// Simulation is a d3 force simulation that lives across passes of the
// transform: vega-force keeps it as the operator's value and, on later passes,
// reconfigures it (new nodes, rebuilt forces, a restarted alpha) instead of
// starting over. The methods are the d3 calls vega-force makes.
type Simulation struct{ s *sim }

// NewSimulation is d3.forceSimulation(nodes) set up with p's parameters and
// forces, as vega-force's simulation() does on the first pass. Nothing has
// ticked yet. d3 initializes each force as it is added, so a link it cannot
// resolve fails here, after the nodes have been placed.
func NewSimulation(ctx context.Context, nodes []jsval.Value, p Params) (*Simulation, error) {
	s := &sim{
		alpha:         1,
		alphaMin:      0.001,
		alphaDecay:    1 - jsmath.Pow(0.001, 1.0/300),
		velocityDecay: 1 - 0.4,
		rng:           lcg{s: 1},
		ctx:           ctx,
	}
	if p.Bound&BoundAlpha != 0 {
		s.alpha = p.Alpha
	}
	if p.Bound&BoundAlphaMin != 0 {
		s.alphaMin = p.AlphaMin
	}
	if p.Bound&BoundAlphaTarget != 0 {
		s.alphaTarget = p.AlphaTarget
	}
	if p.Bound&BoundVelocityDecay != 0 {
		s.velocityDecay = 1 - p.VelocityDecay
	}
	if err := s.setNodes(nodes); err != nil {
		return nil, err
	}
	m := &Simulation{s}
	for i, f := range p.Forces {
		if err := m.SetForce(i, f); err != nil {
			return m, err
		}
	}
	return m, nil
}

// SetContext sets the context the simulation polls for cancellation.
func (m *Simulation) SetContext(ctx context.Context) { m.s.ctx = ctx }

// Tick is simulation.tick() (without the event).
func (m *Simulation) Tick() { m.s.tick() }

// Err is the context error a tick saw.
func (m *Simulation) Err() error { return m.s.err }

// WriteBack copies the simulation's positions onto the nodes.
func (m *Simulation) WriteBack() { m.s.writeBack() }

// The d3 simulation accessors vega-force uses.
func (m *Simulation) Alpha() float64             { return m.s.alpha }
func (m *Simulation) SetAlpha(a float64)         { m.s.alpha = a }
func (m *Simulation) AlphaMin() float64          { return m.s.alphaMin }
func (m *Simulation) SetAlphaMin(a float64)      { m.s.alphaMin = a }
func (m *Simulation) SetAlphaTarget(a float64)   { m.s.alphaTarget = a }
func (m *Simulation) SetAlphaDecay(a float64)    { m.s.alphaDecay = a }
func (m *Simulation) SetVelocityDecay(v float64) { m.s.velocityDecay = 1 - v }

// SetNodes is simulation.nodes(nodes): the nodes are initialized again (a node
// without a position gets its place on the phyllotaxis spiral) and every force
// is initialized with them.
func (m *Simulation) SetNodes(nodes []jsval.Value) error {
	if err := m.s.setNodes(nodes); err != nil {
		return err
	}
	for _, f := range m.s.forces {
		if err := f.initialize(m.s); err != nil {
			return err
		}
	}
	return nil
}

// SetForce is simulation.force(name, f) for the force at index i: it replaces
// the force there (or adds it after the last) and initializes it.
func (m *Simulation) SetForce(i int, f Force) error {
	if f == nil {
		return errors.New("force: nil force")
	}
	impl := f.build()
	if i < len(m.s.forces) {
		m.s.forces[i] = impl
	} else {
		m.s.forces = append(m.s.forces, impl)
	}
	return impl.initialize(m.s)
}

// Reinitialize hands the force at index i back to the simulation, which
// initializes it again so it reads its parameters anew.
func (m *Simulation) Reinitialize(i int) error {
	if i >= len(m.s.forces) {
		return nil
	}
	return m.s.forces[i].initialize(m.s)
}

// Trim removes the forces from index n on.
func (m *Simulation) Trim(n int) {
	if n < len(m.s.forces) {
		m.s.forces = m.s.forces[:n]
	}
}

// Pull reads the nodes' positions and velocities back from the objects: d3
// reads and writes node.x and the rest on the node objects themselves, so
// anything that changed them between passes is seen by the next tick.
func (m *Simulation) Pull() {
	for i, o := range m.s.objs {
		nd := &m.s.nodes[i]
		if v := o.Lookup("x"); !v.IsNullish() {
			nd.x = jsval.ToNumber(v)
		}
		if v := o.Lookup("y"); !v.IsNullish() {
			nd.y = jsval.ToNumber(v)
		}
		if v := o.Lookup("vx"); !v.IsNullish() {
			nd.vx = jsval.ToNumber(v)
		}
		if v := o.Lookup("vy"); !v.IsNullish() {
			nd.vy = jsval.ToNumber(v)
		}
	}
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func (s *sim) setNodes(vals []jsval.Value) error {
	n := len(vals)
	s.nodes = make([]node, n)
	s.objs = make([]*jsval.Object, n)
	s.vals = vals
	const initialRadius = 10
	initialAngle := math.Pi * (3 - math.Sqrt(5))
	for i, v := range vals {
		if !v.IsObj() {
			return fmt.Errorf("force: node %d is not an object", i)
		}
		o := v.ObjValue()
		s.objs[i] = o
		nd := &s.nodes[i]
		if fx := o.Lookup("fx"); !fx.IsNullish() {
			nd.hasFx, nd.fx = true, jsval.ToNumber(fx)
		}
		if fy := o.Lookup("fy"); !fy.IsNullish() {
			nd.hasFy, nd.fy = true, jsval.ToNumber(fy)
		}
		x, y := jsval.ToNumber(o.Lookup("x")), jsval.ToNumber(o.Lookup("y"))
		if nd.hasFx {
			x = nd.fx
		}
		if nd.hasFy {
			y = nd.fy
		}
		if math.IsNaN(x) || math.IsNaN(y) {
			r, a := initialRadius*math.Sqrt(0.5+float64(i)), float64(i)*initialAngle
			x, y = r*jsmath.Cos(a), r*jsmath.Sin(a)
		}
		vx, vy := jsval.ToNumber(o.Lookup("vx")), jsval.ToNumber(o.Lookup("vy"))
		if math.IsNaN(vx) || math.IsNaN(vy) {
			vx, vy = 0, 0
		}
		nd.x, nd.y, nd.vx, nd.vy = x, y, vx, vy
	}
	s.writeBack()
	return nil
}

func (s *sim) writeBack() {
	for i := range s.nodes {
		nd, o := &s.nodes[i], s.objs[i]
		o.Set("index", jsval.Int(i))
		o.Set("x", jsval.Num(nd.x))
		o.Set("y", jsval.Num(nd.y))
		o.Set("vx", jsval.Num(nd.vx))
		o.Set("vy", jsval.Num(nd.vy))
	}
}

func (s *sim) tick() {
	s.alpha += float64((s.alphaTarget - s.alpha) * s.alphaDecay)
	for _, f := range s.forces {
		f.apply(s.alpha)
	}
	for i := range s.nodes {
		nd := &s.nodes[i]
		if !nd.hasFx {
			nd.vx = float64(nd.vx * s.velocityDecay)
			nd.x += nd.vx
		} else {
			nd.x, nd.vx = nd.fx, 0
		}
		if !nd.hasFy {
			nd.vy = float64(nd.vy * s.velocityDecay)
			nd.y += nd.vy
		} else {
			nd.y, nd.vy = nd.fy, 0
		}
	}
}
