package transforms

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// compareSpec builds a comparator from an upstream {field, order} object.
func compareSpec(v jsval.Value) Comparator {
	if !v.IsObj() {
		return nil
	}
	fv := v.Get("field")
	var paths []string
	if fv.IsStr() {
		paths = []string{fv.StrValue()}
	} else {
		paths = strList(fv)
	}
	var orders []string
	if ov := v.Get("order"); ov.IsStr() {
		orders = []string{ov.StrValue()}
	} else {
		orders = strList(ov)
	}
	return CompareBy(jsval.Walker{}, FieldsOf(paths...), orders)
}

func asPair(v jsval.Value) [2]string {
	return [2]string{v.Index(0).StrValue(), v.Index(1).StrValue()}
}

func TestStackPieGolden(t *testing.T) {
	for _, c := range loadGolden(t, "stack_pie.json") {
		t.Run(c.Name, func(t *testing.T) {
			spec := param(c, 0)
			in := cloneTuples(c.Input)
			var got []jsval.Value
			var err error
			field := Field{}
			if f := spec.Get("field"); f.IsStr() {
				field = FieldOf(f.StrValue())
			}
			switch spec.Get("type").StrValue() {
			case "stack":
				got, err = Stack(context.Background(), in, StackParams{
					Field: field, GroupBy: fieldList(spec.Get("groupby")), Sort: compareSpec(spec.Get("sort")),
					Offset: spec.Get("offset").StrValue(), As: asPair(spec.Get("as")),
				})
			case "pie":
				p := PieParams{Field: field, Sort: spec.Get("sort").IsTruthy(), As: asPair(spec.Get("as")),
					StartAngle: spec.Get("startAngle").NumValue()}
				if e := spec.Get("endAngle"); e.IsNum() {
					x := e.NumValue()
					p.EndAngle = &x
				}
				got, err = Pie(context.Background(), in, p)
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := diffValues("out", tupleArr(got), c.Output, 1e-12); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestStackCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := make([]jsval.Value, 10)
	for i := range data {
		data[i] = obj("v", jsval.Int(i), "g", jsval.Int(i))
	}
	if _, err := Stack(ctx, data, StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("g")}); err == nil {
		t.Fatal("expected cancellation")
	}
}

func benchRows(n int) []jsval.Value {
	rows := make([]jsval.Value, n)
	for i := range rows {
		rows[i] = obj("g", jsval.Int(i%50), "h", jsval.Str([]string{"a", "b", "c"}[i%3]), "v", jsval.Num(float64(i%97)+0.5))
	}
	return rows
}

func BenchmarkStack100k(b *testing.B) {
	rows := benchRows(100_000)
	p := StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("g", "h"), Offset: StackZero}
	// stack writes y0 and y1 into its rows: one run off the clock first (see
	// BenchmarkBin100k).
	if _, err := Stack(context.Background(), rows, p); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Stack(context.Background(), rows, p); err != nil {
			b.Fatal(err)
		}
	}
}

// TestStackKeysGolden is upstream's stack grouping over objects, arrays,
// dates, undefined, NaN and look-alike strings (gen_stack_keys.mjs):
// upstream keys a stack by JSON.stringify of the group-by values. The input
// is replayed twice, the second time with every object's keys inserted in
// reverse: JSON.stringify writes integer keys first, ascending, whatever the
// insertion order, so the objects with integer keys group as before (and
// the reversal keeps the others as distinct as they were).
func TestStackKeysGolden(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "stack_keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range root.Items() {
		c := goldenCase{Name: raw.Get("name").StrValue(), Transform: raw.Get("transform"), Output: decodeDialect(raw.Get("output"))}
		input := decodeDialect(raw.Get("rawInput")).Items()
		for _, reversed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reversed=%v", c.Name, reversed), func(t *testing.T) {
				in := cloneTuples(input)
				if reversed {
					for i, tup := range in {
						in[i] = reverseKeys(tup)
					}
				}
				spec := param(c, 0)
				got, err := Stack(context.Background(), in, StackParams{Field: FieldOf("v"), GroupBy: fieldList(spec.Get("groupby"))})
				if err != nil {
					t.Fatal(err)
				}
				for i := range got {
					w := c.Output.Index(i)
					if got[i].Get("y0").NumValue() != w.Get("y0").NumValue() || got[i].Get("y1").NumValue() != w.Get("y1").NumValue() {
						t.Fatalf("row %d (key %s): y0,y1 = %v,%v, want %v,%v", i, string(jsval.AppendJSON(nil, in[i].Get("k"))),
							got[i].Get("y0"), got[i].Get("y1"), w.Get("y0"), w.Get("y1"))
					}
				}
			})
		}
	}
}

// reverseKeys copies v with every nested object's keys inserted in reverse.
func reverseKeys(v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindArr:
		items := make([]jsval.Value, v.Len())
		for i, it := range v.Items() {
			items[i] = reverseKeys(it)
		}
		return jsval.Arr(items)
	case jsval.KindObj:
		o := v.ObjValue()
		r := jsval.NewObject(o.Len())
		for i := o.Len() - 1; i >= 0; i-- {
			r.Set(o.KeyAt(i), reverseKeys(o.ValueAt(i)))
		}
		return jsval.Obj(r)
	}
	return v
}

// TestStackCircularKey: JSON.stringify throws on a group value that holds
// itself, and so does the stack.
func TestStackCircularKey(t *testing.T) {
	o := jsval.NewObject(1)
	o.Set("self", jsval.Obj(o))
	data := []jsval.Value{obj("k", jsval.Obj(o), "v", jsval.Int(1))}
	if _, err := Stack(context.Background(), data, StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("k")}); err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("got %v", err)
	}
}

// TestStackKeyIntegerKeysFirst: JSON.stringify writes an object's integer
// keys first, so two objects that differ only in where an integer key was
// inserted are one stack.
func TestStackKeyIntegerKeysFirst(t *testing.T) {
	a := jsval.NewObject(2)
	a.Set("a", jsval.Str("y"))
	a.Set("1", jsval.Str("x"))
	b := jsval.NewObject(2)
	b.Set("1", jsval.Str("x"))
	b.Set("a", jsval.Str("y"))
	data := []jsval.Value{obj("k", jsval.Obj(a), "v", jsval.Int(1)), obj("k", jsval.Obj(b), "v", jsval.Int(1))}
	out, err := Stack(context.Background(), data, StackParams{Field: FieldOf("v"), GroupBy: FieldsOf("k")})
	if err != nil || out[1].Get("y0").NumValue() != 1 {
		t.Fatalf("got %v, second y0 %v: want one stack", err, out[1].Get("y0"))
	}
}
