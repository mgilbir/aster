package jsval

import (
	"strings"
	"testing"
)

func TestRowCostFollowsShape(t *testing.T) {
	parse := func(s string) Value {
		v, err := ParseJSONString(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	one := RowCost(parse(`{"a":1}`))
	if one != RowBase+RowField {
		t.Errorf("one field: %d", one)
	}
	if got := RowCost(parse(`{"a":1,"b":2,"c":3,"d":4,"e":5}`)) - one; got != 4*RowField {
		t.Errorf("four more fields: %d", got)
	}
	if got := RowCost(parse(`{"a":"`+strings.Repeat("z", 1000)+`"}`)) - one; got != 1000 {
		t.Errorf("a 1000 byte string: %d", got)
	}
	if got := RowCost(parse(`{"a":[1,2,3,4,5,6,7,8,9,10]}`)) - one; got != rowArray+10*rowElem {
		t.Errorf("an array of ten: %d", got)
	}
}
