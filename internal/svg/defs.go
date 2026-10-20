package svg

import (
	"math"
	"strconv"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// gradDef is a registered gradient definition with coordinates resolved.
type gradDef struct {
	id             string
	radial         bool
	x1, y1, x2, y2 scene.Num
	r1, r2         scene.Num
	stops          []scene.GradientStop
	// vals are the coordinates as given (see scene.Gradient.Values), with
	// the defaults filled in: written as they are when set.
	vals     *[6]jsval.Value
	stopsErr error
}

type clipDef struct {
	id   string
	path string
	// width and height are size.width || 0, size.height || 0: the values
	// as given when truthy.
	width   jsval.Value
	height  jsval.Value
	hasSize bool
}

const patternPrefix = "p_"

// gradientRef returns the url() reference for a gradient, assigning its id and
// default coordinates on first use (linear: 0,0 → 1,0; radial: centre 0.5,0.5,
// radius 0 → 0.5) and registering its definition. Gradients that arrive with an
// id keep their coordinates as given, so unset ones stay unset.
func (r *renderer) gradientRef(g *scene.Gradient) string {
	id, seen := r.gradientIDs[g]
	if !seen {
		def := &gradDef{radial: g.Radial, x1: g.X1, y1: g.Y1, x2: g.X2, y2: g.Y2, r1: g.R1, r2: g.R2, stops: g.Stops, stopsErr: g.StopsErr}
		if g.ID != "" {
			id = g.ID
		} else {
			id = "gradient_" + strconv.Itoa(r.nextGrad)
			r.nextGrad++
			or := func(n scene.Num, d float64) scene.Num {
				if n.Set() {
					return n
				}
				return scene.N(d)
			}
			if g.Radial {
				def.x1, def.y1, def.r1 = or(g.X1, 0.5), or(g.Y1, 0.5), or(g.R1, 0)
				def.x2, def.y2, def.r2 = or(g.X2, 0.5), or(g.Y2, 0.5), or(g.R2, 0.5)
			} else {
				def.x1, def.y1, def.x2, def.y2 = or(g.X1, 0), or(g.Y1, 0), or(g.X2, 1), or(g.Y2, 0)
			}
		}
		if g.Values != nil {
			// get(val, def): val != null ? val : def, for a gradient the
			// renderer names (one with an id keeps them as given).
			vals := *g.Values
			if g.ID == "" {
				dflt := [6]float64{0, 0, 1, 0, math.NaN(), math.NaN()}
				if g.Radial {
					dflt = [6]float64{0.5, 0.5, 0.5, 0.5, 0, 0.5}
				}
				for i, v := range vals {
					if v.IsNullish() && dflt[i] == dflt[i] {
						vals[i] = jsval.Num(dflt[i])
					}
				}
			}
			def.vals = &vals
		}
		def.id = id
		if r.gradientIDs == nil {
			r.gradientIDs = map[*scene.Gradient]string{}
			r.gradByID = map[string]int{}
		}
		r.gradientIDs[g] = id
		// defs[id] = g: a later gradient with the same id replaces the
		// definition but keeps its place in the order.
		if i, ok := r.gradByID[id]; ok {
			r.gradients[i] = def
		} else {
			r.gradByID[id] = len(r.gradients)
			r.gradients = append(r.gradients, def)
		}
	}
	prefix := ""
	if g.Radial {
		prefix = patternPrefix
	}
	return "url(#" + prefix + id + ")"
}

// clipRef assigns (on first use) the id of the clip path for a mark or group
// item, records its definition and returns the url() reference. size is the
// group whose box clips: the mark's parent group, or the group item itself.
func (r *renderer) clipRef(owner any, clip bool, fn scene.PathFunc, size *scene.Item) (string, error) {
	id, ok := r.clipIDs[owner]
	if !ok {
		r.nextClip++
		id = "clip" + strconv.Itoa(r.nextClip)
		if r.clipIDs == nil {
			r.clipIDs = map[any]string{}
			r.clipByID = map[string]int{}
		}
		r.clipIDs[owner] = id
	}
	var def *clipDef
	if i, ok := r.clipByID[id]; ok {
		def = r.clips[i]
	} else {
		def = &clipDef{id: id}
		r.clipByID[id] = len(r.clips)
		r.clips = append(r.clips, def)
	}

	switch {
	case fn != nil:
		def.path = fn(nil) // clip(null): the generator's own path string
	case size != nil && size.HasCornerRadius():
		def.path = string(scene.RectPathData(&r.sp, size, true, 0, 0))
	default:
		def.hasSize = true
		switch {
		case size == nil:
			def.width, def.height = jsval.Num(0), jsval.Num(0)
		case size.Raw != nil:
			def.width, def.height = size.Or0("width"), size.Or0("height")
		default:
			def.width, def.height = jsval.Num(size.Width.Zero()), jsval.Num(size.Height.Zero())
		}
	}
	return "url(#" + id + ")", nil
}

// gradAttr writes coordinate i of a gradient definition: the value as given
// when the gradient has its values, else the number n.
func (r *renderer) gradAttr(name lit, g *gradDef, i int, n scene.Num) {
	if g.vals != nil {
		r.valueAttr(name, g.vals[i])
		return
	}
	r.numAttr(name, n)
}

// valueAttr writes an attribute of a value as the markup does: String(value),
// and nothing for null or undefined.
func (r *renderer) valueAttr(name lit, v jsval.Value) {
	if v.IsNullish() {
		return
	}
	r.w.attrOpen(name)
	r.w.valJS(v)
	r.w.attrClose()
}

// defs writes the <defs> element (gradients, then clip paths) if anything was
// registered. It must run after the marks so the collected state is complete.
func (r *renderer) defs() error {
	if len(r.gradients) == 0 && len(r.clips) == 0 {
		return nil
	}
	w := &r.w
	w.start("defs")
	for _, g := range r.gradients {
		if g.radial {
			// SVG radial gradients transform to normalized bounding-box
			// coordinates in a way that is awkward to reproduce on canvas, so
			// the gradient is wrapped in a pattern that keeps it circular.
			w.start("pattern")
			w.attr("id", patternPrefix+g.id)
			w.attrLit("viewBox", "0,0,1,1")
			w.attrLit("width", "100%")
			w.attrLit("height", "100%")
			w.attrLit("preserveAspectRatio", "xMidYMid slice")
			w.start("rect")
			w.attrLit("width", "1")
			w.attrLit("height", "1")
			w.attr("fill", "url(#"+g.id+")")
			w.end()
			w.end()

			w.start("radialGradient")
			w.attr("id", g.id)
			r.gradAttr("fx", g, 0, g.x1)
			r.gradAttr("fy", g, 1, g.y1)
			r.gradAttr("fr", g, 4, g.r1)
			r.gradAttr("cx", g, 2, g.x2)
			r.gradAttr("cy", g, 3, g.y2)
			r.gradAttr("r", g, 5, g.r2)
		} else {
			w.start("linearGradient")
			w.attr("id", g.id)
			r.gradAttr("x1", g, 0, g.x1)
			r.gradAttr("x2", g, 2, g.x2)
			r.gradAttr("y1", g, 1, g.y1)
			r.gradAttr("y2", g, 3, g.y2)
		}
		for _, s := range g.stops {
			w.start("stop")
			if s.Given {
				// offset: stops[i].offset, 'stop-color': stops[i].color,
				// written unless null or undefined.
				r.valueAttr("offset", s.OffsetValue)
				r.valueAttr("stop-color", s.ColorValue)
			} else {
				w.attrNum("offset", s.Offset)
				w.attr("stop-color", s.Color)
			}
			w.end()
		}
		if g.stopsErr != nil {
			return g.stopsErr
		}
		w.end()
	}
	for _, c := range r.clips {
		w.start("clipPath")
		w.attr("id", c.id)
		if c.path != "" {
			w.start("path")
			if c.path == scene.TruthyEmptyPath {
				c.path = ""
			}
			w.attr("d", c.path)
			w.end()
		} else {
			w.start("rect")
			w.attrLit("x", "0")
			w.attrLit("y", "0")
			if c.hasSize {
				w.attrOpen("width")
				w.valJS(c.width)
				w.attrClose()
				w.attrOpen("height")
				w.valJS(c.height)
				w.attrClose()
			}
			w.end()
		}
		w.end()
	}
	w.end()
	return nil
}
