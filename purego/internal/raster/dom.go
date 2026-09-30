package raster

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type tagID uint8

const (
	tagUnknown tagID = iota
	tagChars         // character data inside <text>
	tagSVG
	tagG
	tagDefs
	tagSymbol
	tagUse
	tagPath
	tagRect
	tagCircle
	tagEllipse
	tagLine
	tagPolyline
	tagPolygon
	tagText
	tagTspan
	tagImage
	tagClipPath
	tagLinearGradient
	tagRadialGradient
	tagStop
	tagStyle
	tagSwitch
)

var tagNames = map[string]tagID{
	"svg": tagSVG, "g": tagG, "a": tagG, "defs": tagDefs, "symbol": tagSymbol,
	"use": tagUse, "path": tagPath, "rect": tagRect, "circle": tagCircle,
	"ellipse": tagEllipse, "line": tagLine, "polyline": tagPolyline,
	"polygon": tagPolygon, "text": tagText, "tspan": tagTspan, "image": tagImage,
	"clipPath": tagClipPath, "linearGradient": tagLinearGradient,
	"radialGradient": tagRadialGradient, "stop": tagStop, "style": tagStyle,
	"switch": tagSwitch,
}

type attrID uint8

const (
	aNone attrID = iota
	aID
	aClipPath
	aClipPathUnits
	aClipRule
	aColor
	aCx
	aCy
	aD
	aDisplay
	aDx
	aDy
	aFill
	aFillOpacity
	aFillRule
	aFontFamily
	aFontSize
	aFontStyle
	aFontWeight
	aFx
	aFy
	aFr
	aGradientTransform
	aGradientUnits
	aHeight
	aHref
	aImageRendering
	aLetterSpacing
	aWordSpacing
	aMixBlendMode
	aOffset
	aOpacity
	aPoints
	aPreserveAspectRatio
	aR
	aRotate
	aRx
	aRy
	aSpreadMethod
	aStopColor
	aStopOpacity
	aStroke
	aStrokeDasharray
	aStrokeDashoffset
	aStrokeLinecap
	aStrokeLinejoin
	aStrokeMiterlimit
	aStrokeOpacity
	aStrokeWidth
	aStyle
	aSpace
	aTextAnchor
	aTextDecoration
	aTransform
	aViewBox
	aVisibility
	aWidth
	aX
	aX1
	aX2
	aY
	aY1
	aY2
	aDominantBaseline
	aAlignmentBaseline
	aBaselineShift
	aClass
	aMask
	aOverflow
	aTextLength
	aSystemLanguage
)

var attrNames = map[string]attrID{
	"id": aID, "clip-path": aClipPath, "clipPathUnits": aClipPathUnits,
	"clip-rule": aClipRule, "color": aColor, "cx": aCx, "cy": aCy, "d": aD,
	"display": aDisplay, "dx": aDx, "dy": aDy, "fill": aFill,
	"fill-opacity": aFillOpacity, "fill-rule": aFillRule,
	"font-family": aFontFamily, "font-size": aFontSize, "font-style": aFontStyle,
	"font-weight": aFontWeight, "fx": aFx, "fy": aFy, "fr": aFr,
	"gradientTransform": aGradientTransform, "gradientUnits": aGradientUnits,
	"height": aHeight, "href": aHref, "image-rendering": aImageRendering,
	"letter-spacing": aLetterSpacing, "word-spacing": aWordSpacing,
	"mix-blend-mode": aMixBlendMode, "offset": aOffset, "opacity": aOpacity,
	"points": aPoints, "preserveAspectRatio": aPreserveAspectRatio, "r": aR,
	"rotate": aRotate, "rx": aRx, "ry": aRy, "spreadMethod": aSpreadMethod,
	"stop-color": aStopColor, "stop-opacity": aStopOpacity, "stroke": aStroke,
	"stroke-dasharray": aStrokeDasharray, "stroke-dashoffset": aStrokeDashoffset,
	"stroke-linecap": aStrokeLinecap, "stroke-linejoin": aStrokeLinejoin,
	"stroke-miterlimit": aStrokeMiterlimit, "stroke-opacity": aStrokeOpacity,
	"stroke-width": aStrokeWidth, "style": aStyle, "text-anchor": aTextAnchor,
	"text-decoration": aTextDecoration, "transform": aTransform,
	"viewBox": aViewBox, "visibility": aVisibility, "width": aWidth,
	"x": aX, "x1": aX1, "x2": aX2, "y": aY, "y1": aY1, "y2": aY2,
	"dominant-baseline": aDominantBaseline, "alignment-baseline": aAlignmentBaseline,
	"baseline-shift": aBaselineShift, "class": aClass, "mask": aMask,
	"overflow": aOverflow, "textLength": aTextLength,
	"systemLanguage": aSystemLanguage,
}

type attr struct {
	id  attrID
	val string
}

// node is a parsed element (or a run of character data, tagChars).
type node struct {
	tag    tagID
	attrs  []attr
	style  []attr // declarations from the style attribute; they win over attrs
	kids   []*node
	text   string // tagChars only
	parent *node
}

// get returns the value of a non-inherited attribute, honouring style-attribute
// precedence.
func (n *node) get(id attrID) (string, bool) {
	for i := len(n.style) - 1; i >= 0; i-- {
		if n.style[i].id == id {
			return n.style[i].val, true
		}
	}
	for i := len(n.attrs) - 1; i >= 0; i-- {
		if n.attrs[i].id == id {
			return n.attrs[i].val, true
		}
	}
	return "", false
}

func (n *node) str(id attrID) string {
	v, _ := n.get(id)
	return v
}

// document is a parsed SVG.
type document struct {
	root  *node
	ids   map[string]*node
	count int
}

// Limits bounds the resources an untrusted SVG may consume. Zero fields select
// the defaults below.
type Limits struct {
	MaxInputBytes   int // SVG source size
	MaxElements     int // parsed elements
	MaxDepth        int // element nesting
	MaxPixels       int // output width*height after scaling
	MaxDimension    int // output width or height
	MaxRenderNodes  int // element visits while rendering (bounds <use> expansion)
	MaxImageBytes   int // decoded size of one data: URI
	MaxImagePixels  int // pixels of one decoded raster image
	MaxLayerDepth   int // nested offscreen layers (opacity groups)
	MaxPathSegments int // segments produced by flattening one path
}

const (
	defaultMaxInput    = 256 << 20
	defaultMaxElements = 4_000_000
	defaultMaxDepth    = 256
	defaultMaxPixels   = 64 << 20
	defaultMaxDim      = 32767
	defaultMaxRender   = 8_000_000
	defaultMaxImgBytes = 32 << 20
	defaultMaxImgPix   = 64 << 20
	defaultMaxLayers   = 16
	defaultMaxSegs     = 50_000_000
)

func (l Limits) withDefaults() Limits {
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = defaultMaxInput
	}
	if l.MaxElements <= 0 {
		l.MaxElements = defaultMaxElements
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = defaultMaxDepth
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = defaultMaxPixels
	}
	if l.MaxDimension <= 0 {
		l.MaxDimension = defaultMaxDim
	}
	if l.MaxRenderNodes <= 0 {
		l.MaxRenderNodes = defaultMaxRender
	}
	if l.MaxImageBytes <= 0 {
		l.MaxImageBytes = defaultMaxImgBytes
	}
	if l.MaxImagePixels <= 0 {
		l.MaxImagePixels = defaultMaxImgPix
	}
	if l.MaxLayerDepth <= 0 {
		l.MaxLayerDepth = defaultMaxLayers
	}
	if l.MaxPathSegments <= 0 {
		l.MaxPathSegments = defaultMaxSegs
	}
	return l
}

var errSyntax = errors.New("malformed XML")

// arena allocates nodes and attributes in chunks to keep the parser fast.
type arena struct {
	nodes []node
	attrs []attr
}

func (a *arena) newNode() *node {
	if len(a.nodes) == cap(a.nodes) {
		a.nodes = make([]node, 0, 512)
	}
	a.nodes = a.nodes[:len(a.nodes)+1]
	return &a.nodes[len(a.nodes)-1]
}

// takeAttrs returns a slice of n attrs with cap==len so appends by the caller
// cannot spill into neighbours.
func (a *arena) takeAttrs(buf []attr) []attr {
	n := len(buf)
	if n == 0 {
		return nil
	}
	if cap(a.attrs)-len(a.attrs) < n {
		c := 4096
		if n > c {
			c = n
		}
		a.attrs = make([]attr, 0, c)
	}
	start := len(a.attrs)
	a.attrs = append(a.attrs, buf...)
	return a.attrs[start : start+n : start+n]
}

// parseDocument parses SVG markup into a node tree. It is a small,
// dependency-free XML reader: DTDs are skipped (entities are never expanded
// beyond the predefined and numeric ones), namespaces are reduced to local
// names, and only character data inside text elements is retained.
func parseDocument(src string, lim Limits) (*document, error) {
	if len(src) > lim.MaxInputBytes {
		return nil, fmt.Errorf("raster: SVG input is %d bytes, limit is %d", len(src), lim.MaxInputBytes)
	}
	doc := &document{ids: map[string]*node{}}
	var ar arena
	var stack []*node
	var abuf []attr
	var sbuf []attr
	var skip int // depth inside an unknown element whose subtree is ignored
	i := 0
	n := len(src)
	// Skip UTF-8 BOM.
	if strings.HasPrefix(src, "\xef\xbb\xbf") {
		i = 3
	}
	inText := 0 // depth of <text> ancestors

	for i < n {
		if src[i] != '<' {
			j := strings.IndexByte(src[i:], '<')
			var chunk string
			if j < 0 {
				chunk = src[i:]
				i = n
			} else {
				chunk = src[i : i+j]
				i += j
			}
			if inText > 0 && skip == 0 && len(stack) > 0 {
				top := stack[len(stack)-1]
				c := ar.newNode()
				c.tag = tagChars
				c.text = decodeEntities(chunk)
				c.parent = top
				top.kids = append(top.kids, c)
			}
			continue
		}
		// i at '<'
		if i+1 >= n {
			return nil, errSyntax
		}
		switch {
		case strings.HasPrefix(src[i:], "<!--"):
			j := strings.Index(src[i+4:], "-->")
			if j < 0 {
				return nil, errSyntax
			}
			i += 4 + j + 3
		case strings.HasPrefix(src[i:], "<![CDATA["):
			j := strings.Index(src[i+9:], "]]>")
			if j < 0 {
				return nil, errSyntax
			}
			if inText > 0 && skip == 0 && len(stack) > 0 {
				top := stack[len(stack)-1]
				c := ar.newNode()
				c.tag = tagChars
				c.text = src[i+9 : i+9+j]
				c.parent = top
				top.kids = append(top.kids, c)
			}
			i += 9 + j + 3
		case src[i+1] == '?':
			j := strings.Index(src[i:], "?>")
			if j < 0 {
				return nil, errSyntax
			}
			i += j + 2
		case src[i+1] == '!':
			// DOCTYPE or other declaration: skip, honouring an internal subset.
			depth := 0
			j := i + 2
			for ; j < n; j++ {
				c := src[j]
				if c == '[' {
					depth++
				} else if c == ']' {
					depth--
				} else if c == '>' && depth <= 0 {
					break
				} else if c == '"' || c == '\'' {
					k := strings.IndexByte(src[j+1:], c)
					if k < 0 {
						return nil, errSyntax
					}
					j += k + 1
				}
			}
			if j >= n {
				return nil, errSyntax
			}
			i = j + 1
		case src[i+1] == '/':
			j := strings.IndexByte(src[i:], '>')
			if j < 0 {
				return nil, errSyntax
			}
			i += j + 1
			if skip > 0 {
				skip--
				continue
			}
			if len(stack) == 0 {
				return nil, errSyntax
			}
			top := stack[len(stack)-1]
			if top.tag == tagText {
				inText--
			}
			stack = stack[:len(stack)-1]
		default:
			// Start tag.
			j := i + 1
			for j < n && !isSpaceByte(src[j]) && src[j] != '>' && src[j] != '/' {
				j++
			}
			name := src[i+1 : j]
			if k := strings.IndexByte(name, ':'); k >= 0 {
				name = name[k+1:]
			}
			abuf = abuf[:0]
			sbuf = sbuf[:0]
			selfClose := false
			for {
				for j < n && isSpaceByte(src[j]) {
					j++
				}
				if j >= n {
					return nil, errSyntax
				}
				if src[j] == '>' {
					j++
					break
				}
				if src[j] == '/' {
					if j+1 < n && src[j+1] == '>' {
						selfClose = true
						j += 2
						break
					}
					return nil, errSyntax
				}
				k := j
				for k < n && !isSpaceByte(src[k]) && src[k] != '=' && src[k] != '>' && src[k] != '/' {
					k++
				}
				an := src[j:k]
				if k >= n {
					return nil, errSyntax
				}
				for k < n && isSpaceByte(src[k]) {
					k++
				}
				if k >= n || src[k] != '=' {
					return nil, errSyntax
				}
				k++
				for k < n && isSpaceByte(src[k]) {
					k++
				}
				if k >= n || (src[k] != '"' && src[k] != '\'') {
					return nil, errSyntax
				}
				q := src[k]
				e := strings.IndexByte(src[k+1:], q)
				if e < 0 {
					return nil, errSyntax
				}
				val := src[k+1 : k+1+e]
				j = k + 1 + e + 1
				// Attribute name -> id.
				if p := strings.IndexByte(an, ':'); p >= 0 {
					prefix := an[:p]
					local := an[p+1:]
					switch prefix {
					case "xlink":
						an = local
					case "xml":
						if local == "space" {
							abuf = append(abuf, attr{aSpace, decodeEntities(val)})
						}
						continue
					default:
						continue
					}
				}
				if an == "xmlns" {
					continue
				}
				id, ok := attrNames[an]
				if !ok {
					continue
				}
				if strings.IndexByte(val, '&') >= 0 {
					val = decodeEntities(val)
				}
				if id == aStyle {
					sbuf = parseStyleDecls(val, sbuf)
					continue
				}
				abuf = append(abuf, attr{id, val})
			}
			i = j

			tag, known := tagNames[name]
			if skip > 0 || !known {
				if !selfClose {
					skip++
				}
				continue
			}
			doc.count++
			if doc.count > lim.MaxElements {
				return nil, fmt.Errorf("raster: SVG has more than %d elements", lim.MaxElements)
			}
			nd := ar.newNode()
			nd.tag = tag
			nd.attrs = ar.takeAttrs(abuf)
			nd.style = ar.takeAttrs(sbuf)
			if len(stack) == 0 {
				if doc.root != nil {
					return nil, errors.New("raster: multiple root elements")
				}
				if tag != tagSVG {
					return nil, errors.New("raster: root element is not <svg>")
				}
				doc.root = nd
			} else {
				p := stack[len(stack)-1]
				nd.parent = p
				p.kids = append(p.kids, nd)
			}
			if id, ok := nd.get(aID); ok && id != "" {
				if _, dup := doc.ids[id]; !dup {
					doc.ids[id] = nd
				}
			}
			if !selfClose {
				if len(stack)+1 > lim.MaxDepth {
					return nil, fmt.Errorf("raster: SVG nesting exceeds %d levels", lim.MaxDepth)
				}
				stack = append(stack, nd)
				if tag == tagText {
					inText++
				}
			}
		}
	}
	if doc.root == nil {
		return nil, errors.New("raster: no <svg> root element")
	}
	if len(stack) != 0 {
		return nil, errors.New("raster: unbalanced markup")
	}
	return doc, nil
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

var entityNames = map[string]string{
	"lt": "<", "gt": ">", "amp": "&", "quot": "\"", "apos": "'",
	"nbsp": " ",
}

// decodeEntities resolves predefined and numeric character references. Unknown
// named references are kept literally (no DTD entity expansion, by design).
func decodeEntities(s string) string {
	i := strings.IndexByte(s, '&')
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i >= 0 {
		b.WriteString(s[:i])
		s = s[i:]
		semi := strings.IndexByte(s, ';')
		if semi < 0 || semi > 12 {
			b.WriteByte('&')
			s = s[1:]
		} else {
			ent := s[1:semi]
			done := false
			if len(ent) > 1 && ent[0] == '#' {
				var v uint64
				var err error
				if ent[1] == 'x' || ent[1] == 'X' {
					v, err = strconv.ParseUint(ent[2:], 16, 32)
				} else {
					v, err = strconv.ParseUint(ent[1:], 10, 32)
				}
				if err == nil && v > 0 && v <= utf8.MaxRune && !(v >= 0xD800 && v < 0xE000) {
					b.WriteRune(rune(v))
					done = true
				}
			} else if r, ok := entityNames[ent]; ok {
				b.WriteString(r)
				done = true
			}
			if done {
				s = s[semi+1:]
			} else {
				b.WriteByte('&')
				s = s[1:]
			}
		}
		i = strings.IndexByte(s, '&')
	}
	b.WriteString(s)
	return b.String()
}

// parseStyleDecls parses "name: value; name: value" into attrs, dropping
// declarations for properties this renderer does not know.
func parseStyleDecls(s string, dst []attr) []attr {
	for len(s) > 0 {
		var decl string
		if i := strings.IndexByte(s, ';'); i >= 0 {
			decl, s = s[:i], s[i+1:]
		} else {
			decl, s = s, ""
		}
		c := strings.IndexByte(decl, ':')
		if c < 0 {
			continue
		}
		name := strings.TrimSpace(decl[:c])
		val := strings.TrimSpace(decl[c+1:])
		if j := strings.Index(val, "!important"); j >= 0 {
			val = strings.TrimSpace(val[:j])
		}
		if id, ok := attrNames[name]; ok && id != aStyle && id != aID && val != "" {
			dst = append(dst, attr{id, val})
		}
	}
	return dst
}
