package jsval

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// retainedPer is the heap a value build makes holds, per value, measured
// after a collection with reps of them alive.
func retainedPer(build func() Value, reps int) float64 {
	keep := make([]Value, reps)
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for i := range keep {
		keep[i] = build()
	}
	runtime.GC()
	runtime.ReadMemStats(&b)
	runtime.KeepAlive(keep)
	return float64(int64(b.HeapAlloc)-int64(a.HeapAlloc)-int64(reps)*int64(unsafe.Sizeof(Value{}))) / float64(reps)
}

// RowCost backs the row-weight bound, so it must not be below what a row
// holds, at any number of fields, however the row is built (fields set one
// by one, cloned as ingestion does, parsed from JSON), and should not be far
// above it (on 64-bit platforms, which the costs are measured on: a 32-bit
// one holds about half, and RowCost stays a bound there). Past 32 fields an object builds a hash index of its keys, which
// a per-field cost of 56 bytes did not cover (a row of a thousand fields
// holds 88 bytes a field).
func TestRowCostCoversWhatARowHolds(t *testing.T) {
	for _, n := range []int{1, 8, 32, 33, 48, 64, 100, 256, 1000, 4000} {
		names := make([]string, n)
		var js strings.Builder
		js.WriteString("{")
		for i := range names {
			names[i] = fmt.Sprintf("c%d", i)
			if i > 0 {
				js.WriteString(",")
			}
			fmt.Fprintf(&js, "%q:1", names[i])
		}
		js.WriteString("}")
		doc := []byte(js.String())
		src := NewObject(n)
		for _, k := range names {
			src.Set(k, Num(1))
		}
		reps := max(50, 400_000/n)
		for path, build := range map[string]func() Value{
			"set": func() Value {
				o := NewObject(n)
				for _, k := range names {
					o.Set(k, Num(1))
				}
				return Obj(o)
			},
			"clone": func() Value { return Obj(src.Clone()) },
			"json":  func() Value { v, _ := ParseJSON(doc); return v },
		} {
			held := retainedPer(build, reps)
			cost := float64(RowCost(Obj(src)))
			if cost < 0.95*held || (unsafe.Sizeof(uintptr(0)) == 8 && cost > 2.5*held) {
				t.Errorf("%d fields, %s: RowCost %.0f, a row holds %.0f bytes", n, path, cost, held)
			}
		}
	}
}
