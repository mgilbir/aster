package geo

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// ExtendedProperties are the projection properties vega-projection forwards
// that only d3-geo-projection's projections have (see ProjectionProperties).
// A CustomType takes its parameters from these.
var ExtendedProperties = []string{"coefficient", "distance", "fraction", "lobes", "parallel", "radius", "ratio", "spacing", "tilt"}

// RawFuncs are a raw (unit-sphere) projection: Forward maps [lambda, phi]
// radians to planar coordinates (y up), Invert maps back and is nil when the
// projection has no inverse.
type RawFuncs struct {
	Forward func(lambda, phi float64) (x, y float64)
	Invert  func(x, y float64) (lambda, phi float64)
}

// CustomType is a projection type a caller registers, as vega.projection(name,
// constructor) registers one upstream: d3's geoProjectionMutator over the raw
// projection Raw returns for the current values of its parameters, with the
// scale, clip angle, center and rotation its constructor sets (the setters
// d3-geo-projection's constructors call; each zero value is d3's default).
// Everything else (translate, clipExtent, precision, reflect, fit) is d3's.
type CustomType struct {
	// Raw returns the raw projection for the parameter values, a copy of
	// Params with whatever a specification set. It is called again whenever
	// a parameter changes, and from several renders at once.
	Raw func(params map[string]float64) RawFuncs
	// Params are the parameters the projection takes, from
	// ExtendedProperties, with their defaults. A specification setting a
	// parameter the projection does not take is ignored, as Vega ignores it.
	Params map[string]float64
	// Scale is the default scale; 0 is d3's default, 150.
	Scale float64
	// ClipAngle is the default clip angle in degrees; 0 clips at the
	// antimeridian, as d3 does by default.
	ClipAngle float64
	// Center is the default center, [longitude, latitude] in degrees.
	Center [2]float64
	// Rotate is the default rotation, [lambda, phi, gamma] in degrees.
	Rotate [3]float64
}

// Validate reports what makes c unusable: no Raw, a parameter that is not one
// of ExtendedProperties, a default that is not finite, a clip angle refused
// as setClipAngle refuses it, or a Raw that returns no Forward (or panics)
// for the defaults.
func (c *CustomType) Validate() (err error) {
	if c.Raw == nil {
		return fmt.Errorf("no Raw function")
	}
	for _, k := range slices.Sorted(maps.Keys(c.Params)) {
		if !slices.Contains(ExtendedProperties, k) {
			return fmt.Errorf("parameter %q is not one Vega forwards (%s)", k, strings.Join(ExtendedProperties, ", "))
		}
		if v := c.Params[k]; math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("parameter %q default %v is not finite", k, v)
		}
	}
	if math.IsNaN(c.Scale) || math.IsInf(c.Scale, 0) {
		return fmt.Errorf("scale %v is not finite", c.Scale)
	}
	if math.IsNaN(c.ClipAngle) || math.IsInf(c.ClipAngle, 0) {
		return fmt.Errorf("clip angle %v is not finite", c.ClipAngle)
	}
	for _, v := range append(c.Center[:], c.Rotate[:]...) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("center %v or rotation %v is not finite", c.Center, c.Rotate)
		}
	}
	if math.Abs(c.ClipAngle*radians) >= maxClipRadians {
		return errClipAngle(c.ClipAngle)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Raw panicked for the default parameters: %v", r)
		}
	}()
	if c.Raw(maps.Clone(c.Params)).Forward == nil {
		return fmt.Errorf("Raw returns no Forward function for the default parameters")
	}
	return nil
}

// Registry holds the projection types a caller registered, by lower-case
// name. They are looked up before the built-in ones, so one may replace a
// built-in type, as vega.projection does.
type Registry map[string]*CustomType

// NewProjection creates a projection by type name, case-insensitively, from
// the registry first and then the built-in types; an empty name is Mercator.
func (r Registry) NewProjection(typ string) (Projection, error) {
	if typ == "" {
		typ = "mercator"
	}
	name := jsval.JSLower(typ) // type.toLowerCase()
	if c := r[name]; c != nil {
		return newCustom(name, c)
	}
	return NewProjection(name)
}

// newCustom is the projection a CustomType's constructor makes:
// projectionMutator(raw) with the default parameters, then its scale, clip
// angle, center and rotation. Each setter recenters, so their order does not
// change the result.
func newCustom(name string, c *CustomType) (*standard, error) {
	p := &standard{
		typ: name, custom: c, params: maps.Clone(c.Params),
		k: 150, x: 480, y: 250,
		sx: 1, sy: 1,
		preclip: clipAntimeridian,
		delta2:  0.5,
	}
	if err := p.setCustomProject(); err != nil {
		return nil, err
	}
	if c.Scale != 0 {
		p.SetScale(c.Scale)
	}
	if c.ClipAngle != 0 {
		if err := p.setClipAngle(c.ClipAngle); err != nil {
			return nil, err
		}
	}
	if c.Center != [2]float64{} {
		p.SetCenter(c.Center[0], c.Center[1])
	}
	if c.Rotate != [3]float64{} {
		p.SetRotate(c.Rotate[:])
	}
	return p, nil
}

// setCustomProject is the mutator's m(...): the raw projection for the current
// parameters, then recenter.
func (p *standard) setCustomProject() error {
	f := p.custom.Raw(maps.Clone(p.params))
	if f.Forward == nil {
		return fmt.Errorf("geo: projection %q: Raw returns no Forward function for %v", p.typ, p.params)
	}
	r := &raw{fwd: f.Forward, inv: f.Invert}
	p.projectAt = func(_, _ float64) *raw { return r }
	p.setProject()
	return nil
}

// setParam sets a custom projection's parameter, as its d3 setter does (the
// value converted to a number); one it does not take is ignored.
func (p *standard) setParam(prop string, v jsval.Value) error {
	if _, ok := p.params[prop]; !ok {
		return nil
	}
	p.params[prop] = jsval.ToNumber(v)
	return p.setCustomProject()
}
