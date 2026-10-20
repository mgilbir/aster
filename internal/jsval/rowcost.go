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
//
// An object of more than indexThreshold keys also builds a hash index of
// them, which the fields above do not count: measured after a collection,
// with the keys set one by one, cloned or parsed, it holds 31 to 35 bytes a
// key plus the slack of the table's power-of-two size (3,128 bytes for a row
// of 33 fields where the fields alone come to 1,968; 88 bytes a field at a
// thousand fields and more). rowIndexBase and rowIndexField cover it at every
// size (TestRowCostCoversWhatARowHolds).
const (
	RowBase  = 120
	RowField = 56
	// The hash index of an object of more than indexThreshold keys.
	rowIndexBase  = 1200
	rowIndexField = 48
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
	var n int64
	if t.Kind() == KindArr {
		// An array row: its elements, and the fields written into it.
		n = rowArray + rowElem*int64(t.Len())
		if o = t.ArrayProps(); o == nil {
			return n
		}
		n -= RowBase
	} else if o == nil {
		return RowBase
	}
	n += ObjectCost(o.Len())
	for i, k := 0, o.Len(); i < k; i++ {
		switch f := o.ValueAt(i); f.Kind() {
		case KindStr:
			n += int64(len(f.StrValue()))
		case KindArr:
			n += rowArray + rowElem*int64(f.Len())
		case KindObj:
			n += rowObject + rowMember*int64(f.Len()) + indexCost(f.Len())
		}
	}
	return n
}

// ObjectCost is what a row of the given number of fields holds before the
// text of its strings and what its fields nest: what RowCost charges for a
// row about to be built.
func ObjectCost(fields int) int64 {
	return RowBase + RowField*int64(fields) + indexCost(fields)
}

// indexCost is what the hash index of an object of n keys holds.
func indexCost(n int) int64 {
	if n <= indexThreshold {
		return 0
	}
	return rowIndexBase + rowIndexField*int64(n)
}
