package geo

import (
	"encoding/json"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

type goldenGraticule struct {
	Cfg     map[string]json.RawMessage `json:"cfg"`
	Lines   json.RawMessage            `json:"lines"`
	Outline json.RawMessage            `json:"outline"`
}

func pairAt(v jsval.Value) [2]float64 { return [2]float64{coord(v.Index(0)), coord(v.Index(1))} }

func boxAt(v jsval.Value) [2][2]float64 { return [2][2]float64{pairAt(v.Index(0)), pairAt(v.Index(1))} }

func TestGraticule(t *testing.T) {
	var g []goldenGraticule
	loadGolden(t, "graticule.json.gz", &g)
	for _, c := range g {
		p := GraticuleParams{}
		for k, raw := range c.Cfg {
			v := parseJSONValue(t, raw)
			switch k {
			case "extent", "extentMajor", "extentMinor":
				b := boxAt(v)
				switch k {
				case "extent":
					p.Extent = &b
				case "extentMajor":
					p.ExtentMajor = &b
				default:
					p.ExtentMinor = &b
				}
			case "step", "stepMajor", "stepMinor":
				s := pairAt(v)
				switch k {
				case "step":
					p.Step = &s
				case "stepMajor":
					p.StepMajor = &s
				default:
					p.StepMinor = &s
				}
			case "precision":
				f := v.NumValue()
				p.Precision = &f
			}
		}
		got, err := GraticuleFeature(p)
		want := parseJSONValue(t, c.Lines)
		if err != nil {
			t.Errorf("%s: %v", c.Cfg, err)
			continue
		}
		if !jsval.Equal(got, want) {
			t.Errorf("graticule %v:\n got  %s\n want %s", c.Cfg, trunc(got.String()), trunc(want.String()))
		}
		// The outline depends on the major extent and precision only.
		gen := NewGraticule()
		if p.Extent != nil {
			gen.SetExtent(*p.Extent)
		}
		if p.ExtentMajor != nil {
			gen.SetExtentMajor(*p.ExtentMajor)
		}
		if p.ExtentMinor != nil {
			gen.SetExtentMinor(*p.ExtentMinor)
		}
		if p.Precision != nil {
			gen.SetPrecision(*p.Precision)
		}
		out, err := gen.Outline()
		if err != nil {
			t.Errorf("%s: %v", c.Cfg, err)
			continue
		}
		if wantOut := parseJSONValue(t, c.Outline); !jsval.Equal(out, wantOut) {
			t.Errorf("outline %v:\n got  %s\n want %s", c.Cfg, trunc(out.String()), trunc(wantOut.String()))
		}
	}
}

func TestGraticuleBounded(t *testing.T) {
	huge := [2]float64{1e-9, 1e-9}
	if _, err := GraticuleFeature(GraticuleParams{Step: &huge}); err == nil {
		t.Error("expected an error for a step that would generate billions of lines")
	}
	p := 1e-9
	if _, err := GraticuleFeature(GraticuleParams{Precision: &p}); err == nil {
		t.Error("expected an error for a tiny precision")
	}
	zero := [2]float64{0, 0}
	if v, err := GraticuleFeature(GraticuleParams{Step: &zero}); err != nil || v.IsUndefined() {
		t.Errorf("zero step: %v %v", v, err)
	}
}
