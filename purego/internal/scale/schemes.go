package scale

import (
	"strings"
	"sync"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Scheme is a named colour scheme of vega-scale's registry: either a discrete
// list of colours (categorical schemes such as "category10") or a continuous
// interpolator over [0, 1] (sequential/diverging/cyclical ramps such as
// "viridis" or "blues"). Exactly one of the two is set.
type Scheme struct {
	// Colors is the discrete palette, as "#rrggbb" strings.
	Colors []string
	// Interpolator maps t in [0, 1] to a colour string.
	Interpolator UnitInterpolator
}

// IsDiscrete reports whether the scheme is a list of colours.
func (s Scheme) IsDiscrete() bool { return s.Colors != nil }

var (
	schemeMu sync.RWMutex
	schemes  = map[string]*schemeEntry{}
)

// schemeEntry builds a built-in ramp on first use: parsing every colour of
// some 60 palettes at start-up would cost more than most charts ever spend.
type schemeEntry struct {
	once   sync.Once
	build  func() Scheme
	scheme Scheme
}

func (e *schemeEntry) get() Scheme {
	e.once.Do(func() { e.scheme = e.build() })
	return e.scheme
}

func hexColors(hex string) []string {
	n := len(hex) / 6
	c := make([]string, n)
	for i := 0; i < n; i++ {
		c[i] = "#" + hex[i*6:(i+1)*6]
	}
	return c
}

func init() {
	for _, p := range discretePalettes {
		hex := p.hex
		schemes[p.name] = &schemeEntry{build: func() Scheme { return Scheme{Colors: hexColors(hex)} }}
	}
	for _, p := range continuousPalettes {
		hex := p.hex
		schemes[p.name] = &schemeEntry{build: func() Scheme {
			colors := hexColors(hex)
			vals := make([]jsval.Value, len(colors))
			for i, c := range colors {
				vals[i] = jsval.Str(c)
			}
			return Scheme{Interpolator: InterpolateColors(vals, "", 0, false)}
		}}
	}
}

// LookupScheme is vega-scale's scheme(name): the scheme registered under name
// (case-insensitively), if any.
func LookupScheme(name string) (Scheme, bool) {
	schemeMu.RLock()
	e, ok := schemes[strings.ToLower(name)]
	schemeMu.RUnlock()
	if !ok {
		return Scheme{}, false
	}
	return e.get(), true
}

// RegisterScheme is vega-scale's scheme(name, scheme): it adds or replaces a
// scheme under the lower-cased name.
func RegisterScheme(name string, s Scheme) {
	e := &schemeEntry{scheme: s}
	e.once.Do(func() {})
	schemeMu.Lock()
	schemes[strings.ToLower(name)] = e
	schemeMu.Unlock()
}

// SchemeNames lists the registered scheme names (unsorted).
func SchemeNames() []string {
	schemeMu.RLock()
	defer schemeMu.RUnlock()
	names := make([]string, 0, len(schemes))
	for n := range schemes {
		names = append(names, n)
	}
	return names
}
