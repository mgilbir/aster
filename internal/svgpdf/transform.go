package svgpdf

import (
	"math"
	"regexp"
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
// ok is false for one that does not parse, which the caller ignores, as SVG
// draws an element whose transform is invalid untransformed.
func parseTransform(s string) (Matrix, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Identity(), true
	}
	if m, ok := parseTransformFast(s); ok {
		return m, true
	}
	return parseTransformRegexp(s)
}

// parseTransformRegexp is parseTransform on a regular expression: the
// reference for what a transform means.
func parseTransformRegexp(s string) (Matrix, bool) {
	matches := transformFuncRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return Matrix{}, false
	}
	// Reject stray non-whitespace between/around the recognized functions so
	// malformed input is refused instead of being partially applied.
	last := 0
	var leftover strings.Builder
	for _, loc := range matches {
		leftover.WriteString(s[last:loc[0]])
		last = loc[1]
	}
	leftover.WriteString(s[last:])
	if strings.TrimFunc(leftover.String(), func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' || r == '\r' }) != "" {
		return Matrix{}, false
	}

	var ts []Matrix
	for _, loc := range matches {
		name := s[loc[2]:loc[3]]
		args, ok := parseNumberList(s[loc[4]:loc[5]])
		if !ok {
			return Matrix{}, false
		}
		t, ok := transformFunc(name, args)
		if !ok {
			return Matrix{}, false
		}
		ts = append(ts, t)
	}
	return composeTransforms(ts), true
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

// transformFunc is the matrix of one transform function applied to args; ok
// is false for an unknown function or a wrong number of arguments.
func transformFunc(name string, args []float64) (Matrix, bool) {
	var t Matrix
	switch name {
	case "translate":
		switch len(args) {
		case 1:
			t = Matrix{A: 1, D: 1, E: args[0]}
		case 2:
			t = Matrix{A: 1, D: 1, E: args[0], F: args[1]}
		default:
			return Matrix{}, false
		}
	case "scale":
		switch len(args) {
		case 1:
			t = Matrix{A: args[0], D: args[0]}
		case 2:
			t = Matrix{A: args[0], D: args[1]}
		default:
			return Matrix{}, false
		}
	case "rotate":
		var cx, cy float64
		switch len(args) {
		case 1:
		case 3:
			cx, cy = args[1], args[2]
		default:
			return Matrix{}, false
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
			return Matrix{}, false
		}
		t = Matrix{A: args[0], B: args[1], C: args[2], D: args[3], E: args[4], F: args[5]}
	case "skewX":
		if len(args) != 1 {
			return Matrix{}, false
		}
		t = Matrix{A: 1, D: 1, C: math.Tan(args[0] * math.Pi / 180)}
	case "skewY":
		if len(args) != 1 {
			return Matrix{}, false
		}
		t = Matrix{A: 1, D: 1, B: math.Tan(args[0] * math.Pi / 180)}
	default:
		return Matrix{}, false
	}
	return t, true
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
		nums, ok := appendNumberList(buf[:0], args)
		if !ok || len(nums) > len(buf) {
			return Matrix{}, false
		}
		if funcs == len(ts) {
			return Matrix{}, false
		}
		t, ok := transformFunc(name, nums)
		if !ok {
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

// isListSep reports whether b separates the numbers of a list.
func isListSep(b byte) bool {
	return b == ',' || b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
