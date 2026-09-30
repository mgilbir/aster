package transforms

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
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
		m := Measure{Op: op}
		if f := fields.Index(i); f.IsStr() {
			m.Field = FieldOf(f.StrValue())
		}
		if i < len(params) && params[i] == params[i] {
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
