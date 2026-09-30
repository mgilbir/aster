package vega

import (
	"context"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
)

// This file binds the one-shot functions of package transforms to dataflow
// operators: it reads the resolved parameters (field accessors, comparators,
// expressions, signal values) into the typed parameter structs. Each operator
// recomputes from the complete source whenever it runs.

// -- parameter readers ----------------------------------------------------------

func (p *opParams) flatten(x any, out *[]any) {
	switch v := x.(type) {
	case []any:
		for _, e := range v {
			p.flatten(e, out)
		}
	case nil:
		*out = append(*out, nil)
	default:
		*out = append(*out, v)
	}
}

// list returns a list parameter's elements: a resolved array, the items of an
// array value, or the single value.
func (p *opParams) list(name string) []any {
	x, ok := p.vals.lookup(name)
	if !ok || x == nil {
		return nil
	}
	switch v := x.(type) {
	case []any:
		return v
	case []transforms.Field:
		out := make([]any, len(v))
		for i, f := range v {
			out[i] = f
		}
		return out
	case jsval.Value:
		if v.IsArr() {
			items := v.Items()
			out := make([]any, len(items))
			for i, it := range items {
				out[i] = it
			}
			return out
		}
		if v.IsUndefined() {
			return nil
		}
	}
	return []any{x}
}

func asField(x any) transforms.Field {
	switch v := x.(type) {
	case transforms.Field:
		return v
	case []transforms.Field:
		if len(v) > 0 {
			return v[0]
		}
	case *boundExpr:
		return v.accessor()
	case jsval.Value:
		if v.IsStr() {
			return fieldAccessor(v.StrValue(), "")
		}
	}
	return transforms.Field{}
}

// field reads a field parameter (the null Field when absent).
func (p *opParams) field(name string) transforms.Field { return asField(p.vals.at(name)) }

// fields reads a list of field parameters; absent entries are null Fields.
func (p *opParams) fields(name string) []transforms.Field {
	l := p.list(name)
	if l == nil {
		return nil
	}
	out := make([]transforms.Field, 0, len(l))
	for _, e := range l {
		if fs, ok := e.([]transforms.Field); ok {
			out = append(out, fs...)
			continue
		}
		out = append(out, asField(e))
	}
	return out
}

func (p *opParams) str(name string) string {
	v := p.Value(name)
	if v.IsNullish() {
		return ""
	}
	return v.AsString()
}

func (p *opParams) strs(name string) []string {
	l := p.list(name)
	if l == nil {
		return nil
	}
	out := make([]string, len(l))
	for i, e := range l {
		if s, ok := asString(e); ok {
			out[i] = s
		}
	}
	return out
}

func (p *opParams) has(name string) bool {
	x, ok := p.vals.lookup(name)
	if !ok || x == nil {
		return false
	}
	if v, ok := x.(jsval.Value); ok {
		return !v.IsUndefined()
	}
	return true
}

// num reads a numeric parameter, def when it is absent or null.
func (p *opParams) num(name string, def float64) float64 {
	v := p.Value(name)
	if v.IsNullish() {
		return def
	}
	return jsval.ToNumber(v)
}

func (p *opParams) bool(name string) bool { return p.Value(name).IsTruthy() }

// nums reads a list of numbers; null entries are NaN.
func (p *opParams) nums(name string) []float64 {
	l := p.list(name)
	if l == nil {
		return nil
	}
	out := make([]float64, len(l))
	for i, e := range l {
		v, _ := e.(jsval.Value)
		if v.IsNullish() {
			out[i] = math.NaN()
		} else {
			out[i] = jsval.ToNumber(v)
		}
	}
	return out
}

func (p *opParams) comparator(name string) transforms.Comparator {
	if c, _ := p.vals.at(name).(*compareSpec); c != nil {
		return c.cmp
	}
	return nil
}

func (p *opParams) compareSpec(name string) *compareSpec {
	c, _ := p.vals.at(name).(*compareSpec)
	if c == nil || c.cmp == nil {
		return nil
	}
	return c
}

// pair2 reads a two-element numeric list.
func (p *opParams) pair2(name string) *[2]float64 {
	f := p.nums(name)
	if len(f) < 2 {
		return nil
	}
	return &[2]float64{f[0], f[1]}
}

// -- generic wrappers -----------------------------------------------------------

// txFn computes a transform's output from the complete input.
// requireFields fails the way upstream does when a transform applies a field
// accessor that is null: an empty field name resolves to null at run time
// (`if (!_.$field) return null`), and calling it is a TypeError. Only the
// accessors every tuple goes through are checked: required field parameters
// and group-by lists.
func requireFields(def *transformDef, p *opParams) {
	if def == nil {
		return
	}
	for _, pd := range def.params {
		if pd.typ != "field" {
			continue
		}
		switch {
		case pd.array && pd.name == "groupby":
			for _, f := range p.fields(pd.name) {
				if f.IsNil() {
					fail("TypeError: f is not a function (empty field name in %s)", pd.name)
				}
			}
		case !pd.array && pd.required:
			if _, ok := p.vals.lookup(pd.name); ok && p.field(pd.name).IsNil() {
				fail("TypeError: %s is not a function (empty field name)", pd.name)
			}
		}
	}
}

type txFn func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error)

// tupleTransform wraps a one-shot tuple function as an operator.
func tupleTransform(f txFn) factory { return statefulTransform(func() txFn { return f }) }

// statefulTransform is tupleTransform for functions that keep state per
// operator instance: newFn is called once for each instance.
func statefulTransform(newFn func() txFn) factory {
	return func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		f := newFn()
		def := definitions()[e.typ]
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			if pulse.items != nil {
				return markTransform(n, p, pulse, f)
			}
			in := pulse.tuples
			if len(pulse.multi) > 0 {
				in = concatTuples(pulse.multi)
			}
			if len(in) > 0 {
				requireFields(def, p)
			}
			out, err := f(n, p, in)
			if err != nil {
				failErr(err)
			}
			n.g.view.checkRows(len(out) - len(in))
			res := changedPulse(pulse, out)
			if len(out) > 0 && len(in) > 0 && &out[0] == &in[0] {
				res.tree = pulse.tree // annotated in place: still the same data set
			}
			return res
		}), nil
	}
}

func init() {
	tf := transformFactories
	ctxOf := func(n *opNode) contextT { return n.g.ctx }

	tf["filter"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		b, _ := p.Get("expr").(*boundExpr)
		if b == nil {
			return nil, errMissing("filter", "expr")
		}
		return transforms.Filter(ctxOf(n), in, func(t jsval.Value) bool { return b.call(t).IsTruthy() })
	})

	tf["formula"] = statefulTransform(func() txFn {
		seen := map[*jsval.Object]struct{}{}
		return func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
			b, _ := p.Get("expr").(*boundExpr)
			if b == nil {
				fail("formula requires an expr")
			}
			as := p.str("as")
			if !p.bool("initonly") {
				return transforms.Formula(n.g.ctx, in, transforms.FormulaParams{Expr: b.call, As: as})
			}
			// initonly applies to tuples that entered since the last run only
			todo := make([]jsval.Value, 0, len(in))
			for _, t := range in {
				if o := t.ObjValue(); o != nil {
					if _, ok := seen[o]; !ok {
						seen[o] = struct{}{}
						todo = append(todo, t)
					}
				}
			}
			_, err := transforms.Formula(n.g.ctx, todo, transforms.FormulaParams{Expr: b.call, As: as})
			return in, err
		}
	})

	tf["aggregate"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			ap := transforms.AggregateParams{
				GroupBy:  p.fields("groupby"),
				Measures: measures(p),
				Cross:    p.bool("cross"),
				Rand:     n.g.view.randSource(),
			}
			if kf := p.field("key"); !kf.IsNil() {
				ap.Key = kf
			}
			in := pulse.tuples
			if len(pulse.multi) > 0 {
				in = concatTuples(pulse.multi) // several source data sets
			}
			// A null group-by accessor (an empty field name) must not crash
			// the grouping; it reads as undefined.
			for i, f := range ap.GroupBy {
				if f.IsNil() {
					ap.GroupBy[i] = transforms.NamedField("", nil, func(jsval.Value) jsval.Value { return jsval.Undefined })
				}
			}
			out, err := transforms.Aggregate(n.g.ctx, in, ap)
			if err != nil {
				failErr(err)
			}
			// A group mark faceted over this data set finds each group's
			// tuple here; the cells are keyed like the aggregate's own
			// groups (an explicit key, else the group-by values).
			kf, ok := p.Get("key").(transforms.KeyFunc)
			if !ok {
				kf = transforms.KeyOf(ap.GroupBy...)
			}
			n.value = &aggCells{in: in, out: out, key: kf}
			return changedPulse(pulse, out)
		}), nil
	}

	tf["fold"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		as := p.strs("as")
		return transforms.Fold(ctxOf(n), in, transforms.FoldParams{Fields: p.fields("fields"), As: pairAs(as)})
	})
	tf["flatten"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.Flatten(ctxOf(n), in, transforms.FlattenParams{Fields: p.fields("fields"), As: p.strs("as"), Index: p.str("index")})
	})
	tf["project"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		pp := transforms.ProjectParams{As: p.strs("as")}
		if p.has("fields") {
			pp.Fields = p.fields("fields")
			if pp.Fields == nil {
				pp.Fields = []transforms.Field{}
			}
		}
		return transforms.Project(ctxOf(n), in, pp)
	})
	tf["identifier"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.Identifier(ctxOf(n), in, p.str("as"), &n.g.view.idCounter)
	})
	tf["sample"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		size := clampInt(math.Ceil(p.num("size", 1000))) // `res.length < num` admits ceil(num) tuples
		return transforms.Sample(ctxOf(n), in, size, n.g.view.randSource())
	})
	tf["sequence"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		as := p.str("as")
		if as == "" {
			as = "data"
		}
		seq, err := transforms.Sequence(ctxOf(n), transforms.SequenceParams{
			Start: p.num("start", 0), Stop: p.num("stop", 0), Step: p.num("step", 1), As: as,
		})
		if err != nil || len(in) == 0 {
			return seq, err
		}
		// Upstream adds the sequence to the tuples already flowing through
		// (`pulse.add.concat(this.value)`), so after other transforms it
		// extends the data set rather than replacing it.
		return append(slices.Clip(in), seq...), nil
	})
	tf["stack"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.Stack(ctxOf(n), in, transforms.StackParams{
			Field: p.field("field"), GroupBy: p.fields("groupby"), Sort: p.comparator("sort"),
			Offset: p.str("offset"), As: pairAs(p.strs("as")),
		})
	})
	tf["pie"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		pp := transforms.PieParams{
			Field: p.field("field"), StartAngle: p.num("startAngle", 0),
			Sort: p.bool("sort"), As: pairAs(p.strs("as")),
		}
		if v := p.Value("endAngle"); !v.IsNullish() {
			f := jsval.ToNumber(v)
			pp.EndAngle = &f
		}
		return transforms.Pie(ctxOf(n), in, pp)
	})
	tf["joinaggregate"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		jp := transforms.JoinAggregateParams{GroupBy: p.fields("groupby"), Measures: measures(p), Rand: n.g.view.randSource()}
		if kf := p.field("key"); !kf.IsNil() {
			jp.Key = kf
		}
		return transforms.JoinAggregate(ctxOf(n), in, jp)
	})
	tf["window"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		wp := windowParams(p)
		wp.Rand = n.g.view.randSource()
		return transforms.Window(ctxOf(n), in, wp)
	})
	tf["impute"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		ip := transforms.ImputeParams{
			Field: p.field("field"), Key: p.field("key"), GroupBy: p.fields("groupby"),
			Method: p.str("method"), Value: p.Value("value"),
		}
		if kv, ok := valueList(p.vals.at("keyvals")); ok {
			ip.KeyVals = kv
		}
		return transforms.Impute(ctxOf(n), in, ip)
	})
	tf["pivot"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		pp := transforms.PivotParams{
			GroupBy: p.fields("groupby"), Field: p.field("field"), Value: p.field("value"),
			Op: p.str("op"), Limit: clampInt(p.num("limit", 0)), Rand: n.g.view.randSource(),
		}
		if kf := p.field("key"); !kf.IsNil() {
			pp.Key = kf
		}
		return transforms.Pivot(ctxOf(n), in, pp)
	})
	tf["quantile"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.Quantile(ctxOf(n), in, transforms.QuantileParams{
			GroupBy: p.fields("groupby"), Field: p.field("field"), Probs: p.nums("probs"),
			Step: p.num("step", 0.01), As: pairAs(p.strs("as")),
		})
	})
	tf["loess"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.Loess(ctxOf(n), in, transforms.LoessParams{
			X: p.field("x"), Y: p.field("y"), GroupBy: p.fields("groupby"),
			Bandwidth: p.num("bandwidth", 0.3), As: p.strs("as"),
		})
	})
	tf["regression"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		rp := transforms.RegressionParams{
			X: p.field("x"), Y: p.field("y"), GroupBy: p.fields("groupby"),
			Method: p.str("method"), Order: clampInt(p.num("order", 3)),
			Extent: p.nums("extent"), Params: p.bool("params"), As: p.strs("as"),
		}
		return transforms.Regression(ctxOf(n), in, rp)
	})
	tf["lookup"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		var idx *transforms.LookupIndex
		if ti, _ := p.Get("index").(*tupleIndex); ti != nil {
			idx = ti.lookup
		}
		lp := transforms.LookupParams{
			Index: idx, Fields: p.fields("fields"), Values: p.fields("values"),
			As: p.strs("as"), Default: p.Value("default"),
		}
		return transforms.Lookup(ctxOf(n), in, lp)
	})
	tf["cross"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		cp := transforms.CrossParams{As: pairAs(p.strs("as"))}
		if b, ok := p.Get("filter").(*boundExpr); ok {
			cp.Filter = func(t jsval.Value) bool { return b.call(t).IsTruthy() }
		}
		return transforms.Cross(ctxOf(n), in, cp)
	})
	tf["countpattern"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.CountPattern(ctxOf(n), in, transforms.CountPatternParams{
			Field: p.field("field"), Case: p.str("case"), Pattern: p.str("pattern"),
			Stopwords: p.str("stopwords"), As: pairAs(p.strs("as")),
		})
	})
	tf["kde"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		kp := transforms.KDEParams{
			GroupBy: p.fields("groupby"), Field: p.field("field"), Cumulative: p.bool("cumulative"),
			Counts: p.bool("counts"), Bandwidth: p.num("bandwidth", 0), Extent: p.pair2("extent"),
			Resolve: p.str("resolve"), Steps: p.num("steps", 0), MinSteps: p.num("minsteps", 25),
			MaxSteps: p.num("maxsteps", 200), As: pairAs(p.strs("as")),
		}
		return transforms.KDE(ctxOf(n), in, kp)
	})
	tf["timeunit"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		zone := format.UTC
		if p.str("timezone") != "utc" {
			zone = n.g.view.zone
		}
		tp := transforms.TimeUnitParams{
			Field: p.field("field"), NoInterval: !p.Value("interval").IsTruthy() && p.has("interval"),
			Units: p.strs("units"), Step: p.num("step", 1), MaxBins: p.num("maxbins", 40),
			Extent: p.pair2("extent"), InferUnits: p.bool("inferUnits"), Zone: zone,
			As: pairAs(p.strs("as")),
		}
		out, info, err := transforms.TimeUnit(n.g.ctx, in, tp)
		n.value = timeUnitValue(info)
		return out, err
	})
	tf["bin"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		bp := transforms.BinParams{
			Field: p.field("field"), NoInterval: p.has("interval") && !p.bool("interval"),
			Name: p.str("name"), As: pairAs(p.strs("as")),
		}
		ext := p.pair2("extent")
		if ext == nil {
			fail("bin requires an extent")
		}
		cfg := transforms.BinConfig{
			Extent: *ext, MaxBins: p.num("maxbins", 20), Base: p.num("base", 10),
			Step: p.num("step", 0), MinStep: p.num("minstep", 0), Span: p.num("span", 0),
			NoNice: p.has("nice") && !p.bool("nice"), Divide: p.nums("divide"), Steps: p.nums("steps"),
		}
		if p.has("steps") && cfg.Steps == nil {
			cfg.Steps = []float64{}
		}
		bp.Config = cfg
		if a := p.Value("anchor"); !a.IsNullish() {
			f := jsval.ToNumber(a)
			bp.Anchor = &f
		}
		b, err := transforms.BinTuples(n.g.ctx, in, bp)
		if err != nil {
			return nil, err
		}
		n.value = obj("start", jsval.Num(b.Start), "stop", jsval.Num(b.Stop), "step", jsval.Num(b.Step))
		return in, nil
	})
	tf["dotbin"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		r, err := transforms.DotBinTuples(n.g.ctx, in, transforms.DotBinParams{
			Field: p.field("field"), GroupBy: p.fields("groupby"), Step: p.num("step", 0),
			Smooth: p.bool("smooth"), As: p.str("as"),
		})
		if err != nil {
			return nil, err
		}
		n.value = obj("start", jsval.Num(r.Start), "stop", jsval.Num(r.Stop), "step", jsval.Num(r.Step))
		return in, nil
	})
}

type contextT = context.Context

func errMissing(t, param string) error {
	return &parseError{msg: "missing required " + t + " parameter: " + param}
}

func pairAs(s []string) [2]string {
	var out [2]string
	for i := 0; i < 2 && i < len(s); i++ {
		out[i] = s[i]
	}
	return out
}

// firstKeys lists the keys of the groups in order of first appearance.
func firstKeys(data []jsval.Value, key transforms.KeyFunc) []string {
	groups := transforms.GroupByKey(data, key)
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = g.Key
	}
	return out
}

// measures zips the ops/fields/as/aggregate_params arrays of aggregate-like
// transforms into measures.
func measures(p *opParams) []transforms.Measure {
	ops := p.strs("ops")
	fields := p.fields("fields")
	as := p.list("as")
	ap := p.nums("aggregate_params")
	n := len(ops)
	if n == 0 && len(fields) == 0 {
		return nil
	}
	if n == 0 {
		n = len(fields)
	}
	out := make([]transforms.Measure, n)
	for i := range out {
		m := transforms.Measure{Op: "count"}
		if i < len(ops) {
			m.Op = ops[i]
		}
		if i < len(fields) {
			m.Field = fields[i]
		}
		if i < len(as) {
			if s, ok := asString(as[i]); ok {
				m.As = s
			}
		}
		if i < len(ap) && !math.IsNaN(ap[i]) {
			m.Param = ap[i]
		}
		out[i] = m
	}
	return out
}

func windowParams(p *opParams) transforms.WindowParams {
	ops := p.strs("ops")
	fields := p.fields("fields")
	as := p.list("as")
	params := p.nums("params")
	aggp := p.nums("aggregate_params")
	wp := transforms.WindowParams{
		Sort: p.comparator("sort"), GroupBy: p.fields("groupby"), IgnorePeers: p.bool("ignorePeers"),
	}
	for i, op := range ops {
		s := transforms.WindowOpSpec{Op: op, Param: math.NaN()}
		if i < len(fields) {
			s.Field = fields[i]
		}
		if i < len(as) {
			if a, ok := asString(as[i]); ok {
				s.As = a
			}
		}
		if i < len(params) {
			s.Param = params[i]
		}
		if i < len(aggp) && !math.IsNaN(aggp[i]) {
			s.AggParam = aggp[i]
		}
		wp.Ops = append(wp.Ops, s)
	}
	if fr := p.nums("frame"); len(fr) == 2 {
		conv := func(f float64) transforms.FrameBound {
			if math.IsNaN(f) {
				return transforms.FrameBound{Unbounded: true}
			}
			return transforms.FrameBound{Offset: int(f)}
		}
		wp.Frame = []transforms.FrameBound{conv(fr[0]), conv(fr[1])}
	}
	return wp
}

// timeUnitValue is the floor function upstream keeps as the operator's value,
// as far as a signal can read it: its unit, units, step and covered range.
func timeUnitValue(info transforms.TimeUnitInfo) jsval.Value {
	units := make([]jsval.Value, len(info.Units))
	for i, u := range info.Units {
		units[i] = jsval.Str(u)
	}
	bound := func(f float64) jsval.Value {
		if math.IsInf(f, 0) {
			return jsval.Num(f)
		}
		return jsval.Timestamp(f)
	}
	return obj(
		"unit", jsval.Str(info.Unit), "units", jsval.Arr(units), "step", jsval.Num(info.Step),
		"start", bound(info.Start), "stop", bound(info.Stop),
	)
}

// randSource is the random generator of transforms.
func (v *runView) randSource() transforms.Rand {
	if v.rand != nil && v.rand.Source != nil {
		return transforms.Rand(v.rand.Source)
	}
	return nil
}

// markTransform applies a tuple transform to the items of a mark (a
// post-encoding transform): the items are presented as tuples, the transform
// writes its output fields into them, and every field that changed is copied
// back onto the item.
func markTransform(n *opNode, p *opParams, pulse *flowPulse, f txFn) *flowPulse {
	v := n.g.view
	items := pulse.items
	views := v.itemTuples(items)
	before := make([]map[string]jsval.Value, len(views))
	for i, t := range views {
		o := t.ObjValue()
		snap := make(map[string]jsval.Value, o.Len())
		for j := 0; j < o.Len(); j++ {
			snap[o.KeyAt(j)] = o.ValueAt(j)
		}
		before[i] = snap
	}
	if _, err := f(n, p, views); err != nil {
		failErr(err)
	}
	for i, t := range views {
		o := t.ObjValue()
		for j := 0; j < o.Len(); j++ {
			k, val := o.KeyAt(j), o.ValueAt(j)
			if k == "datum" || k == "bounds" {
				continue
			}
			if old, ok := before[i][k]; ok && jsval.SameRef(old, val) && old.Kind() == val.Kind() {
				continue
			}
			setItemProp(items[i], k, val)
		}
	}
	return nil
}

// clampInt converts a number to an int without relying on the platform's
// out-of-range conversion: NaN is 0, and magnitudes saturate at 2^31-1.
func clampInt(f float64) int {
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int(f)
}
