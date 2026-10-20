package transforms

import (
	"context"
	"errors"
	"github.com/mgilbir/aster/internal/budget"
	"math"
	"slices"
	"strconv"

	"github.com/mgilbir/aster/internal/jsval"
)

// FrameBound is one end of a window frame: an offset in rows relative to the
// current row (negative before, positive after), or unbounded. The offset is
// any number, as upstream adds it to the row index and clamps the sum to the
// partition: an infinite or huge one reaches the partition's end, and a NaN
// or fractional one leaves the frame where upstream leaves it.
type FrameBound struct {
	Unbounded bool
	Offset    float64
	// Text, when IsText, is an offset that is no number or boolean (a
	// string, an array, an object) as its text: upstream's `i + f[0]` and
	// `i + f[1] + 1` concatenate it, so the frame's end is a string ("1" +
	// "0" is "10"), which compares as its number against a number, as text
	// against another string, and reads a row only when it is an array
	// index's canonical text.
	Text   string
	IsText bool
}

// WindowOpSpec is one window or aggregate operation of the window transform.
type WindowOpSpec struct {
	// Op is a window operation (row_number, rank, dense_rank, percent_rank,
	// cume_dist, ntile, lag, lead, first_value, last_value, nth_value,
	// prev_value, next_value) or any aggregate operation.
	Op    string
	Field Field // nil for the operations that take none (row_number, rank, count, ...)
	// Param is the window-op parameter (params[i]): the lag/lead offset, the
	// ntile bucket count or the nth_value index. NaN means unset.
	Param float64
	// AggParam is aggregate_params[i] for exponential aggregates (NaN:
	// null, see Measure.Param).
	AggParam float64
	As       string
	// AsExact is Measure.AsExact.
	AsExact bool
	// CountMeasure is Measure.CountMeasure for an aggregate operation.
	CountMeasure bool
}

// windowOpNames are the own properties of vega-transforms' WindowOps.
var windowOpNames = map[string]bool{
	"row_number": true, "rank": true, "dense_rank": true, "percent_rank": true, "cume_dist": true,
	"ntile": true, "lag": true, "lead": true, "first_value": true, "last_value": true,
	"nth_value": true, "prev_value": true, "next_value": true,
}

// IsWindowOp reports whether op names a window operation
// (hasOwnProperty(WindowOps, op)).
func IsWindowOp(op string) bool { return windowOpNames[op] }

// WindowParams configures Window (vega-transforms' window).
type WindowParams struct {
	// Sort orders tuples within a partition; nil keeps input order.
	Sort Comparator
	// SortField is the first field Sort compares, for the error of applying
	// it to undefined (see adjustPeers).
	SortField string
	// SortReadsTuple reports that the first field Sort compares is the tuple
	// itself (an accessor of no path): handed undefined, it compares it
	// (with no tie: undefined sorts before any tuple) instead of throwing.
	SortReadsTuple bool
	// GroupBy partitions the tuples.
	GroupBy []Field
	Ops     []WindowOpSpec
	// Frame is the two frame bounds; nil means the default [null, 0]: from the
	// partition start to the current row.
	Frame []FrameBound
	// IgnorePeers bases the frame on row position alone. By default, when Sort
	// is set, frame ends are widened to include peers that tie on Sort.
	IgnorePeers bool
	Rand        Rand
}

// windowFrame is upstream's window state: the current row, the frame [i0,i1)
// and the previous frame ends (p0,p1) used to update aggregates incrementally.
type windowFrame struct {
	i0, i1, p0, p1, index int
	data                  []jsval.Value
	compare               Comparator // never nil: without Sort it always says "different"
	// sortField is the field the comparator reads, for the error of handing
	// it undefined.
	sortField string
	sortWhole bool // WindowParams.SortReadsTuple
	// x0 and x1 are the frame's ends as upstream holds them (NaN or
	// fractional ones included), which the operations read rows at; err is
	// the exception one of them raised.
	x0, x1 float64
	// s0 and s1 are the ends' text when upstream holds them as strings
	// (FrameBound.Text), "" otherwise.
	s0, s1 string
	err    error
}

// rowAt is w.data[k] for a frame end k: a number reads a whole row index, a
// string an array index's canonical text; anything else is undefined (ok
// false).
func (w *windowFrame) rowAt(x float64, s string) (jsval.Value, bool) {
	if s != "" {
		k, ok := canonicalRowIndex(s)
		if !ok || k >= len(w.data) {
			return jsval.Undefined, false
		}
		return w.data[k], true
	}
	if x >= 0 && x < float64(len(w.data)) && x == math.Trunc(x) {
		return w.data[int(x)], true
	}
	return jsval.Undefined, false
}

// canonicalRowIndex is the array index s names ("0", "17"; not "05", "1.0"
// or "-1").
func canonicalRowIndex(s string) (int, bool) {
	if s == "" || len(s) > 9 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

// fieldAt is field(w.data[k]) for a frame end k (rowAt), the TypeError of
// reading a field of undefined recorded in w.err.
func (w *windowFrame) fieldAt(get Accessor, name string, x float64, s string) jsval.Value {
	if get == nil {
		return w.field(nil, name, x)
	}
	if t, ok := w.rowAt(x, s); ok {
		return get(t)
	}
	if w.err == nil {
		w.err = readsUndefined(name)
	}
	return jsval.Undefined
}

type windowOp interface {
	init()
	next(w *windowFrame) jsval.Value
}

// windowFn adapts a pair of closures to windowOp.
type windowFn struct {
	initFn func()
	nextFn func(w *windowFrame) jsval.Value
}

func (f windowFn) init() {
	if f.initFn != nil {
		f.initFn()
	}
}
func (f windowFn) next(w *windowFrame) jsval.Value { return f.nextFn(w) }

// newWindowOp builds the state machine for a window operation, mirroring
// vega-transforms' WindowOps. ok is false when op is not a window operation.
func newWindowOp(spec WindowOpSpec) (op windowOp, ok bool, err error) {
	// A null field is upstream's `field(...)` of null: the TypeError when
	// the operation first reads one (w.field), not when it is made.
	get := spec.Field.Get
	if spec.Field.IsNil() {
		get = nil
	}
	switch spec.Op {
	case "row_number":
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return jsval.Int(w.index + 1) }}, true, nil
	case "rank", "percent_rank":
		rank := 1
		next := func(w *windowFrame) int {
			i := w.index
			if i != 0 && w.compare(w.data[i-1], w.data[i]) != 0 {
				rank = i + 1
			}
			return rank
		}
		if spec.Op == "rank" {
			return windowFn{func() { rank = 1 }, func(w *windowFrame) jsval.Value { return jsval.Int(next(w)) }}, true, nil
		}
		return windowFn{func() { rank = 1 }, func(w *windowFrame) jsval.Value {
			return jsval.Num(float64(next(w)-1) / float64(len(w.data)-1))
		}}, true, nil
	case "dense_rank":
		drank := 1
		return windowFn{func() { drank = 1 }, func(w *windowFrame) jsval.Value {
			if i := w.index; i != 0 && w.compare(w.data[i-1], w.data[i]) != 0 {
				drank++
			}
			return jsval.Int(drank)
		}}, true, nil
	case "cume_dist", "ntile":
		cume := 0
		next := func(w *windowFrame) float64 {
			d, i := w.data, w.index
			if cume < i {
				for i+1 < len(d) && w.compare(d[i], d[i+1]) == 0 {
					i++
				}
				cume = i
			}
			return float64(1+cume) / float64(len(d))
		}
		if spec.Op == "cume_dist" {
			return windowFn{func() { cume = 0 }, func(w *windowFrame) jsval.Value { return jsval.Num(next(w)) }}, true, nil
		}
		num := spec.Param
		if !(num > 0) {
			return nil, true, errors.New("ntile num must be greater than zero.")
		}
		return windowFn{func() { cume = 0 }, func(w *windowFrame) jsval.Value {
			return jsval.Num(math.Ceil(float64(num * next(w))))
		}}, true, nil
	case "lag", "lead":
		// offset = +offset || 1: NaN and 0 fall back to 1. Any other number
		// is used as it is, so a huge one reads past the partition (null).
		off := spec.Param
		if off == 0 || off != off {
			off = 1
		}
		// lag tests i >= 0 and lead i < length, and nothing else: a row
		// past the other end, or between two rows, is undefined, and
		// reading its field is a TypeError.
		name := spec.Field.Name
		if spec.Op == "lag" {
			return windowFn{nextFn: func(w *windowFrame) jsval.Value {
				if i := float64(w.index) - off; i >= 0 {
					return w.field(get, name, i)
				}
				return jsval.Null
			}}, true, nil
		}
		return windowFn{nextFn: func(w *windowFrame) jsval.Value {
			if i := float64(w.index) + off; i < float64(len(w.data)) {
				return w.field(get, name, i)
			}
			return jsval.Null
		}}, true, nil
	case "first_value":
		name := spec.Field.Name
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return w.fieldAt(get, name, w.x0, w.s0) }}, true, nil
	case "last_value":
		name := spec.Field.Name
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return w.field(get, name, w.x1-1) }}, true, nil
	case "nth_value":
		nth := spec.Param
		if !(nth > 0) {
			return nil, true, errors.New("nth_value nth must be greater than zero.")
		}
		name := spec.Field.Name
		return windowFn{nextFn: func(w *windowFrame) jsval.Value {
			// i = w.i0 + (nth - 1) < w.i1, in floating point: a huge nth is
			// past the frame (null), a NaN end is never less.
			if w.s0 != "" {
				// a string start concatenates: "1" + 2 is "12", which
				// compares with the end as text when it is a string too
				i := w.s0 + jsval.JSNumberString(nth-1)
				x := jsval.StringToNumber(i)
				if w.s1 != "" && jsval.CompareUTF16(i, w.s1) < 0 || w.s1 == "" && x < w.x1 {
					return w.fieldAt(get, name, x, i)
				}
				return jsval.Null
			}
			if i := w.x0 + (nth - 1); i < w.x1 {
				return w.field(get, name, i)
			}
			return jsval.Null
		}}, true, nil
	case "prev_value":
		prev := jsval.Null
		name := spec.Field.Name
		return windowFn{func() { prev = jsval.Null }, func(w *windowFrame) jsval.Value {
			if v := w.field(get, name, float64(w.index)); !v.IsNullish() {
				prev = v
			}
			return prev
		}}, true, nil
	case "next_value":
		v, at := jsval.Null, -1
		name := spec.Field.Name
		return windowFn{func() { v, at = jsval.Null, -1 }, func(w *windowFrame) jsval.Value {
			if w.index <= at {
				return v
			}
			d := w.data
			j := w.index
			for j < len(d) && w.field(get, name, float64(j)).IsNullish() {
				if w.err != nil {
					return jsval.Undefined
				}
				j++
			}
			if j >= len(d) {
				at, v = len(d), jsval.Null
				return v
			}
			at, v = j, w.field(get, name, float64(j))
			return v
		}}, true, nil
	}
	return nil, false, nil
}

// field is field(w.data[i]) for a window operation: a position that is not
// a row (past either end, between two rows, NaN) is undefined, and reading a
// field of it is the TypeError upstream throws, recorded in w.err for Window
// to return through the operator's error, as upstream's dataflow catches it.
func (w *windowFrame) field(get Accessor, name string, i float64) jsval.Value {
	if get == nil {
		if w.err == nil {
			w.err = &jsval.Thrown{Name: "TypeError", Msg: "field is not a function"}
		}
		return jsval.Undefined
	}
	if i >= 0 && i < float64(len(w.data)) && i == math.Trunc(i) {
		return get(w.data[int(i)])
	}
	if w.err == nil {
		w.err = readsUndefined(name)
	}
	return jsval.Undefined
}

// Window computes window calculations per partition and writes the results
// into the input tuples, which are returned in input order. Within a partition
// tuples are ordered by Sort (stably), each output field is written on every
// tuple: aggregate outputs first, then the window operations in order, as
// upstream does.
func Window(ctx context.Context, data []jsval.Value, p WindowParams) ([]jsval.Value, error) {
	var (
		aggs []Measure
		wins []windowOp
		outs []string
	)
	// WindowState's constructor, in the order of the ops: a window
	// operation's parameter is checked as it is made, an aggregate's field
	// as it is read
	for _, spec := range p.Ops {
		name := spec.Name()
		op, isWin, err := newWindowOp(spec)
		if err != nil {
			return nil, err
		}
		if isWin {
			wins = append(wins, op)
			outs = append(outs, name)
			continue
		}
		if spec.Field.IsNil() && (spec.Op != "count" || spec.CountMeasure) {
			return nil, errNullAggregateField
		}
		aggs = append(aggs, Measure{Op: spec.Op, Field: spec.Field, Param: spec.AggParam, As: spec.As, CountMeasure: spec.CountMeasure})
	}
	var ms *measureSet
	if len(aggs) > 0 {
		var err error
		if ms, err = newMeasureSet(aggs, p.Rand); err != nil {
			return nil, err
		}
		ms.setContext(ctx)
	}
	frame := []FrameBound{{Unbounded: true}, {Offset: 0}}
	if len(p.Frame) == 2 {
		frame = p.Frame
	}
	compare := p.Sort
	rangeFrame := compare != nil && !p.IgnorePeers
	if compare == nil {
		compare = func(a, b jsval.Value) int { return -1 }
	}

	f0, f1 := math.Inf(-1), math.Inf(1)
	if !frame[0].Unbounded {
		f0 = frame[0].Offset
	}
	if !frame[1].Unbounded {
		f1 = frame[1].Offset
	}
	// A frame whose ends are whole (or infinite) and never cross keeps the
	// rows of a cell exactly [i0, i1); any other runs upstream's tuple store
	// as it is (windowStore.slow).
	regular := wholeOrInf(f0) && wholeOrInf(f1) && f0 <= f1+1 && !frame[0].IsText && !frame[1].IsText

	m := budget.NewMeter(ctx)
	led := budget.LedgerFrom(ctx)
	groups, err := GroupByKey(ctx, data, KeyOf(keysOf(ctx), p.GroupBy...))
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if jsval.ObjectPrototypeHas(g.Key) {
			// `this.value[key]` of a plain object finds Object.prototype's
			// member of that name, which has no add
			return nil, &jsval.Thrown{Name: "TypeError", Msg: "group(...).add is not a function"}
		}
	}
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows := slices.Clone(g.Tuples)
		if p.Sort != nil {
			SortTuples(rows, StableComparator(p.Sort))
		}
		n := len(rows)
		w := windowFrame{data: rows, compare: compare, sortField: p.SortField, sortWhole: p.SortReadsTuple}
		var c *cell
		var st *windowStore
		if ms != nil {
			c = ms.newCell()
			if ms.needStore {
				st = &windowStore{rows: rows, slow: !regular, ledger: led}
				ms.store = st
			}
		}
		for _, o := range wins {
			o.init()
		}
		var x0, x1 float64 // the frame as upstream holds it, for the slow store
		var s0, s1 string  // ... and its text, when it holds a string
		for i := 0; i < n; i++ {
			if err := m.Work(1); err != nil {
				return nil, err
			}
			w.p0, w.p1 = w.i0, w.i1
			p0, p1 := x0, x1
			ps0, ps1 := s0, s1
			// start = f[0] == null ? 0 : i + f[0], end = f[1] == null ? n :
			// i + f[1] + 1, each clamped to [0, n].
			x0, x1, s0, s1 = 0, float64(n), "", ""
			if !frame[0].Unbounded {
				if frame[0].IsText {
					x0, s0 = clampFrameText(strconv.Itoa(i)+frame[0].Text, n)
				} else {
					x0 = clampFrame(float64(i)+f0, n)
				}
			}
			if !frame[1].Unbounded {
				if frame[1].IsText {
					x1, s1 = clampFrameText(strconv.Itoa(i)+frame[1].Text+"1", n)
				} else {
					x1 = clampFrame(float64(i)+f1+1, n)
				}
			}
			if rangeFrame {
				a0, a1, err := adjustPeers(&w, x0, x1, s0)
				if err != nil {
					return nil, err
				}
				// a bisection's index replaces an end, a number
				if a0 != x0 && !(math.IsNaN(a0) && math.IsNaN(x0)) {
					x0, s0 = a0, ""
				}
				if a1 != x1 && !(math.IsNaN(a1) && math.IsNaN(x1)) {
					x1, s1 = a1, ""
				}
			}
			w.i0, w.i1, w.index = frameIndex(x0), frameIndex(x1), i
			w.x0, w.x1, w.s0, w.s1 = x0, x1, s0, s1
			t := jsval.Fields(rows[i])
			if c != nil {
				var err error
				if regular {
					err = windowMoveRegular(ms, c, st, rows, w.p0, w.p1, w.i0, w.i1)
				} else {
					err = windowMoveSlow(ms, c, st, rows, framePos{p0, ps0}, framePos{p1, ps1}, framePos{x0, s0}, framePos{x1, s1}, &m)
				}
				if err != nil {
					return nil, err
				}
				if t != nil {
					// Writing costs a pass over the frame (median, quartiles,
					// bootstrap, ...), so large frames are metered every row.
					if err := m.Work(int64(len(c.data))); err != nil {
						return nil, err
					}
					ms.write(c, t)
					if ms.err != nil {
						return nil, ms.err
					}
				}
			}
			for k, o := range wins {
				v := o.next(&w)
				if w.err != nil {
					return nil, w.err
				}
				if t == nil {
					t = jsval.FieldsFor(rows[i], outs[k])
				}
				t.Set(outs[k], v)
			}
		}
		if err := st.flush(); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// wholeOrInf reports an offset that is an integer or infinite.
func wholeOrInf(f float64) bool {
	return math.IsInf(f, 0) || f == math.Trunc(f)
}

// clampFrame is upstream's clamp(x, 0, n); NaN stays NaN.
func clampFrame(x float64, n int) float64 {
	switch {
	case x < 0:
		return 0
	case x > float64(n):
		return float64(n)
	}
	return x
}

// clampFrameText is clamp(x, 0, n) of a frame end upstream computes as the
// string s: below 0 or above n (as a number) it is that bound, a number;
// otherwise, NaN included, the string stays, with the number it reads as.
func clampFrameText(s string, n int) (float64, string) {
	x := jsval.StringToNumber(s)
	switch {
	case x < 0:
		return 0, ""
	case x > float64(n):
		return float64(n), ""
	}
	return x, s
}

// frameIndex is a clamped frame position as an index, for the window
// operations, which read data[i0] and data[i1 - 1]: a position that is not a
// whole number reads past the rows (see windowFrame.field).
func frameIndex(x float64) int {
	if x != math.Trunc(x) { // NaN or fractional
		return -1 << 30
	}
	return int(x)
}

// adjustPeers widens the frame [x0, x1) to whole peer groups (rows equal
// under the sort comparator), as upstream's range frames do.
//
// Upstream indexes d[r0] and d[r1] without a bounds check: a window that
// starts past the last row (r0 == len), ends before the first (r1 == -1), or
// has a fractional end hands undefined to the comparator, which reads a
// field of it and throws. A NaN end is never compared.
func adjustPeers(w *windowFrame, x0, x1 float64, s0 string) (float64, float64, error) {
	d, n := w.data, float64(len(w.data)-1)
	if r0 := x0; r0 > 0 {
		// d[r0] reads the start as it is (a string by its text), d[r0-1]
		// the number before it.
		if _, ok := w.rowAt(r0, s0); !ok || r0 > n || r0 != math.Trunc(r0) {
			if !w.sortWhole {
				return x0, x1, readsUndefined(w.sortField)
			}
			// undefined against a row: never a tie
		} else if i := int(r0); w.compare(d[i], d[i-1]) == 0 {
			x0 = float64(peerBound(d, d[i], w.compare, false))
		}
	}
	if r1 := x1 - 1; r1 < n {
		if r1 < 0 || r1 != math.Trunc(r1) {
			if !w.sortWhole {
				return x0, x1, readsUndefined(w.sortField)
			}
		} else if i := int(r1); w.compare(d[i], d[i+1]) == 0 {
			x1 = float64(peerBound(d, d[i], w.compare, true))
		}
	}
	return x0, x1, nil
}

// readsUndefined is the TypeError of a comparator reading field of undefined.
func readsUndefined(field string) error {
	if segs := jsval.ParseFieldPath(field); len(segs) > 0 {
		field = segs[0]
	}
	return &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of undefined (reading '" + field + "')"}
}

// peerBound is d3's bisector.left (right=false) or bisector.right over the
// whole sorted slice with a comparator.
func peerBound(d []jsval.Value, x jsval.Value, cmp Comparator, right bool) int {
	lo, hi := 0, len(d)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		c := cmp(d[mid], x)
		if c < 0 || (right && c == 0) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Name is the output field: measureName(op, accessorName(field), as).
func (s WindowOpSpec) Name() string {
	if s.AsExact {
		return s.As
	}
	return MeasureName(s.Op, s.Field.Name, s.As)
}
