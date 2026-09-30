package geo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

type pipelineCase struct {
	Case struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Dataset    string `json:"dataset"`
		Projection *struct {
			Type   string          `json:"type"`
			Props  json.RawMessage `json:"props"`
			Fit    string          `json:"fit"`
			Size   json.RawMessage `json:"size"`
			Extent json.RawMessage `json:"extent"`
		} `json:"projection"`
		Params json.RawMessage `json:"params"`
	} `json:"case"`
	Result struct {
		Paths []json.RawMessage `json:"paths"`
		XY    [][]any           `json:"xy"`
		Value json.RawMessage   `json:"value"`
	} `json:"result"`
	Error string `json:"error"`
}

func TestVegaPipeline(t *testing.T) {
	var g struct {
		Datasets map[string]json.RawMessage `json:"datasets"`
		Cases    []pipelineCase             `json:"cases"`
	}
	loadGolden(t, "pipeline.json.gz", &g)
	ctx := context.Background()
	dataset := func(name string) []jsval.Value {
		return parseJSONValue(t, g.Datasets[name]).Items()
	}
	for _, c := range g.Cases {
		if c.Error != "" {
			continue
		}
		t.Run(c.Case.Name, func(t *testing.T) {
			var params jsval.Value
			if len(c.Case.Params) > 0 {
				params = parseJSONValue(t, c.Case.Params)
			}
			var proj Projection
			if pj := c.Case.Projection; pj != nil {
				o := jsval.NewObject(8)
				o.Set("type", jsval.Str(pj.Type))
				if pv := parseJSONValue(t, pj.Props); pv.IsObj() {
					for i := 0; i < pv.ObjValue().Len(); i++ {
						o.Set(pv.ObjValue().KeyAt(i), pv.ObjValue().ValueAt(i))
					}
				}
				if pj.Fit != "" {
					o.Set("fit", jsval.Arr(dataset(pj.Fit)))
				}
				if len(pj.Size) > 0 {
					o.Set("size", parseJSONValue(t, pj.Size))
				}
				if len(pj.Extent) > 0 {
					o.Set("extent", parseJSONValue(t, pj.Extent))
				}
				var err error
				if proj, err = ConfigureProjection(nil, o, nil); err != nil {
					t.Fatal(err)
				}
			}
			switch c.Case.Kind {
			case "geopoint":
				fields := params.Get("fields")
				as := [2]string{}
				if a := params.Get("as"); a.IsArr() {
					as = [2]string{a.Index(0).StrValue(), a.Index(1).StrValue()}
				}
				tuples := dataset(c.Case.Dataset)
				err := GeoPoint(ctx, tuples, GeoPointParams{
					Projection: proj,
					Lon:        FieldAccessor(fields.Index(0).StrValue()),
					Lat:        FieldAccessor(fields.Index(1).StrValue()),
					As:         as,
				})
				if err != nil {
					t.Fatal(err)
				}
				xf, yf := "x", "y"
				if as[0] != "" {
					xf, yf = as[0], as[1]
				}
				for i, tup := range tuples {
					for j, f := range []string{xf, yf} {
						got := tup.Get(f)
						want := c.Result.XY[i][j]
						if s, ok := want.(string); ok && s == "undefined" {
							if !got.IsUndefined() {
								t.Errorf("row %d %s = %v, want undefined", i, f, got)
							}
							continue
						}
						if !same(got.NumValue(), gnum(want)) {
							t.Errorf("row %d %s = %v, want %v", i, f, got.NumValue(), want)
						}
					}
				}
			case "geopath":
				tuples := dataset(c.Case.Dataset)
				gp := GeoPathParams{Projection: proj, As: params.Get("as").StrValue()}
				if r := params.Get("pointRadius"); !r.IsUndefined() {
					gp.PointRadius = ConstRadius(r.NumValue())
				}
				if params.Get("pointRadiusExpr").IsStr() {
					gp.PointRadius = PointRadius{Set: true, Fn: func(o jsval.Value) float64 { return jsval.ToNumber(o.Get("id")) + 1 }}
				}
				if err := GeoPath(ctx, tuples, gp); err != nil {
					t.Fatal(err)
				}
				as := "path"
				if gp.As != "" {
					as = gp.As
				}
				for i, tup := range tuples {
					v := tup.Get(as)
					s, ok := v.StrValue(), v.IsStr()
					checkPath(t, c.Case.Name+" row "+jsval.JSNumberString(float64(i)), s, ok, c.Result.Paths[i])
				}
			case "geojson":
				p := GeoJSONParams{}
				if f := params.Get("fields"); f.IsArr() {
					p.Lon, p.Lat = FieldAccessor(f.Index(0).StrValue()), FieldAccessor(f.Index(1).StrValue())
				}
				if gj := params.Get("geojson"); gj.IsStr() {
					p.GeoJSON = FieldAccessor(gj.StrValue())
				}
				got, err := GeoJSON(ctx, dataset(c.Case.Dataset), p)
				if err != nil {
					t.Fatal(err)
				}
				// Compared as JSON: an absent geojson field is undefined here and
				// null in the recorded JSON.
				if want := parseJSONValue(t, c.Result.Value); string(jsval.AppendJSON(nil, got)) != string(jsval.AppendJSON(nil, want)) {
					t.Errorf("geojson:\n got  %s\n want %s", trunc(got.String()), trunc(want.String()))
				}
			case "graticule":
				var gp GraticuleParams
				po := params.ObjValue()
				for i := 0; po != nil && i < po.Len(); i++ {
					v := po.ValueAt(i)
					switch po.KeyAt(i) {
					case "extent", "extentMajor", "extentMinor":
						b := boxAt(v)
						switch po.KeyAt(i) {
						case "extent":
							gp.Extent = &b
						case "extentMajor":
							gp.ExtentMajor = &b
						default:
							gp.ExtentMinor = &b
						}
					case "step", "stepMajor", "stepMinor":
						s := pairAt(v)
						switch po.KeyAt(i) {
						case "step":
							gp.Step = &s
						case "stepMajor":
							gp.StepMajor = &s
						default:
							gp.StepMinor = &s
						}
					case "precision":
						f := v.NumValue()
						gp.Precision = &f
					}
				}
				got, err := GraticuleFeature(gp)
				if err != nil {
					t.Fatal(err)
				}
				want := parseJSONValue(t, c.Result.Value)
				if !jsval.Equal(got.Get("coordinates"), want.Get("coordinates")) {
					t.Errorf("graticule differs: got %s", trunc(got.String()))
				}
			}
		})
	}
}
