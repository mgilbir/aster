package contour

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Golden vectors come from testdata/gen_contour.mjs, which runs the upstream
// transforms inside a headless Vega view.

func field(name string) Accessor {
	return func(v jsval.Value) jsval.Value { return v.Get(name) }
}

func nums(v jsval.Value) []float64 {
	if !v.IsArr() {
		return nil
	}
	out := make([]float64, v.Len())
	for i, it := range v.Items() {
		out[i] = it.NumValue()
	}
	return out
}

func optBool(v jsval.Value) *bool {
	if v.IsUndefined() {
		return nil
	}
	b := v.BoolValue()
	return &b
}

func optField(v jsval.Value) Accessor {
	if v.IsStr() {
		return field(v.StrValue())
	}
	return nil
}

func density(p jsval.Value) DensityParams {
	return DensityParams{
		X: optField(p.Get("x")), Y: optField(p.Get("y")), Weight: optField(p.Get("weight")),
		Size:      nums(p.Get("size")),
		CellSize:  p.Get("cellSize").NumValue(),
		Bandwidth: bandwidth(p.Get("bandwidth")),
	}
}

func bandwidth(v jsval.Value) []float64 {
	if v.IsNum() {
		return []float64{v.NumValue()}
	}
	return nums(v)
}

func runTransform(t *testing.T, tp jsval.Value, in []jsval.Value) []jsval.Value {
	t.Helper()
	ctx := context.Background()
	var out []jsval.Value
	var err error
	switch typ := tp.Get("type").StrValue(); typ {
	case "contour":
		p := ContourParams{
			Size: nums(tp.Get("size")), Values: nums(tp.Get("values")),
			X: optField(tp.Get("x")), Y: optField(tp.Get("y")), Weight: optField(tp.Get("weight")),
			CellSize: tp.Get("cellSize").NumValue(), Bandwidth: bandwidth(tp.Get("bandwidth")),
			Thresholds: nums(tp.Get("thresholds")), Count: tp.Get("count").NumValue(),
			Nice: tp.Get("nice").BoolValue(), Smooth: optBool(tp.Get("smooth")),
		}
		out, err = Contour(ctx, in, p)
	case "kde2d":
		p := KDE2DParams{DensityParams: density(tp), Counts: tp.Get("counts").BoolValue(), As: tp.Get("as").StrValue()}
		if g := tp.Get("groupby"); g.IsArr() {
			p.GroupBy = []GroupField{}
			for _, n := range g.Items() {
				p.GroupBy = append(p.GroupBy, GroupField{Name: n.StrValue(), Get: field(n.StrValue())})
			}
		}
		out, err = KDE2D(ctx, in, p)
	case "isocontour":
		p := IsocontourParams{
			Field: optField(tp.Get("field")), Thresholds: nums(tp.Get("thresholds")),
			Levels: tp.Get("levels").NumValue(), Nice: tp.Get("nice").BoolValue(),
			Shared: tp.Get("resolve").StrValue() == "shared",
			Zero:   optBool(tp.Get("zero")), Smooth: optBool(tp.Get("smooth")),
			As: tp.Get("as").StrValue(), NoAs: tp.Get("as").IsNull(),
		}
		if s := tp.Get("scale"); s.IsNum() {
			p.Scale = []float64{s.NumValue()}
		} else {
			p.Scale = nums(s)
		}
		p.Translate = nums(tp.Get("translate"))
		out, err = Isocontour(ctx, in, p)
	default:
		t.Fatalf("unknown transform %q", typ)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func near(a, b float64) bool {
	if a == b || (math.IsNaN(a) && math.IsNaN(b)) {
		return true
	}
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func compare(path string, got, want jsval.Value, skip string) error {
	switch {
	case want.IsNull() && got.IsNum() && math.IsNaN(got.NumValue()):
		return nil
	case want.IsNum():
		if !got.IsNum() || !near(got.NumValue(), want.NumValue()) {
			return fmt.Errorf("%s: got %v want %v", path, got, want)
		}
	case want.IsArr():
		if !got.IsArr() || got.Len() != want.Len() {
			return fmt.Errorf("%s: array length got %d want %d", path, got.Len(), want.Len())
		}
		for i := range want.Items() {
			if err := compare(fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i), skip); err != nil {
				return err
			}
		}
	case want.IsObj():
		if !got.IsObj() {
			return fmt.Errorf("%s: not an object", path)
		}
		wo, go_ := want.ObjValue(), got.ObjValue()
		for _, k := range go_.Keys() {
			if !wo.Has(k) && k != skip {
				return fmt.Errorf("%s: unexpected key %q", path, k)
			}
		}
		for _, k := range wo.Keys() {
			if !go_.Has(k) {
				return fmt.Errorf("%s: missing key %q", path, k)
			}
			if err := compare(path+"."+k, go_.Lookup(k), wo.Lookup(k), skip); err != nil {
				return err
			}
		}
	default:
		if !jsval.Equal(got, want) {
			return fmt.Errorf("%s: got %v want %v", path, got, want)
		}
	}
	return nil
}

func TestGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/contour.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases.Items() {
		name := c.Get("name").StrValue()
		t.Run(name, func(t *testing.T) {
			data := c.Get("data").Items()
			if strings.HasPrefix(name, "contour_grid") {
				data = nil // grid contours ignore the source
			}
			tfs := c.Get("transforms").Items()
			outs := c.Get("outputs")
			cur := data
			for i, tf := range tfs {
				cur = runTransform(t, tf, cur)
				var key string
				switch tf.Get("type").StrValue() {
				case "contour":
					key = "out"
				case "kde2d":
					key = "kde"
				default:
					key = "iso"
				}
				want, ok := outs.Get(key), outs.ObjValue().Has(key)
				if !ok {
					_ = i
					continue
				}
				if len(cur) != want.Len() {
					t.Fatalf("%s: %d tuples, want %d", key, len(cur), want.Len())
				}
				for j := range cur {
					if err := compare(fmt.Sprintf("%s[%d]", key, j), cur[j], want.Index(j), "grid"); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
