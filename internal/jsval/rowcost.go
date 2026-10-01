package jsval

// What a row holds on the heap, measured with the heap check of
// WithMemoryLimit (memcheck_test.go) on rows of 1 to 17 fields: about 230
// bytes for a row of one field and 88 more for each further field (the key,
// the value and the slack of the slices that grow as formulas add fields). A
// string takes its length on top, and a nested array or object what a
// container of that size takes. The string budget of the expressions counts
// strings over 512 bytes as they are made, which is before the rows that hold
// them are charged here.
const (
	RowBase  = 176
	RowField = 88
	// Nested containers are counted one level deep: what is deeper is built
	// by expressions, and charged by the budgets of what they build.
	rowArray  = 24 + 48
	rowElem   = 48
	rowObject = 80
	rowMember = 64
)

// RowCost estimates the memory of a row. It reads each field once, so that a
// row cannot hold more than it is charged for: there is no sample to hide a
// heavy row from.
func RowCost(t Value) int64 {
	o := t.ObjValue()
	if o == nil {
		return RowBase
	}
	n := int64(RowBase) + int64(RowField)*int64(o.Len())
	for i, k := 0, o.Len(); i < k; i++ {
		switch f := o.ValueAt(i); f.Kind() {
		case KindStr:
			n += int64(len(f.StrValue()))
		case KindArr:
			n += rowArray + rowElem*int64(f.Len())
		case KindObj:
			n += rowObject + rowMember*int64(f.Len())
		}
	}
	return n
}
