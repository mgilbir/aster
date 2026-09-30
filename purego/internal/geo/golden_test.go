package geo

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// loadGolden reads a recorded upstream vector file from testdata, gunzipping
// .gz files.
func loadGolden(t testing.TB, name string, v any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		r = zr
	}
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// gnum decodes a recorded number: JSON numbers, or the strings the generator
// uses for non-finite values and negative zero.
func gnum(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		switch x {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		case "-0":
			return math.Copysign(0, -1)
		}
	}
	return math.NaN()
}

// same reports whether two numbers are equal, treating NaNs as equal.
func same(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// near is same within a relative tolerance.
func near(a, b, tol float64) bool {
	if same(a, b) {
		return true
	}
	return math.Abs(a-b) <= tol*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func parseJSONValue(t testing.TB, raw json.RawMessage) jsval.Value {
	t.Helper()
	v, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// projFromGolden builds a projection the way vega-geo's Projection operator
// does from a recorded configuration.
func projFromGolden(t testing.TB, typ string, props json.RawMessage) Projection {
	t.Helper()
	obj := jsval.NewObject(4)
	obj.Set("type", jsval.Str(typ))
	if len(props) > 0 {
		pv := parseJSONValue(t, props)
		if o := pv.ObjValue(); o != nil {
			for i := 0; i < o.Len(); i++ {
				obj.Set(o.KeyAt(i), o.ValueAt(i))
			}
		}
	}
	p, err := ConfigureProjection(nil, obj, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(name string) ([]byte, error) { return os.ReadFile(name) }

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
