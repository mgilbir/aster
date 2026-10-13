package transforms

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
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
				p.Frame = append(p.Frame, FrameBound{Offset: jsval.ToNumber(decodeDialect(b))})
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

// TestWindowFramesGolden replays upstream (gen_window_frames.mjs) over
// frames with infinite, huge, NaN, fractional and crossing ends, and over
// `values`, whose array upstream shares between the rows of an epoch (see
// windowStore). Values are compared as tuple ids and which row first held
// the same array.
func TestWindowFramesGolden(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "window_frames.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	cases := root.Items()
	var rows []jsval.Value
	for _, r := range cases[0].Get("rows").Items() {
		rows = append(rows, decodeDialect(r))
	}
	for _, c := range cases[1:] {
		t.Run(c.Get("name").StrValue(), func(t *testing.T) {
			spec := c.Get("transform")
			p := windowParamsFromSpec(spec)
			p.Frame = nil
			for _, f := range c.Get("frame").Items() {
				if f.IsNull() {
					p.Frame = append(p.Frame, FrameBound{Unbounded: true})
				} else {
					p.Frame = append(p.Frame, FrameBound{Offset: decodeDialect(f).NumValue()})
				}
			}
			if pn := c.Get("paramNum"); pn.IsArr() {
				for i, v := range pn.Items() {
					p.Ops[i].Param = math.NaN()
					if !v.IsNull() {
						p.Ops[i].Param = decodeDialect(v).NumValue()
					}
				}
			}
			if s := spec.Get("sort"); s.IsObj() {
				p.SortField = s.Get("field").Index(0).StrValue()
			}
			out, err := Window(context.Background(), cloneTuples(rows), p)
			if want := c.Get("error").StrValue(); want != "" {
				if err == nil || err.Error() != want {
					t.Fatalf("got error %v, want %s", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := c.Get("output").Items()
			// Upstream returns the rows as the dataflow holds them, in input
			// order, as Window does.
			var arrays []jsval.Value
			ops := strList(spec.Get("ops"))
			for i, row := range out {
				w := want[i]
				for k, op := range ops {
					name := op + strconv.Itoa(k)
					got := row.Get(name)
					switch op {
					case "values":
						shared := slices.IndexFunc(arrays, func(a jsval.Value) bool { return jsval.SameRef(a, got) })
						if shared < 0 {
							arrays = append(arrays, got)
							shared = len(arrays) - 1
						}
						var ids []string
						for _, x := range got.Items() {
							ids = append(ids, x.Get("id").AsString())
						}
						var wantIDs []string
						for _, x := range w.Get(name).Get("ids").Items() {
							s := x.AsString()
							if x.IsNull() {
								s = "undefined"
							}
							wantIDs = append(wantIDs, s)
						}
						if !slices.Equal(ids, wantIDs) || shared != int(w.Get(name).Get("shared").NumValue()) {
							t.Fatalf("row %d %s: ids %v shared %d, want %v shared %v", i, name, ids, shared, wantIDs, w.Get(name).Get("shared"))
						}
					case "argmin":
						g := got.Get("id")
						if wv := w.Get(name); !(g.IsUndefined() && wv.IsNull()) && g.AsString() != wv.AsString() {
							t.Fatalf("row %d %s: got %v, want %v", i, name, g, wv)
						}
					default:
						if d := diffValues(name, got, decodeDialect(w.Get(name)), 1e-12); d != "" {
							t.Fatalf("row %d: %s", i, d)
						}
					}
				}
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
			Sort: CompareBy(FieldsOf("v"), nil), SortField: "v", Frame: fr,
			Ops: []WindowOpSpec{{Op: "sum", Field: FieldOf("v")}, {Op: "first_value", Field: FieldOf("v")}, {Op: "last_value", Field: FieldOf("v")}, {Op: "median", Field: FieldOf("v")}},
		})
		// A range frame that starts past the last row or ends before the
		// first hands undefined to the sort comparator, a TypeError upstream
		// too; anything else is a bug.
		if err != nil && err.Error() != "TypeError: Cannot read properties of undefined (reading 'v')" {
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
	for b.Loop() {
		if _, err := Window(context.Background(), base, p); err != nil {
			b.Fatal(err)
		}
	}
}

// TestWindowValuesShared is the audit's C25: the values op cloned the frame
// for every row, O(rows x frame) memory (250 MB for 4,000 rows). Upstream
// gives every row of an epoch the store's own array; so does Window, and
// what a sliding frame still needs is charged to the ledger.
func TestWindowValuesShared(t *testing.T) {
	data := windowBenchData(4000)
	vals := []WindowOpSpec{{Op: "values", Field: FieldOf("x"), As: "v"}}
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
	ctx := budget.WithLedger(context.Background(), l)
	out, err := Window(ctx, data, WindowParams{Frame: []FrameBound{{Unbounded: true}, {Unbounded: true}}, Ops: vals})
	if err != nil {
		t.Fatal(err)
	}
	first := out[0].Get("v")
	for i, row := range out {
		if v := row.Get("v"); !jsval.SameRef(v, first) || v.Len() != len(data) {
			t.Fatalf("row %d: own array of %d", i, v.Len())
		}
	}
	// A frame that slides makes a new array per row, as upstream does: that
	// is charged, and fails a small limit.
	l = budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 16 << 20})
	ctx = budget.WithLedger(context.Background(), l)
	_, err = Window(ctx, windowBenchData(4000), WindowParams{Frame: []FrameBound{{Offset: -2000}, {Offset: 2000}}, Ops: vals})
	if !errors.Is(err, budget.ErrLimit) || l.PeakBytes() > 16<<20 {
		t.Fatalf("sliding frame: %v, peak %d", err, l.PeakBytes())
	}
}
