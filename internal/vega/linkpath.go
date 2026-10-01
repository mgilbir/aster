package vega

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
)

// linkPath is vega-encode's LinkPath: the SVG path of an edge from a source to
// a target position, for each shape and orientation. The positions are the
// field values as they come: upstream concatenates them into the path text, so
// a missing one prints as "undefined" and a string keeps its own spelling.
// str is JavaScript's String(x).
func linkPath(p *opParams, in []jsval.Value, str func(jsval.Value) string) error {
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
	lp := linkPaths[shape+"-"+orient]
	if lp == nil {
		lp = linkPaths[shape]
	}
	if lp == nil {
		fail("LinkPath unsupported type: %s%s", shape, "-"+orient)
	}
	js := linkJS{str}
	for _, t := range in {
		o := t.ObjValue()
		if o == nil {
			continue
		}
		o.Set(as, jsval.Str(lp(js, sx.Apply(t), sy.Apply(t), tx.Apply(t), ty.Apply(t))))
	}
	return nil
}

// linkJS carries the JavaScript conversions the path builders need.
type linkJS struct{ str func(jsval.Value) string }

func n(f float64) string { return jsval.JSNumberString(f) }

func cat(parts ...string) string { return strings.Join(parts, "") }

// plus is the + operator: a string, array, object or date makes it a
// concatenation, anything else a sum.
func (j linkJS) plus(a, b jsval.Value) jsval.Value {
	concat := func(v jsval.Value) bool {
		return v.IsStr() || v.IsArr() || v.IsObj() || v.IsTimestamp() || v.IsPattern()
	}
	if concat(a) || concat(b) {
		return jsval.Str(j.str(a) + j.str(b))
	}
	return jsval.Num(jsval.ToNumber(a) + jsval.ToNumber(b))
}

func lineP(j linkJS, sx, sy, tx, ty jsval.Value) string {
	return cat("M", j.str(sx), ",", j.str(sy), "L", j.str(tx), ",", j.str(ty))
}

// polar converts (angle, radius) pairs to Cartesian for the radial shapes.
func polar(f func(j linkJS, sx, sy, tx, ty jsval.Value) string) linkPathFn {
	return func(j linkJS, sa, sr, ta, tr jsval.Value) string {
		a, r, b, q := jsval.ToNumber(sa), jsval.ToNumber(sr), jsval.ToNumber(ta), jsval.ToNumber(tr)
		return f(j, jsval.Num(r*jsmath.Cos(a)), jsval.Num(r*jsmath.Sin(a)), jsval.Num(q*jsmath.Cos(b)), jsval.Num(q*jsmath.Sin(b)))
	}
}

func arcP(j linkJS, sx, sy, tx, ty jsval.Value) string {
	dx, dy := jsval.ToNumber(tx)-jsval.ToNumber(sx), jsval.ToNumber(ty)-jsval.ToNumber(sy)
	rr := jsmath.Hypot(dx, dy) / 2
	ra := float64(180*jsmath.Atan2(dy, dx)) / 3.141592653589793
	return cat("M", j.str(sx), ",", j.str(sy), "A", n(rr), ",", n(rr), " ", n(ra), " 0 1 ", j.str(tx), ",", j.str(ty))
}

func curveP(j linkJS, sx, sy, tx, ty jsval.Value) string {
	nsx, nsy, ntx, nty := jsval.ToNumber(sx), jsval.ToNumber(sy), jsval.ToNumber(tx), jsval.ToNumber(ty)
	dx, dy := ntx-nsx, nty-nsy
	ix := jsval.Num(float64(0.2 * (dx + dy)))
	iy := jsval.Num(float64(0.2 * (dy - dx)))
	return cat("M", j.str(sx), ",", j.str(sy),
		"C", j.str(j.plus(sx, ix)), ",", j.str(j.plus(sy, iy)),
		" ", j.str(j.plus(tx, iy)), ",", n(nty-ix.NumValue()),
		" ", j.str(tx), ",", j.str(ty))
}

func orthoX(j linkJS, sx, sy, tx, ty jsval.Value) string {
	return cat("M", j.str(sx), ",", j.str(sy), "V", j.str(ty), "H", j.str(tx))
}

func orthoY(j linkJS, sx, sy, tx, ty jsval.Value) string {
	return cat("M", j.str(sx), ",", j.str(sy), "H", j.str(tx), "V", j.str(ty))
}

func orthoR(_ linkJS, a, r, b, q jsval.Value) string {
	sa, sr, ta, tr := jsval.ToNumber(a), jsval.ToNumber(r), jsval.ToNumber(b), jsval.ToNumber(q)
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

func diagonalX(j linkJS, sx, sy, tx, ty jsval.Value) string {
	m := n(jsval.ToNumber(j.plus(sx, tx)) / 2)
	return cat("M", j.str(sx), ",", j.str(sy), "C", m, ",", j.str(sy), " ", m, ",", j.str(ty), " ", j.str(tx), ",", j.str(ty))
}

func diagonalY(j linkJS, sx, sy, tx, ty jsval.Value) string {
	m := n(jsval.ToNumber(j.plus(sy, ty)) / 2)
	return cat("M", j.str(sx), ",", j.str(sy), "C", j.str(sx), ",", m, " ", j.str(tx), ",", m, " ", j.str(tx), ",", j.str(ty))
}

func diagonalR(_ linkJS, a, r, b, q jsval.Value) string {
	sa, sr, ta, tr := jsval.ToNumber(a), jsval.ToNumber(r), jsval.ToNumber(b), jsval.ToNumber(q)
	sc, ss := jsmath.Cos(sa), jsmath.Sin(sa)
	tc, ts := jsmath.Cos(ta), jsmath.Sin(ta)
	mr := (sr + tr) / 2
	return cat("M", n(sr*sc), ",", n(sr*ss), "C", n(mr*sc), ",", n(mr*ss), " ", n(mr*tc), ",", n(mr*ts), " ", n(tr*tc), ",", n(tr*ts))
}

type linkPathFn func(j linkJS, a, b, c, d jsval.Value) string

var linkPaths = map[string]linkPathFn{
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
