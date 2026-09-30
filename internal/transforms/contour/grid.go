package contour

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// ToValue renders the geometry as the GeoJSON object vega-geo emits, with keys
// in upstream order: type, value, coordinates.
func (g Geometry) ToValue() jsval.Value {
	polys := make([]jsval.Value, len(g.Coordinates))
	for i, poly := range g.Coordinates {
		rings := make([]jsval.Value, len(poly))
		for j, ring := range poly {
			pts := make([]jsval.Value, len(ring))
			for k, p := range ring {
				pts[k] = jsval.ArrOf(jsval.Num(p[0]), jsval.Num(p[1]))
			}
			rings[j] = jsval.Arr(pts)
		}
		polys[i] = jsval.Arr(rings)
	}
	return jsval.Obj(jsval.ObjectOf(
		"type", jsval.Str("MultiPolygon"),
		"value", jsval.Num(g.Value),
		"coordinates", jsval.Arr(polys),
	))
}

// ToValue renders the grid as the object KDE2D stores in its output field:
// {values, scale, width, height, x1, y1, x2, y2}.
func (g Grid) ToValue() jsval.Value {
	vals := make([]jsval.Value, len(g.Values))
	for i, v := range g.Values {
		vals[i] = jsval.Num(v)
	}
	o := jsval.ObjectOf(
		"values", jsval.Arr(vals),
		"scale", jsval.Num(g.Scale),
		"width", jsval.Int(g.Width),
		"height", jsval.Int(g.Height),
		"x1", jsval.Num(g.X1),
		"y1", jsval.Num(g.Y1),
		"x2", jsval.Num(g.X2),
		"y2", jsval.Num(g.Y2),
	)
	if g.Translate != nil {
		o.Set("translate", jsval.Arr(numVals(g.Translate)))
	}
	return jsval.Obj(o)
}

func numVals(f []float64) []jsval.Value {
	out := make([]jsval.Value, len(f))
	for i, v := range f {
		out[i] = jsval.Num(v)
	}
	return out
}

// GridFromValue reads a raster grid object ({values, width, height, scale,
// x1.., translate}). Missing values become an empty grid (upstream treats
// them as zero when rendering).
func GridFromValue(v jsval.Value) (Grid, error) {
	if !v.IsObj() {
		return Grid{}, errors.New("contour: field is not a raster grid")
	}
	w, h := v.Get("width").AsDouble(), v.Get("height").AsDouble()
	if math.IsNaN(w) {
		w = 0
	}
	if math.IsNaN(h) {
		h = 0
	}
	w, h = math.Floor(w), math.Floor(h)
	if !(w >= 0 && h >= 0) {
		return Grid{}, errInvalidSize
	}
	if w*h > MaxGridCells {
		return Grid{}, errTooLarge
	}
	g := Grid{Width: int(w), Height: int(h)}
	if vals := v.Get("values"); vals.IsArr() {
		items := vals.Items()
		g.Values = make([]float64, len(items))
		for i, it := range items {
			g.Values[i] = jsval.ToNumber(it)
		}
	}
	num := func(k string) float64 {
		if f := v.Get(k); !f.IsNullish() {
			if x := jsval.ToNumber(f); !math.IsNaN(x) {
				return x
			}
		}
		return 0
	}
	g.Scale, g.X1, g.Y1, g.X2, g.Y2 = num("scale"), num("x1"), num("y1"), num("x2"), num("y2")
	if sc := v.Get("scale"); sc.IsArr() {
		g.Scale = 0
		g.ScaleXY = []float64{math.NaN(), math.NaN()}
		for i := 0; i < 2 && i < sc.Len(); i++ {
			g.ScaleXY[i] = jsval.ToNumber(sc.Index(i))
		}
	}
	if t := v.Get("translate"); t.IsArr() {
		g.Translate = []float64{jsval.ToNumber(t.Index(0)), jsval.ToNumber(t.Index(1))}
	}
	return g, nil
}
