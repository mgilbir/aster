package transforms

import (
	"context"
	"errors"
	"fmt"
	"github.com/mgilbir/aster/internal/budget"
	"math"
	"slices"

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
	// AggParam is aggregate_params[i] for exponential aggregates (0: unset).
	AggParam float64
	As       string
}

// WindowParams configures Window (vega-transforms' window).
type WindowParams struct {
	// Sort orders tuples within a partition; nil keeps input order.
	Sort Comparator
	// SortField is the first field Sort compares, for the error of applying
	// it to undefined (see adjustPeers).
	SortField string
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
	// x0 and x1 are the frame's ends as upstream holds them (NaN or
	// fractional ones included), which the operations read rows at; err is
	// the exception one of them raised.
	x0, x1 float64
	err    error
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

var errNoWindowField = errors.New("window operation requires a field")

// newWindowOp builds the state machine for a window operation, mirroring
// vega-transforms' WindowOps. ok is false when op is not a window operation.
func newWindowOp(spec WindowOpSpec) (op windowOp, ok bool, err error) {
	needField := func() error {
		if spec.Field.IsNil() {
			return fmt.Errorf("%s: %w", spec.Op, errNoWindowField)
		}
		return nil
	}
	get := spec.Field.Get
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
			return nil, true, errors.New("ntile num must be greater than zero")
		}
		return windowFn{func() { cume = 0 }, func(w *windowFrame) jsval.Value {
			return jsval.Num(math.Ceil(float64(num * next(w))))
		}}, true, nil
	case "lag", "lead":
		if err := needField(); err != nil {
			return nil, true, err
		}
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
		if err := needField(); err != nil {
			return nil, true, err
		}
		name := spec.Field.Name
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return w.field(get, name, w.x0) }}, true, nil
	case "last_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		name := spec.Field.Name
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return w.field(get, name, w.x1-1) }}, true, nil
	case "nth_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		nth := spec.Param
		if !(nth > 0) {
			return nil, true, errors.New("nth_value nth must be greater than zero")
		}
		name := spec.Field.Name
		return windowFn{nextFn: func(w *windowFrame) jsval.Value {
			// i = w.i0 + (nth - 1) < w.i1, in floating point: a huge nth is
			// past the frame (null), a NaN end is never less.
			if i := w.x0 + (nth - 1); i < w.x1 {
				return w.field(get, name, i)
			}
			return jsval.Null
		}}, true, nil
	case "prev_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		prev := jsval.Null
		return windowFn{func() { prev = jsval.Null }, func(w *windowFrame) jsval.Value {
			if v := get(w.data[w.index]); !v.IsNullish() {
				prev = v
			}
			return prev
		}}, true, nil
	case "next_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		v, at := jsval.Null, -1
		return windowFn{func() { v, at = jsval.Null, -1 }, func(w *windowFrame) jsval.Value {
			if w.index <= at {
				return v
			}
			d := w.data
			j := w.index
			for j < len(d) && get(d[j]).IsNullish() {
				j++
			}
			if j >= len(d) {
				at, v = len(d), jsval.Null
				return v
			}
			at, v = j, get(d[j])
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
	for _, spec := range p.Ops {
		name := MeasureName(spec.Op, spec.Field.Name, spec.As)
		op, isWin, err := newWindowOp(spec)
		if err != nil {
			return nil, err
		}
		if isWin {
			wins = append(wins, op)
			outs = append(outs, name)
			continue
		}
		aggs = append(aggs, Measure{Op: spec.Op, Field: spec.Field, Param: spec.AggParam, As: spec.As})
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
	regular := wholeOrInf(f0) && wholeOrInf(f1) && f0 <= f1+1

	m := budget.NewMeter(ctx)
	led := budget.LedgerFrom(ctx)
	for _, g := range GroupByKey(data, KeyOf(keysOf(ctx), p.GroupBy...)) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows := slices.Clone(g.Tuples)
		if p.Sort != nil {
			SortTuples(rows, StableComparator(p.Sort))
		}
		n := len(rows)
		w := windowFrame{data: rows, compare: compare, sortField: p.SortField}
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
		for i := 0; i < n; i++ {
			if err := m.Work(1); err != nil {
				return nil, err
			}
			w.p0, w.p1 = w.i0, w.i1
			p0, p1 := x0, x1
			// start = f[0] == null ? 0 : i + f[0], end = f[1] == null ? n :
			// i + f[1] + 1, each clamped to [0, n].
			x0, x1 = 0, float64(n)
			if !frame[0].Unbounded {
				x0 = clampFrame(float64(i)+f0, n)
			}
			if !frame[1].Unbounded {
				x1 = clampFrame(float64(i)+f1+1, n)
			}
			if rangeFrame {
				var err error
				if x0, x1, err = adjustPeers(&w, x0, x1); err != nil {
					return nil, err
				}
			}
			w.i0, w.i1, w.index = frameIndex(x0), frameIndex(x1), i
			w.x0, w.x1 = x0, x1
			t := rows[i].ObjValue()
			if c != nil {
				var err error
				if regular {
					err = windowMoveRegular(ms, c, st, rows, w.p0, w.p1, w.i0, w.i1)
				} else {
					err = windowMoveSlow(ms, c, st, rows, p0, p1, x0, x1, &m)
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
				if t != nil {
					t.Set(outs[k], v)
				}
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
func adjustPeers(w *windowFrame, x0, x1 float64) (float64, float64, error) {
	d, n := w.data, float64(len(w.data)-1)
	if r0 := x0; r0 > 0 {
		if r0 > n || r0 != math.Trunc(r0) {
			return x0, x1, readsUndefined(w.sortField)
		}
		if i := int(r0); w.compare(d[i], d[i-1]) == 0 {
			x0 = float64(peerBound(d, d[i], w.compare, false))
		}
	}
	if r1 := x1 - 1; r1 < n {
		if r1 < 0 || r1 != math.Trunc(r1) {
			return x0, x1, readsUndefined(w.sortField)
		}
		if i := int(r1); w.compare(d[i], d[i+1]) == 0 {
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
