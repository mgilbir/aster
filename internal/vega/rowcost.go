package vega

import "github.com/mgilbir/aster/internal/jsval"

// What a row holds on the heap, measured with the heap check of
// WithMemoryLimit (memcheck_test.go) on rows of 1 to 17 fields: about 230
// bytes for a row of one field and 88 more for each further field (the key,
// the value and the slack of the slices that grow as formulas add fields). A
// string takes its length on top, and a nested array or object what a
// container of that size takes. The string budget of the expressions counts
// strings over 512 bytes as they are made, which is before the rows that hold
// them are charged here.
const (
	rowBase  = 176
	rowField = 88
	// Nested containers are counted one level deep: what is deeper is built
	// by expressions, and charged by the budgets of what they build.
	rowArray  = 24 + 48
	rowElem   = 48
	rowObject = 80
	rowMember = 64
)

// rowCost estimates the memory of a row. It reads each field once, so that a
// row cannot hold more than it is charged for: there is no sample to hide a
// heavy row from.
func rowCost(t jsval.Value) int64 {
	o := t.ObjValue()
	if o == nil {
		return rowBase
	}
	n := int64(rowBase) + int64(rowField)*int64(o.Len())
	for i, k := 0, o.Len(); i < k; i++ {
		switch f := o.ValueAt(i); f.Kind() {
		case jsval.KindStr:
			n += int64(len(f.StrValue()))
		case jsval.KindArr:
			n += rowArray + rowElem*int64(f.Len())
		case jsval.KindObj:
			n += rowObject + rowMember*int64(f.Len())
		}
	}
	return n
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
		n += max(rowCost(t)-unit, 0)
	}
	return n
}
