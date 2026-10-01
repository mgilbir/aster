package transforms

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// Replays of upstream's own tests (see internal/upstream): vega-statistics and the parts of d3-array
// that this package ports.

func identity(v jsval.Value) jsval.Value { return v }

func numbersOfArg(v any) []float64 {
	items, _ := v.([]any)
	out := make([]float64, len(items))
	for i, x := range items {
		out[i] = upstream.Number(x)
	}
	return out
}

func valuesOfArg(v any) []jsval.Value {
	return upstream.ToValue(v).Items()
}

// nullish is JavaScript's `v == null` for a recorded value.
func nullish(v any) bool { return v == nil || upstream.IsUndefined(v) }

// accessorOf resolves a recorded accessor. vega-statistics' regression tests name theirs x and y:
// `x = d => d[0], y = d => d[1]`.
func accessorOf(v any) (Accessor, bool) {
	m, ok := v.(map[string]any)
	if !ok || m["$"] != "function" {
		return nil, false
	}
	switch m["name"] {
	case "x":
		return func(d jsval.Value) jsval.Value { return d.Index(0) }, true
	case "y":
		return func(d jsval.Value) jsval.Value { return d.Index(1) }, true
	}
	return nil, false
}

// instance is a distribution made by a recorded random* call, with the state a stream of draws leaves.
type instance struct {
	dist    Distribution
	kde     *KernelDensity
	applied int // configuration steps already applied
}

func newDistributionFor(fn string, args []any) *instance {
	at := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return upstream.Undefined()
	}
	switch fn {
	case "randomNormal":
		mean, stdev := 0.0, 1.0
		if !nullish(at(0)) {
			mean = upstream.Number(at(0))
		}
		if !nullish(at(1)) {
			stdev = upstream.Number(at(1))
		}
		return &instance{dist: NewNormal(mean, stdev)}
	case "randomUniform":
		lo, hi := at(0), at(1)
		if nullish(hi) {
			if nullish(lo) {
				hi = 1.0
			} else {
				hi = lo
			}
			lo = 0.0
		}
		return &instance{dist: NewUniform(upstream.Number(lo), upstream.Number(hi))}
	case "randomInteger":
		lo, hi := at(0), at(1)
		if upstream.IsUndefined(hi) {
			hi = lo
			lo = 0.0
		}
		return &instance{dist: NewInteger(upstream.Number(lo), upstream.Number(hi))}
	case "randomKDE":
		bandwidth := 0.0
		if !nullish(at(1)) {
			bandwidth = upstream.Number(at(1))
		}
		k := NewKernelDensity(valuesOfArg(at(0)), bandwidth)
		return &instance{dist: k, kde: k}
	}
	return nil
}

func TestUpstreamVegaStatistics(t *testing.T) {
	r := upstream.Start(t, "vega-statistics")
	var rand Rand // the stream setRandom installed
	lcgs := map[float64]Rand{}
	instances := map[string]*instance{}
	key := func(fn string, args []any) string {
		b, _ := json.Marshal(args)
		return fn + string(b)
	}
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		n := func(i int) float64 { return upstream.Number(c.Arg(i)) }
		// mean and stdev default to 0 and 1 for the closed-form functions.
		meanStdev := func() (float64, float64) {
			mean, stdev := 0.0, 1.0
			if !nullish(c.Arg(1)) {
				mean = n(1)
			}
			if !nullish(c.Arg(2)) {
				stdev = n(2)
			}
			return mean, stdev
		}
		switch c.Fn {
		case "randomLCG":
			if seed, ok := upstream.Num(c.Arg(0)); ok {
				lcgs[seed] = LCG(seed)
			}
			r.Skip("constructions (a function is the answer)")
		case "randomLCG()":
			seed, _ := upstream.Num(c.ConstructedWith[0])
			gen, ok := lcgs[seed]
			if !ok {
				r.Skip("a generator whose construction was not recorded")
				continue
			}
			r.Check(c, upstream.Enc(gen()), false)
		case "setRandom":
			origin, isFn := upstream.IsFunction(c.Arg(0))
			if !isFn || origin == nil || origin["from"] != "randomLCG" {
				rand = nil
				r.Skip("setRandom with a generator the recording cannot identify")
				continue
			}
			args, _ := origin["args"].([]any)
			rand = LCG(upstream.Number(args[0]))
			r.Skip("setRandom")
		case "randomNormal", "randomUniform", "randomInteger", "randomKDE", "randomMixture":
			var in *instance
			if c.Fn == "randomMixture" {
				in = newMixture(c.Args)
			} else if !upstream.Contains(c.Args, "function") {
				in = newDistributionFor(c.Fn, c.Args)
			}
			if in == nil {
				r.Skip("distributions built from functions the recording cannot identify")
				delete(instances, key(c.Fn+"()", c.Args))
				continue
			}
			instances[key(c.Fn+"()", c.Args)] = in
			r.Skip("constructions (an object is the answer)")
		case "randomNormal()", "randomUniform()", "randomInteger()", "randomKDE()", "randomMixture()":
			in := instances[key(c.Fn, c.ConstructedWith)]
			if in == nil {
				r.Skip("instances whose construction was not recorded")
				continue
			}
			steps := c.ChainSteps()
			for ; in.applied < len(steps); in.applied++ {
				applyDistributionStep(in, steps[in.applied])
			}
			x := n(0)
			switch c.Method {
			case "sample":
				if rand == nil {
					r.Skip("draws before setRandom")
					continue
				}
				// vega-statistics keeps the second normal of a Box-Muller pair in one module-level
				// variable that every instance (and a kernel density's kernel) shares and a new
				// seed does not clear; here each distribution owns its cache. The replay hands the
				// shared one to whichever distribution draws, which is the same arithmetic.
				r.Check(c, upstream.Enc(sampleShared(in.dist, rand)), false)
			case "pdf":
				r.Check(c, upstream.Enc(in.dist.PDF(x)), false)
			case "cdf":
				r.Check(c, upstream.Enc(in.dist.CDF(x)), false)
			case "icdf":
				v, err := in.dist.ICDF(x)
				if err != nil {
					r.Check(c, nil, true)
					continue
				}
				r.Check(c, upstream.Enc(v), false)
			case "bandwidth":
				if in.kde == nil || len(c.Args) != 0 {
					r.Skip("bandwidth setter")
					continue
				}
				r.Check(c, upstream.Enc(in.kde.Bandwidth), false)
			default:
				r.Skip("unmapped method " + c.Method)
			}
		case "densityNormal", "cumulativeNormal", "quantileNormal", "densityLogNormal", "cumulativeLogNormal", "quantileLogNormal":
			mean, stdev := meanStdev()
			x := n(0)
			var got float64
			switch c.Fn {
			case "densityNormal":
				got = DensityNormal(x, mean, stdev)
			case "cumulativeNormal":
				got = CumulativeNormal(x, mean, stdev)
			case "quantileNormal":
				got = QuantileNormal(x, mean, stdev)
			case "densityLogNormal":
				got, _ = NewLogNormal(mean, stdev).PDF(x), 0
			case "cumulativeLogNormal":
				got = NewLogNormal(mean, stdev).CDF(x)
			case "quantileLogNormal":
				got, _ = NewLogNormal(mean, stdev).ICDF(x)
			}
			r.Check(c, upstream.Enc(got), false)
		case "quantiles":
			if upstream.Contains(c.Args, "function") {
				r.Skip("quantiles of a field (the accessor is the test's own function)")
				continue
			}
			p := numbersOfArg(c.Arg(1))
			r.Check(c, upstream.Floats(Quantiles(valuesOfArg(c.Arg(0)), p, identity)), false)
		case "quartiles":
			if upstream.Contains(c.Args, "function") {
				r.Skip("quantiles of a field (the accessor is the test's own function)")
				continue
			}
			q := Quartiles(valuesOfArg(c.Arg(0)), identity)
			r.Check(c, upstream.Floats(q[:]), false)
		case "bootstrapCI":
			data := valuesOfArg(c.Arg(0))
			if len(data) != 0 {
				r.Skip("bootstrapCI on data (it draws from Math.random)")
				continue
			}
			_, _, ok := BootstrapCI(data, int(n(1)), n(2), identity, nil)
			if ok {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, []any{upstream.Undefined(), upstream.Undefined()}, false)
		case "dotbin":
			if upstream.Contains(c.Args, "function") {
				r.Skip("dotbin of a field")
				continue
			}
			smooth := upstream.ToValue(c.Arg(2)).IsTruthy()
			r.Check(c, upstream.Typed("Float64Array", DotBin(numbersOfArg(c.Arg(0)), n(1), smooth)), false)
		case "bin":
			cfg, ok := binConfig(c.Arg(0))
			if !ok {
				r.Skip("bin options the adapter does not read")
				continue
			}
			res, err := Bin(cfg)
			if err != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, map[string]any{"start": upstream.Enc(res.Start), "stop": upstream.Enc(res.Stop), "step": upstream.Enc(res.Step)}, false)
		case "regressionConstant", "regressionLinear", "regressionLog", "regressionExp", "regressionPow", "regressionQuad", "regressionPoly",
			"regressionConstant()", "regressionLinear()", "regressionLog()", "regressionExp()", "regressionPow()", "regressionQuad()", "regressionPoly()":
			replayRegression(r, c)
		case "sampleCurve":
			r.Skip("sampleCurve (the curve is the test's own function)")
		default:
			r.Skip("unmapped " + c.Fn)
		}
	}
	r.Done(11000)
}

// sharedNormalCache is upstream's module-level `nextSample`.
var sharedNormalCache = math.NaN()

// sampleShared draws from d with upstream's shared cache of the second normal of a pair.
func sampleShared(d Distribution, rand Rand) float64 {
	switch d := d.(type) {
	case *Mixture:
		// mixture.js picks a component, then asks it: its normals share the cache too
		m := len(d.Dists)
		if m == 0 {
			return math.NaN()
		}
		u := rand()
		pick := d.Dists[m-1]
		v := d.weights[0]
		for i := 0; i < m-1; i++ {
			if u < v {
				pick = d.Dists[i]
				break
			}
			v += d.weights[i+1]
		}
		return sampleShared(pick, rand)
	case *Normal:
		d.s.next = sharedNormalCache
		got := d.Sample(rand)
		sharedNormalCache = d.s.next
		return got
	case *KernelDensity:
		d.kernel.next = sharedNormalCache
		got := d.Sample(rand)
		sharedNormalCache = d.kernel.next
		return got
	}
	return d.Sample(rand)
}

// newMixture builds randomMixture(distributions, weights) from the origins of its components.
func newMixture(args []any) *instance {
	items, _ := argAt(args, 0).([]any)
	dists := make([]Distribution, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		origin, ok := m["$origin"].(map[string]any)
		if !ok {
			return nil
		}
		from, _ := origin["from"].(string)
		oargs, _ := origin["args"].([]any)
		if upstream.Contains(oargs, "function") {
			return nil
		}
		in := newDistributionFor(from, oargs)
		if in == nil {
			return nil
		}
		dists = append(dists, in.dist)
	}
	var weights []float64
	if w, ok := argAt(args, 1).([]any); ok {
		for _, x := range w {
			if nullish(x) {
				weights = append(weights, math.NaN())
			} else {
				weights = append(weights, upstream.Number(x))
			}
		}
	}
	return &instance{dist: NewMixture(dists, weights)}
}

func applyDistributionStep(in *instance, step upstream.Step) {
	if len(step.Args) == 0 {
		return
	}
	v := upstream.Number(step.Args[0])
	switch d := in.dist.(type) {
	case *Normal:
		switch step.Method {
		case "mean":
			d.Mean = v
		case "stdev":
			d.Stdev = v
		}
	case *Uniform:
		switch step.Method {
		case "min":
			d.Min = v
		case "max":
			d.Max = v
		}
	case *Integer:
		switch step.Method {
		case "min":
			d.Min = v
		case "max":
			d.Max = v
		}
	case *KernelDensity:
		if step.Method == "bandwidth" {
			d.Bandwidth = v
		}
	}
}

func binConfig(v any) (BinConfig, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return BinConfig{}, false
	}
	var cfg BinConfig
	ext, _ := m["extent"].([]any)
	if len(ext) != 2 {
		return cfg, false
	}
	cfg.Extent = [2]float64{upstream.Number(ext[0]), upstream.Number(ext[1])}
	for k, val := range m {
		switch k {
		case "extent":
		case "maxbins":
			cfg.MaxBins = upstream.Number(val)
		case "base":
			cfg.Base = upstream.Number(val)
		case "step":
			cfg.Step = upstream.Number(val)
		case "steps":
			cfg.Steps = numbersOfArg(val)
		case "minstep":
			cfg.MinStep = upstream.Number(val)
		case "divide":
			cfg.Divide = numbersOfArg(val)
		case "span":
			cfg.Span = upstream.Number(val)
		case "nice":
			cfg.NoNice = !upstream.ToValue(val).IsTruthy()
		default:
			return cfg, false
		}
	}
	return cfg, true
}

func replayRegression(r *upstream.Replay, c *upstream.Call) {
	args := c.Args
	fn := c.Fn
	isMethod := len(fn) > 2 && fn[len(fn)-2:] == "()"
	if isMethod {
		args = c.ConstructedWith
		fn = fn[:len(fn)-2]
	}
	method := fn[len("regression"):]
	x, okx := accessorOf(argAt(args, 1))
	y, oky := accessorOf(argAt(args, 2))
	if !okx || !oky {
		r.Skip("regressions with accessors the recording cannot identify")
		return
	}
	order := 0.0
	if method == "Poly" {
		order = upstream.Number(argAt(args, 3))
	}
	names := map[string]string{"Constant": "constant", "Linear": "linear", "Log": "log", "Exp": "exp", "Pow": "pow", "Quad": "quad", "Poly": "poly"}
	model, err := FitRegression(names[method], valuesOfArg(argAt(args, 0)), x, y, order)
	if err != nil {
		r.Check(c, nil, true)
		return
	}
	if isMethod {
		if c.Method != "predict" {
			r.Skip("unmapped method " + c.Method)
			return
		}
		r.Check(c, upstream.Enc(model.Predict(upstream.Number(c.Arg(0)))), false)
		return
	}
	// the fit object: {coef, rSquared, predict}, with predict a function
	r.Check(c, map[string]any{
		"coef":     upstream.Floats(model.Coef),
		"rSquared": upstream.Enc(model.RSquared),
		"predict":  map[string]any{"$": "function", "name": "predict"},
	}, false)
}

func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return upstream.Undefined()
}

// TestUpstreamD3ArrayQuantile replays d3-array's quantile functions, which vega-statistics builds on.
func TestUpstreamD3ArrayQuantile(t *testing.T) {
	r := upstream.Start(t, "d3-array")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || upstream.Contains(c.Args, "function", "typed") {
			continue
		}
		switch c.Fn {
		case "quantileSorted":
			if len(c.Args) != 2 {
				r.Skip("quantileSorted with unusual input")
				continue
			}
			v, ok := QuantileSorted(numbersOfArg(c.Args[0]), upstream.Number(c.Args[1]))
			if !ok {
				r.Check(c, upstream.Undefined(), false)
				continue
			}
			r.Check(c, upstream.Enc(v), false)
		case "quantile", "median":
			p := 0.5
			if c.Fn == "quantile" {
				if len(c.Args) != 2 {
					r.Skip("quantile with an accessor")
					continue
				}
				p = upstream.Number(c.Args[1])
			} else if len(c.Args) != 1 {
				r.Skip("median with an accessor")
				continue
			}
			if !numbersOnly(c.Args[0]) {
				r.Skip("quantile of values that are not all numbers")
				continue
			}
			vals := valuesOfArg(c.Args[0])
			if len(vals) == 0 || math.IsNaN(p) {
				r.Check(c, upstream.Undefined(), false)
				continue
			}
			q := Quantiles(vals, []float64{p}, identity)[0]
			if math.IsNaN(q) {
				r.Check(c, upstream.Undefined(), false)
				continue
			}
			r.Check(c, upstream.Enc(q), false)
		}
	}
	r.Done(30)
}

// numbersOnly reports whether v is an array of finite-or-not numbers, with no NaN or other value d3
// and Vega treat differently.
func numbersOnly(v any) bool {
	items, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range items {
		if f, isNum := upstream.Num(x); !isNum || math.IsNaN(f) {
			return false
		}
	}
	return true
}
