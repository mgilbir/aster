package transforms

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// measuresFrom zips the fields/ops/aggregate_params/as arrays of an upstream
// aggregate-like transform spec.
func measuresFrom(spec jsval.Value) []Measure {
	ops := strList(spec.Get("ops"))
	fields := spec.Get("fields")
	params := numListOpt(spec.Get("aggregate_params"))
	as := spec.Get("as")
	var ms []Measure
	for i, op := range ops {
		m := Measure{Op: op, Param: math.NaN()}
		if f := fields.Index(i); f.IsStr() {
			m.Field = FieldOf(f.StrValue())
		}
		if i < len(params) && params[i] == params[i] && params[i] != 0 {
			m.Param = params[i]
		}
		if a := as.Index(i); a.IsStr() {
			m.As = a.StrValue()
		}
		ms = append(ms, m)
	}
	return ms
}

func TestAggregateGolden(t *testing.T) {
	for _, c := range loadGolden(t, "aggregate.json") {
		t.Run(c.Name, func(t *testing.T) {
			spec := param(c, 0)
			p := AggregateParams{
				GroupBy:  fieldList(spec.Get("groupby")),
				Measures: measuresFrom(spec),
				Cross:    spec.Get("cross").IsTruthy(),
				// The golden runs include no ci0/ci1: bootstrap is unseeded upstream.
			}
			if k := spec.Get("key"); k.IsStr() {
				p.Key = FieldOf(k.StrValue())
			}
			got, err := Aggregate(context.Background(), cloneTuples(c.Input), p)
			if err != nil {
				t.Fatal(err)
			}
			if d := diffValues("out", tupleArr(got), c.Output, 1e-9); d != "" {
				t.Fatal(d)
			}
		})
	}
}

// TestPivotCellsCharged is the audit's C26: a pivot keeps a running state
// per measure (one per distinct value) in every cell, which the row budget
// counted as a sixteenth of a row: 2,500 keys x 2,500 values held 1.5 GB
// under a 256 MiB limit. The states are charged to the ledger.
func TestPivotCellsCharged(t *testing.T) {
	var data []jsval.Value
	for i := 0; i < 600; i++ {
		data = append(data, obj("k", jsval.Int(i), "c", jsval.Int(i), "v", jsval.Int(1)))
	}
	const max = 16 << 20
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: max})
	ctx := budget.WithLedger(context.Background(), l)
	_, err := Pivot(ctx, data, PivotParams{GroupBy: FieldsOf("k"), Field: FieldOf("c"), Value: FieldOf("v")})
	if !errors.Is(err, budget.ErrLimit) || l.PeakBytes() > max {
		t.Fatalf("got %v, peak %d", err, l.PeakBytes())
	}
	// What fits is given back once the cells are written.
	if _, err := Pivot(ctx, data[:100], PivotParams{GroupBy: FieldsOf("k"), Field: FieldOf("c"), Value: FieldOf("v")}); err != nil || l.Bytes() != 0 {
		t.Fatalf("got %v, %d bytes still charged", err, l.Bytes())
	}
}
