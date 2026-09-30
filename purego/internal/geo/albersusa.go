package geo

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// albersUSA is d3.geoAlbersUsa: a composite of three Albers conic equal-area
// projections (lower 48, Alaska, Hawaii) with mutually exclusive clip regions,
// laid out for a 960x500 canvas.
type albersUSA struct {
	lower48, alaska, hawaii *standard
	// point streams into a capturing sink, used to project single points with
	// each region's clipping applied.
	lower48Point, alaskaPoint, hawaiiPoint Stream
	captured                               pointCapture
	path                                   *Path
}

type pointCapture struct {
	x, y float64
	ok   bool
}

func (c *pointCapture) Point(x, y float64) { c.x, c.y, c.ok = x, y, true }
func (c *pointCapture) LineStart()         {}
func (c *pointCapture) LineEnd()           {}
func (c *pointCapture) PolygonStart()      {}
func (c *pointCapture) PolygonEnd()        {}
func (c *pointCapture) Sphere()            {}

func newAlbersUSA() *albersUSA {
	a := &albersUSA{lower48: newAlbers()}
	a.alaska = newConicEqualArea()
	a.alaska.SetRotate([]float64{154, 0})
	a.alaska.SetCenter(-2, 58.5)
	a.alaska.SetParallels(55, 65) // EPSG:3338
	a.hawaii = newConicEqualArea()
	a.hawaii.SetRotate([]float64{157, 0})
	a.hawaii.SetCenter(-3, 19.9)
	a.hawaii.SetParallels(8, 18) // ESRI:102007
	a.SetScale(1070)
	return a
}

func (a *albersUSA) Type() string { return albersUSAName }

const albersUSAName = "albersusa"

func (a *albersUSA) Path() *Path {
	if a.path == nil {
		a.path = NewPath(a)
	}
	return a.path
}

// Forward tries the lower 48 first, then Alaska, then Hawaii; each region's
// clip extent decides whether the point belongs to it.
func (a *albersUSA) Forward(lon, lat float64) (float64, float64, bool) {
	for _, s := range [...]Stream{a.lower48Point, a.alaskaPoint, a.hawaiiPoint} {
		a.captured.ok = false
		s.Point(lon, lat)
		if a.captured.ok {
			return a.captured.x, a.captured.y, true
		}
	}
	return 0, 0, false
}

// Invert picks the region by where the position falls relative to the
// lower-48 translate and scale.
func (a *albersUSA) Invert(x, y float64) (float64, float64, bool) {
	k := a.lower48.Scale()
	tx, ty := a.lower48.Translate()
	px := (x - tx) / k
	py := (y - ty) / k
	p := a.lower48
	switch {
	case py >= 0.120 && py < 0.234 && px >= -0.425 && px < -0.214:
		p = a.alaska
	case py >= 0.166 && py < 0.234 && px >= -0.214 && px < -0.115:
		p = a.hawaii
	}
	return p.Invert(x, y)
}

type multiplex [3]Stream

func (m *multiplex) Point(x, y float64) {
	for _, s := range m {
		s.Point(x, y)
	}
}
func (m *multiplex) LineStart() {
	for _, s := range m {
		s.LineStart()
	}
}
func (m *multiplex) LineEnd() {
	for _, s := range m {
		s.LineEnd()
	}
}
func (m *multiplex) PolygonStart() {
	for _, s := range m {
		s.PolygonStart()
	}
}
func (m *multiplex) PolygonEnd() {
	for _, s := range m {
		s.PolygonEnd()
	}
}
func (m *multiplex) Sphere() {
	for _, s := range m {
		s.Sphere()
	}
}

func (a *albersUSA) Stream(sink Stream) Stream {
	return &multiplex{a.lower48.Stream(sink), a.alaska.Stream(sink), a.hawaii.Stream(sink)}
}

func (a *albersUSA) dropStreamCache() {
	a.lower48.dropStreamCache()
	a.alaska.dropStreamCache()
	a.hawaii.dropStreamCache()
}

func (a *albersUSA) Precision() float64 { return a.lower48.Precision() }

func (a *albersUSA) SetPrecision(v float64) {
	a.lower48.SetPrecision(v)
	a.alaska.SetPrecision(v)
	a.hawaii.SetPrecision(v)
}

func (a *albersUSA) Scale() float64 { return a.lower48.Scale() }

func (a *albersUSA) SetScale(k float64) {
	a.lower48.SetScale(k)
	a.alaska.SetScale(k * 0.35)
	a.hawaii.SetScale(k)
	tx, ty := a.lower48.Translate()
	a.SetTranslate(tx, ty)
}

func (a *albersUSA) Translate() (float64, float64) { return a.lower48.Translate() }

func (a *albersUSA) SetTranslate(x, y float64) {
	k := a.lower48.Scale()
	a.lower48.SetTranslate(x, y)
	a.lower48.setClipExtent(&[4]float64{x - float64(0.455*k), y - float64(0.238*k), x + float64(0.455*k), y + float64(0.238*k)})
	a.lower48Point = a.lower48.Stream(&a.captured)

	a.alaska.SetTranslate(x-float64(0.307*k), y+float64(0.201*k))
	a.alaska.setClipExtent(&[4]float64{x - float64(0.425*k) + epsilon, y + float64(0.120*k) + epsilon, x - float64(0.214*k) - epsilon, y + float64(0.234*k) - epsilon})
	a.alaskaPoint = a.alaska.Stream(&a.captured)

	a.hawaii.SetTranslate(x-float64(0.205*k), y+float64(0.212*k))
	a.hawaii.setClipExtent(&[4]float64{x - float64(0.214*k) + epsilon, y + float64(0.166*k) + epsilon, x - float64(0.115*k) - epsilon, y + float64(0.234*k) - epsilon})
	a.hawaiiPoint = a.hawaii.Stream(&a.captured)
}

// albersUsa has no clipExtent method, so fitting never touches clipping.
func (a *albersUSA) clipExtentForFit() ([4]float64, bool) { return [4]float64{}, false }
func (a *albersUSA) setClipExtentForFit(*[4]float64)      {}
func (a *albersUSA) setScaleTranslate(k, x, y float64) {
	a.SetScale(k)
	a.SetTranslate(x, y)
}

func (a *albersUSA) FitExtent(x0, y0, x1, y1 float64, object jsval.Value) {
	fitExtent(a, x0, y0, x1, y1, object)
}
func (a *albersUSA) FitSize(w, h float64, object jsval.Value) { fitExtent(a, 0, 0, w, h, object) }
func (a *albersUSA) FitWidth(w float64, object jsval.Value)   { fitWidth(a, w, object) }
func (a *albersUSA) FitHeight(h float64, object jsval.Value)  { fitHeight(a, h, object) }

func (a *albersUSA) Copy() Projection {
	c := newAlbersUSA()
	c.SetScale(a.Scale())
	tx, ty := a.Translate()
	c.SetTranslate(tx, ty)
	c.SetPrecision(a.Precision())
	c.Path().radius = a.Path().radius
	c.Path().radiusFn = a.Path().radiusFn
	return c
}
