package expr

import (
	"fmt"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func benchData(n int) []jsval.Value {
	cats := []string{"a", "b", "c", "d"}
	out := make([]jsval.Value, n)
	for i := range out {
		out[i] = jsval.Obj(jsval.ObjectOf(
			"x", jsval.Num(float64(i%17)),
			"a", jsval.Num(float64(i)*0.5),
			"b", jsval.Num(float64(i%7)),
			"v", jsval.Num(float64(i)*1.37),
			"cat", jsval.Str(cats[i%4]),
			"date", jsval.Timestamp(1.5e12+float64(i)*3.6e6),
		))
	}
	return out
}

// BenchmarkEval times one evaluation per iteration of typical filter and
// formula expressions over 10k datums.
func BenchmarkEval(b *testing.B) {
	data := benchData(10000)
	for _, src := range []string{
		"datum.x > 5 && datum.cat === 'a'",
		"datum.a * 2 + datum.b",
		"floor(datum.v / 10) * 10",
		"if(datum.x > 5, 'hi', 'lo')",
		"datum.cat == 'b' || datum.cat == 'c'",
		"upper(datum.cat) + '-' + datum.x",
		"year(datum.date) === 2017 && month(datum.date) < 6",
		"sqrt(datum.a * datum.a + datum.b * datum.b)",
		"clamp(datum.x, 3, 9) / max(datum.b, 1)",
		"indexof(['a', 'c'], datum.cat) >= 0",
		"test(/^[ab]$/, datum.cat)",
		"format(datum.v, ',.2f')",
		"timeFormat(datum.date, '%Y-%m-%d')",
		"toString(datum.x) + ':' + pad(datum.b, 3, '0', 'left')",
		"isValid(datum.x) ? datum.x : 0",
		"abs(datum.a - 100) < 50 ? {k: datum.cat, v: datum.x} : null",
	} {
		p, err := Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(src, func(b *testing.B) {
			s := NewScope(nil)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s.Datum = data[i%len(data)]
				if _, err := p.Eval(s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFilter10k is a whole filter pass: compile once, evaluate 10k datums.
func BenchmarkFilter10k(b *testing.B) {
	data := benchData(10000)
	p, err := Compile("datum.x > 5 && datum.cat === 'a' || datum.b == 3")
	if err != nil {
		b.Fatal(err)
	}
	s := NewScope(nil)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		kept := 0
		for _, d := range data {
			s.Datum = d
			v, _ := p.Eval(s)
			if v.IsTruthy() {
				kept++
			}
		}
		if kept == 0 {
			b.Fatal("nothing kept")
		}
	}
}

// BenchmarkCompileAndEval10k includes compilation, as a spec with a single
// formula pays it.
func BenchmarkCompileAndEval10k(b *testing.B) {
	data := benchData(10000)
	const src = "datum.x > 5 ? datum.a * 2 + datum.b : upper(datum.cat) + '-' + year(datum.date)"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p, err := Compile(src)
		if err != nil {
			b.Fatal(err)
		}
		s := NewScope(nil)
		for _, d := range data {
			s.Datum = d
			if _, err := p.Eval(s); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkCompile(b *testing.B) {
	for _, src := range []string{
		"datum.x > 5",
		"datum.x > 5 && datum.cat === 'a' || (datum.a * 2 + datum.b) / max(datum.v, 1) < 10 ? timeFormat(datum.date, '%Y') : 'n/a'",
	} {
		b.Run(fmt.Sprint(len(src)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Compile(src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkParse(b *testing.B) {
	const src = "datum.x > 5 && datum.cat === 'a' || (datum.a * 2 + datum.b) / max(datum.v, 1) < 10 ? timeFormat(datum.date, '%Y') : 'n/a'"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}
