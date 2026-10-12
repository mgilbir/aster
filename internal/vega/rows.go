package vega

import (
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// Row accounting. Limits.MaxRows bounds the tuples alive in a render, so each
// tuple is held by exactly one operator: the one that made it. An operator
// holds what its current output made, and when it runs again what it holds
// is replaced, not added to: a rerun is not charged twice, and the rows an
// operator gives up are free for another. An operator that only passes
// tuples on (a filter, a sort, a collect) holds none of them, so it can
// neither add to the count nor take from it.
//
// An operator holds rows in one of two records (opRows). held is what its
// transform made on its last run: the tuples of its output that were not
// tuples before (holdCreated, which also gives them their ids), or for the
// operators that copy or ingest, all they made (a derived relay, a
// prefacet's cells). src is a data set's own rows: what it was seeded with
// (seedCollect), what a Load read, and what modify() and setdata() replace
// them with.
//
// The rows are held before the tuples are built where the count is known
// first (ingestion, the copies of a relay, a prefacet's cells); a transform
// that multiplies tuples reserves them before it allocates (budget.Reserve),
// with what its previous run holds counted as free (budget.Replacing, set
// around every transform by opNode.transform). There is no other place that
// charges rows: every path that makes tuples goes through hold.

// opRows are the two records of the rows an operator holds.
type opRows struct {
	held, src budget.Held
}

// held is the record of the rows n's transform made.
func (n *opNode) held() *budget.Held {
	if n.rows == nil {
		n.rows = &opRows{}
	}
	return &n.rows.held
}

// src is the record of a data set's own rows, held by its input operator.
func (n *opNode) src() *budget.Held {
	if n.rows == nil {
		n.rows = &opRows{}
	}
	return &n.rows.src
}

// checkHeld, set by the tests (CheckHeldForTest), makes every transform's
// output be checked for tuples no operator holds: an object without a tuple
// id that is not the output of holdCreated is a path that made tuples without
// charging them.
var checkHeld bool

// CheckHeldForTest turns on the check that every tuple a transform passes on
// is held by an operator (rows.go), for the whole process. A test package
// calls it from an init function, before any render.
func CheckHeldForTest() { checkHeld = true }

// hold replaces what h holds with rows rows outweighing the budget's RowBytes
// by excess bytes, and ends the render past the row budget.
func (v *runView) hold(h *budget.Held, rows, excess int64) {
	if err := v.bud.Hold(h, rows, excess); err != nil {
		failErr(err)
	}
}

// holdRows is hold for the tuples rows, all of them made by the holder.
func (v *runView) holdRows(h *budget.Held, rows []jsval.Value) {
	v.hold(h, int64(len(rows)), v.rowExcess(rows))
}

// holdCreated makes n hold the tuples its transform created: those of out that
// are not tuples yet. It gives them their ids in output order, as
// vega-dataflow's ingest does when the transform makes them. Tuples passed
// on are held by whoever made them, but a transform that writes fields into
// them (a formula) holds the weight it added: before is the excess weight of
// its input before it ran (rowExcess).
func (v *runView) holdCreated(n *opNode, before int64, out []jsval.Value) {
	unit := v.bud.RowBytes
	var created, createdEx, passedEx int64
	for _, t := range out {
		o := t.ObjValue()
		if o == nil {
			continue
		}
		if o.TupleID() == 0 {
			o.EnsureTupleID()
			created++
			if unit > 0 {
				createdEx += max(jsval.RowCost(t)-unit, 0)
			}
		} else if unit > 0 {
			passedEx += max(jsval.RowCost(t)-unit, 0)
		}
	}
	v.hold(n.held(), created, createdEx+max(passedEx-before, 0))
}

// rowExcess is how many bytes the rows weigh beyond what the budget counts
// each of them for (Budget.RowBytes), which is all a row of a few small fields
// costs. It is 0 when the budget only counts rows.
func (v *runView) rowExcess(rows []jsval.Value) int64 {
	unit := v.bud.RowBytes
	if unit <= 0 {
		return 0
	}
	var n int64
	for _, t := range rows {
		n += max(jsval.RowCost(t)-unit, 0)
	}
	return n
}

// valueBytes is the size of one entry of a tuple list.
const valueBytes = int64(unsafe.Sizeof(jsval.Value{}))

// holdList charges the tuple list n output from the pulse in to the call's
// ledger, replacing the list it charged before. A list is memory the rows do
// not count: a filter, a sort or a collect makes a list of the tuples it
// passes on, and a specification can make thousands of them over the same
// rows. A list that is its input's (an operator that annotates its input in
// place, or hands it on) is not its own. The list is charged as soon as it
// exists, so a render overshoots the ledger by one list at most; the lists
// are given back when the render is over (releaseLists).
func (v *runView) holdList(n *opNode, in *flowPulse, out []jsval.Value) {
	var b int64
	if cap(out) > 0 && !aliases(out, in) {
		b = int64(cap(out)) * valueBytes
	}
	switch d := b - n.list; {
	case d > 0:
		if err := v.led.Alloc(d); err != nil {
			failErr(err)
		}
	case d < 0:
		v.led.Free(-d)
	}
	v.lists += b - n.list
	n.list = b
}

// releaseLists gives the ledger back the tuple lists charged: once the
// render is over they are garbage, the scenegraph keeping the tuples only.
func (v *runView) releaseLists() {
	v.led.Free(v.lists)
	v.lists = 0
}

// aliases reports whether out is the tuple list of in (or of one of its
// sources).
func aliases(out []jsval.Value, in *flowPulse) bool {
	p := unsafe.SliceData(out)
	if cap(in.tuples) > 0 && unsafe.SliceData(in.tuples) == p {
		return true
	}
	for _, m := range in.multi {
		if cap(m) > 0 && unsafe.SliceData(m) == p {
			return true
		}
	}
	return false
}

// unheld reports a tuple of out that no operator holds (see checkHeld).
func unheld(out []jsval.Value) bool {
	for _, t := range out {
		if o := t.ObjValue(); o != nil && o.TupleID() == 0 {
			return true
		}
	}
	return false
}
