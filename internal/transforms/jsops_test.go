package transforms

import (
	"testing"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

func TestAscendingGolden(t *testing.T) {
	b := readGoldenFile(t, "compare.json")
	values := decodeDialect(b.Get("values")).Items()
	table := b.Get("ascending").Items()
	for i, u := range values {
		for j, v := range values {
			want := int(table[i].Index(j).NumValue())
			if got := Ascending(u, v); got != want {
				t.Errorf("Ascending(%v, %v) = %d, want %d", u, v, got, want)
			}
		}
	}
}

func TestCompareByGolden(t *testing.T) {
	b := readGoldenFile(t, "compare.json")
	rows := decodeDialect(b.Get("rows")).Items()
	for k, spec := range b.Get("compares").Items() {
		cmp := CompareBy(fieldList(spec.Get("fields")), strList(spec.Get("orders")))
		res := spec.Get("result")
		for i, x := range rows {
			for j, y := range rows {
				want := int(res.Index(i).Index(j).NumValue())
				got := cmp(x, y)
				if (got > 0) != (want > 0) || (got < 0) != (want < 0) {
					t.Errorf("spec %d: compare(row %d, row %d) = %d, want sign %d", k, i, j, got, want)
				}
			}
		}
	}
}

func TestCompareByNoFields(t *testing.T) {
	if CompareBy(nil, nil) != nil || CompareBy([]Field{{}}, nil) != nil {
		t.Fatal("no fields must give a nil comparator")
	}
}

func TestCompareUTF16(t *testing.T) {
	// U+FF5E is a BMP character above the surrogate range; a supplementary
	// character's high surrogate (0xD83D) sorts before it in UTF-16 but after
	// it in UTF-8 byte order.
	if CompareUTF16("\U0001F600", "～") >= 0 {
		t.Error("emoji must sort before U+FF5E in UTF-16 order")
	}
	if CompareUTF16("a", "ab") >= 0 || CompareUTF16("b", "a") <= 0 || CompareUTF16("x", "x") != 0 {
		t.Error("basic ordering")
	}
}

func TestExtentIgnoresInvalid(t *testing.T) {
	vals := []jsval.Value{jsval.Null, jsval.Num(3), jsval.Num(2), jsval.Str("x"), jsval.Undefined, jsval.Num(9)}
	lo, hi := ExtentIndex(len(vals), func(i int) jsval.Value { return vals[i] })
	if lo != 2 || hi != 5 {
		t.Errorf("ExtentIndex = %d,%d", lo, hi)
	}
	if lo, hi := ExtentIndex(2, func(int) jsval.Value { return jsval.Null }); lo != -1 || hi != -1 {
		t.Error("all-null extent index must be -1,-1")
	}
}

func TestOrderedKeys(t *testing.T) {
	got := OrderedKeys([]string{"b", "10", "a", "2", "01", "-1"})
	want := []string{"2", "10", "b", "a", "01", "-1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("OrderedKeys = %v, want %v", got, want)
		}
	}
}

func TestFieldOfNames(t *testing.T) {
	cases := []struct{ path, name string }{{"a", "a"}, {"a.b", "a.b"}, {"a['b.c']", "a['b.c']"}, {`a\.b`, "a.b"}, {"a[0]", "a[0]"}}
	for _, c := range cases {
		if got := FieldOf(c.path).Name; got != c.name {
			t.Errorf("FieldOf(%q).Name = %q, want %q", c.path, got, c.name)
		}
	}
	o := jsval.Obj(jsval.ObjectOf("a", jsval.Obj(jsval.ObjectOf("b", jsval.Num(4)))))
	if v := FieldOf("a.b").Apply(o); v.NumValue() != 4 {
		t.Error("nested field read")
	}
	// The library accessor never panics; FieldOfStrict is the one that throws
	// like a JavaScript member chain (TestFieldAccessorThrowsOnMissingSteps).
	if !FieldOf("a.b.c").Apply(o).IsUndefined() || !FieldOf("x.y").Apply(o).IsUndefined() {
		t.Error("missing steps must read undefined, not panic")
	}
	if !FieldOf("q").Apply(o).IsUndefined() {
		t.Error("missing leaf is undefined, not null")
	}
}

func TestMeasureName(t *testing.T) {
	if MeasureName("sum", "x", "") != "sum_x" || MeasureName("count", "", "") != "count" || MeasureName("sum", "x", "s") != "s" {
		t.Fatal("MeasureName")
	}
}

func TestKeyOf(t *testing.T) {
	row := jsval.Obj(jsval.ObjectOf("a", jsval.Num(1), "b", jsval.Null, "c", jsval.Str("z")))
	if k := KeyOfPaths(format.Zone{}, "a", "b", "c", "d")(row); k != "1|null|z|undefined" {
		t.Errorf("key = %q", k)
	}
	if KeyOfPaths(format.Zone{})(row) != "" {
		t.Error("empty key")
	}
}

func TestQuantilesAndLCG(t *testing.T) {
	// LCG(1): values recorded from vega.randomLCG(1) in node.
	r := LCG(1)
	want := []float64{0.5138700783782965, 0.43980081818988587, 0.6360180655662054}
	for i, w := range want {
		if got := r(); got < w-1e-15 || got > w+1e-15 {
			t.Errorf("LCG draw %d = %v, want %v", i, got, w)
		}
	}
	data := []jsval.Value{}
	for _, f := range []float64{5, 1, 3, 2, 4} {
		data = append(data, jsval.Obj(jsval.ObjectOf("v", jsval.Num(f))))
	}
	q := Quartiles(data, FieldOf("v").Get)
	if q != [3]float64{2, 3, 4} {
		t.Errorf("Quartiles = %v", q)
	}
}
