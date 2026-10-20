package vega

import (
	"fmt"
	"math"
	"slices"
	"strconv"

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
		steps := p.stepsSpec()
		dp := transforms.DensityParams{
			Method: densityMethod(p.Value("method")), ExtentValues: p.extentValues("extent"),
			StepsValues: &steps, As: p.asPair("value", "density"), AsExact: true,
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
			iters := p.orNumber("iterations", 300) // _.iterations || 300
			if iters > force.MaxIterations {
				return in, fmt.Errorf("%w: force: iterations %v exceeds limit %d", budget.ErrLimit, iters, force.MaxIterations)
			}
			// getForce throws for a parameter it cannot convert, after the
			// nodes of a first pass are placed (forceSimulation(nodes) comes
			// first) and after the forces before it are set: build them one
			// at a time and hold the throw until then.
			var fs []force.Force
			var forceFault any
			fp := force.DefaultParams()
			func() {
				defer func() { forceFault = recover() }()
				// setup(sim, _, true): `_.modified(p)` for the simulation's own
				// parameters, set as +_[p]; a signal's first value is modified
				for _, b := range []struct {
					name string
					bit  force.Bound
					dst  *float64
				}{{"alpha", force.BoundAlpha, &fp.Alpha}, {"alphaMin", force.BoundAlphaMin, &fp.AlphaMin},
					{"alphaTarget", force.BoundAlphaTarget, &fp.AlphaTarget}, {"velocityDecay", force.BoundVelocityDecay, &fp.VelocityDecay}} {
					if p.Modified(b.name) {
						*b.dst = p.givenNum(b.name, *b.dst)
						fp.Bound |= b.bit
					}
				}
				for _, x := range p.list("forces") {
					if sub, _ := x.(*opParams); sub != nil {
						fs = append(fs, forceOf(sub))
					}
				}
			}()
			if forceFault != nil && sim != nil {
				panic(forceFault)
			}
			// pulse.changed(ADD_REM): the nodes are not the ones simulated.
			change := len(in) != len(last)
			for i := 0; !change && i < len(in); i++ {
				change = jsval.Tuple(in[i]) != jsval.Tuple(last[i])
			}
			last = slices.Clone(in)
			paramsMod := p.Modified("alpha", "alphaMin", "alphaTarget", "velocityDecay", "forces")
			ctx := n.g.ctx
			if sim == nil {
				fp.Forces = fs
				var err error
				if sim, err = force.NewSimulation(ctx, in, fp); err != nil {
					return in, err
				}
				if forceFault != nil {
					panic(forceFault)
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
				alpha := p.orNumber("alpha", 1) // _.alpha || 1
				sim.SetAlpha(math.Max(sim.Alpha(), alpha))
				sim.SetAlphaDecay(1 - jsmath.Pow(sim.AlphaMin(), 1/iters))
				if static {
					// for (sim.stop(); --iters >= 0;): floor(iters) ticks, none
					// for a negative count (compared before it is an int).
					ticks := 0
					if iters > 0 {
						ticks = int(iters)
					}
					for i := ticks; i > 0; i-- {
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
		vp := voronoi.Params{X: p.field("x").Get, Y: p.field("y").Get, As: p.textOr("as", "path")}
		if len(in) > 0 { // nothing is read for no data
			vp.Extent = voronoiBounds(p)
		}
		return in, voronoi.Transform(ctxOf(n), in, vp)
	})

	tf["contour"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		cp := contour.ContourParams{Size: p.nums("size"), Count: p.orNumber("count", 10), CountExact: true,
			CountPlusOne: p.orPlusOne("count", 10), Nice: p.bool("nice")}
		if !paramTruthy(p.Get("values")) {
			// params(density2D(), _) only when the contours are of a density
			cp.Size, cp.CellSize, cp.Bandwidth = density2DParams(p)
			if cp.Size == nil {
				cp.Size = []float64{960, 500}
			}
		}
		// values = _.values: when truthy, the grid as given; the marching
		// compares its numbers (null is 0), the thresholds' extent skips
		// null and NaN.
		if vs := p.Get("values"); paramTruthy(vs) {
			// contour.size(size) floors _[0] and _[1] as numbers: null is 0
			if x := p.Get("size"); !paramNullish(x) {
				cp.Size = []float64{jsval.ToNumber(paramElem(x, 0)), jsval.ToNumber(paramElem(x, 1))}
			}
			raw, _ := paramElems(n.g, vs)
			cp.Values, cp.ValuesExtent = make([]float64, len(raw)), make([]float64, len(raw))
			for i, v := range raw {
				cp.Values[i] = jsval.ToNumber(v)
				cp.ValuesExtent[i] = cp.Values[i]
				if v.IsNullish() {
					cp.ValuesExtent[i] = math.NaN()
				}
			}
			cp.ExtentLo, cp.ExtentHi = rawExtent(n.g.view.walk, raw)
			cp.ExtentSet = true
		}
		// thresh = _.thresholds || quantize(...); isArray(thresh) ? thresh :
		// thresh(values)
		if th := p.Get("thresholds"); paramTruthy(th) {
			if !paramIsArray(th) {
				fail("TypeError: thresh is not a function")
			}
			cp.ThresholdValues, _ = paramElems(n.g, th)
			cp.Thresholds = make([]float64, len(cp.ThresholdValues))
			for i, v := range cp.ThresholdValues {
				cp.Thresholds[i] = jsval.ToNumber(v)
			}
		}
		if f := p.field("x"); !f.IsNil() {
			cp.X = densityAccessor(p.Get("x"), f)
		}
		if f := p.field("y"); !f.IsNil() {
			cp.Y = densityAccessor(p.Get("y"), f)
		}
		if f := p.field("weight"); !f.IsNil() {
			cp.Weight = densityAccessor(p.Get("weight"), f)
		}
		smooth := !strictFalse(p.Get("smooth")) // _.smooth !== false
		cp.Smooth = &smooth
		return contour.Contour(ctxOf(n), in, cp)
	})

	tf["isocontour"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		ip := contour.IsocontourParams{
			Levels: p.orNumber("levels", 10), LevelsExact: true, LevelsPlusOne: p.orPlusOne("levels", 10), Nice: p.bool("nice"), Shared: strictIs(p.Value("resolve"), "shared"),
			Transform: isocontourTransform(p),
		}
		// as = _.as === null ? null : _.as || 'contour'
		if av, ok := paramValueOf(p.Get("as")); ok && av.IsNull() {
			ip.NoAs = true
		} else {
			ip.As = p.textOr("as", "contour")
		}
		if f := p.field("field"); !f.IsNil() {
			ip.Field = contour.Accessor(f.Get)
		}
		// tz = _.thresholds || levels(...); isArray(tz) ? tz : tz(values)
		if th := p.Get("thresholds"); paramTruthy(th) {
			if paramIsArray(th) {
				ip.ThresholdValues, _ = paramElems(n.g, th)
				ip.Thresholds = make([]float64, len(ip.ThresholdValues))
				for i, v := range ip.ThresholdValues {
					ip.Thresholds[i] = jsval.ToNumber(v)
				}
			} else {
				ip.ThresholdsNotArray = true
				ip.Thresholds = []float64{}
			}
		}
		// _.zero !== false, _.smooth !== false
		zero := !strictFalse(p.Get("zero"))
		smooth := !strictFalse(p.Get("smooth"))
		ip.Zero, ip.Smooth = &zero, &smooth
		return contour.Isocontour(ctxOf(n), in, ip)
	})
	tf["kde2d"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupByList()
		kp := contour.KDE2DParams{Counts: p.bool("counts"), As: p.str("as"), KeyString: transforms.KeyStringer(ctxOf(n))}
		kp.Size, kp.CellSize, kp.Bandwidth = density2DParams(p)
		// params(): `_[p] != null ? obj[p](_[p])`: a null accessor keeps
		// density2D's d => d[0] and d => d[1]
		if f := p.field("x"); !f.IsNil() {
			kp.X = densityAccessor(p.Get("x"), f)
		}
		if f := p.field("y"); !f.IsNil() {
			kp.Y = densityAccessor(p.Get("y"), f)
		}
		if f := p.field("weight"); !f.IsNil() {
			kp.Weight = densityAccessor(p.Get("weight"), f)
		}
		if p.has("groupby") {
			kp.GroupBy = []contour.GroupField{}
			for _, f := range p.fields("groupby") {
				kp.GroupBy = append(kp.GroupBy, contour.GroupField{Name: f.Key(), Get: contour.Accessor(f.Get)})
			}
		}
		return contour.KDE2D(ctxOf(n), in, kp)
	})

	tf["linkpath"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return in, linkPath(p, in, n.g.view.jsString)
	})
}

// distSpec reads a density distribution sub-parameter.
func distSpec(p *opParams) transforms.DistSpec {
	d := transforms.DistSpec{Kind: p.str("function")}
	// The distributions' setters: mean and min keep `_ || 0`, stdev and max
	// `_ == null ? 1 : _`; the value then computes as a number (NaN for a
	// truthy one that is none).
	orZero := func(name string) *float64 {
		f := 0.0
		if v := p.Value(name); v.IsTruthy() {
			f = jsval.ToNumber(v)
		}
		return &f
	}
	orOne := func(name string) *float64 {
		f := 1.0
		if v := p.Value(name); !v.IsNullish() {
			f = jsval.ToNumber(v)
		}
		return &f
	}
	d.Mean, d.Stdev, d.Min, d.Max = orZero("mean"), orOne("stdev"), orZero("min"), orOne("max")
	switch d.Kind {
	case "kde":
		d.Field = p.field("field")
		d.FieldNull = d.Field.IsNil()
		if from, ok := p.Get("from").([]jsval.Value); ok {
			d.From = from
		}
		bw := p.Value("bandwidth")
		d.Bandwidth, d.BandwidthGiven = jsval.ToNumber(bw), bw.IsTruthy()
		if !d.BandwidthGiven {
			d.Bandwidth = 0
		}
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
	case nil:
		return force.Constant(def)
	}
	// setForceParam(f[p], _[p]) for any value given, null included: +v
	v, _ := paramValueOf(p.vals.at(name))
	return force.Constant(jsval.ToNumber(v))
}

func forceOf(p *opParams) force.Force {
	switch p.str("force") {
	case "center":
		return force.Center{X: p.givenNum("x", 0), Y: p.givenNum("y", 0)}
	case "collide":
		c := force.NewCollide()
		c.Radius = forceParam(p, "radius", 1)
		if _, ok := p.given("strength"); ok {
			c.Strength = p.givenNum("strength", 1)
		}
		if _, ok := p.given("iterations"); ok {
			c.Iterations = p.givenNum("iterations", 1)
		}
		return c
	case "nbody":
		b := force.NewNBody()
		b.Strength = forceParam(p, "strength", -30)
		if _, ok := p.given("theta"); ok {
			b.Theta = p.givenNum("theta", 0.9)
		}
		if _, ok := p.given("distanceMin"); ok {
			b.DistanceMin = p.givenNum("distanceMin", 1)
		}
		if _, ok := p.given("distanceMax"); ok {
			b.DistanceMax = p.givenNum("distanceMax", 0)
		}
		return b
	case "link":
		links, fault := forceLinks(p.Get("links"))
		l := force.NewLink(links)
		l.Fault = fault
		if f := p.field("id"); !f.IsNil() {
			l.ID = force.Accessor(f.Get)
		}
		l.Distance = forceParam(p, "distance", 30)
		if _, ok := p.given("strength"); ok {
			s := forceParam(p, "strength", 0)
			l.Strength = &s
		}
		if _, ok := p.given("iterations"); ok {
			l.Iterations = p.givenNum("iterations", 1)
		}
		return l
	case "x":
		x := force.NewX()
		if _, ok := p.given("strength"); ok {
			x.Strength = p.givenNum("strength", 0.1)
		}
		if f := p.field("x"); !f.IsNil() {
			x.X = force.Param{Fn: force.Accessor(f.Get)}
		}
		return x
	case "y":
		y := force.NewY()
		if _, ok := p.given("strength"); ok {
			y.Strength = p.givenNum("strength", 0.1)
		}
		if f := p.field("y"); !f.IsNil() {
			y.Y = force.Param{Fn: force.Accessor(f.Get)}
		}
		return y
	}
	fail("unsupported force: %s", p.str("force"))
	return nil
}

// isocontourTransform is Isocontour's transformPaths: s = _.scale ||
// grid.scale and t = _.translate || grid.translate, each called with the
// tuple when it is a function; nothing is done when (s === 1 || s == null)
// && !t; otherwise sx = (isNumber(s) ? s : s[0]) || 1, sy likewise with
// s[1], tx = t && t[0] || 0, ty likewise.
func isocontourTransform(p *opParams) func(datum, grid jsval.Value) (float64, float64, float64, float64, bool, error) {
	pick := func(name string, datum, grid jsval.Value) jsval.Value {
		switch x := p.Get(name).(type) {
		case transforms.Field:
			if !x.IsNil() {
				return x.Apply(datum)
			}
		case *boundExpr:
			return x.call(datum)
		default:
			if paramTruthy(x) {
				return paramJS(x)
			}
		}
		if grid.IsNullish() {
			return jsval.Undefined
		}
		return jsval.Member(grid, name)
	}
	orDefault := func(v jsval.Value, def float64) float64 {
		if !v.IsTruthy() {
			return def
		}
		return jsval.ToNumber(v)
	}
	return func(datum, grid jsval.Value) (float64, float64, float64, float64, bool, error) {
		s := pick("scale", datum, grid)
		t := pick("translate", datum, grid)
		if (s.IsNullish() || s.IsNum() && s.NumValue() == 1) && !t.IsTruthy() {
			return 0, 0, 0, 0, false, nil
		}
		var sx, sy jsval.Value
		switch {
		case s.IsNum():
			sx, sy = s, s
		case s.IsNullish():
			return 0, 0, 0, 0, false, &jsval.Thrown{Name: "TypeError",
				Msg: "Cannot read properties of " + nullishName(s) + " (reading '0')"}
		default:
			sx, sy = jsval.Member(s, "0"), jsval.Member(s, "1")
		}
		tx, ty := 0.0, 0.0
		if t.IsTruthy() {
			tx, ty = orDefault(jsval.Member(t, "0"), 0), orDefault(jsval.Member(t, "1"), 0)
		}
		return orDefault(sx, 1), orDefault(sy, 1), tx, ty, true, nil
	}
}

// density2DParams reads the size, cellSize and bandwidth parameters as
// vega-geo's params(density2D(), _) sets them, each when it is not null:
// size [+_[0], +_[1]], both at least 0 ('invalid size'); cellSize +_, at
// least 1 ('invalid cell size'); bandwidth array(_), one entry doubled,
// else exactly two ('invalid bandwidth'). nil and 0 are the defaults.
func density2DParams(p *opParams) (size []float64, cellSize float64, bandwidth []float64) {
	if x := p.Get("size"); !paramNullish(x) {
		s0, s1 := jsval.ToNumber(paramElem(x, 0)), jsval.ToNumber(paramElem(x, 1))
		if !(s0 >= 0 && s1 >= 0) {
			fail("invalid size")
		}
		size = []float64{s0, s1}
	}
	if x := p.Get("cellSize"); !paramNullish(x) {
		v, _ := paramValueOf(x)
		if cellSize = jsval.ToNumber(v); !(cellSize >= 1) {
			fail("invalid cell size")
		}
	}
	if x := p.Get("bandwidth"); !paramNullish(x) {
		l := paramArray(x)
		num := func(e any) float64 { v, _ := paramValueOf(e); return jsval.ToNumber(v) }
		switch len(l) {
		case 1:
			bandwidth = []float64{num(l[0]), num(l[0])}
		case 2:
			bandwidth = []float64{num(l[0]), num(l[1])}
		default:
			fail("invalid bandwidth")
		}
	}
	return size, cellSize, bandwidth
}

// voronoiBounds is Voronoi's clip extent: s = _.size, when truthy [0, 0,
// s[0], s[1]], else s = _.extent, when truthy [s[0][0], s[0][1], s[1][0],
// s[1][1]] (a TypeError for an undefined s[0] or s[1]), each as d3's
// voronoi(bounds) makes it a number; nil for the default.
func voronoiBounds(p *opParams) []float64 {
	at := func(v jsval.Value, i string) jsval.Value {
		if v.IsNullish() {
			fail("TypeError: Cannot read properties of %s (reading '%s')", nullishName(v), i)
		}
		return jsval.Member(v, i)
	}
	if x := p.Get("size"); paramTruthy(x) {
		s := paramJS(x)
		return []float64{0, 0, jsval.ToNumber(at(s, "0")), jsval.ToNumber(at(s, "1"))}
	}
	if x := p.Get("extent"); paramTruthy(x) {
		s := paramJS(x)
		s0, s1 := at(s, "0"), at(s, "1")
		return []float64{jsval.ToNumber(at(s0, "0")), jsval.ToNumber(at(s0, "1")),
			jsval.ToNumber(at(s1, "0")), jsval.ToNumber(at(s1, "1"))}
	}
	return nil
}

// rawExtent is vega-util's extent over values of any type, as numbers: the
// first value that is not null, undefined or NaN starts both ends, the rest
// compare with JavaScript's < and > (a string or an array against a number is
// NaN, so it stays), and none valid is undefined, NaN here.
func rawExtent(w jsval.Walker, vals []jsval.Value) (lo, hi float64) {
	i := 0
	for i < len(vals) && (vals[i].IsNullish() || (vals[i].IsNum() && vals[i].NumValue() != vals[i].NumValue())) {
		i++
	}
	if i == len(vals) {
		return math.NaN(), math.NaN()
	}
	min, max := vals[i], vals[i]
	for ; i < len(vals); i++ {
		v := vals[i]
		if v.IsNullish() {
			continue
		}
		if transforms.Less(w, v, min) {
			min = v
		}
		if transforms.Greater(w, v, max) {
			max = v
		}
	}
	return jsval.ToNumber(min), jsval.ToNumber(max)
}

// forceLinks is the links a link force holds: forceLink(links) keeps what it
// is given as it is (`if (links == null) links = []` only at construction),
// and initialize reads links.length and links[i]. A data set is a list; a
// null throws there; a string's characters are primitives that take no
// index; a number or boolean has no length, so no links; an object with a
// length has the elements it holds.
func forceLinks(x any) ([]jsval.Value, error) {
	if l, ok := x.([]jsval.Value); ok {
		return l, nil
	}
	if x == nil {
		return nil, nil // not given: []
	}
	v := paramJS(x)
	switch {
	case v.IsNullish():
		return nil, &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + nullishName(v) + " (reading 'length')"}
	case v.IsArr():
		return v.Items(), nil
	}
	ln := jsval.ToNumber(jsval.Member(v, "length"))
	if !(ln >= 1) {
		return nil, nil
	}
	if v.IsStr() {
		return nil, &jsval.Thrown{Name: "TypeError", Msg: "Cannot create property 'index' on string"}
	}
	if ln > 1<<20 {
		return nil, fmt.Errorf("%w: force: %v links", budget.ErrLimit, ln)
	}
	out := make([]jsval.Value, int(ln))
	for i := range out {
		out[i] = jsval.Member(v, strconv.Itoa(i))
	}
	return out, nil
}

// densityAccessor is a density2D accessor parameter: a function is used, any
// other value becomes constant(+value); a list of accessors (a Field operator
// given an array) is +[...] NaN, or 0 for none.
func densityAccessor(raw any, f transforms.Field) contour.Accessor {
	if f.Uncallable {
		v := math.NaN()
		if l, ok := raw.([]transforms.Field); ok && len(l) == 0 {
			v = 0
		}
		return func(jsval.Value) jsval.Value { return jsval.Num(v) }
	}
	return contour.Accessor(f.Get)
}
