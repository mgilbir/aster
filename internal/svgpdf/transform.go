package svgpdf

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Matrix is a 2D affine transform in the same layout SVG and PDF use:
//
//	| A C E |
//	| B D F |
//	| 0 0 1 |
//
// mapping (x, y) to (A*x + C*y + E, B*x + D*y + F).
type Matrix struct {
	A, B, C, D, E, F float64
}

// Identity returns the identity matrix.
func Identity() Matrix {
	return Matrix{A: 1, D: 1}
}

// IsIdentity reports whether m is exactly the identity transform.
func (m Matrix) IsIdentity() bool {
	return m == Identity()
}

// Mul returns m * n, i.e. the transform that applies n first, then m.
// This matches SVG semantics where "translate(...) rotate(...)" means the
// rotation is applied to points before the translation.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.C*n.B,
		B: m.B*n.A + m.D*n.B,
		C: m.A*n.C + m.C*n.D,
		D: m.B*n.C + m.D*n.D,
		E: m.A*n.E + m.C*n.F + m.E,
		F: m.B*n.E + m.D*n.F + m.F,
	}
}

// Apply transforms the point (x, y).
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

var transformFuncRe = regexp.MustCompile(`([a-zA-Z]+)\s*\(([^)]*)\)`)

// parseTransform parses an SVG transform attribute (a sequence of
// translate/rotate/scale/matrix/skewX/skewY functions) into a single matrix.
func parseTransform(s string) (Matrix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Identity(), nil
	}
	if m, ok := parseTransformFast(s); ok {
		return m, nil
	}
	return parseTransformRegexp(s)
}

// parseTransformRegexp is parseTransform on a regular expression: the
// reference for what a transform means and the source of every error.
func parseTransformRegexp(s string) (Matrix, error) {
	matches := transformFuncRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return Matrix{}, fmt.Errorf("svgpdf: invalid transform %q", s)
	}
	// Reject stray non-whitespace between/around the recognized functions so
	// malformed input errors instead of being partially applied.
	last := 0
	var leftover strings.Builder
	for _, loc := range matches {
		leftover.WriteString(s[last:loc[0]])
		last = loc[1]
	}
	leftover.WriteString(s[last:])
	if strings.TrimFunc(leftover.String(), func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' || r == '\r' }) != "" {
		return Matrix{}, fmt.Errorf("svgpdf: invalid transform %q", s)
	}

	var ts []Matrix
	for _, loc := range matches {
		name := s[loc[2]:loc[3]]
		args, err := parseNumberList(s[loc[4]:loc[5]])
		if err != nil {
			return Matrix{}, fmt.Errorf("svgpdf: transform %s: %w", name, err)
		}
		t, err := transformFunc(name, args)
		if err != nil {
			return Matrix{}, err
		}
		ts = append(ts, t)
	}
	return composeTransforms(ts), nil
}

// composeTransforms multiplies the matrices of a transform list, first to
// last. Both parsers end here, so a transform is multiplied by one compiled
// copy of the arithmetic: the compiler may fuse the multiply-adds of an inlined
// Mul differently from one call site to the next, and the content stream
// carries these numbers.
//
//go:noinline
func composeTransforms(ts []Matrix) Matrix {
	m := Identity()
	for _, t := range ts {
		m = m.Mul(t)
	}
	return m
}

// transformFunc is the matrix of one transform function applied to args.
func transformFunc(name string, args []float64) (Matrix, error) {
	var t Matrix
	switch name {
	case "translate":
		switch len(args) {
		case 1:
			t = Matrix{A: 1, D: 1, E: args[0]}
		case 2:
			t = Matrix{A: 1, D: 1, E: args[0], F: args[1]}
		default:
			return Matrix{}, fmt.Errorf("svgpdf: translate expects 1 or 2 args, got %d", len(args))
		}
	case "scale":
		switch len(args) {
		case 1:
			t = Matrix{A: args[0], D: args[0]}
		case 2:
			t = Matrix{A: args[0], D: args[1]}
		default:
			return Matrix{}, fmt.Errorf("svgpdf: scale expects 1 or 2 args, got %d", len(args))
		}
	case "rotate":
		var cx, cy float64
		switch len(args) {
		case 1:
		case 3:
			cx, cy = args[1], args[2]
		default:
			return Matrix{}, fmt.Errorf("svgpdf: rotate expects 1 or 3 args, got %d", len(args))
		}
		rad := args[0] * math.Pi / 180
		sin, cos := math.Sin(rad), math.Cos(rad)
		t = Matrix{A: cos, B: sin, C: -sin, D: cos}
		if cx != 0 || cy != 0 {
			// rotate(a, cx, cy) = translate(cx,cy) rotate(a) translate(-cx,-cy)
			t = Matrix{A: 1, D: 1, E: cx, F: cy}.Mul(t).Mul(Matrix{A: 1, D: 1, E: -cx, F: -cy})
		}
	case "matrix":
		if len(args) != 6 {
			return Matrix{}, fmt.Errorf("svgpdf: matrix expects 6 args, got %d", len(args))
		}
		t = Matrix{A: args[0], B: args[1], C: args[2], D: args[3], E: args[4], F: args[5]}
	case "skewX":
		if len(args) != 1 {
			return Matrix{}, fmt.Errorf("svgpdf: skewX expects 1 arg, got %d", len(args))
		}
		t = Matrix{A: 1, D: 1, C: math.Tan(args[0] * math.Pi / 180)}
	case "skewY":
		if len(args) != 1 {
			return Matrix{}, fmt.Errorf("svgpdf: skewY expects 1 arg, got %d", len(args))
		}
		t = Matrix{A: 1, D: 1, B: math.Tan(args[0] * math.Pi / 180)}
	default:
		return Matrix{}, fmt.Errorf("svgpdf: unsupported transform function %q", name)
	}
	return t, nil
}

// parseTransformFast is parseTransformRegexp for the plain, valid forms Vega
// emits ("translate(12,3.5)", "rotate(-90)", ...): it scans the string once
// without allocating. ok is false for anything else, including every
// malformed or unsupported transform, which the regular expression then
// parses to the same matrix or to the error.
func parseTransformFast(s string) (m Matrix, ok bool) {
	var ts [8]Matrix
	funcs := 0
	for i := 0; ; {
		for i < len(s) && isListSep(s[i]) {
			i++
		}
		if i == len(s) {
			break
		}
		start := i
		for i < len(s) && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z') {
			i++
		}
		name := s[start:i]
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\f' || s[i] == '\r') {
			i++ // \s*
		}
		if name == "" || i == len(s) || s[i] != '(' {
			return Matrix{}, false
		}
		i++
		end := strings.IndexByte(s[i:], ')')
		if end < 0 {
			return Matrix{}, false
		}
		args := s[i : i+end]
		i += end + 1

		var buf [6]float64
		n := 0
		for len(args) > 0 {
			if isListSep(args[0]) {
				args = args[1:]
				continue
			}
			f := len(args)
			for j := 0; j < len(args); j++ {
				if isListSep(args[j]) {
					f = j
					break
				}
			}
			if n == len(buf) {
				return Matrix{}, false
			}
			v, err := strconv.ParseFloat(args[:f], 64)
			if err != nil {
				return Matrix{}, false
			}
			buf[n] = v
			n++
			args = args[f:]
		}
		if funcs == len(ts) {
			return Matrix{}, false
		}
		t, err := transformFunc(name, buf[:n])
		if err != nil {
			return Matrix{}, false
		}
		ts[funcs] = t
		funcs++
	}
	if funcs == 0 {
		return Matrix{}, false
	}
	return composeTransforms(ts[:funcs]), true
}

// isListSep reports whether b separates the numbers of a list, as in
// parseNumberList.
func isListSep(b byte) bool {
	return b == ',' || b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
