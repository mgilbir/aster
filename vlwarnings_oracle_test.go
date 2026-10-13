package aster_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/oracle"
)

var warningsVerbose = flag.Bool("warnings.v", false, "TestVegaLiteWarningsMatchNode: log every difference")

// compileWarnings compiles spec and returns the warnings the call's handler
// was told of, in order.
func compileWarnings(c *aster.Converter, spec []byte) ([]string, error) {
	var got []string
	_, err := c.VegaLiteToVega(spec, aster.WithErrorHandler(func(err error) error {
		var w aster.Warning
		if errors.As(err, &w) {
			got = append(got, string(w))
			return nil
		}
		return err
	}))
	return got, err
}

// TestVegaLiteWarningsMatchNode compiles every Vega-Lite specification of the
// corpus with both versions, here and in node, and compares the warnings the
// compiler logs (C38): the same messages, with upstream's text, as many times
// as upstream logs each.
func TestVegaLiteWarningsMatchNode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: compiles the Vega-Lite corpus in node")
	}
	var files []string
	for _, g := range []string{"testdata/corpus/regress/*.vl.json", "testdata/corpus/vegalite/*.vl.json",
		"testdata/vega-lite/v6.4.3/specs/*.vl.json", "testdata/vl-convert/*.vl.json", "internal/vegalite/testdata/specs/*/*.json"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	sort.Strings(files)
	if len(files) < 1000 {
		t.Fatalf("only %d specs", len(files))
	}
	for _, v := range []struct{ set, version string }{{oracle.VL6, "6.4"}, {oracle.VL5, "5.8"}} {
		o := oracle.For(t, v.set)
		c, err := aster.New(aster.WithVegaLiteVersion(v.version))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var specs, withWarnings, differ int
		for _, f := range files {
			spec, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := o.Compile(spec)
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}
			got, gerr := compileWarnings(c, spec)
			if want.Err != "" || gerr != nil {
				continue // errors are compared elsewhere
			}
			specs++
			if len(want.Warnings) > 0 {
				withWarnings++
			}
			if !slices.Equal(got, want.Warnings) {
				differ++
				if differ <= 40 || *warningsVerbose {
					t.Errorf("%s (VL %s): warnings differ\n  engine: %s\n  node:   %s", f, v.version, strings.Join(got, " | "), strings.Join(want.Warnings, " | "))
				}
			}
		}
		t.Logf("VL %s: %d specs compiled, %d with warnings upstream, %d differ", v.version, specs, withWarnings, differ)
	}
}

// warningSpecs reach upstream's log.warn sites, at least one each (C38).
var warningSpecs = []string{
	// encoding
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"nope":{"field":"a","type":"quantitative"},"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"area","encoding":{"x":{"field":"a","type":"quantitative"},"size":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"line","encoding":{"x":{"field":"a","type":"quantitative"},"size":{"field":"b","aggregate":"sum","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"color":{"field":"b","type":"nominal"},"stroke":{"value":"red"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"detail":[{"field":"b","type":"nominal"},{"type":"nominal"}],"y":{"type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"arc","encoding":{"angle":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"bar","encoding":{"x":{"field":"a","type":"quantitative"},"xOffset":{"field":"b","type":"nominal"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"bar","encoding":{"xOffset":{"field":"b","type":"nominal"},"y":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":"a","y":5,"color":true}}`,
	// channel defs
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","aggregate":"avg"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"nominal","aggregate":"count"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"color":{"field":"b","bin":"binned","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","formatType":"myfmt"},"y":{"field":"b","type":"quantitative","axis":{"formatType":"x"}}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"size":{"field":"b","type":"nominal"},"shape":{"field":"a","type":"quantitative"},"opacity":{"field":"b","type":"nominal"},"order":{"field":"b","type":"nominal"},"longitude":{"field":"a","type":"nominal"},"latitude":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"row":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"shape":{"field":"b","type":"ordinal"},"size":{"field":"a","type":"temporal"}}}`,
	// transforms and data
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","transform":[{"calcualte":"1","as":"c"}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":"1","b":2}],"format":{"parse":{"a":"bogus"}}},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":"1","b":2}],"format":{"parse":{"a":"number"}}},"mark":"point","transform":[{"filter":{"field":"a","equal":"x"}}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","transform":[{"lookup":"a","from":{"data":{"values":[{"a":1,"c":3}]},"key":"a"}}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","transform":[{"filter":{"field":"a","range":[{"year":2000,"quarter":7},5]}},{"filter":{"field":"b","equal":{"year":2000,"month":1,"day":3}}}],"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	// stack
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","aggregate":"mean","type":"quantitative","scale":{"type":"log"},"stack":true},"color":{"field":"a","type":"nominal"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative","stack":"zero"},"y2":{"field":"a"},"color":{"field":"a","type":"nominal"}}}`,
	// scales
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","scale":{"type":"ordinal","scheme":"blues","padding":1}},"shape":{"field":"b","type":"nominal","scale":{"type":"linear"}},"color":{"field":"b","type":"quantitative","scale":{"type":"threshold"}}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"temporal","scale":{"type":"linear"}},"y":{"field":"b","type":"quantitative","scale":{"domain":"unaggregated"}},"size":{"field":"b","aggregate":"variance","type":"quantitative","scale":{"domain":"unaggregated"}}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"width":{"step":20},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"layer":[{"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","scale":{"domain":[0,1]}}}},{"mark":"point","encoding":{"x":{"field":"b","type":"quantitative","scale":{"domain":[0,5]}}}}]}`,
	`{"data":{"values":[{"a":1,"b":2}]},"layer":[{"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","axis":{"title":"A"}}}},{"mark":"point","encoding":{"x":{"field":"b","type":"quantitative","axis":{"title":"B","labelColor":"red"}}}}]}`,
	`{"data":{"values":[{"a":1,"b":2}]},"layer":[{"mark":"bar","encoding":{"x":{"field":"a","type":"nominal","sort":{"op":"sum","field":"b"}}}},{"mark":"bar","encoding":{"x":{"field":"a","type":"nominal","sort":{"op":"mean","field":"b"}}}}]}`,
	`{"data":{"values":[{"a":1,"b":2}]},"layer":[{"mark":"bar","encoding":{"x":{"field":"a","type":"nominal","sort":{"op":"sum","field":"b"}}}},{"mark":"bar","encoding":{"x":{"field":"b","type":"nominal","sort":{"op":"mean","field":"a"}}}}]}`,
	// layout, autosize, composition
	`{"data":{"values":[{"a":1,"b":2}]},"width":"container","height":"container","hconcat":[{"mark":"point"}],"autosize":"fit"}`,
	`{"data":{"values":[{"a":1,"b":2}]},"width":"container","autosize":"pad","mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"width":{"step":10},"height":{"step":10},"autosize":"fit","mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"nominal"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"width":{"step":10},"autosize":"fit","mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"resolve":{"axis":{"x":"shared"}},"hconcat":[{"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}}]}`,
	`{"data":{"values":[{"a":1,"b":2}]},"resolve":{"scale":{"x":"independent"},"axis":{"x":"shared"}},"layer":[{"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"}}},{"mark":"point","encoding":{"x":{"field":"b","type":"quantitative"}}}]}`,
	`{"data":{"values":[{"a":1,"b":2}]},"repeat":{"row":["a"]},"columns":2,"spec":{"mark":"point","encoding":{"x":{"field":{"repeat":"row"},"type":"quantitative"},"y":{"field":{"repeat":"column"},"type":"quantitative"}}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"facet":{"row":{"field":"a"}},"columns":2,"spec":{"mark":"point"}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"facet":{"row":{"field":"a"},"foo":{"field":"b"}},"spec":{"mark":"point"}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative"},"row":{"field":"a","type":"nominal"},"facet":{"field":"b","type":"nominal"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"projection":{"type":"mercator"},"layer":[{"mark":"circle","projection":{"type":"albers"},"encoding":{"longitude":{"field":"a","type":"quantitative"},"latitude":{"field":"b","type":"quantitative"}}}]}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"line","encoding":{"x":{"field":"a","type":"quantitative"},"x2":{"field":"b"},"y":{"field":"b","type":"quantitative"}}}`,
	// composite marks
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"boxplot","params":[{"name":"p","select":"point"}],"encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative","aggregate":"sum"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"errorbar","center":"median","extent":"stderr"},"encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"errorbar","center":"mean"},"encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"},"y2":{"field":"a"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"errorband","interpolate":"linear"},"encoding":{"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"errorband","tension":1},"encoding":{"y":{"field":"b","type":"quantitative"}}}`,
	// marks
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"bar","orient":"horizontal"},"encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"point","color":"red","stroke":"blue"},"encoding":{"x":{"field":"a","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"rect","align":{"expr":"'left'"},"width":{"band":0.5},"size":5},"encoding":{"x":{"field":"a","type":"quantitative"},"x2":{"field":"b"},"y":{"field":"b","type":"quantitative","bin":"binned"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":{"type":"bar","width":{"band":0.5}},"encoding":{"x":{"field":"a","type":"quantitative"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","bin":"binned"}}}`,
	// selections
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","params":[{"name":"s","select":{"type":"interval","encodings":["x","color"]}},{"name":"t","select":{"type":"point","encodings":["y"]}}],"encoding":{"x":{"field":"a","type":"quantitative","aggregate":"sum"},"color":{"value":"red"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","params":[{"name":"s","select":"interval","value":{"a":[1,2]}}],"encoding":{"x":{"field":"a","type":"quantitative"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"line","params":[{"name":"s","select":{"type":"point","nearest":true}}],"encoding":{"x":{"field":"a","type":"quantitative"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","params":[{"name":"s","select":"interval","bind":"scales"}],"encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","params":[{"name":"s","select":{"type":"point","fields":["a","b"]},"bind":"legend"}],"encoding":{"x":{"field":"a","type":"quantitative"},"color":{"field":"b","type":"nominal"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"mark":"point","params":[{"name":"s","select":{"type":"interval","translate":"mousedown","encodings":["x","y"]}}],"encoding":{"x":{"field":"a","type":"quantitative"},"y":{"field":"b","type":"quantitative"}}}`,
	`{"data":{"values":[{"a":1,"b":2}]},"layer":[{"mark":"point","params":[{"name":"s","select":{"type":"interval","encodings":["x","y"]}}],"encoding":{"x":{"field":"a","type":"quantitative"},"y":{"field":"b","type":"quantitative"}}},{"mark":"point","encoding":{"x":{"field":"a","type":"quantitative","scale":{"domain":{"param":"s"}}},"y":{"field":"b","type":"quantitative","scale":{"domain":{"param":"s","encoding":"color"}}}}}]}`,
}

// TestVegaLiteWarningSitesMatchNode compiles warningSpecs with both
// versions and compares the warnings with node's, in order.
func TestVegaLiteWarningSitesMatchNode(t *testing.T) {
	for _, v := range []struct{ set, version string }{{oracle.VL6, "6.4"}, {oracle.VL5, "5.8"}} {
		o := oracle.For(t, v.set)
		c, err := aster.New(aster.WithVegaLiteVersion(v.version))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, spec := range warningSpecs {
			want, err := o.Compile([]byte(spec))
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}
			got, gerr := compileWarnings(c, []byte(spec))
			if (want.Err != "") != (gerr != nil) {
				t.Errorf("%s (VL %s): engine error %v, node error %q", spec, v.version, gerr, want.Err)
				continue
			}
			if !slices.Equal(got, want.Warnings) {
				t.Errorf("%s (VL %s): warnings differ\n  engine: %s\n  node:   %s", spec, v.version, strings.Join(got, " | "), strings.Join(want.Warnings, " | "))
			}
		}
	}
}
