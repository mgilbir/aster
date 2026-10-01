package vega

import (
	"fmt"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
	"github.com/mgilbir/aster/internal/transforms/contour"
	"github.com/mgilbir/aster/internal/transforms/force"
	"github.com/mgilbir/aster/internal/transforms/voronoi"
)

func init() {
	tf := transformFactories
	ctxOf := func(n *opNode) contextT { return n.g.ctx }

	tf["density"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		dp := transforms.DensityParams{
			Method: p.str("method"), Extent: p.pair2("extent"), Steps: p.num("steps", 0),
			MinSteps: p.num("minsteps", 25), MaxSteps: p.num("maxsteps", 200), As: pairAs(p.strs("as")),
		}
		if sub, ok := p.Get("distribution").(*opParams); ok {
			dp.Distribution = distSpec(sub)
		}
		return transforms.Density(ctxOf(n), in, dp)
	})

	// The simulation is the operator's value (vega-force's Force.transform):
	// the first pass builds it and, unless static, ticks once; a later pass
	// reconfigures it and only a static one ticks again, since the ticks of a
	// running simulation come from a wall-clock timer that a static render
	// never reaches.
	tf["force"] = statefulTransform(func() txFn {
		var sim *force.Simulation
		var last []jsval.Value
		return func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
			static := p.bool("static")
			iters := p.num("iterations", 300)
			if iters == 0 || math.IsNaN(iters) {
				iters = 300
			}
			if iters > force.MaxIterations {
				return in, fmt.Errorf("%w: force: iterations %v exceeds limit %d", budget.ErrLimit, iters, force.MaxIterations)
			}
			var fs []force.Force
			for _, x := range p.list("forces") {
				if sub, _ := x.(*opParams); sub != nil {
					fs = append(fs, forceOf(sub))
				}
			}
			// pulse.changed(ADD_REM): the nodes are not the ones simulated.
			change := len(in) != len(last)
			for i := 0; !change && i < len(in); i++ {
				change = in[i].ObjValue() != last[i].ObjValue()
			}
			last = slices.Clone(in)
			paramsMod := p.Modified("alpha", "alphaMin", "alphaTarget", "velocityDecay", "forces")
			ctx := n.g.ctx
			if sim == nil {
				fp := force.DefaultParams()
				fp.Forces = fs
				var err error
				if sim, err = force.NewSimulation(ctx, in, fp); err != nil {
					return in, err
				}
				if !static {
					change = true
					sim.Tick()
				}
			} else {
				sim.SetContext(ctx)
				sim.Pull()
				if change {
					if err := sim.SetNodes(in); err != nil {
						return in, err
					}
				}
				if paramsMod {
					if p.Modified("alpha") {
						sim.SetAlpha(p.num("alpha", 1))
					}
					if p.Modified("alphaMin") {
						sim.SetAlphaMin(p.num("alphaMin", 0.001))
					}
					if p.Modified("alphaTarget") {
						sim.SetAlphaTarget(p.num("alphaTarget", 0))
					}
					if p.Modified("velocityDecay") {
						sim.SetVelocityDecay(p.num("velocityDecay", 0.4))
					}
					if p.Modified("forces") {
						for i, f := range fs {
							if err := sim.SetForce(i, f); err != nil {
								return in, err
							}
						}
						sim.Trim(len(fs))
					}
				}
			}
			if paramsMod || change || p.Modified("static", "iterations") {
				alpha := p.num("alpha", 1)
				if alpha == 0 || math.IsNaN(alpha) {
					alpha = 1
				}
				sim.SetAlpha(math.Max(sim.Alpha(), alpha))
				sim.SetAlphaDecay(1 - jsmath.Pow(sim.AlphaMin(), 1/iters))
				if static {
					for i := int(iters); i > 0; i-- {
						if err := ctx.Err(); err != nil {
							return in, err
						}
						sim.Tick()
						if err := sim.Err(); err != nil {
							return in, err
						}
					}
				} else if !change {
					sim.WriteBack()
					return in, errStopPulse // defer to the simulation's own ticks
				}
			}
			sim.WriteBack()
			return in, sim.Err()
		}
	})

	tf["voronoi"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		vp := voronoi.Params{
			X: p.field("x").Get, Y: p.field("y").Get, Size: p.nums("size"),
			As: p.str("as"),
		}
		vp.Extent = extent4(p)
		return in, voronoi.Transform(ctxOf(n), in, vp)
	})

	tf["contour"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		cp := contour.ContourParams{
			Size: p.nums("size"), CellSize: p.num("cellSize", 0), Bandwidth: p.nums("bandwidth"),
			Count: p.num("count", 0), Nice: p.bool("nice"),
		}
		if p.has("values") {
			cp.Values = p.nums("values")
			if cp.Values == nil {
				cp.Values = []float64{}
			}
		}
		if p.has("thresholds") {
			cp.Thresholds = p.nums("thresholds")
			if cp.Thresholds == nil {
				cp.Thresholds = []float64{}
			}
		}
		if f := p.field("x"); !f.IsNil() {
			cp.X = contour.Accessor(f.Get)
		}
		if f := p.field("y"); !f.IsNil() {
			cp.Y = contour.Accessor(f.Get)
		}
		if f := p.field("weight"); !f.IsNil() {
			cp.Weight = contour.Accessor(f.Get)
		}
		if p.has("smooth") {
			b := p.bool("smooth")
			cp.Smooth = &b
		}
		return contour.Contour(ctxOf(n), in, cp)
	})

	tf["isocontour"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		ip := contour.IsocontourParams{
			Levels: p.num("levels", 0), Nice: p.bool("nice"), Shared: p.str("resolve") == "shared",
			As: p.str("as"), Scale: p.nums("scale"), Translate: p.nums("translate"),
		}
		if f := p.field("field"); !f.IsNil() {
			ip.Field = contour.Accessor(f.Get)
		}
		if p.has("thresholds") {
			ip.Thresholds = p.nums("thresholds")
			if ip.Thresholds == nil {
				ip.Thresholds = []float64{}
			}
		}
		if p.has("zero") {
			b := p.bool("zero")
			ip.Zero = &b
		}
		if p.has("smooth") {
			b := p.bool("smooth")
			ip.Smooth = &b
		}
		if b, ok := p.Get("scale").(transforms.Field); ok {
			ip.ScaleFn = func(d jsval.Value) []float64 { return numList(b.Apply(d)) }
		}
		if b, ok := p.Get("translate").(transforms.Field); ok {
			ip.TranslateFn = func(d jsval.Value) []float64 { return numList(b.Apply(d)) }
		}
		return contour.Isocontour(ctxOf(n), in, ip)
	})

	tf["kde2d"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		kp := contour.KDE2DParams{Counts: p.bool("counts"), As: p.str("as")}
		kp.Size = p.nums("size")
		kp.X = contour.Accessor(p.field("x").Get)
		kp.Y = contour.Accessor(p.field("y").Get)
		if f := p.field("weight"); !f.IsNil() {
			kp.Weight = contour.Accessor(f.Get)
		}
		kp.CellSize = p.num("cellSize", 0)
		kp.Bandwidth = p.nums("bandwidth")
		if p.has("groupby") {
			kp.GroupBy = []contour.GroupField{}
			for _, f := range p.fields("groupby") {
				kp.GroupBy = append(kp.GroupBy, contour.GroupField{Name: f.Name, Get: contour.Accessor(f.Get)})
			}
		}
		return contour.KDE2D(ctxOf(n), in, kp)
	})

	tf["linkpath"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return in, linkPath(p, in, n.g.view.jsString)
	})
}

func numList(v jsval.Value) []float64 {
	if v.IsArr() {
		out := make([]float64, v.Len())
		for i, x := range v.Items() {
			out[i] = jsval.ToNumber(x)
		}
		return out
	}
	if v.IsNullish() {
		return nil
	}
	return []float64{jsval.ToNumber(v)}
}

// extent4 reads voronoi's [[x0, y0], [x1, y1]] extent as x0, y0, x1, y1.
func extent4(p *opParams) []float64 {
	e := p.extent2x2("extent")
	if e == nil {
		return nil
	}
	return []float64{e[0][0], e[0][1], e[1][0], e[1][1]}
}

// distSpec reads a density distribution sub-parameter.
func distSpec(p *opParams) transforms.DistSpec {
	d := transforms.DistSpec{Kind: p.str("function")}
	opt := func(name string) *float64 {
		if v := p.Value(name); !v.IsNullish() {
			f := jsval.ToNumber(v)
			return &f
		}
		return nil
	}
	d.Mean, d.Stdev, d.Min, d.Max = opt("mean"), opt("stdev"), opt("min"), opt("max")
	switch d.Kind {
	case "kde":
		d.Field = p.field("field")
		if from, ok := p.Get("from").([]jsval.Value); ok {
			d.From = from
		}
		d.Bandwidth = p.num("bandwidth", 0)
	case "mixture":
		for _, x := range p.list("distributions") {
			if sub, ok := x.(*opParams); ok {
				d.Distributions = append(d.Distributions, distSpec(sub))
			}
		}
		d.Weights = p.nums("weights")
	}
	return d
}

// forceParam reads a number-or-expression force parameter.
func forceParam(p *opParams, name string, def float64) force.Param {
	switch v := p.vals.at(name).(type) {
	case transforms.Field:
		return force.Param{Fn: force.Accessor(v.Get)}
	case *boundExpr:
		return force.Param{Fn: force.Accessor(v.call)}
	case jsval.Value:
		if !v.IsNullish() {
			return force.Constant(jsval.ToNumber(v))
		}
	}
	return force.Constant(def)
}

func forceOf(p *opParams) force.Force {
	switch p.str("force") {
	case "center":
		return force.Center{X: p.num("x", 0), Y: p.num("y", 0)}
	case "collide":
		c := force.NewCollide()
		c.Radius = forceParam(p, "radius", 1)
		if p.has("strength") {
			c.Strength = p.num("strength", 1)
		}
		if p.has("iterations") {
			c.Iterations = clampInt(p.num("iterations", 1))
		}
		return c
	case "nbody":
		b := force.NewNBody()
		b.Strength = forceParam(p, "strength", -30)
		if p.has("theta") {
			b.Theta = p.num("theta", 0.9)
		}
		if p.has("distanceMin") {
			b.DistanceMin = p.num("distanceMin", 1)
		}
		if p.has("distanceMax") {
			b.DistanceMax = p.num("distanceMax", 0)
		}
		return b
	case "link":
		links, _ := p.Get("links").([]jsval.Value)
		l := force.NewLink(links)
		if f := p.field("id"); !f.IsNil() {
			l.ID = force.Accessor(f.Get)
		}
		l.Distance = forceParam(p, "distance", 30)
		if p.has("strength") {
			s := forceParam(p, "strength", 0)
			l.Strength = &s
		}
		if p.has("iterations") {
			l.Iterations = clampInt(p.num("iterations", 1))
		}
		return l
	case "x":
		x := force.NewX()
		if p.has("strength") {
			x.Strength = p.num("strength", 0.1)
		}
		if f := p.field("x"); !f.IsNil() {
			x.X = force.Param{Fn: force.Accessor(f.Get)}
		}
		return x
	case "y":
		y := force.NewY()
		if p.has("strength") {
			y.Strength = p.num("strength", 0.1)
		}
		if f := p.field("y"); !f.IsNil() {
			y.Y = force.Param{Fn: force.Accessor(f.Get)}
		}
		return y
	}
	fail("unsupported force: %s", p.str("force"))
	return nil
}
