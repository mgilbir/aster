package vega

import "github.com/mgilbir/aster/internal/jsval"

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
