package jsval

// What a row holds on the heap, measured with the heap check of
// WithMemoryLimit (memcheck_test.go) on rows of 1 to 17 fields when a Value
// was 48 bytes: about 230 bytes for a row of one field and 88 more for each
// further field (the key, the value and the slack of the slices that grow as
// formulas add fields). A Value is now 16 bytes, which takes 32 off each
// field, array element and object member, and about 60 off the row itself
// (measured on parsed and cloned rows: 252 to 155 bytes for one field, 1102
// to 462 for 17). A string takes its length on top, and a nested array or
// object what a container of that size takes. The string budget of the
// expressions counts strings over 512 bytes as they are made, which is before
// the rows that hold them are charged here.
const (
	RowBase  = 120
	RowField = 56
	// Nested containers are counted one level deep: what is deeper is built
	// by expressions, and charged by the budgets of what they build.
	rowArray  = 24 + 16
	rowElem   = 16
	rowObject = 80
	rowMember = 32
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
