package vega

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

func scatterSpec(n int) jsval.Value {
	var b strings.Builder
	b.WriteString(`{"width":600,"height":400,"padding":5,"data":[{"name":"t","values":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"x":%d,"y":%d,"c":"%c"}`, (i*7919)%1000, (i*104729)%1000, 'a'+i%5)
	}
	b.WriteString(`]}],"scales":[
	 {"name":"x","type":"linear","domain":{"data":"t","field":"x"},"range":"width","nice":true},
	 {"name":"y","type":"linear","domain":{"data":"t","field":"y"},"range":"height","nice":true},
	 {"name":"c","type":"ordinal","domain":{"data":"t","field":"c"},"range":"category"}],
	 "axes":[{"orient":"bottom","scale":"x"},{"orient":"left","scale":"y"}],
	 "legends":[{"fill":"c"}],
	 "marks":[{"type":"symbol","from":{"data":"t"},"encode":{"enter":{
	   "x":{"scale":"x","field":"x"},"y":{"scale":"y","field":"y"},"fill":{"scale":"c","field":"c"},"size":{"value":30}}}}]}`)
	v, err := jsval.ParseJSONString(b.String())
	if err != nil {
		panic(err)
	}
	return v
}

func benchmarkScatter(b *testing.B, n int) {
	spec := scatterSpec(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScatter1k(b *testing.B)   { benchmarkScatter(b, 1000) }
func BenchmarkScatter20k(b *testing.B)  { benchmarkScatter(b, 20000) }
func BenchmarkScatter100k(b *testing.B) { benchmarkScatter(b, 100000) }

func facetSpec(groups, per int) jsval.Value {
	var b strings.Builder
	b.WriteString(`{"width":600,"height":400,"padding":5,"data":[{"name":"t","values":[`)
	for i := 0; i < groups*per; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"g":"g%d","x":%d,"y":%d}`, i/per, i%per, (i*7919)%100)
	}
	b.WriteString(`]}],"scales":[
	 {"name":"gx","type":"band","domain":{"data":"t","field":"g"},"range":"width"},
	 {"name":"x","type":"linear","domain":[0,` + fmt.Sprint(per) + `],"range":[0,3]},
	 {"name":"y","type":"linear","domain":[0,100],"range":[20,0]}],
	 "marks":[{"type":"group","from":{"facet":{"name":"f","data":"t","groupby":"g"}},
	   "encode":{"update":{"x":{"scale":"gx","field":"g"},"width":{"value":3},"height":{"value":20}}},
	   "marks":[{"type":"symbol","from":{"data":"f"},"encode":{"update":{"x":{"scale":"x","field":"x"},"y":{"scale":"y","field":"y"}}}}]}]}`)
	v, err := jsval.ParseJSONString(b.String())
	if err != nil {
		panic(err)
	}
	return v
}

func BenchmarkFacet200x10(b *testing.B) {
	spec := facetSpec(200, 10)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFacet1000x5(b *testing.B) {
	spec := facetSpec(1000, 5)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
