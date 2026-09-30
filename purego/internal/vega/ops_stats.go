package vega

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/transforms"
	"github.com/mgilbir/aster/purego/internal/transforms/contour"
	"github.com/mgilbir/aster/purego/internal/transforms/force"
	"github.com/mgilbir/aster/purego/internal/transforms/voronoi"
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

	tf["force"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		fp := force.DefaultParams()
		fp.Static = p.bool("static")
		if v := p.num("iterations", 300); v != 0 {
			fp.Iterations = int(v)
		}
		fp.Alpha = p.num("alpha", 1)
		fp.AlphaMin = p.num("alphaMin", 0.001)
		fp.AlphaTarget = p.num("alphaTarget", 0)
		fp.VelocityDecay = p.num("velocityDecay", 0.4)
		for _, x := range p.list("forces") {
			if sub, _ := x.(*opParams); sub != nil {
				fp.Forces = append(fp.Forces, forceOf(sub))
			}
		}
		return in, force.Run(n.g.ctx, in, fp)
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
		return in, linkPath(p, in)
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
	switch v := p.vals[name].(type) {
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
			c.Iterations = int(p.num("iterations", 1))
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
			l.Iterations = int(p.num("iterations", 1))
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
