package geo

import (
	"fmt"
	"sort"
	"strings"
)

// The registered projections are constructed with the same chain of setter
// calls d3 uses, in the same order, so the resulting state (including
// derived clip extents) is identical.

func newConicEqualArea() *standard {
	p := newConic("conicequalarea", conicEqualAreaRaw)
	p.SetScale(155.424)
	p.SetCenter(0, 33.6442)
	return p
}

func newAlbers() *standard {
	p := newConicEqualArea()
	p.typ = "albers"
	p.SetParallels(29.5, 45.5)
	p.SetScale(1070)
	p.SetTranslate(480, 250)
	p.SetRotate([]float64{96, 0})
	p.SetCenter(-0.6, 38.7)
	return p
}

func newMercatorLike(typ string, r *raw) *standard {
	p := newStandard(typ, func(_, _ float64) *raw { return r })
	p.mercator = true
	p.reclip()
	return p
}

func newTransverseMercator() *standard {
	p := newMercatorLike("transversemercator", transverseMercatorRaw)
	p.SetRotate([]float64{0, 0, 90}) // d3 calls the unwrapped rotate here
	p.transverse = true
	p.SetScale(159.155)
	return p
}

func fixed(typ string, r *raw, scale float64, clipAngle float64) func() Projection {
	return func() Projection {
		p := newStandard(typ, func(_, _ float64) *raw { return r })
		p.SetScale(scale)
		if clipAngle != 0 {
			p.setClipAngle(clipAngle)
		}
		return p
	}
}

// registry maps the lower-case names vega-projection registers to constructors.
var registry = map[string]func() Projection{
	"albers":               func() Projection { return newAlbers() },
	"albersusa":            func() Projection { return newAlbersUSA() },
	"azimuthalequalarea":   fixed("azimuthalequalarea", azimuthalEqualAreaRaw, 124.75, 180-1e-3),
	"azimuthalequidistant": fixed("azimuthalequidistant", azimuthalEquidistantRaw, 79.4188, 180-1e-3),
	"conicconformal": func() Projection {
		p := newConic("conicconformal", conicConformalRaw)
		p.SetScale(109.5)
		p.SetParallels(30, 30)
		return p
	},
	"conicequalarea": func() Projection { return newConicEqualArea() },
	"conicequidistant": func() Projection {
		p := newConic("conicequidistant", conicEquidistantRaw)
		p.SetScale(131.154)
		p.SetCenter(0, 13.9389)
		return p
	},
	"equalearth":      fixed("equalearth", equalEarthRaw, 177.158, 0),
	"equirectangular": fixed("equirectangular", equirectangularRaw, 152.63, 0),
	"gnomonic":        fixed("gnomonic", gnomonicRaw, 144.049, 60),
	"identity":        func() Projection { return newIdentity("identity") },
	"mercator": func() Projection {
		p := newMercatorLike("mercator", mercatorRaw)
		p.SetScale(961 / tau)
		return p
	},
	"mollweide":          fixed("mollweide", mollweideRaw, 169.529, 0),
	"naturalearth1":      fixed("naturalearth1", naturalEarth1Raw, 175.295, 0),
	"orthographic":       fixed("orthographic", orthographicRaw, 249.5, 90+epsilon),
	"stereographic":      fixed("stereographic", stereographicRaw, 250, 142),
	"transversemercator": func() Projection { return newTransverseMercator() },
}

// ProjectionTypes lists the registered projection names (lower case), sorted.
func ProjectionTypes() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// NewProjection creates a projection by registered name, case-insensitively; an
// empty name is Mercator, as Vega's Projection operator does.
func NewProjection(typ string) (Projection, error) {
	if typ == "" {
		typ = "mercator"
	}
	ctor := registry[strings.ToLower(typ)]
	if ctor == nil {
		return nil, fmt.Errorf("unrecognized projection type: %s", typ)
	}
	return ctor(), nil
}
