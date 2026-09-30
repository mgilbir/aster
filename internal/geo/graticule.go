package geo

import (
	"errors"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// maxGraticulePoints bounds the number of lines and points a graticule may
// generate: far beyond any drawable graticule, but finite for hostile steps.
const maxGraticulePoints = 1 << 22

// ErrGraticuleTooLarge is returned when graticule parameters would generate an
// unreasonable number of points.
var ErrGraticuleTooLarge = errors.New("geo: graticule parameters generate too many points")

// Graticule is d3.geoGraticule: a generator of meridians and parallels.
type Graticule struct {
	x0, x1, X0, X1 float64 // minor and major longitude extents
	y0, y1, Y0, Y1 float64 // minor and major latitude extents
	dx, dy         float64 // minor steps
	DX, DY         float64 // major steps
	precision      float64
	budget         int
	err            error
}

// NewGraticule returns a generator with d3's defaults: major extent
// [[-180, -90+eps], [180, 90-eps]], minor extent [[-180, -80-eps],
// [180, 80+eps]], steps 10 (minor) and 90/360 (major), precision 2.5.
func NewGraticule() *Graticule {
	g := &Graticule{dx: 10, dy: 10, DX: 90, DY: 360, precision: 2.5}
	g.SetExtentMajor([2][2]float64{{-180, -90 + epsilon}, {180, 90 - epsilon}})
	g.SetExtentMinor([2][2]float64{{-180, -80 - epsilon}, {180, 80 + epsilon}})
	return g
}

// SetExtent sets both the major and minor extents ([[x0, y0], [x1, y1]]).
func (g *Graticule) SetExtent(e [2][2]float64) {
	g.SetExtentMajor(e)
	g.SetExtentMinor(e)
}

// SetExtentMajor sets the extent of the major lines.
func (g *Graticule) SetExtentMajor(e [2][2]float64) {
	g.X0, g.X1 = e[0][0], e[1][0]
	g.Y0, g.Y1 = e[0][1], e[1][1]
	if g.X0 > g.X1 {
		g.X0, g.X1 = g.X1, g.X0
	}
	if g.Y0 > g.Y1 {
		g.Y0, g.Y1 = g.Y1, g.Y0
	}
}

// SetExtentMinor sets the extent of the minor lines.
func (g *Graticule) SetExtentMinor(e [2][2]float64) {
	g.x0, g.x1 = e[0][0], e[1][0]
	g.y0, g.y1 = e[0][1], e[1][1]
	if g.x0 > g.x1 {
		g.x0, g.x1 = g.x1, g.x0
	}
	if g.y0 > g.y1 {
		g.y0, g.y1 = g.y1, g.y0
	}
}

// SetStep sets both the major and minor steps.
func (g *Graticule) SetStep(s [2]float64) {
	g.SetStepMajor(s)
	g.SetStepMinor(s)
}

// SetStepMajor sets the [longitude, latitude] spacing of the major lines.
func (g *Graticule) SetStepMajor(s [2]float64) { g.DX, g.DY = s[0], s[1] }

// SetStepMinor sets the [longitude, latitude] spacing of the minor lines.
func (g *Graticule) SetStepMinor(s [2]float64) { g.dx, g.dy = s[0], s[1] }

// SetPrecision sets the angular spacing of points along each line, in degrees.
func (g *Graticule) SetPrecision(p float64) { g.precision = p }

// rangeOf is d3.range(start, stop, step): start + i*step for the i where it is
// below stop. It reports false when that would exceed the point budget.
func (g *Graticule) rangeOf(start, stop, step float64) ([]float64, bool) {
	n := math.Max(0, math.Ceil((stop-start)/step))
	if math.IsNaN(n) || math.IsInf(n, 0) {
		n = 0 // `| 0` in JavaScript
	}
	if n > float64(maxGraticulePoints) || g.budget+int(n) > maxGraticulePoints {
		g.err = ErrGraticuleTooLarge
		return nil, false
	}
	g.budget += int(n)
	out := make([]float64, int(n))
	for i := range out {
		out[i] = start + float64(float64(i)*step)
	}
	return out, true
}

type ll = [2]float64

// meridian is graticuleX: the points of the line of constant longitude x,
// sampled every 90 degrees of latitude between y0 and y1.
func (g *Graticule) meridian(y0, y1 float64, x float64) []ll {
	ys, ok := g.rangeOf(y0, y1-epsilon, 90)
	if !ok {
		return nil
	}
	ys = append(ys, y1)
	out := make([]ll, len(ys))
	for i, y := range ys {
		out[i] = ll{x, y}
	}
	return out
}

// parallel is graticuleY: the line of constant latitude y, sampled every
// `precision` degrees of longitude between x0 and x1.
func (g *Graticule) parallel(x0, x1 float64, y float64) []ll {
	xs, ok := g.rangeOf(x0, x1-epsilon, g.precision)
	if !ok {
		return nil
	}
	xs = append(xs, x1)
	out := make([]ll, len(xs))
	for i, x := range xs {
		out[i] = ll{x, y}
	}
	return out
}

func (g *Graticule) lines() ([][]ll, error) {
	g.err, g.budget = nil, 0
	var out [][]ll
	if v, ok := g.rangeOf(math.Ceil(g.X0/g.DX)*g.DX, g.X1, g.DX); ok {
		for _, x := range v {
			out = append(out, g.meridian(g.Y0, g.Y1, x))
		}
	}
	if v, ok := g.rangeOf(math.Ceil(g.Y0/g.DY)*g.DY, g.Y1, g.DY); ok {
		for _, y := range v {
			out = append(out, g.parallel(g.X0, g.X1, y))
		}
	}
	if v, ok := g.rangeOf(math.Ceil(g.x0/g.dx)*g.dx, g.x1, g.dx); ok {
		for _, x := range v {
			if abs(jsMod(x, g.DX)) > epsilon {
				out = append(out, g.meridian(g.y0, g.y1, x))
			}
		}
	}
	if v, ok := g.rangeOf(math.Ceil(g.y0/g.dy)*g.dy, g.y1, g.dy); ok {
		for _, y := range v {
			if abs(jsMod(y, g.DY)) > epsilon {
				out = append(out, g.parallel(g.x0, g.x1, y))
			}
		}
	}
	if g.err != nil {
		return nil, g.err
	}
	return out, nil
}

func lineValue(pts []ll) jsval.Value {
	items := make([]jsval.Value, len(pts))
	for i, p := range pts {
		items[i] = jsval.ArrOf(jsval.Num(p[0]), jsval.Num(p[1]))
	}
	return jsval.Arr(items)
}

// Lines returns the graticule as a GeoJSON MultiLineString.
func (g *Graticule) Lines() (jsval.Value, error) {
	lines, err := g.lines()
	if err != nil {
		return jsval.Undefined, err
	}
	items := make([]jsval.Value, len(lines))
	for i, l := range lines {
		items[i] = lineValue(l)
	}
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiLineString"), "coordinates", jsval.Arr(items))), nil
}

// LineStrings returns each line of the graticule as a GeoJSON LineString.
func (g *Graticule) LineStrings() ([]jsval.Value, error) {
	lines, err := g.lines()
	if err != nil {
		return nil, err
	}
	out := make([]jsval.Value, len(lines))
	for i, l := range lines {
		out[i] = jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "coordinates", lineValue(l)))
	}
	return out, nil
}

// Outline returns the boundary of the major extent as a GeoJSON Polygon.
func (g *Graticule) Outline() (jsval.Value, error) {
	g.err, g.budget = nil, 0
	a := g.meridian(g.Y0, g.Y1, g.X0)
	b := g.parallel(g.X0, g.X1, g.Y1)
	c := g.meridian(g.Y0, g.Y1, g.X1)
	d := g.parallel(g.X0, g.X1, g.Y0)
	if g.err != nil {
		return jsval.Undefined, g.err
	}
	ring := append([]ll(nil), a...)
	ring = append(ring, b[min(1, len(b)):]...)
	for i := len(c) - 2; i >= 0; i-- {
		ring = append(ring, c[i])
	}
	for i := len(d) - 2; i >= 0; i-- {
		ring = append(ring, d[i])
	}
	return jsval.Obj(jsval.ObjectOf("type", jsval.Str("Polygon"), "coordinates", jsval.ArrOf(lineValue(ring)))), nil
}

// Graticule10 is d3.geoGraticule10(): the default 10-degree graticule.
func Graticule10() jsval.Value {
	v, _ := NewGraticule().Lines()
	return v
}
