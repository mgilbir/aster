package aster

import (
	"fmt"
	"testing"
)

// BenchmarkTextLong renders 5000 text marks of 200 characters. Every iteration
// writes different strings, so the converter's width cache never answers.
func BenchmarkTextLong(b *testing.B) {
	c, err := New()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		spec := fmt.Sprintf(`{"width":400,"height":300,"data":[{"name":"t","transform":[{"type":"sequence","start":%d,"stop":%d,"as":"x"},{"type":"formula","as":"s","expr":"pad('' + datum.x, 200, 'z', 'left')"}]}],"marks":[{"type":"text","from":{"data":"t"},"encode":{"update":{"text":{"field":"s"},"x":{"value":0},"y":{"field":"x"}}}}]}`,
			(i+1)*10000, (i+1)*10000+5000)
		if _, err := c.VegaToSVG([]byte(spec)); err != nil {
			b.Fatal(err)
		}
	}
}
