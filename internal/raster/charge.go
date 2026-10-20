package raster

import (
	"fmt"

	"github.com/mgilbir/aster/internal/budget"
)

// reserveChunk is how much a reservation takes from the ledger at a time:
// a stream of small allocations (the nodes of a parse, the characters of a
// text) charges an addition each, and the ledger an atomic operation per
// chunk.
const reserveChunk = 64 << 10

// reservation charges a ledger for a stream of allocations before each is
// made, a chunk ahead, so that charging one costs a comparison (ADR 0010:
// every structure the input sizes is charged before it is built). What it
// took is given back with release; what it took and has not handed out,
// with trim. The zero value with a nil ledger charges nothing, and never
// fails.
type reservation struct {
	l    *budget.Ledger
	held int64 // taken from the ledger
	left int64 // of held, not handed out yet
}

// take hands out n bytes, taking more from the ledger first when what is
// left does not cover them; it fails with an error wrapping budget.ErrLimit
// when the ledger cannot give them. A chunk the ledger refuses is retried
// at the exact size, so the chunking never refuses what fits.
func (rs *reservation) take(n int64) error {
	if n <= rs.left {
		rs.left -= n
		return nil
	}
	if rs.l == nil {
		rs.left = 0
		return nil
	}
	need := n - rs.left
	chunk := max(need, reserveChunk)
	if err := rs.l.Alloc(chunk); err != nil {
		if chunk == need {
			return err
		}
		if err := rs.l.Alloc(need); err != nil {
			return err
		}
		chunk = need
	}
	rs.held += chunk
	rs.left += chunk - n
	return nil
}

// trim gives back what was taken and not handed out.
func (rs *reservation) trim() {
	rs.l.Free(rs.left)
	rs.held -= rs.left
	rs.left = 0
}

// release gives back everything taken: the memory it paid for is no longer
// held.
func (rs *reservation) release() {
	rs.l.Free(rs.held)
	rs.held, rs.left = 0, 0
}

// overLedger is the error of a stage the ledger refused memory to.
func overLedger(stage string, err error) error {
	return fmt.Errorf("raster: %s: %w", stage, err)
}
