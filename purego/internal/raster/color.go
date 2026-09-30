package raster

import (
	"math"
	"strconv"
	"strings"
)

// rgba is a non-premultiplied colour with float alpha.
type rgba struct {
	r, g, b uint8
	a       float32
}

var black = rgba{0, 0, 0, 1}

var namedColors = map[string]uint32{
	"aliceblue": 0xf0f8ff, "antiquewhite": 0xfaebd7, "aqua": 0x00ffff, "aquamarine": 0x7fffd4,
	"azure": 0xf0ffff, "beige": 0xf5f5dc, "bisque": 0xffe4c4, "black": 0x000000,
	"blanchedalmond": 0xffebcd, "blue": 0x0000ff, "blueviolet": 0x8a2be2, "brown": 0xa52a2a,
	"burlywood": 0xdeb887, "cadetblue": 0x5f9ea0, "chartreuse": 0x7fff00, "chocolate": 0xd2691e,
	"coral": 0xff7f50, "cornflowerblue": 0x6495ed, "cornsilk": 0xfff8dc, "crimson": 0xdc143c,
	"cyan": 0x00ffff, "darkblue": 0x00008b, "darkcyan": 0x008b8b, "darkgoldenrod": 0xb8860b,
	"darkgray": 0xa9a9a9, "darkgreen": 0x006400, "darkgrey": 0xa9a9a9, "darkkhaki": 0xbdb76b,
	"darkmagenta": 0x8b008b, "darkolivegreen": 0x556b2f, "darkorange": 0xff8c00,
	"darkorchid": 0x9932cc, "darkred": 0x8b0000, "darksalmon": 0xe9967a, "darkseagreen": 0x8fbc8f,
	"darkslateblue": 0x483d8b, "darkslategray": 0x2f4f4f, "darkslategrey": 0x2f4f4f,
	"darkturquoise": 0x00ced1, "darkviolet": 0x9400d3, "deeppink": 0xff1493,
	"deepskyblue": 0x00bfff, "dimgray": 0x696969, "dimgrey": 0x696969, "dodgerblue": 0x1e90ff,
	"firebrick": 0xb22222, "floralwhite": 0xfffaf0, "forestgreen": 0x228b22, "fuchsia": 0xff00ff,
	"gainsboro": 0xdcdcdc, "ghostwhite": 0xf8f8ff, "gold": 0xffd700, "goldenrod": 0xdaa520,
	"gray": 0x808080, "grey": 0x808080, "green": 0x008000, "greenyellow": 0xadff2f,
	"honeydew": 0xf0fff0, "hotpink": 0xff69b4, "indianred": 0xcd5c5c, "indigo": 0x4b0082,
	"ivory": 0xfffff0, "khaki": 0xf0e68c, "lavender": 0xe6e6fa, "lavenderblush": 0xfff0f5,
	"lawngreen": 0x7cfc00, "lemonchiffon": 0xfffacd, "lightblue": 0xadd8e6, "lightcoral": 0xf08080,
	"lightcyan": 0xe0ffff, "lightgoldenrodyellow": 0xfafad2, "lightgray": 0xd3d3d3,
	"lightgreen": 0x90ee90, "lightgrey": 0xd3d3d3, "lightpink": 0xffb6c1, "lightsalmon": 0xffa07a,
	"lightseagreen": 0x20b2aa, "lightskyblue": 0x87cefa, "lightslategray": 0x778899,
	"lightslategrey": 0x778899, "lightsteelblue": 0xb0c4de, "lightyellow": 0xffffe0,
	"lime": 0x00ff00, "limegreen": 0x32cd32, "linen": 0xfaf0e6, "magenta": 0xff00ff,
	"maroon": 0x800000, "mediumaquamarine": 0x66cdaa, "mediumblue": 0x0000cd,
	"mediumorchid": 0xba55d3, "mediumpurple": 0x9370db, "mediumseagreen": 0x3cb371,
	"mediumslateblue": 0x7b68ee, "mediumspringgreen": 0x00fa9a, "mediumturquoise": 0x48d1cc,
	"mediumvioletred": 0xc71585, "midnightblue": 0x191970, "mintcream": 0xf5fffa,
	"mistyrose": 0xffe4e1, "moccasin": 0xffe4b5, "navajowhite": 0xffdead, "navy": 0x000080,
	"oldlace": 0xfdf5e6, "olive": 0x808000, "olivedrab": 0x6b8e23, "orange": 0xffa500,
	"orangered": 0xff4500, "orchid": 0xda70d6, "palegoldenrod": 0xeee8aa, "palegreen": 0x98fb98,
	"paleturquoise": 0xafeeee, "palevioletred": 0xdb7093, "papayawhip": 0xffefd5,
	"peachpuff": 0xffdab9, "peru": 0xcd853f, "pink": 0xffc0cb, "plum": 0xdda0dd,
	"powderblue": 0xb0e0e6, "purple": 0x800080, "rebeccapurple": 0x663399, "red": 0xff0000,
	"rosybrown": 0xbc8f8f, "royalblue": 0x4169e1, "saddlebrown": 0x8b4513, "salmon": 0xfa8072,
	"sandybrown": 0xf4a460, "seagreen": 0x2e8b57, "seashell": 0xfff5ee, "sienna": 0xa0522d,
	"silver": 0xc0c0c0, "skyblue": 0x87ceeb, "slateblue": 0x6a5acd, "slategray": 0x708090,
	"slategrey": 0x708090, "snow": 0xfffafa, "springgreen": 0x00ff7f, "steelblue": 0x4682b4,
	"tan": 0xd2b48c, "teal": 0x008080, "thistle": 0xd8bfd8, "tomato": 0xff6347,
	"turquoise": 0x40e0d0, "violet": 0xee82ee, "wheat": 0xf5deb3, "white": 0xffffff,
	"whitesmoke": 0xf5f5f5, "yellow": 0xffff00, "yellowgreen": 0x9acd32,
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// parseColor parses a CSS colour. ok is false for anything unrecognised
// (including currentColor, which the caller resolves).
func parseColor(s string) (c rgba, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if s[0] == '#' {
		h := s[1:]
		var v [8]int
		for i := 0; i < len(h); i++ {
			if i >= 8 {
				return c, false
			}
			v[i] = hexVal(h[i])
			if v[i] < 0 {
				return c, false
			}
		}
		switch len(h) {
		case 3:
			return rgba{uint8(v[0] * 17), uint8(v[1] * 17), uint8(v[2] * 17), 1}, true
		case 4:
			return rgba{uint8(v[0] * 17), uint8(v[1] * 17), uint8(v[2] * 17), float32(v[3]*17) / 255}, true
		case 6:
			return rgba{uint8(v[0]<<4 | v[1]), uint8(v[2]<<4 | v[3]), uint8(v[4]<<4 | v[5]), 1}, true
		case 8:
			return rgba{uint8(v[0]<<4 | v[1]), uint8(v[2]<<4 | v[3]), uint8(v[4]<<4 | v[5]), float32(v[6]<<4|v[7]) / 255}, true
		}
		return c, false
	}
	if i := strings.IndexByte(s, '('); i > 0 && strings.HasSuffix(s, ")") {
		fn := strings.ToLower(strings.TrimSpace(s[:i]))
		args := splitColorArgs(s[i+1 : len(s)-1])
		switch fn {
		case "rgb", "rgba":
			if len(args) < 3 || len(args) > 4 {
				return c, false
			}
			var ch [3]uint8
			for k := 0; k < 3; k++ {
				v, pct, ok := parseNumPct(args[k])
				if !ok {
					return c, false
				}
				if pct {
					v = v * 255 / 100
				}
				ch[k] = clampByte(v)
			}
			a := float32(1)
			if len(args) == 4 {
				av, pct, ok := parseNumPct(args[3])
				if !ok {
					return c, false
				}
				if pct {
					av /= 100
				}
				a = float32(math.Max(0, math.Min(1, av)))
			}
			return rgba{ch[0], ch[1], ch[2], a}, true
		case "hsl", "hsla":
			if len(args) < 3 || len(args) > 4 {
				return c, false
			}
			h, _, ok1 := parseNumPct(strings.TrimSuffix(args[0], "deg"))
			sat, _, ok2 := parseNumPct(args[1])
			l, _, ok3 := parseNumPct(args[2])
			if !ok1 || !ok2 || !ok3 {
				return c, false
			}
			a := float32(1)
			if len(args) == 4 {
				av, pct, ok := parseNumPct(args[3])
				if !ok {
					return c, false
				}
				if pct {
					av /= 100
				}
				a = float32(math.Max(0, math.Min(1, av)))
			}
			r, g, b := hslToRGB(h, sat/100, l/100)
			return rgba{r, g, b, a}, true
		}
		return c, false
	}
	ls := s
	for i := 0; i < len(ls); i++ {
		if ls[i] >= 'A' && ls[i] <= 'Z' {
			ls = strings.ToLower(ls)
			break
		}
	}
	if ls == "transparent" {
		return rgba{0, 0, 0, 0}, true
	}
	if v, ok := namedColors[ls]; ok {
		return rgba{uint8(v >> 16), uint8(v >> 8), uint8(v), 1}, true
	}
	return c, false
}

func splitColorArgs(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '/' || r == '\n' })
	return f
}

func parseNumPct(s string) (v float64, pct bool, ok bool) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		pct = true
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false, false
	}
	return v, pct, true
}

func clampByte(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	h /= 360
	s = math.Max(0, math.Min(1, s))
	l = math.Max(0, math.Min(1, l))
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	f := func(t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return clampByte(f(h+1.0/3) * 255), clampByte(f(h) * 255), clampByte(f(h-1.0/3) * 255)
}
