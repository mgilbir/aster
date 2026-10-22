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
	for b.Loop() {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFacet1000x5(b *testing.B) {
	spec := facetSpec(1000, 5)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// nestedRefsSpec is the gallery's brushing scatter plots in small: a group
// per pair of fields (the cross of fields with itself), and in each a symbol
// per row whose x and y read the field's name and the scale's from the
// parent group's datum, as nested references ({"datum": {"parent": ...}})
// and a scale named by one ({"parent": "xscale"}).
func nestedRefsSpec(rows int) jsval.Value {
	var b strings.Builder
	b.WriteString(`{"width":400,"height":400,"data":[{"name":"points","values":[`)
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"a":%d,"b":%d,"c":%d,"d":%d}`, (i*7919)%1000, (i*104729)%1000, (i*31)%97, (i*17)%53)
	}
	b.WriteString(`]},
	 {"name":"fields","values":["a","b","c","d"]},
	 {"name":"cross","source":"fields","transform":[{"type":"cross","as":["x","y"]},
	   {"type":"formula","as":"xscale","expr":"datum.x.data + 'X'"},{"type":"formula","as":"yscale","expr":"datum.y.data + 'Y'"}]}],
	 "scales":[
	  {"name":"groupx","type":"band","range":"width","domain":{"data":"fields","field":"data"}},
	  {"name":"groupy","type":"band","range":"height","domain":{"data":"fields","field":"data"}}`)
	for _, f := range []string{"a", "b", "c", "d"} {
		fmt.Fprintf(&b, `,{"name":"%sX","zero":false,"range":[0,90],"domain":{"data":"points","field":"%s"}},{"name":"%sY","zero":false,"range":[90,0],"domain":{"data":"points","field":"%s"}}`, f, f, f, f)
	}
	b.WriteString(`],
	 "marks":[{"type":"group","name":"cell","from":{"data":"cross"},
	   "encode":{"enter":{"x":{"scale":"groupx","field":"x.data"},"y":{"scale":"groupy","field":"y.data"},"width":{"value":90},"height":{"value":90}}},
	   "marks":[{"type":"symbol","from":{"data":"points"},"encode":{"enter":{
	     "x":{"scale":{"parent":"xscale"},"field":{"datum":{"parent":"x.data"}}},
	     "y":{"scale":{"parent":"yscale"},"field":{"datum":{"parent":"y.data"}}},"size":{"value":36}}}}]}]}`)
	v, err := jsval.ParseJSONString(b.String())
	if err != nil {
		panic(err)
	}
	return v
}

// BenchmarkNestedRefs renders nestedRefsSpec: 16 groups of 300 symbols. Its
// references compiled from their generated source rebuilt the item, its mark
// and the group chain as values on every evaluation, five times the time.
func BenchmarkNestedRefs(b *testing.B) {
	spec := nestedRefsSpec(300)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Render(context.Background(), spec, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// parseHeavySpec is a specification that is mostly specification: view
// groups, each with its own scales, axes, a legend, a title, signals and
// marks whose encodings use every kind of value reference (fields, signals,
// scales with band and offset, production rules with tests, nested
// references), over five rows of data. Parsing it (and compiling its
// encoders) is most of a render, as for a dashboard of small charts.
func parseHeavySpec(views int) jsval.Value {
	var b strings.Builder
	b.WriteString(`{"width":800,"height":600,"signals":[{"name":"hover","value":null},{"name":"pick","value":"b"}],
	 "data":[{"name":"t","values":[{"k":"a","v":3,"w":1},{"k":"b","v":7,"w":4},{"k":"c","v":2,"w":9},{"k":"d","v":5,"w":2},{"k":"e","v":8,"w":6}]}],
	 "marks":[`)
	for i := 0; i < views; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"group","name":"view%[1]d","encode":{"update":{"x":{"value":%[2]d},"y":{"value":%[3]d},"width":{"value":120},"height":{"value":80}}},
		 "signals":[{"name":"local%[1]d","update":"pick + '%[1]d'"}],
		 "scales":[{"name":"x","type":"band","domain":{"data":"t","field":"k"},"range":"width","padding":0.1},
		  {"name":"y","type":"linear","domain":{"data":"t","field":"v"},"range":"height","nice":true},
		  {"name":"c","type":"ordinal","domain":{"data":"t","field":"k"},"range":"category"}],
		 "axes":[{"orient":"bottom","scale":"x","title":"key %[1]d"},{"orient":"left","scale":"y","grid":true,"tickCount":4}],
		 "legends":[{"fill":"c","title":"legend %[1]d"}],
		 "title":{"text":"view %[1]d","subtitle":"five rows"},
		 "marks":[
		  {"type":"rect","name":"bars%[1]d","from":{"data":"t"},"encode":{"update":{
		    "x":{"scale":"x","field":"k"},"width":{"scale":"x","band":1,"offset":-1},
		    "y":{"scale":"y","field":"v"},"y2":{"scale":"y","value":0},
		    "fill":[{"test":"datum.k === pick","value":"firebrick"},{"test":"hover && hover.k === datum.k","signal":"'orange'"},{"scale":"c","field":"k"}],
		    "opacity":{"signal":"datum.w > 3 ? 1 : 0.6"},"tooltip":{"signal":"{key: datum.k, value: datum.v}"}}}},
		  {"type":"text","from":{"data":"bars%[1]d"},"encode":{"update":{
		    "x":{"field":"x","offset":2},"y":{"field":"y","offset":-2},"text":{"field":"datum.v"},
		    "fill":{"field":{"group":"fill"}},"fontSize":{"signal":"local%[1]d === 'b' ? 9 : 8"},"align":{"value":"left"}}}},
		  {"type":"rule","from":{"data":"t"},"encode":{"update":{
		    "x":{"scale":"x","field":"k","band":0.5},"y":{"scale":"y","signal":"datum.w"},"x2":{"field":{"group":"width"}},
		    "stroke":{"value":"#888"},"strokeDash":{"value":[2,2]}}}}]}`, i, 10+(i%5)*155, 10+(i/5)*115)
	}
	b.WriteString(`]}`)
	v, err := jsval.ParseJSONString(b.String())
	if err != nil {
		panic(err)
	}
	return v
}

// BenchmarkParseHeavy runs the parse stage of Render on parseHeavySpec with
// twenty views: the specification parsed, its encoders compiled and its
// dataflow's operators built, as Render does before it runs them. It is
// the benchmark that catches a slower parse, which the renders of data sets
// above dilute.
func BenchmarkParseHeavy(b *testing.B) {
	spec := parseHeavySpec(20)
	if _, err := Render(context.Background(), spec, Options{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		v, flow, _ := parseRuntime(context.Background(), spec, Options{})
		v.build(flow)
		v.releaseLists()
	}
}
