package scene

import (
	"errors"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsmath"
)

// PathCmd is one parsed SVG path command with its parameters. Cmd keeps the
// original letter: upper case is absolute, lower case relative.
type PathCmd struct {
	Cmd byte
	N   uint8
	P   [7]float64
}

// ErrInvalidPath reports a malformed SVG path string.
var ErrInvalidPath = errors.New("scene: invalid SVG path")

var errPathParamCount = errors.New("scene: Invalid SVG path, incorrect parameter count")
var errPathParamType = errors.New("scene: Invalid SVG path, incorrect parameter type")

func paramCount(lower byte) int {
	switch lower {
	case 'm', 'l', 't':
		return 2
	case 'h', 'v':
		return 1
	case 'z':
		return 0
	case 'c':
		return 6
	case 's', 'q':
		return 4
	case 'a':
		return 7
	}
	return -1
}

func isPathCmdLetter(c byte) bool {
	switch c | 0x20 {
	case 'm', 'l', 'h', 'v', 'z', 'c', 's', 'q', 't', 'a':
		return c < 0x80
	}
	return false
}

// isJSSpace is the set of code points JavaScript's `\s` and String.trim treat
// as white space.
func isJSSpace(r rune) bool {
	switch {
	case r == ' ' || (r >= '\t' && r <= '\r'):
		return true
	case r < 0x80:
		return false
	}
	switch r {
	case 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// skipSpace returns the index after any white space starting at i.
func skipSpace(s string, i int) int {
	for i < len(s) {
		c := s[i]
		if c < 0x80 {
			if !isJSSpace(rune(c)) {
				return i
			}
			i++
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			return i
		}
		i += w
	}
	return i
}

// scanNumber matches vega's numberPattern at s[i:]:
// [+-]?(\d*\.\d+|\d+\.|\d+)([eE][+-]?\d+)? and returns the end index.
func scanNumber(s string, i int) (int, bool) {
	j := i
	if j < len(s) && (s[j] == '+' || s[j] == '-') {
		j++
	}
	d1 := j
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	intDigits := j - d1
	if j < len(s) && s[j] == '.' {
		k := j + 1
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		switch {
		case k-(j+1) > 0:
			j = k
		case intDigits > 0:
			j = j + 1
		default:
			return i, false
		}
	} else if intDigits == 0 {
		return i, false
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		e := k
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > e {
			j = k
		}
	}
	return j, true
}

// ParsePath parses SVG path data the way vega's path parser does: a command
// letter followed by parameters, implicit repetition (with `m` repeating as
// `l`), arc flags read as single 0/1 characters, and no validation that the
// path starts with a moveto.
func ParsePath(path string) ([]PathCmd, error) {
	var cmds []PathCmd
	i := 0
	for i < len(path) {
		// Find the next command letter (vega's regex skips leading junk).
		for i < len(path) && !isPathCmdLetter(path[i]) {
			i++
		}
		if i >= len(path) {
			break
		}
		cmd := path[i]
		j := i + 1
		for j < len(path) && !isPathCmdLetter(path[j]) {
			j++
		}
		seg := path[i+1 : j]
		i = j

		lower := cmd | 0x20
		pc := paramCount(lower)
		seg = trimJS(seg)

		params, err := parseParams(lower, pc, seg)
		if err != nil {
			return nil, err
		}
		count := len(params)
		if count < pc {
			return nil, errPathParamCount
		}
		var c PathCmd
		c.Cmd, c.N = cmd, uint8(pc)
		copy(c.P[:], params[:pc])
		cmds = append(cmds, c)
		if count == pc {
			continue
		}
		if lower == 'm' {
			if cmd == 'M' {
				cmd = 'L'
			} else {
				cmd = 'l'
			}
		}
		for k := pc; k < count; k += pc {
			c = PathCmd{Cmd: cmd, N: uint8(pc)}
			copy(c.P[:], params[k:k+pc])
			cmds = append(cmds, c)
		}
	}
	return cmds, nil
}

func trimJS(s string) string {
	i := skipSpace(s, 0)
	s = s[i:]
	end := len(s)
	for end > 0 {
		r, w := utf8.DecodeLastRuneInString(s[:end])
		if !isJSSpace(r) {
			break
		}
		end -= w
	}
	return s[:end]
}

func parseParams(lower byte, pc int, seg string) ([]float64, error) {
	var params []float64
	idx := 0
	for pc > 0 && idx < len(seg) {
		for k := 0; k < pc; k++ {
			if lower == 'a' && (k == 3 || k == 4) {
				if idx >= len(seg) || (seg[idx] != '0' && seg[idx] != '1') {
					return nil, errPathParamType
				}
				params = append(params, float64(seg[idx]-'0'))
				idx++
			} else {
				end, ok := scanNumber(seg, idx)
				if !ok {
					return nil, errPathParamType
				}
				f, _ := strconv.ParseFloat(seg[idx:end], 64) // range errors give ±Inf, as JS does
				params = append(params, f)
				idx = end
			}
			// Optional separator: \s+,?\s* or ,\s*
			if idx < len(seg) {
				k2 := skipSpace(seg, idx)
				if k2 > idx {
					idx = k2
					if idx < len(seg) && seg[idx] == ',' {
						idx = skipSpace(seg, idx+1)
					}
				} else if seg[idx] == ',' {
					idx = skipSpace(seg, idx+1)
				}
			}
		}
	}
	return params, nil
}

// RenderPath replays parsed path commands into ctx, offset by (l, t) and scaled
// by (sx, sy), converting elliptical arcs to cubic Béziers the way vega does
// (segments of at most a quarter turn). It follows vega's renderer including
// its handling of smooth curve reflections.
func RenderPath(ctx PathContext, path []PathCmd, l, t, sx, sy float64) error {
	var (
		x, y, controlX, controlY   float64
		tempX, tempY               float64
		tempControlX, tempControlY float64
		anchorX, anchorY           float64
		prev                       byte
		havePrev                   bool
	)
	// tempControl* are undefined (NaN in arithmetic) until the first `t`.
	tempControlX, tempControlY = math.NaN(), math.NaN()
	scaled := sx != 1 || sy != 1

	for i := range path {
		cur := path[i]
		p := &cur.P
		if scaled {
			scaleCmd(&cur, sx, sy)
		}
		switch cur.Cmd {
		case 'l':
			x += p[0]
			y += p[1]
			ctx.LineTo(x+l, y+t)
		case 'L':
			x, y = p[0], p[1]
			ctx.LineTo(x+l, y+t)
		case 'h':
			x += p[0]
			ctx.LineTo(x+l, y+t)
		case 'H':
			x = p[0]
			ctx.LineTo(x+l, y+t)
		case 'v':
			y += p[0]
			ctx.LineTo(x+l, y+t)
		case 'V':
			y = p[0]
			ctx.LineTo(x+l, y+t)
		case 'm':
			x += p[0]
			y += p[1]
			anchorX, anchorY = x, y
			ctx.MoveTo(x+l, y+t)
		case 'M':
			x, y = p[0], p[1]
			anchorX, anchorY = x, y
			ctx.MoveTo(x+l, y+t)
		case 'c':
			tempX = x + p[4]
			tempY = y + p[5]
			controlX = x + p[2]
			controlY = y + p[3]
			ctx.BezierCurveTo(x+p[0]+l, y+p[1]+t, controlX+l, controlY+t, tempX+l, tempY+t)
			x, y = tempX, tempY
		case 'C':
			x, y = p[4], p[5]
			controlX, controlY = p[2], p[3]
			ctx.BezierCurveTo(p[0]+l, p[1]+t, controlX+l, controlY+t, x+l, y+t)
		case 's':
			tempX = x + p[2]
			tempY = y + p[3]
			controlX = 2*x - controlX
			controlY = 2*y - controlY
			ctx.BezierCurveTo(controlX+l, controlY+t, x+p[0]+l, y+p[1]+t, tempX+l, tempY+t)
			controlX = x + p[0]
			controlY = y + p[1]
			x, y = tempX, tempY
		case 'S':
			tempX, tempY = p[2], p[3]
			controlX = 2*x - controlX
			controlY = 2*y - controlY
			ctx.BezierCurveTo(controlX+l, controlY+t, p[0]+l, p[1]+t, tempX+l, tempY+t)
			x, y = tempX, tempY
			controlX, controlY = p[0], p[1]
		case 'q':
			tempX = x + p[2]
			tempY = y + p[3]
			controlX = x + p[0]
			controlY = y + p[1]
			ctx.QuadraticCurveTo(controlX+l, controlY+t, tempX+l, tempY+t)
			x, y = tempX, tempY
		case 'Q':
			tempX, tempY = p[2], p[3]
			ctx.QuadraticCurveTo(p[0]+l, p[1]+t, tempX+l, tempY+t)
			x, y = tempX, tempY
			controlX, controlY = p[0], p[1]
		case 't':
			tempX = x + p[0]
			tempY = y + p[1]
			// Upstream reads `previous[0]`; with scaling active the shared
			// scratch command array has already been overwritten with the
			// current letter, so the previous command reads as 't' itself.
			if !havePrev {
				return ErrInvalidPath // TypeError upstream: no previous command
			}
			pl := prev
			if scaled {
				pl = 't'
			}
			switch pl {
			case 't':
				controlX = 2*x - tempControlX
				controlY = 2*y - tempControlY
			case 'q':
				controlX = 2*x - controlX
				controlY = 2*y - controlY
			case 'Q', 'T':
				// keep the control point as last set: `previous[0]` matched
				// but neither reflection branch applies.
			default:
				controlX, controlY = x, y
			}
			tempControlX, tempControlY = controlX, controlY
			ctx.QuadraticCurveTo(controlX+l, controlY+t, tempX+l, tempY+t)
			x, y = tempX, tempY
			controlX = x + p[0]
			controlY = y + p[1]
		case 'T':
			tempX, tempY = p[0], p[1]
			controlX = 2*x - controlX
			controlY = 2*y - controlY
			ctx.QuadraticCurveTo(controlX+l, controlY+t, tempX+l, tempY+t)
			x, y = tempX, tempY
		case 'a':
			drawArc(ctx, x+l, y+t, p[0], p[1], p[2], p[3], p[4], p[5]+x+l, p[6]+y+t)
			x += p[5]
			y += p[6]
		case 'A':
			drawArc(ctx, x+l, y+t, p[0], p[1], p[2], p[3], p[4], p[5]+l, p[6]+t)
			x, y = p[5], p[6]
		case 'z', 'Z':
			x, y = anchorX, anchorY
			ctx.ClosePath()
		}
		prev, havePrev = cur.Cmd, true
	}
	return nil
}

// scaleCmd applies vega's per-command scaling: arcs scale radii and end point,
// h/v scale one axis, everything else alternates x/y.
func scaleCmd(c *PathCmd, sx, sy float64) {
	p := &c.P
	switch c.Cmd {
	case 'a', 'A':
		p[0] = float64(p[0] * sx)
		p[1] = float64(p[1] * sy)
		p[5] = float64(p[5] * sx)
		p[6] = float64(p[6] * sy)
	case 'h', 'H':
		p[0] = float64(p[0] * sx)
	case 'v', 'V':
		p[0] = float64(p[0] * sy)
	default:
		for i := 0; i < int(c.N); i++ {
			if i%2 == 0 {
				p[i] = float64(p[i] * sx)
			} else {
				p[i] = float64(p[i] * sy)
			}
		}
	}
}

// drawArc converts an SVG elliptical arc from (ox, oy) to (x, y) into cubic
// Bézier segments (the algorithm from Inkscape's svgtopdf, as used by vega).
func drawArc(ctx PathContext, ox, oy, rx, ry, rot, large, sweep, x, y float64) {
	// Products are wrapped in float64() so that arm64 does not fuse them into
	// FMAs, which would differ from V8 in the last bit.
	th := rot * degToRad
	sinTh, cosTh := jsmath.Sin(th), jsmath.Cos(th)
	rx, ry = math.Abs(rx), math.Abs(ry)
	px := float64(cosTh*(ox-x)*0.5) + float64(sinTh*(oy-y)*0.5)
	py := float64(cosTh*(oy-y)*0.5) - float64(sinTh*(ox-x)*0.5)
	pl := float64(px*px)/float64(rx*rx) + float64(py*py)/float64(ry*ry)
	if pl > 1 {
		pl = math.Sqrt(pl)
		rx *= pl
		ry *= pl
	}

	a00, a01 := cosTh/rx, sinTh/rx
	a10, a11 := -sinTh/ry, cosTh/ry
	x0 := float64(a00*ox) + float64(a01*oy)
	y0 := float64(a10*ox) + float64(a11*oy)
	x1 := float64(a00*x) + float64(a01*y)
	y1 := float64(a10*x) + float64(a11*y)

	d := float64((x1-x0)*(x1-x0)) + float64((y1-y0)*(y1-y0))
	sfSq := 1/d - 0.25
	if sfSq < 0 {
		sfSq = 0
	}
	sf := math.Sqrt(sfSq)
	if sweep == large {
		sf = -sf
	}
	xc := 0.5*(x0+x1) - float64(sf*(y1-y0))
	yc := 0.5*(y0+y1) + float64(sf*(x1-x0))

	th0 := jsmath.Atan2(y0-yc, x0-xc)
	th1 := jsmath.Atan2(y1-yc, x1-xc)
	thArc := th1 - th0
	if thArc < 0 && sweep == 1 {
		thArc += tau
	} else if thArc > 0 && sweep == 0 {
		thArc -= tau
	}

	fsegs := math.Ceil(math.Abs(thArc / (halfPi + 0.001)))
	switch {
	case fsegs != fsegs || fsegs < 1:
		// NaN geometry yields no segments upstream (`i < NaN` is false).
		return
	case fsegs > 64:
		fsegs = 64 // a finite arc needs at most 4; this only guards Infinity
	}
	segs := int(fsegs)
	for i := 0; i < segs; i++ {
		th2 := th0 + float64(float64(i)*thArc)/fsegs
		th3 := th0 + float64(float64(i+1)*thArc)/fsegs
		bx := arcBezier(xc, yc, th2, th3, rx, ry, sinTh, cosTh)
		ctx.BezierCurveTo(bx[0], bx[1], bx[2], bx[3], bx[4], bx[5])
	}
}

func arcBezier(cx, cy, th0, th1, rx, ry, sinTh, cosTh float64) [6]float64 {
	a00, a01 := float64(cosTh*rx), float64(-sinTh*ry)
	a10, a11 := float64(sinTh*rx), float64(cosTh*ry)

	cosTh0, sinTh0 := jsmath.Cos(th0), jsmath.Sin(th0)
	cosTh1, sinTh1 := jsmath.Cos(th1), jsmath.Sin(th1)

	thHalf := 0.5 * (th1 - th0)
	sinThH2 := jsmath.Sin(thHalf * 0.5)
	t := (8.0 / 3.0) * sinThH2 * sinThH2 / jsmath.Sin(thHalf)
	x1 := cx + cosTh0 - float64(t*sinTh0)
	y1 := cy + sinTh0 + float64(t*cosTh0)
	x3 := cx + cosTh1
	y3 := cy + sinTh1
	x2 := x3 + float64(t*sinTh1)
	y2 := y3 - float64(t*cosTh1)
	return [6]float64{
		float64(a00*x1) + float64(a01*y1), float64(a10*x1) + float64(a11*y1),
		float64(a00*x2) + float64(a01*y2), float64(a10*x2) + float64(a11*y2),
		float64(a00*x3) + float64(a01*y3), float64(a10*x3) + float64(a11*y3),
	}
}
