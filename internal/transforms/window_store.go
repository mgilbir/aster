package transforms

import (
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// windowStore is upstream's TupleStore as a window cell uses it: the frame's
// tuples, added and removed as the frame moves, consolidated before each row
// is written (values()). Two of its behaviours show in the output:
//
//   - values() returns the store's own array, and a later row's additions
//     are pushed into it, until a removal makes values() build a new one. So
//     every row written between two removals holds the same array, which
//     ends up holding everything added until the next removal: with a frame
//     of [null, 0] every row's `values` is the whole partition. The rows of
//     such a run (an epoch) share one array here too, built when the epoch
//     ends, which keeps memory linear where a copy per row is quadratic.
//   - With a frame whose ends cross, or are NaN or fractional, the store is
//     told to remove tuples it never added, and its arithmetic shows: a
//     removal is forgotten, the array has holes, or its length goes negative
//     (a RangeError). Such frames (slow) run the store as upstream does;
//     every other frame holds exactly the rows [i0, i1) and is tracked by
//     its ends.
type windowStore struct {
	rows   []jsval.Value
	slow   bool
	ledger *budget.Ledger
	holes  bool // add has undefined entries (see measureSet.store)

	// The rows of the current epoch's array: rows[e0:hi] (regular frames), or
	// add (slow ones).
	e0, hi int
	add    []jsval.Value
	rem    []jsval.Value
	// pending are the rows written in the current epoch, which get its
	// array when it ends.
	pending []pendingValues
}

type pendingValues struct {
	t    *jsval.Object
	name string
}

// share is the `values` output of t: a placeholder (the field keeps its
// place among t's keys) that flush replaces with the epoch's array.
func (st *windowStore) share(t *jsval.Object, name string) jsval.Value {
	st.pending = append(st.pending, pendingValues{t, name})
	return jsval.Undefined
}

// flush ends the epoch: the rows written in it get its array.
func (st *windowStore) flush() error {
	if st == nil || len(st.pending) == 0 {
		return nil
	}
	var items []jsval.Value
	if st.slow {
		items = st.add
	} else {
		items = slices.Clone(st.rows[st.e0:st.hi])
	}
	if err := st.ledger.Alloc(int64(len(items))*16 + 64); err != nil {
		return err
	}
	arr := jsval.Arr(items)
	for _, p := range st.pending {
		p.t.Set(p.name, arr)
	}
	st.pending = st.pending[:0]
	return nil
}

// windowMoveRegular moves the frame from [p0, p1) to [i0, i1) (both ends never
// decrease, and i0 <= i1): the rows that leave are removed from the running
// statistics, then the rows that enter are added, as upstream orders it, and
// the store holds rows[i0:i1].
func windowMoveRegular(ms *measureSet, c *cell, st *windowStore, rows []jsval.Value, p0, p1, i0, i1 int) error {
	for j := p0; j < i0; j++ {
		ms.rem(c, rows[j])
	}
	if st != nil && i0 > p0 {
		// A removal: values() builds a new array, after this row's
		// additions went into the old one.
		st.hi = i1
		if err := st.flush(); err != nil {
			return err
		}
		st.e0 = i0
	}
	for j := p1; j < i1; j++ {
		ms.add(c, rows[j])
	}
	c.data = nil
	if i0 < i1 {
		c.data = rows[i0:i1]
	}
	if st != nil {
		st.hi = i1
	}
	return nil
}

// errArrayLength is the RangeError of new Array(n) with n < 0.
var errArrayLength = &jsval.Thrown{Name: "RangeError", Msg: "Invalid array length"}

// windowMoveSlow is windowMoveRegular for a frame upstream does not keep within
// the rows: positions are what upstream computes (NaN or fractional ones
// included), a row past the partition or between two rows is undefined,
// which a measure reading a field of it throws on, and the store is run as
// upstream runs it.
func windowMoveSlow(ms *measureSet, c *cell, st *windowStore, rows []jsval.Value, p0, p1, i0, i1 float64, m *budget.Meter) error {
	at := func(j float64) (jsval.Value, bool) {
		if j >= 0 && j < float64(len(rows)) && j == math.Trunc(j) {
			return rows[int(j)], true
		}
		return jsval.Undefined, false
	}
	for j := p0; j < i0; j++ {
		if err := m.Work(1); err != nil {
			return err
		}
		t, ok := at(j)
		if !ok {
			c.num--
			if ms.countOnly {
				continue
			}
			return readsUndefined(ms.firstField())
		}
		ms.rem(c, t)
		if st != nil {
			st.rem = append(st.rem, t)
		}
	}
	for j := p1; j < i1; j++ {
		if err := m.Work(1); err != nil {
			return err
		}
		t, ok := at(j)
		if !ok {
			c.num++
			if ms.countOnly {
				continue
			}
			return readsUndefined(ms.firstField())
		}
		ms.add(c, t)
		if st != nil {
			st.add = append(st.add, t)
		}
	}
	if st == nil {
		return nil
	}
	return st.values(c, m)
}

// values is TupleStore.values(): with removals pending, the added tuples
// less one occurrence of each removed one (by identity) go to a new array of
// length added - removed, which has holes when fewer were found.
func (st *windowStore) values(c *cell, m *budget.Meter) error {
	if len(st.rem) > 0 {
		n, k := len(st.add), len(st.rem)
		if n-k < 0 {
			return errArrayLength
		}
		if err := m.Work(int64(n + k)); err != nil {
			return err
		}
		removed := make(map[*jsval.Object]bool, k)
		for _, r := range st.rem {
			removed[r.ObjValue()] = true
		}
		x := make([]jsval.Value, 0, n-k)
		for _, v := range st.add {
			if o := v.ObjValue(); removed[o] {
				removed[o] = false
			} else {
				x = append(x, v)
			}
		}
		holes := n - k - len(x)
		for ; holes > 0; holes-- {
			x = append(x, jsval.Undefined)
		}
		if err := st.flush(); err != nil {
			return err
		}
		st.add, st.rem = x, st.rem[:0]
	}
	c.data = st.add
	st.holes = slices.ContainsFunc(st.add, jsval.Value.IsUndefined)
	return nil
}
