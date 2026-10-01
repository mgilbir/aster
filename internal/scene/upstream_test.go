package scene

import (
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// Replays of upstream's own tests (see internal/upstream): d3-path and vega-scenegraph's path parser.

// callPath applies a recorded method of d3-path's Path to p. ok is false for a method the engine's path
// does not have (arcTo).
func callPath(p *StringPath, method string, args []any) (ok bool) {
	n := func(i int) float64 {
		if i < len(args) {
			return upstream.Number(args[i])
		}
		return upstream.Number(upstream.Undefined())
	}
	switch method {
	case "moveTo":
		p.MoveTo(n(0), n(1))
	case "lineTo":
		p.LineTo(n(0), n(1))
	case "closePath":
		p.ClosePath()
	case "quadraticCurveTo":
		p.QuadraticCurveTo(n(0), n(1), n(2), n(3))
	case "bezierCurveTo":
		p.BezierCurveTo(n(0), n(1), n(2), n(3), n(4), n(5))
	case "rect":
		p.Rect(n(0), n(1), n(2), n(3))
	case "arc":
		p.Arc(n(0), n(1), n(2), n(3), n(4), upstream.ToValue(argAt(args, 5)).IsTruthy())
	default:
		return false
	}
	return true
}

func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return upstream.Undefined()
}

func TestUpstreamD3Path(t *testing.T) {
	r := upstream.Start(t, "d3-path")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		var p StringPath
		switch c.Fn {
		case "path()":
		case "pathRound()":
			digits := 3.0 // pathRound(digits = 3)
			if len(c.ConstructedWith) > 0 && !upstream.IsUndefined(c.ConstructedWith[0]) {
				digits = upstream.Number(c.ConstructedWith[0])
			}
			p.SetDigits(int(digits))
		default:
			r.Skip("constructions and unmapped " + c.Fn)
			continue
		}
		replayed := true
		for _, step := range c.ChainSteps() {
			if !callPath(&p, step.Method, step.Args) {
				replayed = false
				break
			}
		}
		if !replayed {
			r.Skip("paths built with arcTo (the engine's has none)")
			continue
		}
		switch c.Method {
		case "toString":
			if p.Err() != nil {
				r.Skip("a path that already failed")
				continue
			}
			r.Check(c, p.String(), false)
		default:
			if !callPath(&p, c.Method, c.Args) {
				r.Skip("unmapped method " + c.Method)
				continue
			}
			// a void method: the answer is undefined, unless it throws (a negative radius)
			if p.Err() != nil {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, upstream.Undefined(), false)
		}
	}
	r.Done(100)
}

func TestUpstreamVegaScenegraphPathParse(t *testing.T) {
	r := upstream.Start(t, "vega-scenegraph")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "pathParse" {
			continue
		}
		s, ok := argAt(c.Args, 0).(string)
		if !ok {
			r.Skip("a path that is not a string")
			continue
		}
		cmds, err := ParsePath(s)
		if err != nil {
			r.Check(c, nil, true)
			continue
		}
		out := make([]any, len(cmds))
		for j, cmd := range cmds {
			row := []any{string(rune(cmd.Cmd))}
			for k := 0; k < int(cmd.N); k++ {
				row = append(row, upstream.Enc(cmd.P[k]))
			}
			out[j] = row
		}
		r.Check(c, out, false)
	}
	r.Done(30)
}
