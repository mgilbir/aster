package svg

import (
	"strconv"

	"github.com/mgilbir/aster/internal/scene"
)

// gradDef is a registered gradient definition with coordinates resolved.
type gradDef struct {
	id             string
	radial         bool
	x1, y1, x2, y2 scene.Num
	r1, r2         scene.Num
	stops          []scene.GradientStop
}

type clipDef struct {
	id        string
	path      string
	width     scene.Num
	height    scene.Num
	hasSize   bool
	pathValid bool
}

const patternPrefix = "p_"

// gradientRef returns the url() reference for a gradient, assigning its id and
// default coordinates on first use (linear: 0,0 → 1,0; radial: centre 0.5,0.5,
// radius 0 → 0.5) and registering its definition. Gradients that arrive with an
// id keep their coordinates as given, so unset ones stay unset.
func (r *renderer) gradientRef(g *scene.Gradient) string {
	id, seen := r.gradientIDs[g]
	if !seen {
		def := &gradDef{radial: g.Radial, x1: g.X1, y1: g.Y1, x2: g.X2, y2: g.Y2, r1: g.R1, r2: g.R2, stops: g.Stops}
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
		if size != nil {
			def.width, def.height = scene.N(size.Width.Zero()), scene.N(size.Height.Zero())
		} else {
			def.width, def.height = scene.N(0), scene.N(0)
		}
	}
	return "url(#" + id + ")", nil
}

// defs writes the <defs> element (gradients, then clip paths) if anything was
// registered. It must run after the marks so the collected state is complete.
func (r *renderer) defs() {
	if len(r.gradients) == 0 && len(r.clips) == 0 {
		return
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
			w.attrRaw("viewBox", "0,0,1,1")
			w.attrRaw("width", "100%")
			w.attrRaw("height", "100%")
			w.attrRaw("preserveAspectRatio", "xMidYMid slice")
			w.start("rect")
			w.attrRaw("width", "1")
			w.attrRaw("height", "1")
			w.attr("fill", "url(#"+g.id+")")
			w.end()
			w.end()

			w.start("radialGradient")
			w.attr("id", g.id)
			r.numAttr("fx", g.x1)
			r.numAttr("fy", g.y1)
			r.numAttr("fr", g.r1)
			r.numAttr("cx", g.x2)
			r.numAttr("cy", g.y2)
			r.numAttr("r", g.r2)
		} else {
			w.start("linearGradient")
			w.attr("id", g.id)
			r.numAttr("x1", g.x1)
			r.numAttr("x2", g.x2)
			r.numAttr("y1", g.y1)
			r.numAttr("y2", g.y2)
		}
		for _, s := range g.stops {
			w.start("stop")
			w.attrNum("offset", s.Offset)
			w.attr("stop-color", s.Color)
			w.end()
		}
		w.end()
	}
	for _, c := range r.clips {
		w.start("clipPath")
		w.attr("id", c.id)
		if c.path != "" {
			w.start("path")
			w.attr("d", c.path)
			w.end()
		} else {
			w.start("rect")
			w.attrRaw("x", "0")
			w.attrRaw("y", "0")
			if c.hasSize {
				r.numAttr("width", c.width)
				r.numAttr("height", c.height)
			}
			w.end()
		}
		w.end()
	}
	w.end()
}
