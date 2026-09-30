package transforms

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// goldenCase is one recorded upstream run (see testdata/lib.mjs).
type goldenCase struct {
	Name      string
	Input     []jsval.Value // decoded tuples
	Transform jsval.Value   // the upstream transform array as written in the spec
	Output    jsval.Value   // decoded output tuples (undefined when Error is set)
	Error     string
	Signals   jsval.Value
}

// decodeDialect maps the {"$":...} encodings written by lib.mjs back to
// values: NaN, +-Infinity and dates.
func decodeDialect(v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindArr:
		items := make([]jsval.Value, v.Len())
		for i, it := range v.Items() {
			items[i] = decodeDialect(it)
		}
		return jsval.Arr(items)
	case jsval.KindObj:
		o := v.ObjValue()
		if tag, ok := o.Get("$"); ok && o.Len() <= 2 {
			switch tag.StrValue() {
			case "undef":
				return jsval.Undefined
			case "NaN":
				return jsval.Num(math.NaN())
			case "Inf":
				return jsval.Num(math.Inf(1))
			case "-Inf":
				return jsval.Num(math.Inf(-1))
			case "date":
				return jsval.Timestamp(decodeDialect(o.Lookup("v")).NumValue())
			}
		}
		c := jsval.NewObject(o.Len())
		for i := 0; i < o.Len(); i++ {
			c.Set(o.KeyAt(i), decodeDialect(o.ValueAt(i)))
		}
		return jsval.Obj(c)
	}
	return v
}

// loadGolden reads testdata/<file>, a JSON array of recorded cases.
func loadGolden(t testing.TB, file string) []goldenCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	var cases []goldenCase
	for _, c := range root.Items() {
		gc := goldenCase{
			Name:      c.Get("name").StrValue(),
			Input:     decodeDialect(c.Get("input")).Items(),
			Transform: c.Get("transform"),
			Output:    decodeDialect(c.Get("output")),
			Error:     c.Get("error").StrValue(),
			Signals:   c.Get("signals"),
		}
		cases = append(cases, gc)
	}
	return cases
}

// cloneTuples deep-copies input tuples one level (transforms may annotate the
// objects they receive).
func cloneTuples(in []jsval.Value) []jsval.Value {
	out := make([]jsval.Value, len(in))
	for i, t := range in {
		out[i] = cloneDeep(t)
	}
	return out
}

func cloneDeep(v jsval.Value) jsval.Value {
	switch v.Kind() {
	case jsval.KindObj:
		o := v.ObjValue()
		c := jsval.NewObject(o.Len())
		for i := 0; i < o.Len(); i++ {
			c.Set(o.KeyAt(i), cloneDeep(o.ValueAt(i)))
		}
		return jsval.Obj(c)
	case jsval.KindArr:
		items := make([]jsval.Value, v.Len())
		for i, it := range v.Items() {
			items[i] = cloneDeep(it)
		}
		return jsval.Arr(items)
	}
	return v
}

// diffValues returns "" when got matches want: same structure, same object
// key order, numbers within tol (relative for large magnitudes), and "" for
// a mismatch describing the first difference. Keys whose want value is
// undefined are ignored on both sides (upstream omits undefined properties).
func diffValues(path string, got, want jsval.Value, tol float64) string {
	gn, gok := got.NumberOrNull()
	wn, wok := want.NumberOrNull()
	if gok && wok && got.Kind() == want.Kind() {
		if math.IsNaN(gn) && math.IsNaN(wn) || gn == wn {
			return ""
		}
		if math.Abs(gn-wn) <= tol*math.Max(1, math.Abs(wn)) {
			return ""
		}
		return fmt.Sprintf("%s: got %v want %v", path, gn, wn)
	}
	if got.Kind() != want.Kind() {
		return fmt.Sprintf("%s: got %s (%v) want %s (%v)", path, got.Kind(), got, want.Kind(), want)
	}
	switch want.Kind() {
	case jsval.KindArr:
		if got.Len() != want.Len() {
			return fmt.Sprintf("%s: length got %d want %d", path, got.Len(), want.Len())
		}
		for i := range want.Items() {
			if d := diffValues(fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i), tol); d != "" {
				return d
			}
		}
	case jsval.KindObj:
		gk, wk := definedKeys(got.ObjValue()), definedKeys(want.ObjValue())
		if len(gk) != len(wk) {
			return fmt.Sprintf("%s: keys got %v want %v", path, gk, wk)
		}
		for i, k := range wk {
			if gk[i] != k {
				return fmt.Sprintf("%s: keys got %v want %v", path, gk, wk)
			}
			if d := diffValues(path+"."+k, got.Get(k), want.Get(k), tol); d != "" {
				return d
			}
		}
	default:
		if !jsval.Equal(got, want) {
			return fmt.Sprintf("%s: got %v want %v", path, got, want)
		}
	}
	return ""
}

func definedKeys(o *jsval.Object) []string {
	var ks []string
	for i := 0; i < o.Len(); i++ {
		if !o.ValueAt(i).IsUndefined() {
			ks = append(ks, o.KeyAt(i))
		}
	}
	return ks
}

// tupleArr wraps tuples as an array value for diffValues.
func tupleArr(ts []jsval.Value) jsval.Value { return jsval.Arr(ts) }

// spec helpers for reading the transform parameters of a golden case.
func param(c goldenCase, i int) jsval.Value { return c.Transform.Index(i) }

func strList(v jsval.Value) []string {
	var out []string
	for _, it := range v.Items() {
		out = append(out, it.StrValue())
	}
	return out
}

func fieldList(v jsval.Value) []Field {
	var out []Field
	for _, it := range v.Items() {
		if it.IsNullish() {
			out = append(out, Field{})
		} else {
			out = append(out, FieldOf(it.StrValue()))
		}
	}
	return out
}

func numListOpt(v jsval.Value) []float64 {
	var out []float64
	for _, it := range v.Items() {
		if it.IsNullish() {
			out = append(out, math.NaN())
		} else {
			out = append(out, it.NumValue())
		}
	}
	return out
}

func obj(kv ...any) jsval.Value { return jsval.Obj(jsval.ObjectOf(kv...)) }

// readGoldenFile parses testdata/<file> as one JSON document.
func readGoldenFile(t testing.TB, file string) jsval.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(b)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return v
}
