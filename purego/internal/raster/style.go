package raster

import (
	"math"
	"strconv"
	"strings"
)

type paintKind uint8

const (
	pNone paintKind = iota
	pColor
	pCurrent
	pURL
)

type paint struct {
	kind   paintKind
	c      rgba
	id     string
	fbKind paintKind // fallback after url(): none, colour or currentColor
	fb     rgba
}

const (
	anchorStart uint8 = iota
	anchorMiddle
	anchorEnd
)

// state is the computed style and geometry context for an element.
type state struct {
	ctm           matrix
	fill, stroke  paint
	fillOpacity   float64
	strokeOpacity float64
	strokeWidth   float64
	cap           lineCap
	join          lineJoin
	miter         float64
	dash          []float64
	dashOffset    float64
	evenOdd       bool
	clipEvenOdd   bool
	color         rgba
	visible       bool
	pixelated     bool
	crisp         bool       // shape-rendering: optimizeSpeed / crispEdges (no anti-aliasing)
	textCrisp     bool       // text-rendering: optimizeSpeed
	filterLinear  bool       // color-interpolation-filters: linearRGB (the default)
	paintOrder    [3]uint8   // 0 fill, 1 stroke, 2 markers, in painting order
	markers       *[3]string // marker-start, marker-mid, marker-end element ids (shared, copy on write)

	families      []string
	fontSize      float64
	fontWeight    int
	italic        bool
	anchor        uint8
	letterSpacing float64
	wordSpacing   float64
	decoration    uint8 // bit0 underline, bit1 overline, bit2 line-through
	baseline      string
	baselineShift string

	clip   *mask
	vw, vh float64 // viewport size for percentage lengths
}

func initialState(vw, vh float64) state {
	return state{
		ctm:           identity,
		fill:          paint{kind: pColor, c: black},
		stroke:        paint{kind: pNone},
		fillOpacity:   1,
		strokeOpacity: 1,
		strokeWidth:   1,
		miter:         4,
		color:         black,
		visible:       true,
		filterLinear:  true,
		paintOrder:    [3]uint8{0, 1, 2},
		families:      []string{"serif"},
		fontSize:      12,
		fontWeight:    400,
		vw:            vw,
		vh:            vh,
	}
}

// parseLength splits a CSS length into value and unit.
func parseLength(s string) (v float64, unit string, ok bool) {
	sc := numScanner{s: s}
	v, ok = sc.number()
	if !ok {
		return 0, "", false
	}
	unit = strings.TrimSpace(s[sc.i:])
	return v, unit, true
}

// toPx converts a length to user units. axis: 0 x, 1 y, 2 diagonal.
func (st *state) toPx(v float64, unit string, axis int) float64 {
	switch unit {
	case "", "px":
		return v
	case "pt":
		return v * 96 / 72
	case "pc":
		return v * 16
	case "mm":
		return v * 96 / 25.4
	case "cm":
		return v * 96 / 2.54
	case "in":
		return v * 96
	case "em":
		return v * st.fontSize
	case "ex":
		return v * st.fontSize / 2
	case "%":
		switch axis {
		case 0:
			return v / 100 * st.vw
		case 1:
			return v / 100 * st.vh
		}
		return v / 100 * math.Sqrt((st.vw*st.vw+st.vh*st.vh)/2)
	}
	return v
}

// length parses attribute s as a length, returning def when absent or invalid.
func (st *state) length(s string, axis int, def float64) float64 {
	if s == "" {
		return def
	}
	v, u, ok := parseLength(s)
	if !ok {
		return def
	}
	return st.toPx(v, u, axis)
}

func parseOpacity(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1, false
	}
	pct := false
	if strings.HasSuffix(s, "%") {
		pct = true
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v != v {
		return 1, false
	}
	if pct {
		v /= 100
	}
	if v < 0 {
		v = 0
	} else if v > 1 {
		v = 1
	}
	return v, true
}

func parsePaint(s string) (paint, bool) {
	s = strings.TrimSpace(s)
	switch s {
	case "":
		return paint{}, false
	case "none":
		return paint{kind: pNone}, true
	case "currentColor", "currentcolor":
		return paint{kind: pCurrent}, true
	}
	if strings.HasPrefix(s, "url(") {
		end := strings.IndexByte(s, ')')
		if end < 0 {
			return paint{}, false
		}
		ref := strings.TrimSpace(s[4:end])
		ref = strings.Trim(ref, `"'`)
		if !strings.HasPrefix(ref, "#") {
			ref = ""
		} else {
			ref = ref[1:]
		}
		p := paint{kind: pURL, id: ref}
		rest := strings.TrimSpace(s[end+1:])
		switch {
		case rest == "":
			p.fbKind = pNone
		case rest == "none":
			p.fbKind = pNone
		case rest == "currentColor" || rest == "currentcolor":
			p.fbKind = pCurrent
		default:
			if c, ok := parseColor(rest); ok {
				p.fbKind, p.fb = pColor, c
			}
		}
		return p, true
	}
	if c, ok := parseColor(s); ok {
		return paint{kind: pColor, c: c}, true
	}
	return paint{}, false
}

// parseURLRef extracts the fragment id from "url(#id)".
func parseURLRef(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "url(") {
		return "", false
	}
	end := strings.IndexByte(s, ')')
	if end < 0 {
		return "", false
	}
	ref := strings.Trim(strings.TrimSpace(s[4:end]), `"'`)
	if !strings.HasPrefix(ref, "#") {
		return "", false
	}
	return ref[1:], true
}

func parseFontFamilies(s string) []string {
	var out []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t\n,")
		if s == "" {
			break
		}
		var name string
		if s[0] == '"' || s[0] == '\'' {
			q := s[0]
			end := strings.IndexByte(s[1:], q)
			if end < 0 {
				name = s[1:]
				s = ""
			} else {
				name = s[1 : 1+end]
				s = s[end+2:]
			}
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				name, s = s, ""
			} else {
				name, s = s[:end], s[end+1:]
			}
			name = strings.TrimSpace(name)
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func parseFontWeight(s string, cur int) int {
	switch strings.TrimSpace(s) {
	case "normal":
		return 400
	case "bold":
		return 700
	case "bolder":
		if cur < 400 {
			return 400
		} else if cur < 600 {
			return 700
		}
		return 900
	case "lighter":
		if cur > 700 {
			return 700
		} else if cur > 500 {
			return 400
		}
		return 100
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 1 || v > 1000 {
		return cur
	}
	return int(v)
}

func parseDashArray(st *state, s string) []float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "none" {
		return nil
	}
	var out []float64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		v, u, ok := parseLength(f)
		if !ok {
			return nil
		}
		v = st.toPx(v, u, 2)
		if v < 0 || v != v || math.IsInf(v, 0) {
			return nil
		}
		out = append(out, v)
		if len(out) > 256 {
			return nil
		}
	}
	return out
}

var fontSizeKeywords = map[string]float64{
	"xx-small": 9, "x-small": 10, "small": 13, "medium": 16, "large": 18,
	"x-large": 24, "xx-large": 32,
}

// applyProps folds an element's presentation attributes and style declarations
// into st. Style declarations win over attributes (CSS precedence).
func (st *state) applyProps(n *node) {
	// font-size first: em-valued properties depend on the computed size.
	for _, list := range [2][]attr{n.attrs, n.style} {
		for _, a := range list {
			if a.id == aFontSize {
				st.applyProp(a.id, a.val)
			}
		}
	}
	for _, a := range n.attrs {
		if a.id != aFontSize {
			st.applyProp(a.id, a.val)
		}
	}
	for _, a := range n.style {
		if a.id != aFontSize {
			st.applyProp(a.id, a.val)
		}
	}
}

func (st *state) applyProp(id attrID, val string) {
	if val == "inherit" {
		return
	}
	switch id {
	case aFill:
		if p, ok := parsePaint(val); ok {
			st.fill = p
		}
	case aStroke:
		if p, ok := parsePaint(val); ok {
			st.stroke = p
		}
	case aFillOpacity:
		if v, ok := parseOpacity(val); ok {
			st.fillOpacity = v
		}
	case aStrokeOpacity:
		if v, ok := parseOpacity(val); ok {
			st.strokeOpacity = v
		}
	case aStrokeWidth:
		v, u, ok := parseLength(val)
		if ok {
			w := st.toPx(v, u, 2)
			if w >= 0 && !math.IsInf(w, 0) {
				st.strokeWidth = w
			}
		}
	case aStrokeLinecap:
		switch strings.TrimSpace(val) {
		case "butt":
			st.cap = capButt
		case "round":
			st.cap = capRound
		case "square":
			st.cap = capSquare
		}
	case aStrokeLinejoin:
		switch strings.TrimSpace(val) {
		case "miter", "miter-clip", "arcs":
			st.join = joinMiter
		case "round":
			st.join = joinRound
		case "bevel":
			st.join = joinBevel
		}
	case aStrokeMiterlimit:
		if v, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && v >= 1 && !math.IsInf(v, 0) {
			st.miter = v
		}
	case aStrokeDasharray:
		st.dash = parseDashArray(st, val)
	case aStrokeDashoffset:
		v, u, ok := parseLength(val)
		if ok {
			st.dashOffset = st.toPx(v, u, 2)
		}
	case aFillRule:
		st.evenOdd = strings.TrimSpace(val) == "evenodd"
	case aClipRule:
		st.clipEvenOdd = strings.TrimSpace(val) == "evenodd"
	case aColor:
		if c, ok := parseColor(val); ok {
			st.color = c
		}
	case aVisibility:
		switch strings.TrimSpace(val) {
		case "visible":
			st.visible = true
		case "hidden", "collapse":
			st.visible = false
		}
	case aFontFamily:
		if f := parseFontFamilies(val); len(f) > 0 {
			st.families = f
		}
	case aFontSize:
		val = strings.TrimSpace(val)
		if kw, ok := fontSizeKeywords[val]; ok {
			st.fontSize = kw
		} else if val == "larger" {
			st.fontSize *= 1.2
		} else if val == "smaller" {
			st.fontSize /= 1.2
		} else if v, u, ok := parseLength(val); ok {
			var sz float64
			switch u {
			case "%":
				sz = v / 100 * st.fontSize
			case "em":
				sz = v * st.fontSize
			case "ex":
				sz = v * st.fontSize / 2
			default:
				sz = st.toPx(v, u, 2)
			}
			if sz >= 0 && sz < 1e6 {
				st.fontSize = sz
			}
		}
	case aFontWeight:
		st.fontWeight = parseFontWeight(val, st.fontWeight)
	case aFontStyle:
		switch strings.TrimSpace(val) {
		case "italic", "oblique":
			st.italic = true
		case "normal":
			st.italic = false
		}
	case aTextAnchor:
		switch strings.TrimSpace(val) {
		case "start":
			st.anchor = anchorStart
		case "middle":
			st.anchor = anchorMiddle
		case "end":
			st.anchor = anchorEnd
		}
	case aLetterSpacing:
		if strings.TrimSpace(val) == "normal" {
			st.letterSpacing = 0
		} else if v, u, ok := parseLength(val); ok {
			st.letterSpacing = st.toPx(v, u, 0)
		}
	case aWordSpacing:
		if strings.TrimSpace(val) == "normal" {
			st.wordSpacing = 0
		} else if v, u, ok := parseLength(val); ok {
			st.wordSpacing = st.toPx(v, u, 0)
		}
	case aTextDecoration:
		var d uint8
		for _, f := range strings.Fields(val) {
			switch f {
			case "underline":
				d |= 1
			case "overline":
				d |= 2
			case "line-through":
				d |= 4
			}
		}
		st.decoration = d
	case aDominantBaseline:
		st.baseline = strings.TrimSpace(val)
	case aAlignmentBaseline:
		if st.baseline == "" {
			st.baseline = strings.TrimSpace(val)
		}
	case aBaselineShift:
		st.baselineShift = strings.TrimSpace(val)
	case aMarkerStart, aMarkerMid, aMarkerEnd:
		i := int(id - aMarkerStart)
		ref, _ := parseURLRef(val)
		var m [3]string
		if st.markers != nil {
			m = *st.markers
		}
		m[i] = ref
		if m == ([3]string{}) {
			st.markers = nil
		} else {
			st.markers = &m
		}
	case aPaintOrder:
		st.paintOrder = parsePaintOrder(val)
	case aShapeRendering:
		switch strings.TrimSpace(val) {
		case "optimizeSpeed", "crispEdges":
			st.crisp = true
		case "auto", "geometricPrecision":
			st.crisp = false
		}
	case aTextRendering:
		switch strings.TrimSpace(val) {
		case "optimizeSpeed":
			st.textCrisp = true
		case "auto", "optimizeLegibility", "geometricPrecision":
			st.textCrisp = false
		}
	case aColorInterpolationFilters:
		switch strings.TrimSpace(val) {
		case "sRGB":
			st.filterLinear = false
		case "linearRGB", "auto":
			st.filterLinear = true
		}
	case aImageRendering:
		switch strings.TrimSpace(val) {
		case "pixelated", "crisp-edges", "optimizeSpeed":
			st.pixelated = true
		case "auto", "smooth", "optimizeQuality", "high-quality":
			st.pixelated = false
		}
	}
}

// parseTransform parses an SVG transform list. ok is false when the syntax is
// invalid (the element is then rendered untransformed, as resvg does).
func parseTransform(s string) (matrix, bool) {
	m := identity
	sc := numScanner{s: s}
	for {
		sc.skipWS()
		if sc.i >= len(s) {
			return m, true
		}
		if s[sc.i] == ',' {
			sc.i++
			continue
		}
		j := sc.i
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
			j++
		}
		name := s[sc.i:j]
		sc.i = j
		sc.skipWS()
		if sc.i >= len(s) || s[sc.i] != '(' {
			return identity, false
		}
		sc.i++
		var a [6]float64
		n := 0
		for {
			sc.skipWS()
			if sc.i < len(s) && s[sc.i] == ')' {
				sc.i++
				break
			}
			if n == 6 {
				return identity, false
			}
			v, ok := sc.number()
			if !ok {
				return identity, false
			}
			a[n] = v
			n++
			sc.skipSep()
		}
		var t matrix
		switch name {
		case "matrix":
			if n != 6 {
				return identity, false
			}
			t = matrix{a[0], a[1], a[2], a[3], a[4], a[5]}
		case "translate":
			switch n {
			case 1:
				t = translate(a[0], 0)
			case 2:
				t = translate(a[0], a[1])
			default:
				return identity, false
			}
		case "scale":
			switch n {
			case 1:
				t = scaleM(a[0], a[0])
			case 2:
				t = scaleM(a[0], a[1])
			default:
				return identity, false
			}
		case "rotate":
			if n != 1 && n != 3 {
				return identity, false
			}
			sn, cs := math.Sincos(a[0] * math.Pi / 180)
			t = matrix{cs, sn, -sn, cs, 0, 0}
			if n == 3 {
				t = translate(a[1], a[2]).mul(t).mul(translate(-a[1], -a[2]))
			}
		case "skewX":
			if n != 1 {
				return identity, false
			}
			t = matrix{1, 0, math.Tan(a[0] * math.Pi / 180), 1, 0, 0}
		case "skewY":
			if n != 1 {
				return identity, false
			}
			t = matrix{1, math.Tan(a[0] * math.Pi / 180), 0, 1, 0, 0}
		default:
			return identity, false
		}
		m = m.mul(t)
	}
}

// parsePaintOrder parses paint-order; omitted kinds follow in the default
// fill, stroke, markers order.
func parsePaintOrder(val string) [3]uint8 {
	def := [3]uint8{0, 1, 2}
	var out [3]uint8
	var seen [3]bool
	n := 0
	for _, f := range strings.Fields(val) {
		var k int
		switch f {
		case "normal":
			return def
		case "fill":
			k = 0
		case "stroke":
			k = 1
		case "markers":
			k = 2
		default:
			return def
		}
		if seen[k] {
			return def
		}
		seen[k] = true
		out[n] = uint8(k)
		n++
	}
	if n == 0 {
		return def
	}
	for k := 0; k < 3; k++ {
		if !seen[k] {
			out[n] = uint8(k)
			n++
		}
	}
	return out
}

// strokeFirst reports whether the stroke is painted before the fill.
func (st *state) strokeFirst() bool {
	po := st.paintOrder
	return po[0] == 1 || (po[0] == 2 && po[1] == 1)
}
