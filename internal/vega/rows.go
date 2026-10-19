package vega

import (
	"math"
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

// rowsOf is n's records, made on first use from a slab the view keeps, so
// that the few operators that hold rows cost no allocation each (and a chart
// of a few data sets none at all).
func (v *runView) rowsOf(n *opNode) *opRows {
	if n.rows == nil {
		if len(v.rowSlab) == 0 {
			if !v.rowBufUsed {
				v.rowSlab, v.rowBufUsed = v.rowBuf[:], true
			} else {
				v.rowSlab = make([]opRows, 16)
			}
		}
		n.rows, v.rowSlab = &v.rowSlab[0], v.rowSlab[1:]
	}
	return n.rows
}

// holdHeld replaces what n's transform holds; holdSrc what n holds of its
// data set's own rows. An operator that has held nothing and holds nothing
// needs no record.
func (v *runView) holdHeld(n *opNode, rows, excess int64) {
	if n.rows == nil && rows == 0 && excess == 0 {
		return
	}
	v.hold(&v.rowsOf(n).held, rows, excess)
}

func (v *runView) holdSrc(n *opNode, rows, excess int64) {
	if n.rows == nil && rows == 0 && excess == 0 {
		return
	}
	v.hold(&v.rowsOf(n).src, rows, excess)
}

// srcRecord is n's record of its data set's own rows, nil when it has none.
func (n *opNode) srcRecord() *budget.Held {
	if n.rows == nil {
		return nil
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

// holdSrcRows and holdHeldRows hold the tuples rows, all of them made by
// the holder.
func (v *runView) holdSrcRows(n *opNode, rows []jsval.Value) {
	v.holdSrc(n, int64(len(rows)), v.rowExcess(rows))
}

func (v *runView) holdHeldRows(n *opNode, rows []jsval.Value) {
	v.holdHeld(n, int64(len(rows)), v.rowExcess(rows))
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
	for i, t := range out {
		v.g.rows(i, len(out), 1)
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
	v.holdHeld(n, created, createdEx+max(passedEx-before, 0))
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
		v.work(1)
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
	var c uint32
	if cap(out) > 0 && !aliases(out, in) {
		c = uint32(min(uint64(cap(out)), math.MaxUint32))
	}
	if c == n.listCap {
		return
	}
	b := int64(c) * valueBytes
	switch d := b - int64(n.listCap)*valueBytes; {
	case d > 0:
		if err := v.led.Alloc(d); err != nil {
			failErr(err)
		}
	case d < 0:
		v.led.Free(-d)
	}
	v.lists += b - int64(n.listCap)*valueBytes
	n.listCap = c
}

// chargeRun charges the call's ledger n bytes an operator keeps for the rest
// of the run (a prefacet's record of its cells), given back with the tuple
// lists when the run is over.
func (v *runView) chargeRun(n int64) {
	if err := v.led.Alloc(n); err != nil {
		failErr(err)
	}
	v.lists += n
	v.runBytes += n
}

// envItem is what an element of an array the expression environment hands an
// expression holds, as expr's perItem: the Value and the slack of what it
// refers to.
const envItem = 24

// chargeItems charges what an expression-environment method (domain(),
// range(), gradient(), data() of a mark, …) is about to hand an expression:
// a step per 16 elements made, and, past 256 elements (an array that size
// is free in an expression too), the elements' bytes for the rest of the
// run, as the evaluator charges an array it builds. A formula can keep what
// it is handed in every row, and an encoder can call it for every item.
func (v *runView) chargeItems(n int) {
	v.g.work(n/16 + 1)
	if n > 256 {
		v.chargeRun(int64(n) * envItem)
	}
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
