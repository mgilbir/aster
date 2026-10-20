package vega

import (
	"context"
	"errors"
	"github.com/mgilbir/aster/internal/expr"
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
		// a Field operator given an array makes an array of accessors: a
		// truthy value that is no function
		return transforms.UncallableField()
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

// fields reads a list of field parameters as vega-util's array() lists them:
// none for an absent or null one (a single null accessor too: a scale
// domain's group-by of an empty field name), the elements of a list, where
// a null entry is the null Field, and a single accessor alone.
func (p *opParams) fields(name string) []transforms.Field {
	l := paramArray(p.vals.at(name))
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
	return p.walk.String(v)
}

func (p *opParams) strs(name string) []string {
	l := p.list(name)
	if l == nil {
		return nil
	}
	out := make([]string, len(l))
	for i, e := range l {
		if s, ok := paramText(p.walk, e); ok {
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

// errStopPulse is what a transform function returns to end its pass with
// StopPropagation: the operator ran but its targets are not re-evaluated.
var errStopPulse = errors.New("vega: stop propagation")

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
		case pd.array && (pd.name == "groupby" || calledFields[def.typ+"."+pd.name]):
			for _, f := range p.fields(pd.name) {
				if f.IsNil() {
					fail("TypeError: f is not a function (empty field name in %s)", pd.name)
				}
			}
		case !pd.array && pd.required && !nullFieldDefaults[def.typ+"."+pd.name]:
			if _, ok := p.vals.lookup(pd.name); ok && p.field(pd.name).IsNil() {
				fail("TypeError: %s is not a function (empty field name)", pd.name)
			}
		}
	}
}

// requireGroupBy fails the way an aggregate's first tuple does when a group-by
// accessor is null (a falsy field: null, false, 0, ""): the cell key or the
// cell's tuple calls it, a TypeError.
func requireGroupBy(dims []transforms.Field) {
	for _, f := range dims {
		if f.IsNil() {
			fail("TypeError: this.cellkey is not a function (a null group-by field)")
		}
	}
}

// calledFields are the field lists whose every entry the transform calls on
// each tuple (project's, fold's and flatten's fields): a null one is a
// TypeError, as a group-by's is.
var calledFields = map[string]bool{"Project.fields": true, "Fold.fields": true, "Flatten.fields": true}

// nullFieldDefaults are the required field parameters a null accessor
// leaves at the transform's default rather than calling: kde2d's params()
// sets x and y only when `_[p] != null` (density2D reads d[0], d[1]).
var nullFieldDefaults = map[string]bool{"KDE2D.x": true, "KDE2D.y": true}

type txFn func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error)

// tupleTransform wraps a one-shot tuple function as an operator.
func tupleTransform(f txFn) factory { return statefulTransform(func() txFn { return f }) }

// statefulTransform is tupleTransform for functions that keep state per
// operator instance: newFn is called once for each instance.
func statefulTransform(newFn func() txFn) factory {
	return func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		f := newFn()
		typ := e.typ
		def := definitions()[typ]
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			if pulse.items != nil {
				return markTransform(n, p, pulse, f)
			}
			if pulse.noSource {
				requireSource(typ, p)
			}
			in := pulse.input(n.g, typ)
			if len(pulse.multi) > 0 {
				in = concatTuples(n.g, pulse.multi)
			}
			if len(in) > 0 {
				requireFields(def, p)
			}
			if pulse.src != nil {
				checkWritesAdds(n.g, typ, p, in)
			}
			n.g.view.txPulse = pulse
			defer func() { n.g.view.txPulse = nil }()
			before := n.g.view.rowExcess(in)
			out, err := f(n, p, in)
			if err == errStopPulse {
				return stopPulse
			}
			if err != nil {
				failErr(err)
			}
			// The tuples a transform creates are ingested in output order
			// (vega-dataflow's ingest), which is the order a sort breaks
			// ties in; the operator holds them.
			n.g.view.holdCreated(n, before, out)
			res := changedPulse(pulse, out)
			sourcePulse(typ, pulse, res)
			if keepsTree[typ] || len(out) > 0 && len(in) > 0 && &out[0] == &in[0] {
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
		// last is the source the previous run saw: the tuples that are not in
		// it are the ones the pulse adds.
		var last []jsval.Value
		return func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
			b, _ := p.Get("expr").(*boundExpr)
			if b == nil {
				fail("formula requires an expr")
			}
			as := p.keyOf("as") // t[_.as]
			prev := last
			last = append(last[:0:0], in...)
			// Formula's flag: `initonly` visits the added tuples; modified
			// parameters visit the whole source; a pulse that modified a
			// field the expression reads (or its own output) visits the added
			// and modified tuples; any other pulse visits the added ones. A
			// formula over random() shows the difference. The engine's tuple
			// pulses do not say which fields changed, so a pulse that changed
			// something stands for one that modified the fields read.
			all := !p.bool("initonly") && (p.Modified() ||
				len(b.fn.fields) > 0 && n.g.view.txPulse != nil && n.g.view.txPulse.changed)
			if all {
				return transforms.Formula(n.g.ctx, in, transforms.FormulaParams{Expr: b.call, As: as})
			}
			// the added tuples only: those that entered since the last run
			seen := make(map[*jsval.Object]struct{}, len(prev))
			for _, t := range prev {
				if o := jsval.Tuple(t); o != nil {
					seen[o] = struct{}{}
				}
			}
			todo := make([]jsval.Value, 0, len(in))
			for i, t := range in {
				n.g.rows(i, len(in), 2)
				if o := jsval.Tuple(t); o != nil {
					if _, ok := seen[o]; ok {
						continue
					}
				}
				todo = append(todo, t) // a value that is no tuple cannot be told apart: new
			}
			_, err := transforms.Formula(n.g.ctx, todo, transforms.FormulaParams{Expr: b.call, As: as})
			return in, err
		}
	})

	tf["aggregate"] = func(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
		return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
			in := pulse.tuples
			if len(pulse.multi) > 0 {
				in = concatTuples(n.g, pulse.multi) // several source data sets
			}
			ap := transforms.AggregateParams{
				GroupBy:  p.fields("groupby"),
				Measures: measures(n.g, p, len(in) > 0),
				Cross:    p.bool("cross"),
				Rand:     n.g.view.randSource(),
			}
			if kf := p.field("key"); !kf.IsNil() {
				ap.Key = kf
			}
			if len(in) > 0 {
				requireGroupBy(ap.GroupBy)
			}
			out, err := transforms.Aggregate(n.g.ctx, in, ap)
			if err != nil {
				failErr(err)
			}
			n.g.view.holdCreated(n, 0, out)
			// A group mark faceted over this data set finds each group's
			// tuple here; the cells are keyed like the aggregate's own
			// groups (an explicit key, else the group-by values).
			kf, ok := p.Get("key").(transforms.KeyFunc)
			if !ok {
				kf = transforms.KeyOf(n.g.view.keys, ap.GroupBy...)
			}
			n.value = &aggCells{in: in, out: out, key: kf, ctx: n.g.ctx}
			return changedPulse(pulse, out)
		}), nil
	}

	tf["fold"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireFieldsList(false)
		return transforms.Fold(ctxOf(n), in, transforms.FoldParams{Fields: p.fields("fields"), As: p.asPair("key", "value"), AsExact: true})
	})
	tf["flatten"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireFieldsList(true)
		return transforms.Flatten(ctxOf(n), in, transforms.FlattenParams{Fields: p.fields("fields"), As: p.truthyTextsUpTo("as", len(p.fields("fields"))), Index: p.textOr("index", "")})
	})
	tf["project"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireFieldsList(true)
		pp := transforms.ProjectParams{As: p.truthyTextsUpTo("as", len(p.fields("fields")))}
		if p.has("fields") {
			pp.Fields = p.fields("fields")
			if pp.Fields == nil {
				pp.Fields = []transforms.Field{}
			}
		}
		return transforms.Project(ctxOf(n), in, pp)
	})
	tf["identifier"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.Identifier(ctxOf(n), in, p.keyOf("as"), &n.g.view.idCounter)
	})
	tf["sample"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		// `res.length < num` admits ceil(+num) tuples, num = _.size as it is:
		// an absent size is undefined, NaN, no tuple (the definition's 1000
		// is documentation only)
		sv, _ := paramValueOf(p.Get("size"))
		size := clampInt(math.Ceil(jsval.ToNumber(sv)))
		return transforms.Sample(ctxOf(n), in, size, n.g.view.randSource())
	})
	tf["sequence"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		as := p.textOr("as", "data")
		var seq []jsval.Value
		var err error
		// `range(start, stop, _.step || 1)`: only a falsy step is 1. A truthy
		// one that reads as 0 or NaN ([], "0", "x") is d3.range's step of
		// zero, whose count Infinity | 0 is no values at all.
		if sv := p.Value("step"); sv.IsTruthy() && !(jsval.ToNumber(sv) != 0 && !math.IsNaN(jsval.ToNumber(sv))) {
			seq = nil
		} else {
			seq, err = transforms.Sequence(ctxOf(n), transforms.SequenceParams{
				Start: p.num("start", 0), Stop: p.num("stop", 0), Step: p.num("step", 1), As: as,
			})
		}
		if err != nil || len(in) == 0 {
			return seq, err
		}
		// Upstream adds the sequence to the tuples already flowing through
		// (`pulse.add.concat(this.value)`), so after other transforms it
		// extends the data set rather than replacing it.
		return append(slices.Clip(in), seq...), nil
	})
	tf["stack"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requirePartitionList(len(in))
		return transforms.Stack(ctxOf(n), in, transforms.StackParams{
			Field: p.field("field"), GroupBy: p.fields("groupby"), Sort: p.comparator("sort"),
			Offset: strictText(p.Value("offset"), "center", "normalize"), As: p.asPair("y0", "y1"), AsExact: true,
		})
	})
	tf["pie"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		pp := transforms.PieParams{
			Field: p.field("field"), StartValue: p.orValue(jsval.Num(0), "startAngle"),
			Sort: p.bool("sort"), As: p.asPair("startAngle", "endAngle"), AsExact: true,
		}
		if v := p.Value("endAngle"); !v.IsNullish() {
			f := jsval.ToNumber(v)
			pp.EndAngle = &f
		}
		return transforms.Pie(ctxOf(n), in, pp)
	})
	tf["joinaggregate"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		jp := transforms.JoinAggregateParams{GroupBy: p.fields("groupby"), Measures: measures(n.g, p, len(in) > 0), Rand: n.g.view.randSource()}
		if kf := p.field("key"); !kf.IsNil() {
			jp.Key = kf
		}
		// it aggregates what the pulse adds and writes into its source
		if pl := n.g.view.txPulse; pl != nil && pl.src != nil {
			jp.Into = pl.sourceView(n.g)
		}
		return transforms.JoinAggregate(ctxOf(n), in, jp)
	})
	tf["window"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupKeyList(len(in))
		wp := windowParams(n.g, p)
		wp.Rand = n.g.view.randSource()
		return transforms.Window(ctxOf(n), in, wp)
	})
	tf["impute"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupByList()
		method, notStrict, proto := imputeMethod(p)
		ip := transforms.ImputeParams{
			Field: p.field("field"), Key: p.field("key"), GroupBy: p.fields("groupby"),
			Method: method, MethodNotStrictValue: notStrict, ProtoMember: proto, Value: p.Value("value"),
		}
		if kv, ok := valueList(p.vals.at("keyvals")); ok {
			ip.KeyVals = kv
		}
		return transforms.Impute(ctxOf(n), in, ip)
	})
	tf["pivot"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		if p.field("field").Uncallable { // accessorFields(key).concat of undefined
			fail("TypeError: Cannot read properties of undefined (reading 'concat')")
		}
		pp := transforms.PivotParams{
			GroupBy: p.fields("groupby"), Field: p.field("field"), Value: p.field("value"),
			Op: pivotOp(p), Limit: clampInt(p.orNumber("limit", 0)), Rand: n.g.view.randSource(),
		}
		if kf := p.field("key"); !kf.IsNil() {
			pp.Key = kf
		}
		return transforms.Pivot(ctxOf(n), in, pp)
	})
	tf["quantile"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupByList()
		qp := transforms.QuantileParams{
			GroupBy: p.fields("groupby"), Field: p.field("field"),
			Step: p.orNumber("step", 0.01), StepExact: true, As: p.asPair("prob", "value"), AsExact: true,
		}
		// p = _.probs || range(...): the probabilities as given, each
		// quantileSorted's +p, and the output's prob as it is
		if pr := p.Get("probs"); paramTruthy(pr) {
			if paramIsArray(pr) {
				qp.ProbValues, _ = paramElems(n.g, pr)
				qp.Probs = make([]float64, len(qp.ProbValues))
				for i, v := range qp.ProbValues {
					qp.Probs[i] = jsval.ToNumber(v)
				}
			} else {
				qp.ProbsNotArray, qp.Probs = true, []float64{}
			}
		}
		return transforms.Quantile(ctxOf(n), in, qp)
	})
	tf["loess"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupByList()
		x, y := p.field("x"), p.field("y")
		as := p.asPair(x.Key(), y.Key())
		return transforms.Loess(ctxOf(n), in, transforms.LoessParams{
			X: x, Y: y, GroupBy: p.fields("groupby"),
			Bandwidth: p.orNumber("bandwidth", 0.3), BandwidthExact: true, As: as[:],
		})
	})
	tf["regression"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupByList()
		x, y := p.field("x"), p.field("y")
		rp := transforms.RegressionParams{
			X: x, Y: y, GroupBy: p.fields("groupby"),
			Params: p.bool("params"), Strict: true,
		}
		as := p.asPair(x.Key(), y.Key())
		rp.As = as[:]
		// method = _.method || 'linear', looked up as Methods[method] but
		// compared with ===
		mv := p.orValue(jsval.Str("linear"), "method")
		rp.Method, _ = mv.PropertyKey(32)
		rp.MethodIsString = mv.IsStr()
		// order = _.order == null ? 3 : _.order, kept as given: an order that
		// is no number skips regressionPoly's shortcuts and makes its
		// k = order + 1 a string (the transform reads OrderRaw)
		rp.Order, rp.OrderValue, rp.OrderIsNumber = 3, 3, true
		if ov, _ := paramValueOf(p.Get("order")); !ov.IsNullish() {
			// The conversion of anything but a number happens where upstream
			// does it (the degrees of freedom of a poly, regressionPoly's
			// `order + 1`): an object with a toString of its own throws only there.
			rp.OrderIsNumber, rp.OrderRaw = ov.IsNum(), ov
			if rp.OrderIsNumber {
				rp.Order = jsval.ToNumber(ov)
				rp.OrderValue = rp.Order
			}
		}
		// domain = _.extent; dom = domain || extent(g, x)
		if ex := p.Get("extent"); paramTruthy(ex) {
			if paramIsArray(ex) {
				rp.ExtentValues, _ = paramElems(n.g, ex)
			} else {
				rp.ExtentNotArray = true
				ev := paramJS(ex)
				rp.ExtentValues = []jsval.Value{jsval.Member(ev, "0"), jsval.Member(ev, "1")}
			}
		}
		return transforms.Regression(ctxOf(n), in, rp)
	})
	tf["lookup"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		var idx *transforms.LookupIndex
		if ti, _ := p.Get("index").(*tupleIndex); ti != nil {
			idx = ti.lookup
		}
		return transforms.Lookup(ctxOf(n), in, lookupParams(n.g, p, idx))
	})
	tf["cross"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		cp := transforms.CrossParams{As: p.asPair("a", "b"), AsExact: true}
		if b, ok := p.Get("filter").(*boundExpr); ok {
			cp.Filter = func(t jsval.Value) bool { return b.call(t).IsTruthy() }
		}
		return transforms.Cross(ctxOf(n), in, cp)
	})
	tf["countpattern"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		return transforms.CountPattern(ctxOf(n), in, transforms.CountPatternParams{
			Field: p.field("field"), Case: strictText(p.Value("case"), "upper", "lower"), Pattern: p.textOr("pattern", ""),
			Stopwords: p.textOr("stopwords", ""), As: p.asPair("text", "count"), AsExact: true,
		})
	})
	tf["kde"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requireGroupByList()
		steps := p.stepsSpec()
		bw := p.Value("bandwidth")
		kp := transforms.KDEParams{
			GroupBy: p.fields("groupby"), Field: p.field("field"), Cumulative: p.bool("cumulative"),
			Counts: p.bool("counts"), Bandwidth: jsval.ToNumber(bw), BandwidthGiven: bw.IsTruthy(),
			ExtentValues: p.extentValues("extent"), StepsValues: &steps,
			As: p.asPair("value", "density"), AsExact: true,
		}
		if !kp.BandwidthGiven {
			kp.Bandwidth = 0
		}
		if strictIs(p.Value("resolve"), "shared") { // _.resolve === 'shared'
			kp.Resolve = "shared"
		}
		return transforms.KDE(ctxOf(n), in, kp)
	})
	tf["timeunit"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		zone := format.UTC
		if !strictIs(p.Value("timezone"), "utc") { // _.timezone === 'utc'
			zone = n.g.view.zone
		}
		tp := transforms.TimeUnitParams{
			Field: p.field("field"), NoInterval: !p.Value("interval").IsTruthy() && p.has("interval"),
			Units: timeUnitUnits(p), UnitsStrict: timeUnitStrict(p), UnitsGiven: paramTruthy(p.Get("units")), // `_.units ? ... : timeBin(...)`
			StepRaw: p.Value("step"), StepRawGiven: true, MaxBins: timeUnitMaxBins(p), MaxBinsExact: true,
			Extent: p.pair2("extent"), InferUnits: p.bool("inferUnits"), Zone: zone,
			As: p.asPair("unit0", "unit1"), AsExact: true,
		}
		out, info, err := transforms.TimeUnit(n.g.ctx, in, tp)
		n.value = timeUnitValue(info)
		return out, err
	})
	tf["bin"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		bp := transforms.BinParams{
			Field: p.field("field"), NoInterval: p.has("interval") && !p.bool("interval"),
			Name: p.textOr("name", ""), As: p.asPair("bin0", "bin1"), AsExact: true,
		}
		// bin(_) reads every parameter as it is (transforms.BinJS).
		ext := p.Get("extent")
		if paramNullish(ext) {
			fail("TypeError: Cannot read properties of %s (reading '0')", nullishText(ext))
		}
		spec := transforms.BinSpec{
			MaxBins: p.Value("maxbins"), Base: p.Value("base"), Span: p.Value("span"), Step: p.Value("step"),
			MinStep: p.Value("minstep"), Nice: p.Value("nice"),
			Extent0: paramElem(ext, 0), Extent1: paramElem(ext, 1),
		}
		if d := p.Get("divide"); paramTruthy(d) {
			spec.Divide, spec.DivideLen = paramElems(n.g, d)
		}
		if st := p.Get("steps"); paramTruthy(st) {
			spec.Steps, spec.StepsLen = paramElems(n.g, st)
		}
		bp.Spec = &spec
		if a := p.Value("anchor"); !a.IsNullish() {
			bp.AnchorValue = &a
		}
		b, err := transforms.BinTuples(n.g.ctx, in, bp)
		if err != nil {
			return nil, err
		}
		n.value = obj("start", b.StartV(), "stop", b.StopV(), "step", b.StepV())
		return in, nil
	})
	tf["dotbin"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		p.requirePartitionList(len(in))
		r, err := transforms.DotBinTuples(n.g.ctx, in, transforms.DotBinParams{
			Field: p.field("field"), GroupBy: p.fields("groupby"), StepValue: p.Value("step"),
			Smooth: p.bool("smooth"), As: p.textOr("as", "bin"),
		})
		if err != nil {
			return nil, err
		}
		n.value = obj("start", jsval.Num(r.Start), "stop", jsval.Num(r.Stop), "step", r.StepValue)
		return in, nil
	})
}

type contextT = context.Context

func errMissing(t, param string) error {
	return &parseError{msg: "missing required " + t + " parameter: " + param}
}

// firstKeys lists the keys of the groups in order of first appearance.
func firstKeys(ctx context.Context, data []jsval.Value, key transforms.KeyFunc) []string {
	groups, err := transforms.GroupByKey(ctx, data, key)
	if err != nil {
		failErr(err)
	}
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = g.Key
	}
	return out
}

// measures is Aggregate.init's reading of the measures of an aggregate or a
// joinaggregate:
//
//	fields = _.fields || [null], ops = _.ops || ['count'],
//	aggregate_params = _.aggregate_params || [null], as = _.as || [],
//	n = fields.length; if (n !== ops.length) error(...)
//	for (i = 0; i < n; ++i) field = fields[i]; op = ops[i]; ...
//
// read as JavaScript reads them: array-likes by length and index, an op by
// the property it names (AggregateOps[op]: ["sum"] is sum) but compared
// with === 'count', an output name kept when it is truthy (as[i] ||
// measureName). hasData reports that the operator has tuples to add: the
// ops a cell cannot be made of (an Object.prototype member) fail when the
// first cell is.
func measures(g *flowGraph, p *opParams, hasData bool) []transforms.Measure {
	fields, ops := p.Get("fields"), p.Get("ops")
	if !paramTruthy(fields) {
		fields = []any{nil}
	}
	if !paramTruthy(ops) {
		ops = []any{jsval.Str("count")}
	}
	aps, as := p.Get("aggregate_params"), p.Get("as")
	if !paramTruthy(aps) {
		aps = []any{nil}
	}
	if !paramTruthy(as) {
		as = []any{}
	}
	nv, ov := paramLengthValue(fields), paramLengthValue(ops)
	if !jsval.SameRef(nv, ov) {
		fail("Unmatched number of fields and aggregate ops.")
	}
	n := paramBound(jsval.ToNumber(nv))
	out := make([]transforms.Measure, 0, min(n, 64))
	for i := 0; i < n; i++ {
		g.work(1)
		if m := measureOf(p, paramAt(fields, i), paramAt(ops, i), paramAt(aps, i), paramAt(as, i), hasData); m.Op != "" {
			out = append(out, m)
		}
	}
	return out
}

// measureOf is one measure of Aggregate.init or WindowState: field, op,
// aggregate_param and as are the i-th elements of the parameters' lists.
func measureOf(p *opParams, field, op, ap, as any, hasData bool) transforms.Measure {
	f := accessorOf(field)
	opv, _ := paramValueOf(op)
	opKey := ""
	if k, ok := opv.PropertyKey(32); ok {
		opKey = k
	}
	strictCount := strictIs(opv, "count")
	if f.IsNil() && !strictCount {
		fail("Null aggregate field specified.")
	}
	m := transforms.Measure{Op: opKey, Field: f, Param: math.NaN(), CountMeasure: !strictCount && opKey == "count"}
	if !transforms.IsAggregateOp(opKey) {
		if !opMakesCells(opKey) {
			fail("TypeError: AggregateOps[op] is not a function")
		}
		if hasData {
			fail("TypeError: op.init is not a function")
		}
		// No cell is made: the measure has no output to write.
		m.Op = ""
		return m
	}
	if apv, ok := paramValueOf(ap); !ok || apv.IsTruthy() {
		m.Param = jsval.ToNumber(apv)
	}
	// map[mname] on a plain object finds Object.prototype's member for a field
	// named like one: a function, truthy, with no push.
	if !strictCount && expr.IsObjectPrototypeName(f.Key()) {
		fail("TypeError: m.push is not a function")
	}
	m.As, m.AsExact = outputName(p, as, opKey, f), true
	return m
}

// accessorOf is a list element of a field parameter as the accessor it is,
// the null Field for anything else (undefined, null, or a value that is no
// function: a Field operator hands on accessors only).
func accessorOf(x any) transforms.Field {
	if f, ok := x.(transforms.Field); ok {
		return f
	}
	if b, ok := x.(*boundExpr); ok {
		return b.accessor()
	}
	return transforms.Field{}
}

// outputName is measureName(op, accessorName(field), as):
// `as || op + '_' + mname` (op alone for a falsy mname), as the property key the
// output is written to; opText is String(op).
func outputName(p *opParams, as any, opText string, field transforms.Field) string {
	if asv, ok := paramValueOf(as); !ok || asv.IsTruthy() {
		return p.walk.String(asv)
	}
	return transforms.MeasureName(opText, field.Name, "")
}

// opMakesCells reports an op that is no aggregate op but whose
// AggregateOps[op](name, param) returns: a member of Object.prototype that
// takes those arguments (Object, toString, valueOf, hasOwnProperty, ...).
// Aggregate.init keeps what it returns as a measure, and only the cell made
// with it throws, calling its init. The getters' definers throw for a
// parameter that is no function, and the lookups return undefined, whose
// name compileMeasures reads.
func opMakesCells(key string) bool {
	switch key {
	case "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__":
		return false
	}
	_, ok := jsval.Inherited(key)
	return ok
}

// windowParams reads a window's parameters as WindowState does: ops,
// fields, params, aggregate_params and as through vega-util's array() (a
// value that is no array is a list of one), each op by the property it names
// (hasOwnProperty(WindowOps, op), else AggregateOps[op] as an aggregate's),
// and the frame as processPartition reads it (_.frame || [null, 0], f[0]
// and f[1]).
func windowParams(g *flowGraph, p *opParams) transforms.WindowParams {
	ops := paramArray(p.Get("ops"))
	fields := paramArray(p.Get("fields"))
	params := paramArray(p.Get("params"))
	aps := paramArray(p.Get("aggregate_params"))
	as := paramArray(p.Get("as"))
	at := func(l []any, i int) any {
		if i < len(l) {
			return l[i]
		}
		return nil
	}
	wp := transforms.WindowParams{
		Sort: p.comparator("sort"), GroupBy: p.fields("groupby"), IgnorePeers: p.bool("ignorePeers"),
	}
	if cs := p.compareSpec("sort"); cs != nil && len(cs.fields) > 0 {
		wp.SortField, wp.SortReadsTuple = cs.fields[0], cs.whole
	}
	for i, op := range ops {
		g.work(1)
		opv, _ := paramValueOf(op)
		key, _ := opv.PropertyKey(32)
		field := at(fields, i)
		if transforms.IsWindowOp(key) {
			s := transforms.WindowOpSpec{Op: key, Field: accessorOf(field), Param: math.NaN(), AggParam: math.NaN()}
			if v, ok := paramValueOf(at(params, i)); ok {
				s.Param = jsval.ToNumber(v) // +param
			}
			s.As, s.AsExact = outputName(p, at(as, i), key, s.Field), true
			wp.Ops = append(wp.Ops, s)
			continue
		}
		// WindowState makes its cell, and so calls each measure's init,
		// before it has seen a tuple.
		m := measureOf(p, field, op, at(aps, i), at(as, i), true)
		wp.Ops = append(wp.Ops, transforms.WindowOpSpec{
			Op: m.Op, Field: m.Field, Param: math.NaN(), AggParam: m.Param, As: m.As, AsExact: m.AsExact, CountMeasure: m.CountMeasure,
		})
	}
	if fr := p.Get("frame"); paramTruthy(fr) {
		// f == null is unbounded; anything else is an offset, added to
		// the row's index (transforms.FrameBound).
		conv := func(e any) transforms.FrameBound {
			v, ok := paramValueOf(e)
			switch {
			case ok && v.IsNullish():
				return transforms.FrameBound{Unbounded: true}
			case v.IsNum() || v.Kind() == jsval.KindBool:
				return transforms.FrameBound{Offset: jsval.ToNumber(v)}
			}
			// i + f: a string, an array or an object makes the end a
			// string (i + "" is String(i)).
			return transforms.FrameBound{Text: p.walk.String(v), IsText: true}
		}
		wp.Frame = []transforms.FrameBound{conv(paramAt(fr, 0)), conv(paramAt(fr, 1))}
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
		"unit", jsval.Str(info.Unit), "units", jsval.Arr(units), "step", stepValueOf(info),
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
		n.g.rows(i, len(views), itemTupleCost)
		o := t.ObjValue()
		snap := make(map[string]jsval.Value, o.Len())
		for j := 0; j < o.Len(); j++ {
			snap[o.KeyAt(j)] = o.ValueAt(j)
		}
		before[i] = snap
	}
	// The transform works on the items themselves upstream, so what it wrote
	// before it failed stays on them (the force simulation has placed the
	// nodes before a link it cannot resolve throws).
	copyBack := func() {
		for i, t := range views {
			n.g.rows(i, len(views), itemTupleCost)
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
	}
	stop := false
	func() {
		defer copyBack()
		if _, err := f(n, p, views); err == errStopPulse {
			stop = true
		} else if err != nil {
			failErr(err)
		}
	}()
	if stop {
		return stopPulse
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

// lookupParams reads a lookup's parameters as Lookup.transform does: keys =
// _.fields and values = _.values read by length and index, `if (values)`
// choosing the values branch (an empty list too), `n > 1 && !as` and
// `as.length !== n * m` the errors they are, the default names
// values.map(accessorName), and each output name as[k] the property key it
// names ("undefined" past the end).
func lookupParams(g *flowGraph, p *opParams, idx *transforms.LookupIndex) transforms.LookupParams {
	lp := transforms.LookupParams{Index: idx, Default: p.Value("default"), Exact: true}
	keys := p.Get("fields")
	if paramNullish(keys) {
		fail("TypeError: Cannot read properties of %s (reading 'length')", nullishText(keys))
	}
	n := paramBound(paramLength(keys))
	lp.Fields = make([]transforms.Field, n)
	for i := range lp.Fields {
		g.work(1)
		lp.Fields[i] = accessorOf(paramAt(keys, i))
	}
	asx := p.Get("as")
	asGiven := paramTruthy(asx)
	names := func(count int) {
		lp.As = make([]string, count)
		for k := range lp.As {
			g.work(1)
			v, _ := paramValueOf(paramAt(asx, k))
			lp.As[k] = p.walk.String(v)
		}
	}
	if vx := p.Get("values"); paramTruthy(vx) {
		lp.ValuesGiven = true
		m := paramBound(paramLength(vx))
		lp.Values = make([]transforms.Field, m)
		for j := range lp.Values {
			g.work(1)
			lp.Values[j] = accessorOf(paramAt(vx, j))
		}
		if n > 1 && !asGiven {
			fail(`Multi-field lookup requires explicit "as" parameter.`)
		}
		if asGiven && !jsval.SameRef(paramLengthValue(asx), jsval.Num(float64(n*m))) {
			fail(`The "as" parameter has too few output field names.`)
		}
		if !asGiven {
			if !paramIsArray(vx) {
				fail("TypeError: values.map is not a function")
			}
			// values.map(accessorName)
			lp.As = make([]string, m)
			for j, f := range lp.Values {
				switch {
				case f.IsNil():
					lp.As[j] = "null"
				default:
					lp.As[j] = f.Key()
				}
			}
			return lp
		}
		names(n * m)
		return lp
	}
	if !asGiven {
		fail("Missing output field names.")
	}
	names(n)
	return lp
}

// requireSource fails as a transform does that reads `pulse.source` as a list
// when its pulse has none (the source operator has not produced a pulse: a
// mark transform whose encoder has not run, run by a parameter that changed).
// The transforms below dereference it at once, some behind the guard that
// they have nothing to do unless a parameter changed or it is their first run
// (every parameter counts as modified then).
func requireSource(typ string, p *opParams) {
	fault := func() { failReading("forEach") }
	switch typ {
	case "pie":
		failReading("map") // data = pulse.source, values = data.map(field)
	case "stack":
		// partition(pulse.source, _.groupby, ...): `data.slice()` with no
		// groupby, else `data.length`.
		if g, isVal := p.Get("groupby").(jsval.Value); p.Get("groupby") == nil || isVal && g.IsNullish() {
			failReading("slice")
		}
		failReading("length")
	case "impute", "values":
		fault() // no guard
	case "heatmap":
		// `if (!pulse.changed() && !_.modified()) return StopPropagation`,
		// then `shared ? max(source.map(...)) : 0` and `source.forEach`.
		if p.Modified() {
			if strictIs(p.Value("resolve"), "shared") {
				failReading("map")
			}
			fault()
		}
	case "dotbin", "kde", "kde2d", "quantile", "loess", "regression", "isocontour", "cross", "wordcloud":
		if p.Modified() {
			fault()
		}
	case "contour":
		if p.Modified() && !paramTruthy(p.Get("values")) {
			fault()
		}
	case "timeunit":
		// units given: the floor reads no source (the tuples are visited)
		if p.Modified() && (p.bool("inferUnits") || !paramTruthy(p.Get("units"))) && p.Get("extent") == nil {
			fault()
		}
	}
}

func failReading(prop string) {
	fail("TypeError: Cannot read properties of undefined (reading '%s')", prop)
}

// timeUnitMaxBins is timeBin's `opt.maxbins || 40`: a truthy value of any
// type is converted as a number (an array or object makes NaN), and stays so.
func timeUnitMaxBins(p *opParams) float64 {
	if v := p.Value("maxbins"); v.IsTruthy() {
		return jsval.ToNumber(v)
	}
	return 40
}

// stepValueOf is the step a time unit holds: as given where it was, else the
// number the units chose.
func stepValueOf(info transforms.TimeUnitInfo) jsval.Value {
	if !info.StepValue.IsUndefined() {
		return info.StepValue
	}
	return jsval.Num(info.Step)
}

// timeUnitUnits are the units a time unit floors to: none where `_.units` is
// falsy (timeBin chooses them).
func timeUnitUnits(p *opParams) []string {
	if !paramTruthy(p.Get("units")) {
		return nil
	}
	return p.strs("units")
}

// timeUnitStrict reports, for each of the units, whether it is the string.
func timeUnitStrict(p *opParams) []bool {
	if !paramTruthy(p.Get("units")) {
		return nil
	}
	l := p.list("units")
	out := make([]bool, len(l))
	for i, e := range l {
		v, _ := paramValueOf(e)
		out[i] = v.IsStr()
	}
	return out
}
