package jsval

import (
	"bytes"
	"fmt"
	"testing"
)

// rowsJSON is a data file shaped like the vega-datasets tables: an array of
// flat records with a few string and number fields.
func rowsJSON(n int) []byte {
	var b bytes.Buffer
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"Name":"car %d","Miles_per_Gallon":%d.5,"Cylinders":%d,"Displacement":%d,"Horsepower":%d,"Weight_in_lbs":%d,"Acceleration":%d.2,"Year":"19%02d-01-01","Origin":"USA"}`,
			i, 10+i%30, 4+i%5, 100+i%300, 50+i%150, 1500+i%3000, 8+i%10, 70+i%12)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// coordsJSON is a GeoJSON-like nest of [x, y] pairs.
func coordsJSON(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"type":"Polygon","coordinates":[[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "[%d.123456,-%d.654321]", i%180, i%90)
	}
	b.WriteString(`]]}`)
	return b.Bytes()
}

func BenchmarkParseJSONRows(b *testing.B) {
	data := rowsJSON(400)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseJSON(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseJSONCoordinates(b *testing.B) {
	data := coordsJSON(5000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseJSON(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkObjectSetSmall builds the tuples transforms derive: a handful of
// fields each.
func BenchmarkObjectSetSmall(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		o := NewObject(4)
		o.Set("a", Num(1))
		o.Set("b", Str("x"))
		o.Set("c", Num(3))
		o.Set("d", True)
		_ = o.Lookup("c")
	}
}
