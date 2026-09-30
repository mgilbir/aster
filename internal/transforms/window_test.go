package transforms

import (
	"context"
	"math"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func sortFromSpec(spec jsval.Value) Comparator {
	if !spec.IsObj() {
		return nil
	}
	return CompareBy(fieldList(spec.Get("field")), strList(spec.Get("order")))
}

func windowParamsFromSpec(spec jsval.Value) WindowParams {
	p := WindowParams{
		Sort:        sortFromSpec(spec.Get("sort")),
		GroupBy:     fieldList(spec.Get("groupby")),
		IgnorePeers: spec.Get("ignorePeers").IsTruthy(),
	}
	if f := spec.Get("frame"); f.IsArr() {
		for _, b := range f.Items() {
			if b.IsNullish() {
				p.Frame = append(p.Frame, FrameBound{Unbounded: true})
			} else {
				p.Frame = append(p.Frame, FrameBound{Offset: int(b.NumValue())})
			}
		}
	}
	ops := strList(spec.Get("ops"))
	fields := spec.Get("fields")
	params := numListOpt(spec.Get("params"))
	aparams := numListOpt(spec.Get("aggregate_params"))
	as := spec.Get("as")
	for i, op := range ops {
		s := WindowOpSpec{Op: op, Param: math.NaN()}
		if f := fields.Index(i); f.IsStr() {
			s.Field = FieldOf(f.StrValue())
		}
		if i < len(params) {
			s.Param = params[i]
		}
		if i < len(aparams) && aparams[i] == aparams[i] {
			s.AggParam = aparams[i]
		}
		if a := as.Index(i); a.IsStr() {
			s.As = a.StrValue()
		}
		p.Ops = append(p.Ops, s)
	}
	return p
}

func TestWindowGolden(t *testing.T) {
	for _, c := range loadGolden(t, "window.json") {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Window(context.Background(), cloneTuples(c.Input), windowParamsFromSpec(param(c, 0)))
			if c.Error != "" {
				if err == nil {
					t.Fatalf("want error (%s)", c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := diffValues("out", tupleArr(got), c.Output, 1e-9); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestWindowArgminReferencesTuple(t *testing.T) {
	data := []jsval.Value{
		obj("v", jsval.Num(3)), obj("v", jsval.Num(1)), obj("v", jsval.Num(2)),
	}
	out, err := Window(context.Background(), data, WindowParams{
		Ops: []WindowOpSpec{{Op: "argmin", Field: FieldOf("v"), As: "m"}, {Op: "argmax", Field: FieldOf("v"), As: "M"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMin := []int{0, 1, 1}
	wantMax := []int{0, 0, 0}
	for i, row := range out {
		if row.Get("m").ObjValue() != data[wantMin[i]].ObjValue() {
			t.Errorf("row %d argmin points at wrong tuple", i)
		}
		if row.Get("M").ObjValue() != data[wantMax[i]].ObjValue() {
			t.Errorf("row %d argmax points at wrong tuple", i)
		}
	}
}

func TestWindowNoPanicOnEdgeFrames(t *testing.T) {
	data := []jsval.Value{obj("v", jsval.Num(1)), obj("v", jsval.Num(2)), obj("v", jsval.Num(3))}
	for _, fr := range [][]FrameBound{{{Offset: 3}, {Offset: 1}}, {{Offset: 5}, {Offset: 9}}, {{Offset: -9}, {Offset: -5}}} {
		_, err := Window(context.Background(), cloneTuples(data), WindowParams{
			Sort: CompareBy(FieldsOf("v"), nil), Frame: fr,
			Ops: []WindowOpSpec{{Op: "sum", Field: FieldOf("v")}, {Op: "first_value", Field: FieldOf("v")}, {Op: "last_value", Field: FieldOf("v")}, {Op: "median", Field: FieldOf("v")}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowErrors(t *testing.T) {
	data := []jsval.Value{obj("v", jsval.Num(1))}
	for _, ops := range [][]WindowOpSpec{
		{{Op: "ntile", Param: 0}},
		{{Op: "nth_value", Field: FieldOf("v"), Param: math.NaN()}},
		{{Op: "lag"}},
		{{Op: "sum"}},
		{{Op: "bogus", Field: FieldOf("v")}},
	} {
		if _, err := Window(context.Background(), data, WindowParams{Ops: ops}); err == nil {
			t.Errorf("%v: want error", ops[0].Op)
		}
	}
}

func windowBenchData(n int) []jsval.Value {
	data := make([]jsval.Value, n)
	for i := range data {
		data[i] = obj("g", jsval.Int(i%50), "t", jsval.Int(i), "x", jsval.Num(float64(i%97)))
	}
	return data
}

func BenchmarkWindowSumRank100k(b *testing.B) {
	base := windowBenchData(100_000)
	p := WindowParams{
		Sort: CompareBy(FieldsOf("t"), nil), GroupBy: FieldsOf("g"),
		Frame: []FrameBound{{Offset: -5}, {Offset: 0}},
		Ops:   []WindowOpSpec{{Op: "sum", Field: FieldOf("x")}, {Op: "rank"}, {Op: "mean", Field: FieldOf("x")}},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Window(context.Background(), base, p); err != nil {
			b.Fatal(err)
		}
	}
}
