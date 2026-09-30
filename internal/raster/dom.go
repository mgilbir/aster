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
	tagMask
	tagFilter
	tagPattern
	tagMarker
	tagFeGaussianBlur
	tagFeOffset
	tagFeFlood
	tagFeColorMatrix
	tagFeComposite
	tagFeMerge
	tagFeMergeNode
	tagFeBlend
	tagFeDropShadow
	tagFeComponentTransfer
	tagFeFuncR
	tagFeFuncG
	tagFeFuncB
	tagFeFuncA
	tagFeUnsupported // a filter primitive this renderer does not implement
)

var tagNames = map[string]tagID{
	"svg": tagSVG, "g": tagG, "a": tagG, "defs": tagDefs, "symbol": tagSymbol,
	"use": tagUse, "path": tagPath, "rect": tagRect, "circle": tagCircle,
	"ellipse": tagEllipse, "line": tagLine, "polyline": tagPolyline,
	"polygon": tagPolygon, "text": tagText, "tspan": tagTspan, "image": tagImage,
	"clipPath": tagClipPath, "linearGradient": tagLinearGradient,
	"radialGradient": tagRadialGradient, "stop": tagStop, "style": tagStyle,
	"switch": tagSwitch, "mask": tagMask, "filter": tagFilter, "pattern": tagPattern,
	"marker": tagMarker, "feGaussianBlur": tagFeGaussianBlur, "feOffset": tagFeOffset,
	"feFlood": tagFeFlood, "feColorMatrix": tagFeColorMatrix, "feComposite": tagFeComposite,
	"feMerge": tagFeMerge, "feMergeNode": tagFeMergeNode, "feBlend": tagFeBlend,
	"feDropShadow": tagFeDropShadow, "feComponentTransfer": tagFeComponentTransfer,
	"feFuncR": tagFeFuncR, "feFuncG": tagFeFuncG, "feFuncB": tagFeFuncB, "feFuncA": tagFeFuncA,
	"feImage": tagFeUnsupported, "feTile": tagFeUnsupported, "feMorphology": tagFeUnsupported,
	"feConvolveMatrix": tagFeUnsupported, "feDisplacementMap": tagFeUnsupported,
	"feTurbulence": tagFeUnsupported, "feDiffuseLighting": tagFeUnsupported,
	"feSpecularLighting": tagFeUnsupported,
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
	aMaskUnits
	aMaskContentUnits
	aMaskType
	aFilter
	aFilterUnits
	aPrimitiveUnits
	aMarkerStart
	aMarkerMid
	aMarkerEnd
	aMarkerUnits
	aMarkerWidth
	aMarkerHeight
	aRefX
	aRefY
	aOrient
	aPatternUnits
	aPatternContentUnits
	aPatternTransform
	aShapeRendering
	aFloodColor
	aFloodOpacity
	aStdDeviation
	aIn
	aIn2
	aResult
	aMode
	aType
	aValues
	aOperator
	aK1
	aK2
	aK3
	aK4
	aLengthAdjust
	aIsolation
	aTableValues
	aSlope
	aIntercept
	aAmplitude
	aExponent
	aColorInterpolationFilters
	aPaintOrder
	aTextRendering
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
	"maskUnits":      aMaskUnits, "maskContentUnits": aMaskContentUnits, "mask-type": aMaskType,
	"filter": aFilter, "filterUnits": aFilterUnits, "primitiveUnits": aPrimitiveUnits,
	"marker-start": aMarkerStart, "marker-mid": aMarkerMid, "marker-end": aMarkerEnd,
	"markerUnits": aMarkerUnits, "markerWidth": aMarkerWidth, "markerHeight": aMarkerHeight,
	"refX": aRefX, "refY": aRefY, "orient": aOrient, "patternUnits": aPatternUnits,
	"patternContentUnits": aPatternContentUnits, "patternTransform": aPatternTransform,
	"shape-rendering": aShapeRendering, "flood-color": aFloodColor, "flood-opacity": aFloodOpacity,
	"stdDeviation": aStdDeviation, "in": aIn, "in2": aIn2, "result": aResult, "mode": aMode,
	"type": aType, "values": aValues, "operator": aOperator, "k1": aK1, "k2": aK2, "k3": aK3,
	"k4": aK4, "lengthAdjust": aLengthAdjust, "isolation": aIsolation,
	"tableValues": aTableValues, "slope": aSlope, "intercept": aIntercept,
	"amplitude": aAmplitude, "exponent": aExponent,
	"color-interpolation-filters": aColorInterpolationFilters, "paint-order": aPaintOrder,
	"text-rendering": aTextRendering,
}

// presentation lists the attributes that a style sheet or a style attribute may
// set (usvg only accepts presentation attributes there).
var presentation = func() (t [256]bool) {
	for _, id := range []attrID{
		aAlignmentBaseline, aBaselineShift, aClipPath, aClipRule, aColor,
		aColorInterpolationFilters, aDisplay, aDominantBaseline, aFill, aFillOpacity,
		aFillRule, aFilter, aFloodColor, aFloodOpacity, aFontFamily, aFontSize,
		aFontStyle, aFontWeight, aImageRendering, aIsolation, aLetterSpacing,
		aMarkerStart, aMarkerMid, aMarkerEnd, aMask, aMaskType, aMixBlendMode,
		aOpacity, aOverflow, aPaintOrder, aShapeRendering, aStopColor, aStopOpacity,
		aStroke, aStrokeDasharray, aStrokeDashoffset, aStrokeLinecap, aStrokeLinejoin,
		aStrokeMiterlimit, aStrokeOpacity, aStrokeWidth, aTextAnchor, aTextDecoration,
		aTextRendering, aTransform, aVisibility, aWordSpacing,
	} {
		t[id] = true
	}
	return
}()

type attr struct {
	id  attrID
	imp bool // declared !important (style sheets and style attributes only)
	val string
}

// rawAttr is an attribute kept verbatim for selector matching. It is only
// recorded for documents that contain a <style> element.
type rawAttr struct{ name, val string }

// node is a parsed element (or a run of character data, tagChars).
type node struct {
	tag    tagID
	attrs  []attr
	style  []attr // declarations from the style attribute; they win over attrs
	kids   []*node
	text   string // tagChars only
	parent *node

	css *cssNode // only for documents with a style sheet (selector matching)
}

// cssNode is what selector matching needs to know about an element.
type cssNode struct {
	name string
	raw  []rawAttr
	prev *node // previous sibling element
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
	css   []string // style sheet sources in document order
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
	MaxStyleBytes   int // total size of <style> sheets
	MaxCSSRules     int // rules across all style sheets
	MaxCSSWork      int // selector-matching steps while applying style sheets
	MaxFilterPixels int // pixels of one filter region
	MaxEffectPixels int // pixels processed by filters and pattern tiles in one render
	// MaxPixelOps bounds the total pixel work of one render: pixels covered by
	// fills and strokes, composited layer and mask areas and filter/pattern
	// pixels, summed over the whole document. It bounds the product of element
	// count and canvas size that MaxRenderNodes and MaxPixels leave open.
	MaxPixelOps int
	// MaxCanvasBytes bounds the pixel memory alive at once: the canvas plus
	// every offscreen layer (opacity group, mask, filter) in flight.
	MaxCanvasBytes int
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
	defaultMaxStyle    = 4 << 20
	defaultMaxRules    = 100_000
	defaultMaxCSSWork  = 100_000_000
	defaultMaxFilterPx = 32 << 20
	defaultMaxEffectPx = 1 << 31
	defaultMaxPixelOps = 1 << 29
	defaultMaxCanvasB  = 1 << 30
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
	if l.MaxStyleBytes <= 0 {
		l.MaxStyleBytes = defaultMaxStyle
	}
	if l.MaxCSSRules <= 0 {
		l.MaxCSSRules = defaultMaxRules
	}
	if l.MaxCSSWork <= 0 {
		l.MaxCSSWork = defaultMaxCSSWork
	}
	if l.MaxFilterPixels <= 0 {
		l.MaxFilterPixels = defaultMaxFilterPx
	}
	if l.MaxEffectPixels <= 0 {
		l.MaxEffectPixels = defaultMaxEffectPx
	}
	if l.MaxPixelOps <= 0 {
		l.MaxPixelOps = defaultMaxPixelOps
	}
	if l.MaxCanvasBytes <= 0 {
		l.MaxCanvasBytes = defaultMaxCanvasB
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
	var rbuf []rawAttr
	var skip int // depth inside an unknown element whose subtree is ignored
	wantCSS := strings.Contains(src, "<style")
	var styleBuf strings.Builder // text of the <style> element being read
	inStyle := false
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
			if inStyle && skip == 0 {
				styleBuf.WriteString(decodeEntities(chunk))
			} else if inText > 0 && skip == 0 && len(stack) > 0 {
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
			if inStyle && skip == 0 {
				styleBuf.WriteString(src[i+9 : i+9+j])
			} else if inText > 0 && skip == 0 && len(stack) > 0 {
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
			if inStyle && top.tag == tagStyle {
				inStyle = false
				if styleBuf.Len() > 0 {
					doc.css = append(doc.css, styleBuf.String())
					styleBuf.Reset()
				}
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
			rbuf = rbuf[:0]
			styleType := ""
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
							abuf = append(abuf, attr{id: aSpace, val: decodeEntities(val)})
						}
						continue
					default:
						continue
					}
				}
				if an == "xmlns" {
					continue
				}
				if strings.IndexByte(val, '&') >= 0 {
					val = decodeEntities(val)
				}
				if wantCSS && an != "style" {
					rbuf = append(rbuf, rawAttr{an, val})
				}
				if an == "type" {
					styleType = val
				}
				id, ok := attrNames[an]
				if !ok {
					continue
				}
				if id == aStyle {
					sbuf = parseStyleDecls(val, sbuf)
					continue
				}
				// usvg accepts these only from style sheets and style attributes.
				if id == aMixBlendMode || id == aIsolation {
					continue
				}
				if id == aImageRendering {
					switch strings.TrimSpace(val) {
					case "smooth", "high-quality", "crisp-edges", "pixelated":
						continue
					}
				}
				abuf = append(abuf, attr{id: id, val: val})
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
			if wantCSS {
				nd.css = &cssNode{name: name}
				if len(rbuf) > 0 {
					nd.css.raw = append([]rawAttr(nil), rbuf...)
				}
			}
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
				if tag == tagStyle && (styleType == "" || styleType == "text/css") {
					inStyle = true
					styleBuf.Reset()
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
	if len(doc.css) > 0 {
		doc.applyCSS(lim)
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

// parseStyleDecls parses the declarations of a style attribute into attrs,
// dropping properties that are not presentation attributes.
func parseStyleDecls(s string, dst []attr) []attr {
	forEachDecl(s, func(name, val string, imp bool) {
		dst = appendDecl(dst, name, val, imp, true)
	})
	return dst
}

// appendDecl appends the attribute(s) a declaration stands for. When resolve
// is set an existing declaration for the property is merged following usvg:
// a later declaration wins unless the earlier one is !important.
func appendDecl(dst []attr, name, val string, imp, resolve bool) []attr {
	if val == "" {
		return dst
	}
	if name == "marker" {
		for _, id := range [3]attrID{aMarkerStart, aMarkerMid, aMarkerEnd} {
			dst = putDecl(dst, id, val, imp, resolve)
		}
		return dst
	}
	id, ok := attrNames[name]
	if !ok || !presentation[id] {
		return dst
	}
	return putDecl(dst, id, val, imp, resolve)
}

func putDecl(dst []attr, id attrID, val string, imp, resolve bool) []attr {
	if resolve {
		for i := range dst {
			if dst[i].id == id {
				if !dst[i].imp {
					dst[i] = attr{id: id, imp: imp, val: val}
				}
				return dst
			}
		}
	}
	return append(dst, attr{id: id, imp: imp, val: val})
}
