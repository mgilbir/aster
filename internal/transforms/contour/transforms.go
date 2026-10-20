package contour

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// ContourParams are the parameters of the vega-geo Contour transform.
type ContourParams struct {
	// Size is [width, height]: the dimensions of Values when given, else the
	// output view size in pixels for density estimation.
	Size []float64
	// Values is a row-major grid to contour. nil means "estimate the density
	// of the input tuples"; an empty non-nil slice is a (degenerate) grid.
	Values []float64
	// ValuesExtent, when not nil, are the given values as the thresholds'
	// extent reads them (vega-util's extent skips null: NaN here), where
	// Values are them as the marching compares them (null is 0).
	ValuesExtent []float64
	// ThresholdValues: see IsocontourParams.
	ThresholdValues []jsval.Value
	// Density estimation inputs, see DensityParams.
	X, Y, Weight Accessor
	CellSize     float64
	Bandwidth    []float64
	// Thresholds, when non-nil (even if empty), overrides Count and Nice.
	Thresholds []float64
	// Count is the desired number of contours; zero means 10 (NaN is NaN:
	// the caller applies `_.count || 10`).
	Count float64
	// CountExact reports a Count the caller resolved (`_.count || 10` of a
	// truthy value that is 0 as a number stays 0), and CountPlusOne its
	// `count + 1` as JavaScript adds it.
	CountExact   bool
	CountPlusOne float64
	// ExtentSet: ExtentLo and ExtentHi are the numbers of vega-util's extent of
	// the raw values (the first valid one starts both; later ones compare with
	// JavaScript's < and >), NaN for none.
	ExtentSet          bool
	ExtentLo, ExtentHi float64
	Nice               bool
	Smooth             *bool // nil means true
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// Contour generates contour MultiPolygons for a value grid or, if p.Values is
// nil, for the kernel density estimate of source. Each output tuple is the
// GeoJSON geometry object itself ({type, value, coordinates}); density-based
// contours are returned in view pixels (the raster padding and cell size are
// undone), as upstream does.
func Contour(ctx context.Context, source []jsval.Value, p ContourParams) ([]jsval.Value, error) {
	if len(p.Size) != 2 {
		return nil, errors.New("contour: size must have two entries")
	}
	values := p.Values
	w, h := math.Floor(p.Size[0]), math.Floor(p.Size[1])
	var grid Grid
	density := values == nil
	if density {
		var err error
		grid, err = Density(ctx, source, DensityParams{
			X: p.X, Y: p.Y, Weight: p.Weight, Size: p.Size,
			CellSize: p.CellSize, Bandwidth: p.Bandwidth,
		}, true)
		if err != nil {
			return nil, err
		}
		values = grid.Values
		w, h = float64(grid.Width), float64(grid.Height)
	}
	if !(w >= 0 && h >= 0) {
		return nil, errInvalidSize
	}
	// Each side too, before it is an int: 0 x Infinity is NaN.
	if w*h > MaxGridCells || w > MaxGridCells || h > MaxGridCells {
		return nil, errTooLarge
	}

	thresholds := p.Thresholds
	if thresholds == nil {
		count := p.Count
		if count == 0 && !p.CountExact {
			count = 10
		}
		ext := values
		if p.ValuesExtent != nil && !density {
			ext = p.ValuesExtent
		}
		var err error
		k1 := count + 1
		if p.CountExact {
			k1 = p.CountPlusOne
		}
		if p.ExtentSet && !density {
			// the extent of the raw values, as given (ExtentSet)
			thresholds, err = quantizeAt(count, k1, p.Nice, true, p.ExtentLo, p.ExtentHi)
		} else {
			thresholds, err = quantizeK1(count, k1, p.Nice, !density)(ext)
		}
		if err != nil {
			return nil, err
		}
	}
	geoms, err := Contours(ctx, values, int(w), int(h), thresholds, boolOr(p.Smooth, true))
	if err == nil && p.ThresholdValues != nil {
		for k := range geoms {
			if k < len(p.ThresholdValues) {
				geoms[k].Raw, geoms[k].HasRaw = p.ThresholdValues[k], true
			}
		}
	}
	if density {
		releaseGrid(ctx, grid)
	}
	if err != nil {
		return nil, err
	}
	if density {
		s := grid.Scale
		if s == 0 {
			s = 1
		}
		transformGeometries(geoms, grid, s, s, 0, 0)
	}
	led := budget.LedgerFrom(ctx)
	vertices, err := chargeValues(led, geoms)
	if err != nil {
		return nil, err
	}
	out := make([]jsval.Value, len(geoms))
	for i, g := range geoms {
		out[i] = g.ToValue()
	}
	led.Free(vertices * vertexBytes)
	return out, nil
}

// chargeValues charges the ledger for the jsval form of geoms before it is
// built (it is output, kept as long as the dataflow), and returns the number
// of vertices, whose rings the caller gives back once they are converted.
func chargeValues(led *budget.Ledger, geoms []Geometry) (int64, error) {
	var n int64
	for _, g := range geoms {
		for _, poly := range g.Coordinates {
			for _, ring := range poly {
				n += int64(len(ring))
			}
		}
	}
	return n, led.Alloc(n * pointValueBytes)
}

// IsocontourParams are the parameters of the vega-geo Isocontour transform.
type IsocontourParams struct {
	// Field reads the raster grid from a tuple; nil means the tuple itself.
	Field Accessor
	// Thresholds, when non-nil, overrides Levels/Nice/Resolve/Zero.
	Thresholds []float64
	Levels     float64 // zero means 10
	// LevelsExact reports Levels the caller resolved, LevelsPlusOne its
	// `levels + 1` (see ContourParams.CountExact).
	LevelsExact   bool
	LevelsPlusOne float64
	Nice          bool
	Shared        bool  // resolve: "shared" instead of "independent"
	Zero          *bool // nil means true
	Smooth        *bool // nil means true
	// Scale and Translate post-transform the coordinates. One element is a
	// uniform scale; two are [sx, sy]. ScaleFn/TranslateFn compute them per
	// tuple and take precedence when set. Unset scale falls back to the
	// grid's own scale (so KDE grids are mapped back to pixels).
	Scale, Translate     []float64
	ScaleFn, TranslateFn func(datum jsval.Value) []float64
	As                   string // output field; "" means "contour"
	NoAs                 bool   // as: null, so the geometry is the tuple
	// ThresholdValues, when not nil, are the thresholds as given, which
	// the contours carry as their value (Thresholds are them as numbers,
	// as the marching compares them). ThresholdsNotArray reports truthy
	// thresholds that are no array: upstream calls them on each grid, a
	// TypeError.
	ThresholdValues    []jsval.Value
	ThresholdsNotArray bool
	// Transform, when set, replaces Scale, Translate and their functions:
	// the scale and translation of a tuple's contours, given the tuple
	// and its grid as values (apply false: none), or an error.
	Transform func(datum, grid jsval.Value) (sx, sy, tx, ty float64, apply bool, err error)
}

// Isocontour computes level sets for the raster grid of every source tuple.
// Each output tuple is a copy of its source tuple's fields plus the contour
// geometry under As (copied fields win over a same-named As, as upstream's
// rederive overwrites).
func Isocontour(ctx context.Context, source []jsval.Value, p IsocontourParams) ([]jsval.Value, error) {
	field := p.Field
	if field == nil {
		field = func(v jsval.Value) jsval.Value { return v }
	}
	grids := make([]Grid, len(source))
	defer func() {
		for _, g := range grids {
			releaseGrid(ctx, g)
		}
	}()
	for i, t := range source {
		g, err := GridFromValue(ctx, field(t))
		if err != nil {
			return nil, err
		}
		grids[i] = g
	}
	smooth := boolOr(p.Smooth, true)
	led := budget.LedgerFrom(ctx)

	var shared []float64
	quant := func([]float64) ([]float64, error) { return nil, nil }
	if p.Thresholds != nil {
		shared = p.Thresholds
	} else {
		levels := p.Levels
		if levels == 0 && !p.LevelsExact {
			levels = 10
		}
		k1 := levels + 1
		if p.LevelsExact {
			k1 = p.LevelsPlusOne
		}
		quant = quantizeK1(levels, k1, p.Nice, boolOr(p.Zero, true))
		if p.Shared {
			maxes := make([]float64, len(grids))
			for i, g := range grids {
				_, maxes[i] = extent(g.Values)
			}
			var err error
			if shared, err = quant(maxes); err != nil {
				return nil, err
			}
		}
	}
	as := p.As
	if as == "" {
		as = "contour"
	}

	var out []jsval.Value
	for i, t := range source {
		g := grids[i]
		if p.ThresholdsNotArray {
			return nil, &jsval.Thrown{Name: "TypeError", Msg: "tz is not a function"}
		}
		tz := shared
		if p.Thresholds == nil && !p.Shared {
			var err error
			if tz, err = quant(g.Values); err != nil {
				return nil, err
			}
		}
		if g.ValuesNullish != "" && len(tz) > 0 {
			// contour(grid.values, tz) maps tz over isorings(values, ...),
			// which reads values[0] of it.
			return nil, &jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + g.ValuesNullish + " (reading '0')"}
		}
		geoms, err := Contours(ctx, g.Values, g.Width, g.Height, tz, smooth)
		if err != nil {
			return nil, err
		}
		if p.ThresholdValues != nil {
			for k := range geoms {
				if k < len(p.ThresholdValues) {
					geoms[k].Raw, geoms[k].HasRaw = p.ThresholdValues[k], true
				}
			}
		}
		if p.Transform != nil {
			sx, sy, tx, ty, apply, err := p.Transform(t, field(t))
			if err != nil {
				return nil, err
			}
			if apply {
				transformGeometries(geoms, g, sx, sy, tx, ty)
			}
		} else {
			p.transformPaths(geoms, g, t)
		}
		vertices, err := chargeValues(led, geoms)
		if err != nil {
			return nil, err
		}
		for _, geom := range geoms {
			var o *jsval.Object
			if p.NoAs {
				o = geom.ToValue().ObjValue()
			} else {
				o = jsval.ObjectOf(as, geom.ToValue())
			}
			if to := jsval.Fields(t); to != nil {
				for k := 0; k < to.Len(); k++ {
					o.Set(to.KeyAt(k), to.ValueAt(k))
				}
			}
			out = append(out, jsval.Obj(o))
		}
		led.Free(vertices * vertexBytes)
	}
	return out, nil
}

// transformPaths rescales contours to the requested output space:
// scale/translate parameters override the grid's own scale, and nothing is done
// when the scale is 1 or unset and there is no translation.
func (p *IsocontourParams) transformPaths(geoms []Geometry, g Grid, datum jsval.Value) {
	var s, t []float64
	if p.ScaleFn != nil {
		s = p.ScaleFn(datum)
	} else if len(p.Scale) > 0 && !(len(p.Scale) == 1 && p.Scale[0] == 0) {
		s = p.Scale
	} else if g.ScaleXY != nil {
		s = g.ScaleXY
	} else if g.Scale != 0 {
		s = []float64{g.Scale}
	}
	if p.TranslateFn != nil {
		t = p.TranslateFn(datum)
	} else if p.Translate != nil {
		t = p.Translate
		g.TranslateIsText = [2]bool{} // parameters are numbers here
	} else {
		t = g.Translate
	}
	if p.TranslateFn != nil {
		g.TranslateIsText = [2]bool{}
	}
	if (len(s) == 0 || (len(s) == 1 && s[0] == 1)) && t == nil {
		return
	}
	sx, sy := 1.0, 1.0
	if len(s) > 0 {
		if sx = s[0]; sx == 0 || math.IsNaN(sx) {
			sx = 1
		}
		if len(s) == 1 {
			sy = sx
		} else if sy = s[1]; sy == 0 || math.IsNaN(sy) {
			sy = 1
		}
	}
	tx, ty := 0.0, 0.0
	if len(t) > 0 {
		tx = t[0]
	}
	if len(t) > 1 {
		ty = t[1]
	}
	if math.IsNaN(tx) {
		tx = 0
	}
	if math.IsNaN(ty) {
		ty = 0
	}
	transformGeometries(geoms, g, sx, sy, tx, ty)
}

// transformGeometries maps raster coordinates to output space in place. A
// negative-area scale reverses each ring to keep the winding order.
func transformGeometries(geoms []Geometry, g Grid, sx, sy, tx, ty float64) {
	flip := sx*sy < 0
	for _, geom := range geoms {
		for _, poly := range geom.Coordinates {
			for _, ring := range poly {
				if flip {
					for i, j := 0, len(ring)-1; i < j; i, j = i+1, j-1 {
						ring[i], ring[j] = ring[j], ring[i]
					}
				}
				for k := range ring {
					x, y := float64((ring[k][0]-g.X1)*sx), float64((ring[k][1]-g.Y1)*sy)
					if g.TranslateIsText[0] {
						ring[k][0] = jsval.StringToNumber(jsval.JSNumberString(x) + g.TranslateText[0])
					} else {
						ring[k][0] = x + tx
					}
					if g.TranslateIsText[1] {
						ring[k][1] = jsval.StringToNumber(jsval.JSNumberString(y) + g.TranslateText[1])
					} else {
						ring[k][1] = y + ty
					}
				}
			}
		}
	}
}

// GroupField names a groupby field and reads it from a tuple.
type GroupField struct {
	Name string
	Get  Accessor
}

// KDE2DParams are the parameters of the vega-geo KDE2D transform.
type KDE2DParams struct {
	DensityParams
	// GroupBy partitions the input; nil means a single group (even when the
	// input is empty), an empty non-nil slice partitions by nothing (no groups
	// for empty input).
	GroupBy []GroupField
	Counts  bool
	As      string // output field for the grid; "" means "grid"
	// KeyString is String(v), which keys the groups (upstream keys an object
	// by the array of group values, which String joins): a date writes to
	// the second in the view's zone. nil means jsval's AsString.
	KeyString func(jsval.Value) string
}

// KDE2D estimates one density raster per group. Each output tuple holds the
// grid under As and the group's field values.
func KDE2D(ctx context.Context, source []jsval.Value, p KDE2DParams) ([]jsval.Value, error) {
	type group struct {
		dims []jsval.Value
		data []jsval.Value
	}
	var groups []*group
	if p.GroupBy == nil {
		groups = []*group{{data: source}}
	} else {
		str := p.KeyString
		if str == nil {
			str = jsval.Value.AsString
		}
		index := map[string]*group{}
		var sb strings.Builder
		for _, t := range source {
			dims := make([]jsval.Value, len(p.GroupBy))
			sb.Reset()
			for i, f := range p.GroupBy {
				dims[i] = f.Get(t)
				if i > 0 {
					sb.WriteByte(',')
				}
				if !dims[i].IsNullish() {
					sb.WriteString(str(dims[i]))
				}
			}
			key := sb.String()
			g := index[key]
			if g == nil {
				g = &group{dims: dims}
				index[key] = g
				groups = append(groups, g)
			}
			g.data = append(g.data, t)
		}
	}
	as := p.As
	if as == "" {
		as = "grid"
	}
	led := budget.LedgerFrom(ctx)
	out := make([]jsval.Value, 0, len(groups))
	for _, g := range groups {
		// Each group's grid is charged before it is made, and its jsval
		// form, which the output keeps, before that is built.
		grid, err := Density(ctx, g.data, p.DensityParams, p.Counts)
		if err != nil {
			return nil, err
		}
		if err := led.Alloc(jsValueBytes * int64(len(grid.Values))); err != nil {
			releaseGrid(ctx, grid)
			return nil, err
		}
		o := jsval.ObjectOf(as, grid.ToValue())
		releaseGrid(ctx, grid)
		for i, f := range p.GroupBy {
			o.Set(f.Name, g.dims[i])
		}
		out = append(out, jsval.Obj(o))
	}
	return out, nil
}
