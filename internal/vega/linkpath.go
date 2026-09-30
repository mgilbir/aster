package vega

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
)

// linkPath is vega-encode's LinkPath: the SVG path of an edge from a source to
// a target position, for each shape and orientation. Numbers are written the
// way JavaScript concatenates them.
func linkPath(p *opParams, in []jsval.Value) error {
	field := func(name, dflt string) transforms.Field {
		if f := p.field(name); !f.IsNil() {
			return f
		}
		return transforms.FieldOf(dflt)
	}
	sx, sy := field("sourceX", "source.x"), field("sourceY", "source.y")
	tx, ty := field("targetX", "target.x"), field("targetY", "target.y")
	as := p.str("as")
	if as == "" {
		as = "path"
	}
	orient := p.str("orient")
	if orient == "" {
		orient = "vertical"
	}
	shape := p.str("shape")
	if shape == "" {
		shape = "line"
	}
	path := linkPaths[shape+"-"+orient]
	if path == nil {
		path = linkPaths[shape]
	}
	if path == nil {
		fail("LinkPath unsupported type: %s%s", shape, "-"+orient)
	}
	for _, t := range in {
		o := t.ObjValue()
		if o == nil {
			continue
		}
		o.Set(as, jsval.Str(path(
			jsval.ToNumber(sx.Apply(t)), jsval.ToNumber(sy.Apply(t)),
			jsval.ToNumber(tx.Apply(t)), jsval.ToNumber(ty.Apply(t)),
		)))
	}
	return nil
}

func n(f float64) string { return jsval.JSNumberString(f) }

func cat(parts ...string) string { return strings.Join(parts, "") }

func lineP(sx, sy, tx, ty float64) string {
	return cat("M", n(sx), ",", n(sy), "L", n(tx), ",", n(ty))
}

// polar converts (angle, radius) pairs to Cartesian for the radial shapes.
func polar(f func(sx, sy, tx, ty float64) string) func(sa, sr, ta, tr float64) string {
	return func(sa, sr, ta, tr float64) string {
		return f(sr*jsmath.Cos(sa), sr*jsmath.Sin(sa), tr*jsmath.Cos(ta), tr*jsmath.Sin(ta))
	}
}

func arcP(sx, sy, tx, ty float64) string {
	dx, dy := tx-sx, ty-sy
	rr := jsmath.Hypot(dx, dy) / 2
	ra := float64(180*jsmath.Atan2(dy, dx)) / 3.141592653589793
	return cat("M", n(sx), ",", n(sy), "A", n(rr), ",", n(rr), " ", n(ra), " 0 1 ", n(tx), ",", n(ty))
}

func curveP(sx, sy, tx, ty float64) string {
	dx, dy := tx-sx, ty-sy
	ix := float64(0.2 * (dx + dy))
	iy := float64(0.2 * (dy - dx))
	return cat("M", n(sx), ",", n(sy), "C", n(sx+ix), ",", n(sy+iy), " ", n(tx+iy), ",", n(ty-ix), " ", n(tx), ",", n(ty))
}

func orthoX(sx, sy, tx, ty float64) string {
	return cat("M", n(sx), ",", n(sy), "V", n(ty), "H", n(tx))
}

func orthoY(sx, sy, tx, ty float64) string {
	return cat("M", n(sx), ",", n(sy), "H", n(tx), "V", n(ty))
}

func orthoR(sa, sr, ta, tr float64) string {
	sc, ss := jsmath.Cos(sa), jsmath.Sin(sa)
	tc, ts := jsmath.Cos(ta), jsmath.Sin(ta)
	var sf bool
	if abs(ta-sa) > 3.141592653589793 {
		sf = ta <= sa
	} else {
		sf = ta > sa
	}
	flag := "0"
	if sf {
		flag = "1"
	}
	return cat("M", n(sr*sc), ",", n(sr*ss), "A", n(sr), ",", n(sr), " 0 0,", flag, " ", n(sr*tc), ",", n(sr*ts), "L", n(tr*tc), ",", n(tr*ts))
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func diagonalX(sx, sy, tx, ty float64) string {
	m := (sx + tx) / 2
	return cat("M", n(sx), ",", n(sy), "C", n(m), ",", n(sy), " ", n(m), ",", n(ty), " ", n(tx), ",", n(ty))
}

func diagonalY(sx, sy, tx, ty float64) string {
	m := (sy + ty) / 2
	return cat("M", n(sx), ",", n(sy), "C", n(sx), ",", n(m), " ", n(tx), ",", n(m), " ", n(tx), ",", n(ty))
}

func diagonalR(sa, sr, ta, tr float64) string {
	sc, ss := jsmath.Cos(sa), jsmath.Sin(sa)
	tc, ts := jsmath.Cos(ta), jsmath.Sin(ta)
	mr := (sr + tr) / 2
	return cat("M", n(sr*sc), ",", n(sr*ss), "C", n(mr*sc), ",", n(mr*ss), " ", n(mr*tc), ",", n(mr*ts), " ", n(tr*tc), ",", n(tr*ts))
}

var linkPaths = map[string]func(a, b, c, d float64) string{
	"line":                  lineP,
	"line-radial":           polar(lineP),
	"arc":                   arcP,
	"arc-radial":            polar(arcP),
	"curve":                 curveP,
	"curve-radial":          polar(curveP),
	"orthogonal-horizontal": orthoX,
	"orthogonal-vertical":   orthoY,
	"orthogonal-radial":     orthoR,
	"diagonal-horizontal":   diagonalX,
	"diagonal-vertical":     diagonalY,
	"diagonal-radial":       diagonalR,
}
