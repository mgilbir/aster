package transforms

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// reshapeCases loads the recorded cases whose name starts with prefix+"/".
func reshapeCases(t *testing.T, prefix string) []goldenCase {
	t.Helper()
	var out []goldenCase
	for _, c := range loadGolden(t, "reshape.json") {
		if strings.HasPrefix(c.Name, prefix+"/") {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no golden cases for %s", prefix)
	}
	return out
}

func reshapeCheck(t *testing.T, c goldenCase, got []jsval.Value, err error) {
	t.Helper()
	if strings.Contains(c.Name, "/err") {
		if err == nil {
			t.Fatal("expected an error")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if d := diffValues("out", tupleArr(got), c.Output, 1e-12); d != "" {
		t.Fatal(d)
	}
}

func reshapeSortSpec(spec jsval.Value) Comparator {
	s := spec.Get("sort")
	if !s.IsObj() {
		return nil
	}
	toList := func(v jsval.Value) []jsval.Value {
		if v.IsArr() {
			return v.Items()
		}
		if v.IsUndefined() {
			return nil
		}
		return []jsval.Value{v}
	}
	var fs []Field
	for _, f := range toList(s.Get("field")) {
		fs = append(fs, FieldOf(f.StrValue()))
	}
	var os []string
	for _, o := range toList(s.Get("order")) {
		os = append(os, o.StrValue())
	}
	return CompareBy(fs, os)
}

func TestCollectGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "collect") {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Collect(context.Background(), cloneTuples(c.Input), reshapeSortSpec(param(c, 0)))
			reshapeCheck(t, c, got, err)
		})
	}
}

func xOf(t jsval.Value) jsval.Value { return t.Get("x") }

func TestFilterFormulaGolden(t *testing.T) {
	preds := map[string]func(jsval.Value) bool{
		"datum.x > 2": func(d jsval.Value) bool { x := d.Get("x"); return Greater(x, jsval.Num(2)) },
		"false":       func(jsval.Value) bool { return false },
	}
	exprs := map[string]Accessor{
		"datum.x * 2":  func(d jsval.Value) jsval.Value { return jsval.Num(jsval.ToNumber(d.Get("x")) * 2) },
		"datum.id + 1": func(d jsval.Value) jsval.Value { return jsval.Num(d.Get("id").NumValue() + 1) },
		"datum.s":      func(d jsval.Value) jsval.Value { return d.Get("s") },
	}
	for _, c := range reshapeCases(t, "filter") {
		t.Run(c.Name, func(t *testing.T) {
			got, err := Filter(context.Background(), cloneTuples(c.Input), preds[param(c, 0).Get("expr").StrValue()])
			reshapeCheck(t, c, got, err)
		})
	}
	for _, c := range reshapeCases(t, "formula") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			got, err := Formula(context.Background(), cloneTuples(c.Input), FormulaParams{Expr: exprs[s.Get("expr").StrValue()], As: s.Get("as").StrValue()})
			reshapeCheck(t, c, got, err)
		})
	}
}

func TestIdentifierGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "identifier") {
		t.Run(c.Name, func(t *testing.T) {
			var counter float64
			got, err := Identifier(context.Background(), cloneTuples(c.Input), param(c, 0).Get("as").StrValue(), &counter)
			reshapeCheck(t, c, got, err)
		})
	}
	var counter float64 = 5
	Identifier(context.Background(), []jsval.Value{obj(), obj()}, "id", &counter)
	if counter != 7 {
		t.Fatalf("counter %v", counter)
	}
}

func TestProjectFoldFlattenGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "project") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			p := ProjectParams{As: strListNil(s.Get("as"))}
			if s.Get("fields").IsArr() {
				p.Fields = append([]Field{}, fieldList(s.Get("fields"))...)
			}
			got, err := Project(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c, got, err)
		})
	}
	for _, c := range reshapeCases(t, "fold") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			p := FoldParams{Fields: fieldList(s.Get("fields"))}
			if as := s.Get("as"); as.IsArr() {
				p.As = [2]string{as.Index(0).StrValue(), as.Index(1).StrValue()}
			}
			got, err := Fold(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c, got, err)
		})
	}
	for _, c := range reshapeCases(t, "flatten") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			p := FlattenParams{Fields: fieldList(s.Get("fields")), As: strListNil(s.Get("as")), Index: s.Get("index").StrValue()}
			got, err := Flatten(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c, got, err)
		})
	}
}

// strListNil is strList with null entries as "".
func strListNil(v jsval.Value) []string { return strList(v) }

func TestFlattenNullErrors(t *testing.T) {
	_, err := Flatten(context.Background(), []jsval.Value{obj("a", jsval.Null)}, FlattenParams{Fields: FieldsOf("a")})
	if err == nil {
		t.Fatal("want error for null array field")
	}
}

func TestPivotGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "pivot") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			p := PivotParams{
				GroupBy: fieldList(s.Get("groupby")),
				Field:   FieldOf(s.Get("field").StrValue()),
				Value:   FieldOf(s.Get("value").StrValue()),
				Op:      s.Get("op").StrValue(),
				Limit:   int(s.Get("limit").NumValue()),
			}
			got, err := Pivot(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c, got, err)
		})
	}
}

func TestLookupGolden(t *testing.T) {
	for _, c := range loadReshapeOther(t) {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c.goldenCase, 0)
			ix, err := NewLookupIndex(context.Background(), c.other, FieldOf(s.Get("key").StrValue()))
			if err != nil {
				t.Fatal(err)
			}
			p := LookupParams{
				Index:   ix,
				Fields:  fieldList(s.Get("fields")),
				Values:  fieldList(s.Get("values")),
				As:      strList(s.Get("as")),
				Default: s.Get("default"),
			}
			got, err := Lookup(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c.goldenCase, got, err)
		})
	}
}

type lookupCase struct {
	goldenCase
	other []jsval.Value
}

func loadReshapeOther(t *testing.T) []lookupCase {
	t.Helper()
	root := loadRaw(t, "reshape.json")
	var out []lookupCase
	for _, c := range root.Items() {
		name := c.Get("name").StrValue()
		if !strings.HasPrefix(name, "lookup/") {
			continue
		}
		var lc lookupCase
		lc.Name = name
		lc.Input = decodeDialect(c.Get("input")).Items()
		lc.Transform = c.Get("transform")
		lc.Output = decodeDialect(c.Get("output"))
		lc.other = decodeDialect(c.Get("other")).Items()
		out = append(out, lc)
	}
	return out
}

func TestCrossGolden(t *testing.T) {
	filters := map[string]func(jsval.Value) bool{
		"datum.a.x < datum.b.x":  func(d jsval.Value) bool { return d.Get("a").Get("x").NumValue() < d.Get("b").Get("x").NumValue() },
		"datum.l.x != datum.r.x": func(d jsval.Value) bool { return d.Get("l").Get("x").NumValue() != d.Get("r").Get("x").NumValue() },
	}
	for _, c := range reshapeCases(t, "cross") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			p := CrossParams{Filter: filters[s.Get("filter").StrValue()]}
			if as := s.Get("as"); as.IsArr() {
				p.As = [2]string{as.Index(0).StrValue(), as.Index(1).StrValue()}
			}
			got, err := Cross(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c, got, err)
		})
	}
}

func TestCountPatternGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "countpattern") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			p := CountPatternParams{
				Field: FieldOf(s.Get("field").StrValue()), Case: s.Get("case").StrValue(),
				Pattern: s.Get("pattern").StrValue(), Stopwords: s.Get("stopwords").StrValue(),
			}
			if as := s.Get("as"); as.IsArr() {
				p.As = [2]string{as.Index(0).StrValue(), as.Index(1).StrValue()}
			}
			got, err := CountPattern(context.Background(), cloneTuples(c.Input), p)
			reshapeCheck(t, c, got, err)
		})
	}
	_, err := CountPattern(context.Background(), []jsval.Value{obj("t", jsval.Null)}, CountPatternParams{Field: FieldOf("t")})
	if err == nil {
		t.Fatal("want error for non-string")
	}
	_, err = CountPattern(context.Background(), []jsval.Value{obj("t", jsval.Str("a"))}, CountPatternParams{Field: FieldOf("t"), Pattern: "("})
	if err == nil {
		t.Fatal("want error for bad pattern")
	}
}

func TestFieldExtentGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "extent") {
		t.Run(c.Name, func(t *testing.T) {
			lo, hi, ok, err := FieldExtent(context.Background(), c.Input, FieldOf(param(c, 0).Get("field").StrValue()))
			if err != nil {
				t.Fatal(err)
			}
			want := rawField(t, c.Name, "signal")
			if want.IsNullish() || want.Index(0).IsNullish() {
				if ok {
					t.Fatalf("got %v %v want undefined", lo, hi)
				}
				return
			}
			if !ok || lo != want.Index(0).NumValue() || hi != want.Index(1).NumValue() {
				t.Fatalf("got %v %v %v want %v", lo, hi, ok, want)
			}
		})
	}
}

func TestSequenceGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "sequence") {
		t.Run(c.Name, func(t *testing.T) {
			s := param(c, 0)
			got, err := Sequence(context.Background(), SequenceParams{
				Start: s.Get("start").NumValue(), Stop: s.Get("stop").NumValue(), Step: s.Get("step").NumValue(), As: s.Get("as").StrValue(),
			})
			reshapeCheck(t, c, got, err)
		})
	}
	if _, err := Sequence(context.Background(), SequenceParams{Start: 0, Stop: 1e12}); err == nil {
		t.Fatal("want limit error")
	}
	if got, err := Sequence(context.Background(), SequenceParams{Start: 0, Stop: math.NaN()}); err != nil || len(got) != 0 {
		t.Fatalf("NaN stop: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Sequence(ctx, SequenceParams{Start: 0, Stop: 100000}); err == nil {
		t.Fatal("want cancellation")
	}
}

func TestSampleGolden(t *testing.T) {
	for _, c := range reshapeCases(t, "sample") {
		t.Run(c.Name, func(t *testing.T) {
			seed := loadSeed(t, c.Name)
			got, err := Sample(context.Background(), cloneTuples(c.Input), int(param(c, 0).Get("size").NumValue()), LCG(seed))
			reshapeCheck(t, c, got, err)
		})
	}
}

func loadSeed(t *testing.T, name string) float64 { return rawField(t, name, "seed").NumValue() }

func rawField(t *testing.T, name, field string) jsval.Value {
	t.Helper()
	for _, c := range loadRaw(t, "reshape.json").Items() {
		if c.Get("name").StrValue() == name {
			return c.Get(field)
		}
	}
	t.Fatal("no case " + name)
	return jsval.Undefined
}

func loadRaw(t *testing.T, file string) jsval.Value {
	t.Helper()
	b, err := readTestdata(file)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func BenchmarkCollect(b *testing.B) {
	data := make([]jsval.Value, 100000)
	for i := range data {
		data[i] = obj("x", jsval.Num(float64((i*7919)%100003)))
	}
	cmp := CompareBy(FieldsOf("x"), nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Collect(context.Background(), data, cmp)
	}
}

func BenchmarkPivot(b *testing.B) {
	data := make([]jsval.Value, 20000)
	for i := range data {
		data[i] = obj("g", jsval.Int(i%50), "k", jsval.Str(string(rune('a'+i%8))), "v", jsval.Int(i))
	}
	p := PivotParams{GroupBy: FieldsOf("g"), Field: FieldOf("k"), Value: FieldOf("v")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Pivot(context.Background(), data, p)
	}
}

func BenchmarkLookup(b *testing.B) {
	from := make([]jsval.Value, 1000)
	for i := range from {
		from[i] = obj("id", jsval.Int(i), "p", jsval.Int(i*2))
	}
	ix, _ := NewLookupIndex(context.Background(), from, FieldOf("id"))
	prim := make([]jsval.Value, 100000)
	for i := range prim {
		prim[i] = obj("k", jsval.Int(i%1200))
	}
	p := LookupParams{Index: ix, Fields: FieldsOf("k"), Values: FieldsOf("p")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Lookup(context.Background(), prim, p)
	}
}

func readTestdata(file string) ([]byte, error) { return os.ReadFile(filepath.Join("testdata", file)) }
