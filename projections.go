package aster

import (
	"fmt"
	"maps"
	"strings"

	"github.com/mgilbir/aster/internal/geo"
)

// Projection is a cartographic projection type that specifications can name
// in a projection's "type" once registered with WithProjection, as
// vega.projection(name, constructor) registers one upstream.
//
// It is built as d3's geoProjectionMutator builds the projections of
// d3-geo-projection: the raw projection Raw returns for the current
// parameters, scaled, translated, centred, rotated, clipped, resampled and
// fitted as every built-in projection is. Scale, ClipAngle, Center and Rotate
// are the defaults its constructor sets, as d3-geo-projection's constructors
// set them; the zero value of each is d3's own default. A specification's scale,
// translate, center, rotate, clipAngle, clipExtent, precision, reflectX,
// reflectY and fit apply to it unchanged.
//
// A projection that clips the sphere its own way (d3-geo-projection's
// interrupted and polyhedral projections, which replace the clipping stream)
// cannot be described.
type Projection struct {
	// Raw returns the raw projection for the parameter values: a copy of
	// Params, with the values a specification set (as numbers, in the
	// specification's units: convert degrees to radians in Raw). It is
	// called once when a projection is created and again whenever a
	// parameter changes, and must be safe to call, as must the functions it
	// returns, from several renders at once. A panic in any of them fails the
	// render with an error.
	Raw func(params map[string]float64) RawProjection
	// Params are the parameters the projection takes, with their defaults.
	// Each is one of the properties Vega forwards to a projection for
	// d3-geo-projection: coefficient, distance, fraction, lobes, parallel,
	// radius, ratio, spacing and tilt. A specification setting one the
	// projection does not take is ignored, as Vega ignores it.
	Params map[string]float64
	// Scale is the default scale; 0 is d3's default, 150.
	Scale float64
	// ClipAngle is the default clip angle in degrees, which a specification
	// may change; 0 clips at the antimeridian, d3's default.
	ClipAngle float64
	// Center is the default center, [longitude, latitude] in degrees.
	Center [2]float64
	// Rotate is the default rotation, [lambda, phi, gamma] in degrees.
	Rotate [3]float64
}

// RawProjection is a raw projection: Forward maps a point of the unit sphere,
// longitude lambda and latitude phi in radians, to the plane, y pointing up
// (as d3's raw projections do: the projection flips it), and Invert maps
// back. Invert may be nil, in which case the projection has no inverse and
// Vega's invert() of it gives no position.
type RawProjection struct {
	Forward func(lambda, phi float64) (x, y float64)
	Invert  func(x, y float64) (lambda, phi float64)
}

// WithProjection registers a projection type under name, which
// specifications name case-insensitively, as Vega's projection types are
// named. A name already registered, a built-in one included, is replaced, as
// vega.projection replaces it. New calls p.Raw once with the default
// parameters, and returns an error for an empty name, a nil Raw, a Raw that
// returns no Forward function (or panics), a parameter Vega does not forward,
// or a default that is not finite.
//
// The types are the Converter's own: another Converter does not see them.
func WithProjection(name string, p Projection) Option {
	params := maps.Clone(p.Params)
	ct := &geo.CustomType{Params: params, Scale: p.Scale, ClipAngle: p.ClipAngle, Center: p.Center, Rotate: p.Rotate}
	if raw := p.Raw; raw != nil {
		ct.Raw = func(params map[string]float64) geo.RawFuncs {
			r := raw(params)
			return geo.RawFuncs{Forward: r.Forward, Invert: r.Invert}
		}
	}
	return func(c *config) {
		if name == "" {
			c.errs = append(c.errs, fmt.Errorf("aster: WithProjection: empty projection name"))
			return
		}
		if err := ct.Validate(); err != nil {
			c.errs = append(c.errs, fmt.Errorf("aster: WithProjection(%q): %w", name, err))
			return
		}
		if c.projections == nil {
			c.projections = geo.Registry{}
		}
		c.projections[strings.ToLower(name)] = ct
	}
}
