package contour

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/mgilbir/aster/internal/budget"

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
	value := jsval.Num(g.Value)
	if g.HasRaw {
		value = g.Raw
	}
	return jsval.Obj(jsval.ObjectOf(
		"type", jsval.Str("MultiPolygon"),
		"value", value,
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

// releaseGrid gives back to the ledger in ctx the values of a grid made by
// Density or GridFromValue, once the grid is dropped.
func releaseGrid(ctx context.Context, g Grid) {
	budget.LedgerFrom(ctx).Free(gridValueBytes * int64(len(g.Values)))
}

// GridFromValue reads a raster grid object ({values, width, height, scale,
// x1.., translate}). Missing values become an empty grid (upstream treats
// them as zero when rendering). The values are charged to the ledger in
// ctx; releaseGrid gives them back.
func GridFromValue(ctx context.Context, v jsval.Value) (Grid, error) {
	if !v.IsObj() {
		return Grid{}, errors.New("contour: field is not a raster grid")
	}
	// contour.size([grid.width, grid.height]): Math.floor of each, which
	// must be at least 0 (an undefined one is NaN: 'invalid size').
	w, h := math.Floor(jsval.ToNumber(v.Get("width"))), math.Floor(jsval.ToNumber(v.Get("height")))
	if !(w >= 0 && h >= 0) {
		return Grid{}, errInvalidSize
	}
	// Each side too, before it is an int: 0 x Infinity is NaN.
	if w*h > MaxGridCells || w > MaxGridCells || h > MaxGridCells {
		return Grid{}, errTooLarge
	}
	g := Grid{Width: int(w), Height: int(h)}
	if vals := v.Get("values"); vals.IsArr() {
		items := vals.Items()
		if err := budget.LedgerFrom(ctx).Alloc(gridValueBytes * int64(len(items))); err != nil {
			return Grid{}, err
		}
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
	if vv := v.Get("values"); vv.IsNullish() {
		g.ValuesNullish = "undefined"
		if vv.IsNull() {
			g.ValuesNullish = "null"
		}
	}
	g.Scale, g.X1, g.Y1, g.X2, g.Y2 = num("scale"), num("x1"), num("y1"), num("x2"), num("y2")
	if sc := v.Get("scale"); sc.IsArr() {
		g.Scale = 0
		g.ScaleXY = []float64{math.NaN(), math.NaN()}
		for i := 0; i < 2 && i < sc.Len(); i++ {
			g.ScaleXY[i] = jsval.ToNumber(sc.Index(i))
		}
	}
	if t := v.Get("translate"); t.IsTruthy() && !t.IsNum() && !t.IsBool() {
		// `t && t[0] || 0`: the elements of an array, a string's characters or
		// an object's members 0 and 1.
		g.Translate = make([]float64, 2)
		for i := range g.Translate {
			e := jsval.Member(t, strconv.Itoa(i))
			g.Translate[i] = jsval.ToNumber(e)
			// t[0] || 0: a string is truthy unless empty, an array or an
			// object always; either joins the sum as its text
			if e.IsStr() && e.StrValue() != "" || e.IsArr() || e.IsObj() {
				g.TranslateText[i], g.TranslateIsText[i] = jsval.WalkerFor(ctx).String(e), true
			}
		}
	}
	return g, nil
}
