package purego

// Specs that make a layer panic. The public API recovers ("internal error"),
// so the host survives, but each of these is a bug: the compiler should return
// a validation error. Found by TestTypeConfusion (extreme_security_test.go);
// more of the same family: go test ./purego -run TestTypeConfusion -args -sec.n=6000 -sec.seed=12

import (
	"strings"
	"testing"
)

func TestVegaLiteInvalidSpecsDoNotPanic(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	data := `"data":{"values":[{"a":1,"b":2}]}`
	enc := `"encoding":{"x":{"field":"a","type":"quantitative"}}`
	for name, spec := range map[string]string{
		// vegalite/mark.go:408 getMarkGroup: no mark compiler for an unknown
		// mark type, mc.encodeEntry dereferences nil.
		"unknown mark type":       `{` + data + `,"mark":"nope",` + enc + `}`,
		"mark is an array":        `{` + data + `,"mark":[1,2],` + enc + `}`,
		"mark type is an object":  `{` + data + `,"mark":{"type":{}},` + enc + `}`,
		"layer with unknown mark": `{` + data + `,"layer":[{"mark":"nope",` + enc + `}]}`,
		// vegalite/selection.go:1591: parseSelector of a translate string
		// without a drag ("between") is indexed unconditionally.
		"selection translate is not a drag": `{` + data + `,"params":[{"name":"p","select":{"type":"interval","translate":"__proto__"}}],"mark":"point",` + enc + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.VegaLiteToVega([]byte(spec))
			if err != nil && strings.Contains(err.Error(), "internal error") {
				t.Errorf("panic inside the compiler: %v", err)
			}
		})
	}
}
