package geo

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// Replay of d3-geo's own tests (see internal/upstream).

// projectionNames maps d3-geo's projection constructors to the registry's names.
var projectionNames = map[string]string{
	"geoAlbers":               "albers",
	"geoAlbersUsa":            "albersusa",
	"geoAzimuthalEqualArea":   "azimuthalequalarea",
	"geoAzimuthalEquidistant": "azimuthalequidistant",
	"geoConicConformal":       "conicconformal",
	"geoConicEqualArea":       "conicequalarea",
	"geoConicEquidistant":     "conicequidistant",
	"geoEqualEarth":           "equalearth",
	"geoEquirectangular":      "equirectangular",
	"geoGnomonic":             "gnomonic",
	"geoIdentity":             "identity",
	"geoMercator":             "mercator",
	"geoOrthographic":         "orthographic",
	"geoStereographic":        "stereographic",
	"geoTransverseMercator":   "transversemercator",
}

// angleSetter is the projections that have angle(), which Vega does not forward.
type angleSetter interface{ SetAngle(float64) }

// configureProjection applies one recorded configuration step.
func configureProjection(p Projection, step upstream.Step) bool {
	if len(step.Args) != 1 || upstream.Contains(step.Args, "function") {
		return false
	}
	v := upstream.ToValue(step.Args[0])
	switch step.Method {
	case "scale", "translate", "center", "rotate", "parallels", "precision", "clipAngle", "clipExtent", "reflectX", "reflectY":
		return p.Set(step.Method, v) == nil
	case "angle":
		if a, ok := p.(angleSetter); ok {
			a.SetAngle(jsval.ToNumber(v))
			return true
		}
	}
	return false
}

// projectionFromOrigin rebuilds the projection a recorded function came from: `geoMercator()` and what
// was configured on it since.
func projectionFromOrigin(v any) (Projection, bool) {
	origin, ok := upstream.IsFunction(v)
	if !ok || origin == nil {
		return nil, false
	}
	from, _ := origin["from"].(string)
	typ, ok := projectionNames[from]
	if !ok {
		return nil, false
	}
	p, err := NewProjection(typ)
	if err != nil {
		return nil, false
	}
	for _, step := range stepsFromRaw(origin["chain"]) {
		if !configureProjection(p, step) {
			return nil, false
		}
	}
	return p, true
}

func stepsFromRaw(raw any) []upstream.Step {
	items, _ := raw.([]any)
	out := make([]upstream.Step, 0, len(items))
	for _, item := range items {
		pair, ok := item.([]any)
		if !ok || len(pair) != 2 {
			continue
		}
		m, _ := pair[0].(string)
		a, _ := pair[1].([]any)
		out = append(out, upstream.Step{Method: m, Args: a})
	}
	return out
}

func point(v any) ([2]float64, bool) {
	items, ok := v.([]any)
	if !ok || len(items) != 2 {
		return [2]float64{}, false
	}
	return [2]float64{upstream.Number(items[0]), upstream.Number(items[1])}, true
}

func pairValue(p [2]float64) any { return upstream.Floats(p[:]) }

func extentValue(e [2][2]float64) any {
	return []any{pairValue(e[0]), pairValue(e[1])}
}

func TestUpstreamD3Geo(t *testing.T) {
	r := upstream.Start(t, "d3-geo")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		obj := func(i int) jsval.Value { return upstream.ToValue(c.Arg(i)) }
		switch c.Fn {
		case "geoArea":
			r.Check(c, upstream.Enc(GeoArea(obj(0))), false)
		case "geoBounds":
			r.Check(c, extentValue(GeoBounds(obj(0))), false)
		case "geoCentroid":
			r.Check(c, pairValue(GeoCentroid(obj(0))), false)
		case "geoContains":
			p, ok := point(c.Arg(1))
			if !ok {
				r.Skip("a point of another shape")
				continue
			}
			r.Check(c, GeoContains(obj(0), p), false)
		case "geoLength":
			r.Check(c, upstream.Enc(GeoLength(obj(0))), false)
		case "geoDistance":
			a, ok1 := point(c.Arg(0))
			b, ok2 := point(c.Arg(1))
			if !ok1 || !ok2 {
				r.Skip("a point of another shape")
				continue
			}
			r.Check(c, upstream.Enc(GeoDistance(a, b)), false)
		case "geoInterpolate()":
			a, ok1 := point(argAt(c.ConstructedWith, 0))
			b, ok2 := point(argAt(c.ConstructedWith, 1))
			if !ok1 || !ok2 {
				r.Skip("a point of another shape")
				continue
			}
			interp, _ := GeoInterpolate(a, b)
			r.Check(c, pairValue(interp(upstream.Number(c.Arg(0)))), false)
		case "geoInterpolate.distance()", "geoInterpolate.distance":
			r.Skip("distance property")
		case "geoRotation()":
			rot := numbersFrom(argAt(c.ConstructedWith, 0))
			forward, invert := GeoRotation(rot)
			p, ok := point(c.Arg(0))
			if !ok {
				r.Skip("a point of another shape")
				continue
			}
			var lon, lat float64
			switch c.Method {
			case "":
				lon, lat = forward(p[0], p[1])
			case "invert":
				lon, lat = invert(p[0], p[1])
			default:
				r.Skip("unmapped method " + c.Method)
				continue
			}
			r.Check(c, pairValue([2]float64{lon, lat}), false)
		case "geoCircle()":
			replayCircle(r, c)
		case "geoGraticule()":
			replayGraticule(r, c)
		case "geoPath()":
			replayPath(r, c)
		default:
			if typ, ok := projectionNames[strings.TrimSuffix(c.Fn, "()")]; ok && strings.HasSuffix(c.Fn, "()") {
				replayProjection(r, c, typ)
				continue
			}
			r.Skip("unmapped " + c.Fn)
		}
	}
	r.Done(600)
}

func nullish(v any) bool { return v == nil || upstream.IsUndefined(v) }

func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return upstream.Undefined()
}

func numbersFrom(v any) []float64 {
	items, _ := v.([]any)
	out := make([]float64, len(items))
	for i, x := range items {
		out[i] = upstream.Number(x)
	}
	return out
}

func replayProjection(r *upstream.Replay, c *upstream.Call, typ string) {
	p, err := NewProjection(typ)
	if err != nil {
		r.Skip("projection the engine does not have")
		return
	}
	for _, step := range c.ChainSteps() {
		if !configureProjection(p, step) {
			r.Skip("configuration the engine does not take")
			return
		}
	}
	if len(c.ViaSteps()) != 0 {
		r.Skip("projection methods of another shape")
		return
	}
	switch c.Method {
	case "":
		pt, ok := point(argAt(c.Args, 0))
		if !ok {
			r.Skip("a point of another shape")
			return
		}
		x, y, ok := p.Forward(pt[0], pt[1])
		if !ok {
			r.Check(c, nil, false)
			return
		}
		r.Check(c, pairValue([2]float64{x, y}), false)
	case "invert":
		pt, ok := point(argAt(c.Args, 0))
		if !ok {
			r.Skip("a point of another shape")
			return
		}
		lon, lat, ok := p.Invert(pt[0], pt[1])
		if !ok {
			r.Check(c, nil, false)
			return
		}
		r.Check(c, pairValue([2]float64{lon, lat}), false)
	case "scale":
		if len(c.Args) == 0 {
			r.Check(c, upstream.Enc(p.Scale()), false)
			return
		}
		r.Skip("setters")
	case "translate":
		if len(c.Args) == 0 {
			x, y := p.Translate()
			r.Check(c, pairValue([2]float64{x, y}), false)
			return
		}
		r.Skip("setters")
	case "angle":
		a, ok := p.(interface{ Angle() float64 })
		if !ok || len(c.Args) != 0 {
			r.Skip("angle on a projection without it")
			return
		}
		r.Check(c, upstream.Enc(a.Angle()), false)
	default:
		r.Skip("unmapped method " + c.Method)
	}
}

func replayCircle(r *upstream.Replay, c *upstream.Call) {
	center := [2]float64{0, 0}
	radius, precision := 90.0, 2.0
	for _, step := range c.ChainSteps() {
		if len(step.Args) != 1 || upstream.Contains(step.Args, "function") {
			r.Skip("circle configuration of another shape")
			return
		}
		switch step.Method {
		case "center":
			p, ok := point(step.Args[0])
			if !ok {
				r.Skip("circle configuration of another shape")
				return
			}
			center = p
		case "radius":
			radius = upstream.Number(step.Args[0])
		case "precision":
			precision = upstream.Number(step.Args[0])
		default:
			r.Skip("circle configuration of another shape")
			return
		}
	}
	if c.Method != "" || len(c.Args) != 0 {
		r.Skip("circle questions of another shape")
		return
	}
	poly, ok := GeoCircle(center, radius, precision)
	if !ok {
		r.Check(c, nil, true)
		return
	}
	r.Check(c, upstream.FromValue(poly), false)
}

func replayGraticule(r *upstream.Replay, c *upstream.Call) {
	g := NewGraticule()
	for _, step := range c.ChainSteps() {
		if len(step.Args) != 1 {
			r.Skip("graticule configuration of another shape")
			return
		}
		switch step.Method {
		case "extent", "extentMajor", "extentMinor":
			ext, ok := extentFrom(step.Args[0])
			if !ok {
				r.Skip("graticule configuration of another shape")
				return
			}
			switch step.Method {
			case "extent":
				g.SetExtent(ext)
			case "extentMajor":
				g.SetExtentMajor(ext)
			default:
				g.SetExtentMinor(ext)
			}
		case "step", "stepMajor", "stepMinor":
			p, ok := point(step.Args[0])
			if !ok {
				r.Skip("graticule configuration of another shape")
				return
			}
			switch step.Method {
			case "step":
				g.SetStep(p)
			case "stepMajor":
				g.SetStepMajor(p)
			default:
				g.SetStepMinor(p)
			}
		case "precision":
			g.SetPrecision(upstream.Number(step.Args[0]))
		default:
			r.Skip("graticule configuration of another shape")
			return
		}
	}
	if len(c.Args) != 0 {
		r.Skip("graticule questions of another shape")
		return
	}
	switch c.Method {
	case "", "lines", "outline":
		var v jsval.Value
		var err error
		switch c.Method {
		case "":
			v, err = g.Lines()
		case "outline":
			v, err = g.Outline()
		default:
			var ls []jsval.Value
			ls, err = g.LineStrings()
			v = jsval.Arr(ls)
		}
		if err != nil {
			r.Check(c, nil, true)
			return
		}
		r.Check(c, upstream.FromValue(v), false)
	default:
		r.Skip("graticule getters (the engine has no accessors)")
	}
}

func extentFrom(v any) ([2][2]float64, bool) {
	items, ok := v.([]any)
	if !ok || len(items) != 2 {
		return [2][2]float64{}, false
	}
	a, ok1 := point(items[0])
	b, ok2 := point(items[1])
	return [2][2]float64{a, b}, ok1 && ok2
}

func replayPath(r *upstream.Replay, c *upstream.Call) {
	path := NewPath(nil)
	// geoPath(projection, context)
	steps := c.ChainSteps()
	if len(c.ConstructedWith) > 0 {
		steps = append([]upstream.Step{{Method: "projection", Args: c.ConstructedWith[:1]}}, steps...)
		if len(c.ConstructedWith) > 1 && c.ConstructedWith[1] != nil && !upstream.IsUndefined(c.ConstructedWith[1]) {
			r.Skip("a path with a drawing context")
			return
		}
	}
	for _, step := range steps {
		switch step.Method {
		case "projection":
			if len(step.Args) != 1 {
				r.Skip("path configuration of another shape")
				return
			}
			if nullish(step.Args[0]) {
				path.SetProjection(nil)
				continue
			}
			p, ok := projectionFromOrigin(step.Args[0])
			if !ok {
				r.Skip("a projection the recording cannot identify")
				return
			}
			path.SetProjection(p)
		case "digits":
			if len(step.Args) != 1 {
				r.Skip("path configuration of another shape")
				return
			}
			if nullish(step.Args[0]) {
				path.SetDigits(-1)
			} else {
				path.SetDigits(int(upstream.Number(step.Args[0])))
			}
		default:
			r.Skip("path configuration the adapter does not map (" + step.Method + ")")
			return
		}
	}
	obj := upstream.ToValue(argAt(c.Args, 0))
	switch c.Method {
	case "":
		if len(c.Args) == 0 {
			r.Skip("path without an object")
			return
		}
		s, ok := path.String(obj)
		if !ok {
			r.Check(c, nil, false)
			return
		}
		r.Check(c, s, false)
	case "area":
		r.Check(c, upstream.Enc(path.Area(obj)), false)
	case "measure":
		r.Check(c, upstream.Enc(path.Measure(obj)), false)
	case "bounds":
		r.Check(c, extentValue(path.Bounds(obj)), false)
	case "centroid":
		r.Check(c, pairValue(path.Centroid(obj)), false)
	case "digits":
		if len(c.Args) != 0 {
			r.Skip("setters")
			return
		}
		if path.Digits() < 0 {
			r.Check(c, nil, false) // d3's null: full precision
			return
		}
		r.Check(c, upstream.Enc(float64(path.Digits())), false)
	default:
		r.Skip("unmapped method " + c.Method)
	}
}
