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
// current row (negative before, positive after), or unbounded.
type FrameBound struct {
	Unbounded bool
	Offset    int
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
	// sortField is the field the comparator reads; err is set by adjustPeers
	// when upstream's comparator would be handed undefined.
	sortField string
	err       error
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
		off := 1
		if p := spec.Param; p == p && p != 0 && math.Abs(p) < 1e9 {
			off = int(p) // `+offset || 1`: NaN and 0 fall back to 1
		}
		if spec.Op == "lag" {
			return windowFn{nextFn: func(w *windowFrame) jsval.Value {
				if i := w.index - off; i >= 0 && i < len(w.data) {
					return get(w.data[i])
				}
				return jsval.Null
			}}, true, nil
		}
		return windowFn{nextFn: func(w *windowFrame) jsval.Value {
			if i := w.index + off; i >= 0 && i < len(w.data) {
				return get(w.data[i])
			}
			return jsval.Null
		}}, true, nil
	case "first_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return atOrUndefined(get, w.data, w.i0) }}, true, nil
	case "last_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		return windowFn{nextFn: func(w *windowFrame) jsval.Value { return atOrUndefined(get, w.data, w.i1-1) }}, true, nil
	case "nth_value":
		if err := needField(); err != nil {
			return nil, true, err
		}
		nth := spec.Param
		if !(nth > 0) {
			return nil, true, errors.New("nth_value nth must be greater than zero")
		}
		return windowFn{nextFn: func(w *windowFrame) jsval.Value {
			if nth > float64(len(w.data)) {
				return jsval.Null
			}
			if i := w.i0 + int(nth) - 1; i < w.i1 {
				return atOrUndefined(get, w.data, i)
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

// atOrUndefined reads the field of data[i]. Upstream throws when i is out of
// range (an empty frame); this answers undefined.
func atOrUndefined(get Accessor, data []jsval.Value, i int) jsval.Value {
	if i < 0 || i >= len(data) {
		return jsval.Undefined
	}
	return get(data[i])
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
		ms.ctx = ctx
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

	tick := budget.NewTicker(ctx)
	for _, g := range GroupByKey(data, KeyOf(p.GroupBy...)) {
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
		if ms != nil {
			c = ms.newCell()
		}
		for _, o := range wins {
			o.init()
		}
		for i := 0; i < n; i++ {
			if err := tick.Add(1); err != nil {
				return nil, err
			}
			w.p0, w.p1 = w.i0, w.i1
			start := 0
			if !frame[0].Unbounded {
				start = i + frame[0].Offset
			}
			end := n
			if !frame[1].Unbounded {
				end = i + frame[1].Offset + 1
			}
			w.i0, w.i1, w.index = min(max(start, 0), n), min(max(end, 0), n), i
			if rangeFrame {
				if adjustPeers(&w); w.err != nil {
					return nil, w.err
				}
			}
			t := rows[i].ObjValue()
			if c != nil {
				for j := w.p0; j < w.i0; j++ {
					ms.rem(c, rows[j])
				}
				for j := w.p1; j < w.i1; j++ {
					ms.add(c, rows[j])
				}
				c.data = nil
				if w.i0 < w.i1 {
					c.data = rows[w.i0:w.i1]
				}
				if t != nil {
					// Writing costs a pass over the frame (median, quartiles,
					// bootstrap, ...), so large frames are polled every row.
					if err := tick.Add(w.i1 - w.i0); err != nil {
						return nil, err
					}
					ms.write(c, t)
					if ms.err != nil {
						return nil, ms.err
					}
				}
			}
			for k, o := range wins {
				if v := o.next(&w); t != nil {
					t.Set(outs[k], v)
				}
			}
		}
	}
	return data, nil
}

// adjustPeers widens the frame to whole peer groups (rows equal under the sort
// comparator), as upstream's range frames do.
//
// Upstream indexes d[r0] and d[r1] without a bounds check: a window that
// starts past the last row (r0 == len) or ends before the first (r1 == -1)
// hands undefined to the comparator, which reads a field of it and throws.
func adjustPeers(w *windowFrame) {
	r0, r1, d, n := w.i0, w.i1-1, w.data, len(w.data)-1
	if r0 > 0 && r0 >= len(d) {
		w.err = readsUndefined(w.sortField)
		return
	}
	if r0 > 0 && w.compare(d[r0], d[r0-1]) == 0 {
		w.i0 = peerBound(d, d[r0], w.compare, false)
	}
	if r1 < n && r1 < 0 {
		w.err = readsUndefined(w.sortField)
		return
	}
	if r1 < n && w.compare(d[r1], d[r1+1]) == 0 {
		w.i1 = peerBound(d, d[r1], w.compare, true)
	}
}

// readsUndefined is the TypeError of a comparator reading field of undefined.
func readsUndefined(field string) error {
	if segs := jsval.ParseFieldPath(field); len(segs) > 0 {
		field = segs[0]
	}
	return fmt.Errorf("Cannot read properties of undefined (reading '%s')", field)
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
