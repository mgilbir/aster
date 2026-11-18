package scale

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// An array interpolated from a string reads the string's code units by
// index (d3's genericArray, `a[i]`): the string is decoded once, not at each
// read, which walked it from its start and made the loop quadratic. At this
// length a quadratic loop takes minutes, a linear one milliseconds.
func TestInterpolateArrayFromStringReadsItOnce(t *testing.T) {
	const n = 1 << 19
	a := jsval.Str(strings.Repeat("aé", n/2)) // n code units, not ASCII
	nums := make([]jsval.Value, n)
	for i := range nums {
		nums[i] = jsval.Int(i)
	}
	b := jsval.ArrOf(nums...)
	done := make(chan jsval.Value)
	go func() {
		done <- InterpolateValue(jsval.WalkerFor(t.Context()), a, b)(1)
	}()
	select {
	case v := <-done:
		if !v.IsArr() || v.Len() != n {
			t.Fatalf("interpolated %v, want %d elements", v.Kind(), n)
		}
	case <-time.After(testutil.Scaled(60 * time.Second)): // hang guard, not a calibrated bound
		t.Fatalf("interpolating from a long string takes over %v", testutil.Scaled(60*time.Second))
	}
}
