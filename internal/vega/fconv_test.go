package vega

import (
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Numbers an expression or a transform hands the kernel are range-checked as
// floats before they are ints: Go's conversion of NaN, an infinity or a huge
// number to an int is implementation-defined (amd64 gives the minimum int,
// arm64 saturates or gives 0), so the answer would depend on the machine.
func TestOutOfRangeNumbersAreNotIndexes(t *testing.T) {
	arr := jsval.ArrOf(jsval.Num(1), jsval.Num(2))
	for _, f := range []float64{math.NaN(), math.Inf(1), 1e300, 2, -0.5} {
		if got := propOf(arr, jsval.Num(f)); !got.IsUndefined() {
			t.Errorf("[1,2][%v] = %v, want undefined", f, got)
		}
	}
	if got := propOf(arr, jsval.Num(math.Copysign(0, -1))); got.NumValue() != 1 {
		t.Errorf("[1,2][-0] = %v, want 1", got)
	}
	res := renderSpec(t, `{"marks":[{"type":"group","marks":[{"type":"text","encode":{"enter":{
	  "text":{"signal":"inScope({$id: NaN}) + '/' + inScope({$id: 1e300}) + '/' + inScope({$id: -1/0}) + '/' + inScope({$id: 0.5})"}}}}]}]}`)
	if got := marks(res)[0].Items[0].Items[0].Items[0].Text().AsString(); got != "false/false/false/false" {
		t.Errorf("inScope of non-ids: %s", got)
	}
}
