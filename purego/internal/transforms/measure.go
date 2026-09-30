package transforms

import (
	"fmt"
	"math"
	"slices"

	"github.com/mgilbir/aster/purego/internal/jsmath"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// opKind enumerates the aggregate operations of vega-transforms.
type opKind uint8

const (
	opValues opKind = iota
	opCount
	opMissing
	opValid
	opSum
	opProduct
	opMean
	opAverage
	opVariance
	opVariancep
	opStdev
	opStdevp
	opStderr
	opDistinct
	opCI0
	opCI1
	opMedian
	opQ1
	opQ3
	opMin
	opMax
	opArgmin
	opArgmax
	opExponential
	opExponentialB
)

var opKinds = map[string]opKind{
	"values": opValues, "count": opCount, "missing": opMissing, "valid": opValid,
	"sum": opSum, "product": opProduct, "mean": opMean, "average": opAverage,
	"variance": opVariance, "variancep": opVariancep, "stdev": opStdev,
	"stdevp": opStdevp, "stderr": opStderr, "distinct": opDistinct,
	"ci0": opCI0, "ci1": opCI1, "median": opMedian, "q1": opQ1, "q3": opQ3,
	"min": opMin, "max": opMax, "argmin": opArgmin, "argmax": opArgmax,
	"exponential": opExponential, "exponentialb": opExponentialB,
}

// ValidAggregateOps lists the operation names aggregate, joinaggregate and
// window accept, in upstream's order.
var ValidAggregateOps = []string{
	"values", "count", "missing", "valid", "sum", "product", "mean", "average",
	"variance", "variancep", "stdev", "stdevp", "stderr", "distinct", "ci0",
	"ci1", "median", "q1", "q3", "min", "max", "argmin", "argmax",
	"exponential", "exponentialb",
}

// IsAggregateOp reports whether op names an aggregate operation.
func IsAggregateOp(op string) bool { _, ok := opKinds[op]; return ok }

// opIndex is upstream's `idx`: outputs are written in this order, and it is
// also the order in which the incremental state is updated (variance needs the
// mean already updated, argmin needs the previous min, ...).
func (k opKind) index() int {
	switch k {
	case opValues:
		return -1
	case opAverage, opVariance, opExponentialB:
		return 1
	case opVariancep, opStdev, opStdevp, opStderr:
		return 2
	case opDistinct, opCI0, opCI1, opMedian, opQ1, opQ3, opArgmin, opArgmax:
		return 3
	case opMin, opMax:
		return 4
	}
	return 0
}

// Measure is one aggregation: an operation over a field with an optional
// parameter (the decay of exponential) and output name.
type Measure struct {
	Op    string
	Field Field // the null Field is only valid for "count"
	// Param is aggregate_params[i]; 0 means unset (upstream turns a falsy
	// parameter into null).
	Param float64
	// As is the output field; empty means the derived default (MeasureName).
	As string
}

type measureOut struct {
	name string
	kind opKind
	idx  int
}

// measureGroup is upstream's compiled measures for one input field: the
// outputs derived from it and which running statistics they need.
type measureGroup struct {
	field Field
	outs  []measureOut // sorted by idx, stable

	sum, product, mean, variance, exp, minmax bool
	argmin, argmax, wantMin, wantMax          bool
	expR                                      float64
}

// mstate is the running state of one measureGroup in one cell.
type mstate struct {
	valid, missing                 int
	sum, product, mean, meanD, dev float64
	exp                            float64
	min, max                       jsval.Value
	argmin, argmax                 jsval.Value
}

func (g *measureGroup) newState() mstate {
	return mstate{product: 1, min: jsval.Undefined, max: jsval.Undefined}
}

// measureSet is a compiled list of measures: counts (which need no field) and
// one measureGroup per distinct input field name.
type measureSet struct {
	counts    []string
	groups    []*measureGroup
	countOnly bool
	needStore bool // some output reads the tuples of its cell
	rand      Rand
	// names lists output field names in the order they were requested.
	names []string
}

// newMeasureSet compiles measures. Upstream groups the measures of one field
// name into a single accumulator so a field's statistics are computed once.
func newMeasureSet(ms []Measure, rnd Rand) (*measureSet, error) {
	if rnd == nil {
		rnd = DefaultRand
	}
	s := &measureSet{countOnly: true, rand: rnd}
	byName := map[string]*measureGroup{}
	for _, m := range ms {
		kind, ok := opKinds[m.Op]
		if !ok {
			return nil, fmt.Errorf("unknown aggregate op %q", m.Op)
		}
		if m.Field.IsNil() && m.Op != "count" {
			return nil, fmt.Errorf("null aggregate field specified")
		}
		name := MeasureName(m.Op, m.Field.Name, m.As)
		s.names = append(s.names, name)
		if kind == opCount {
			s.counts = append(s.counts, name)
			continue
		}
		s.countOnly = false
		g := byName[m.Field.Name]
		if g == nil {
			g = &measureGroup{field: m.Field, expR: math.NaN()}
			byName[m.Field.Name] = g
			s.groups = append(s.groups, g)
		}
		g.outs = append(g.outs, measureOut{name: name, kind: kind, idx: kind.index()})
		if kind == opExponential && m.Param != 0 {
			// Every exponential output of the field shares the last parameter
			// (upstream keys its operator table by op name).
			g.expR = m.Param
		}
	}
	for _, g := range s.groups {
		slices.SortStableFunc(g.outs, func(a, b measureOut) int { return a.idx - b.idx })
		for _, o := range g.outs {
			switch o.kind {
			case opValues, opDistinct, opCI0, opCI1, opMedian, opQ1, opQ3:
				s.needStore = true
			case opSum:
				g.sum = true
			case opProduct:
				g.product = true
			case opMean, opAverage:
				g.mean = true
			case opVariance, opVariancep, opStdev, opStdevp, opStderr:
				g.mean, g.variance = true, true
			case opExponential, opExponentialB:
				g.exp = true
			case opMin:
				g.wantMin = true
				s.needStore = true
			case opMax:
				g.wantMax = true
				s.needStore = true
			case opArgmin:
				g.argmin, g.wantMin = true, true
				s.needStore = true
			case opArgmax:
				g.argmax, g.wantMax = true, true
				s.needStore = true
			}
		}
	}
	return s, nil
}

// cell is the accumulator of one group of tuples.
type cell struct {
	num  int
	aggs []mstate
	// data holds the tuples of the cell for outputs that read them; the
	// caller maintains it (aggregate appends, window points it at the frame).
	data []jsval.Value
	memo storeMemo
}

func (s *measureSet) newCell() *cell {
	c := &cell{}
	if !s.countOnly {
		c.aggs = make([]mstate, len(s.groups))
		for i, g := range s.groups {
			c.aggs[i] = g.newState()
		}
	}
	return c
}

func (s *measureSet) resetCell(c *cell) {
	c.num = 0
	c.data = nil
	c.memo = storeMemo{}
	for i, g := range s.groups {
		c.aggs[i] = g.newState()
	}
}

// add accumulates t into c. It does not touch c.data.
func (s *measureSet) add(c *cell, t jsval.Value) {
	c.num++
	if s.countOnly {
		return
	}
	for i, g := range s.groups {
		g.add(&c.aggs[i], g.field.Get(t), t)
	}
}

// rem removes t from c's running statistics. It does not touch c.data.
func (s *measureSet) rem(c *cell, t jsval.Value) {
	c.num--
	if s.countOnly {
		return
	}
	for i, g := range s.groups {
		g.rem(&c.aggs[i], g.field.Get(t))
	}
}

func isEmptyString(v jsval.Value) bool { return v.IsStr() && v.StrValue() == "" }

func (g *measureGroup) add(m *mstate, v, t jsval.Value) {
	if v.IsNullish() || isEmptyString(v) {
		m.missing++
		return
	}
	if v.IsNum() && v.NumValue() != v.NumValue() {
		return
	}
	m.valid++
	var x float64
	if g.sum || g.product || g.mean || g.exp {
		x = jsval.ToNumber(v)
	}
	if g.sum {
		m.sum += x
	}
	if g.product {
		m.product *= x
	}
	if g.mean {
		m.meanD = x - m.mean
		m.mean += m.meanD / float64(m.valid)
	}
	if g.exp {
		m.exp = float64(g.expR*m.exp) + x
	}
	if g.variance {
		m.dev += float64(m.meanD * (x - m.mean))
	}
	// argmin/argmax run before min/max, so they compare against the previous
	// extremum; the first value therefore never sets them and the extent of the
	// stored tuples is used instead (see write).
	if g.argmin && Less(v, m.min) {
		m.argmin = t
	}
	if g.argmax && Greater(v, m.max) {
		m.argmax = t
	}
	if g.wantMin && (m.min.IsUndefined() || Less(v, m.min)) {
		m.min = v
	}
	if g.wantMax && (m.max.IsUndefined() || Greater(v, m.max)) {
		m.max = v
	}
}

func (g *measureGroup) rem(m *mstate, v jsval.Value) {
	if v.IsNullish() || isEmptyString(v) {
		m.missing--
		return
	}
	if v.IsNum() && v.NumValue() != v.NumValue() {
		return
	}
	m.valid--
	var x float64
	if g.sum || g.product || g.mean || g.exp {
		x = jsval.ToNumber(v)
	}
	if g.sum {
		m.sum -= x
	}
	if g.product {
		m.product /= x
	}
	if g.mean {
		m.meanD = x - m.mean
		if m.valid != 0 {
			m.mean -= m.meanD / float64(m.valid)
		} else {
			m.mean -= m.mean
		}
	}
	if g.exp {
		m.exp = (m.exp - x/jsmath.Pow(g.expR, float64(m.valid-1))) / g.expR
	}
	if g.variance {
		m.dev -= float64(m.meanD * (x - m.mean))
	}
	// A removed extremum invalidates the running one (NaN marks "recompute").
	if g.argmin && LessEq(v, m.min) {
		m.argmin = jsval.Undefined
	}
	if g.argmax && GreaterEq(v, m.max) {
		m.argmax = jsval.Undefined
	}
	if g.wantMin && LessEq(v, m.min) {
		m.min = jsval.Num(math.NaN())
	}
	if g.wantMax && GreaterEq(v, m.max) {
		m.max = jsval.Num(math.NaN())
	}
}

// write stores the outputs of cell c into t: counts first, then each group's
// measures in index order, as upstream's celltuple/set do.
func (s *measureSet) write(c *cell, t *jsval.Object) {
	for _, n := range s.counts {
		t.Set(n, jsval.Int(c.num))
	}
	if s.countOnly {
		return
	}
	// TupleStore.values(): consolidating the store forgets which accessor the
	// memoised statistics belong to (but not the statistics themselves).
	c.memo.owner = 0
	for i, g := range s.groups {
		g.write(s, i+1, &c.aggs[i], c.data, &c.memo, t)
	}
}

// storeMemo mirrors upstream TupleStore's memoisation of derived statistics.
// It is reproduced faithfully, including a quirk that is visible in output:
// the extent, quartile and interval caches share ONE "current accessor" slot
// (owner) and none of them is invalidated when tuples come and go. Only
// values(), called before each output is written, clears the owner. So when
// median/q1/q3/ci run before min/max/argmin/argmax for the same field, the
// latter find the owner already set and reuse an extent computed for an earlier
// row (window frames) or an earlier field (aggregate cells).
type storeMemo struct {
	owner  int // 1-based measureGroup index of the accessor last memoised, 0 for none
	ext    [2]jsval.Value
	extOK  bool
	q      [3]float64
	qOK    bool
	ci     [2]float64
	ciOK   bool // the interval has been computed at least once
	ciDone bool // ... and was defined
}

func (g *measureGroup) write(s *measureSet, id int, m *mstate, data []jsval.Value, memo *storeMemo, t *jsval.Object) {
	get := g.field.Get
	extent := func() [2]jsval.Value {
		if memo.owner != id || !memo.extOK {
			lo, hi := ExtentIndex(len(data), func(i int) jsval.Value { return get(data[i]) })
			memo.ext = [2]jsval.Value{elemAt(data, lo), elemAt(data, hi)}
			memo.extOK, memo.owner = true, id
		}
		return memo.ext
	}
	quart := func() [3]float64 {
		if memo.owner != id || !memo.qOK {
			memo.q = Quartiles(data, get)
			memo.qOK, memo.owner = true, id
		}
		return memo.q
	}
	ci := func() ([2]float64, bool) {
		if memo.owner != id || !memo.ciOK {
			lo, hi, ok := BootstrapCI(data, 1000, 0.05, get, s.rand)
			memo.ci, memo.ciDone, memo.ciOK, memo.owner = [2]float64{lo, hi}, ok, true, id
		}
		return memo.ci, memo.ciDone
	}
	valid := float64(m.valid)
	for _, o := range g.outs {
		var v jsval.Value
		switch o.kind {
		case opValues:
			v = jsval.Arr(slices.Clone(data))
		case opMissing:
			v = jsval.Int(m.missing)
		case opValid:
			v = jsval.Int(m.valid)
		case opSum:
			v = validOr(m, jsval.Num(m.sum))
		case opProduct:
			v = validOr(m, jsval.Num(m.product))
		case opMean, opAverage:
			v = validOr(m, jsval.Num(m.mean))
		case opVariance:
			if m.valid > 1 {
				v = jsval.Num(math.Max(0, m.dev) / (valid - 1))
			}
		case opVariancep:
			if m.valid > 0 {
				v = jsval.Num(math.Max(0, m.dev) / valid)
			}
		case opStdev:
			if m.valid > 1 {
				v = jsval.Num(math.Sqrt(math.Max(0, m.dev) / (valid - 1)))
			}
		case opStdevp:
			if m.valid > 0 {
				v = jsval.Num(math.Sqrt(math.Max(0, m.dev) / valid))
			}
		case opStderr:
			if m.valid > 1 {
				v = jsval.Num(math.Sqrt(math.Max(0, m.dev) / (valid * (valid - 1))))
			}
		case opDistinct:
			seen := make(map[string]struct{}, 16)
			for i := len(data) - 1; i >= 0; i-- {
				seen[get(data[i]).AsString()] = struct{}{}
			}
			v = jsval.Int(len(seen))
		case opCI0, opCI1:
			if r, ok := ci(); ok {
				v = jsval.Num(r[o.kind-opCI0])
			}
		case opMedian, opQ1, opQ3:
			q := quart()
			idx := map[opKind]int{opQ1: 0, opMedian: 1, opQ3: 2}[o.kind]
			if f := q[idx]; !math.IsNaN(f) {
				v = jsval.Num(f)
			}
		case opMin:
			if m.min.IsNum() && math.IsNaN(m.min.NumValue()) {
				m.min = jsval.Undefined
				if e := extent()[0]; !e.IsNullish() {
					m.min = get(e)
				}
			}
			v = m.min
		case opMax:
			if m.max.IsNum() && math.IsNaN(m.max.NumValue()) {
				m.max = jsval.Undefined
				if e := extent()[1]; !e.IsNullish() {
					m.max = get(e)
				}
			}
			v = m.max
		case opArgmin:
			v = m.argmin
			if !v.IsTruthy() {
				v = objOrEmpty(extent()[0])
			}
		case opArgmax:
			v = m.argmax
			if !v.IsTruthy() {
				v = objOrEmpty(extent()[1])
			}
		case opExponential:
			if m.valid > 0 {
				v = jsval.Num(float64(m.exp*(1-g.expR)) / (1 - jsmath.Pow(g.expR, valid)))
			}
		case opExponentialB:
			if m.valid > 0 {
				v = jsval.Num(m.exp * (1 - g.expR))
			}
		}
		t.Set(o.name, v)
	}
}

func elemAt(data []jsval.Value, i int) jsval.Value {
	if i < 0 || i >= len(data) {
		return jsval.Undefined
	}
	return data[i]
}

// objOrEmpty is `x || {}`.
func objOrEmpty(v jsval.Value) jsval.Value {
	if v.IsTruthy() {
		return v
	}
	return jsval.Obj(nil)
}

func validOr(m *mstate, v jsval.Value) jsval.Value {
	if m.valid == 0 {
		return jsval.Undefined
	}
	return v
}
