package raster

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
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
	tagFeTile
	tagFeMorphology
	tagFeConvolveMatrix
	tagFeDisplacementMap
	tagFeTurbulence
	tagFeDiffuseLighting
	tagFeSpecularLighting
	tagFeImage
	tagFeDistantLight // the light sources, children of a lighting primitive
	tagFePointLight
	tagFeSpotLight
	// tagFeUnsupported is the kind of a primitive whose result is transparent
	// black: a lighting with no light source, an feImage with nothing to
	// draw, a primitive whose sub-region is empty.
	tagFeUnsupported
	// tagFePassThrough is the kind of a primitive that acts as the Filter
	// Effects "pass through filter" (§4: "The pass through filter output is
	// equal to the primary input of the filter primitive"): an feColorMatrix
	// or feConvolveMatrix whose preconditions do not hold, a blur or a
	// morphology whose radius disables it. No element has it.
	tagFePassThrough
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
	"feImage": tagFeImage, "feTile": tagFeTile, "feMorphology": tagFeMorphology,
	"feConvolveMatrix": tagFeConvolveMatrix, "feDisplacementMap": tagFeDisplacementMap,
	"feTurbulence": tagFeTurbulence, "feDiffuseLighting": tagFeDiffuseLighting,
	"feSpecularLighting": tagFeSpecularLighting, "feDistantLight": tagFeDistantLight,
	"fePointLight": tagFePointLight, "feSpotLight": tagFeSpotLight,
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
	aVectorEffect
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
	aOrder
	aKernelMatrix
	aDivisor
	aBias
	aTargetX
	aTargetY
	aEdgeMode
	aPreserveAlpha
	aRadius
	aScale
	aXChannelSelector
	aYChannelSelector
	aBaseFrequency
	aNumOctaves
	aSeed
	aStitchTiles
	aSurfaceScale
	aDiffuseConstant
	aSpecularConstant
	aSpecularExponent
	aLightingColor
	aAzimuth
	aElevation
	aZ
	aPointsAtX
	aPointsAtY
	aPointsAtZ
	aLimitingConeAngle
	aKernelUnitLength
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
	"mix-blend-mode": aMixBlendMode, "vector-effect": aVectorEffect, "offset": aOffset, "opacity": aOpacity,
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
	"order":          aOrder, "kernelMatrix": aKernelMatrix, "divisor": aDivisor, "bias": aBias,
	"targetX": aTargetX, "targetY": aTargetY, "edgeMode": aEdgeMode, "preserveAlpha": aPreserveAlpha,
	"radius": aRadius, "scale": aScale, "xChannelSelector": aXChannelSelector,
	"yChannelSelector": aYChannelSelector, "baseFrequency": aBaseFrequency, "numOctaves": aNumOctaves,
	"seed": aSeed, "stitchTiles": aStitchTiles, "surfaceScale": aSurfaceScale,
	"diffuseConstant": aDiffuseConstant, "specularConstant": aSpecularConstant,
	"specularExponent": aSpecularExponent, "lighting-color": aLightingColor,
	"azimuth": aAzimuth, "elevation": aElevation, "z": aZ, "pointsAtX": aPointsAtX,
	"pointsAtY": aPointsAtY, "pointsAtZ": aPointsAtZ, "limitingConeAngle": aLimitingConeAngle,
	"kernelUnitLength": aKernelUnitLength,
}

// presentation lists the attributes that a style sheet or a style attribute may
// set (usvg only accepts presentation attributes there).
var presentation = func() (t [256]bool) {
	for _, id := range []attrID{
		aAlignmentBaseline, aBaselineShift, aClipPath, aClipRule, aColor,
		aColorInterpolationFilters, aDisplay, aDominantBaseline, aFill, aFillOpacity,
		aFillRule, aFilter, aFloodColor, aFloodOpacity, aFontFamily, aFontSize,
		aFontStyle, aFontWeight, aImageRendering, aIsolation, aLetterSpacing, aLightingColor,
		aMarkerStart, aMarkerMid, aMarkerEnd, aMask, aMaskType, aMixBlendMode,
		aOpacity, aOverflow, aPaintOrder, aShapeRendering, aStopColor, aStopOpacity,
		aStroke, aStrokeDasharray, aStrokeDashoffset, aStrokeLinecap, aStrokeLinejoin,
		aStrokeMiterlimit, aStrokeOpacity, aStrokeWidth, aTextAnchor, aTextDecoration,
		aTextRendering, aTransform, aVectorEffect, aVisibility, aWordSpacing,
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
	raw  []rawAttr // sorted by name once indexed (cssApplier.indexNode)
	prev *node     // previous sibling element
	ix   cssIndexed
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
	// res is what the document is charged to the call's ledger: its tree,
	// its style sheets and what they gave its elements (see parseDoc).
	res reservation
}

// Limits bounds the resources an untrusted SVG may consume. Zero fields select
// the defaults below.
type Limits struct {
	MaxInputBytes  int // SVG source size
	MaxElements    int // parsed elements
	MaxDepth       int // element nesting
	MaxPixels      int // output width*height after scaling
	MaxDimension   int // output width or height
	MaxRenderNodes int // element visits while rendering (bounds <use> expansion)
	MaxImageBytes  int // decoded size of one data: URI
	MaxImagePixels int // pixels of one decoded raster image
	MaxLayerDepth  int // nested offscreen layers (opacity groups)
	// MaxPathSegments bounds the path geometry held at once, in points: the
	// path data parsed, the polylines flattened and the stroke outlines
	// built for the elements being drawn (an element's are given back when
	// it is done), and the scan converter's edges, each counted as five
	// points (its size). It bounds the memory a path's geometry takes,
	// which flattening (up to 400 points a curve) and stroking (up to 157
	// a round join) multiply.
	MaxPathSegments int
	MaxStyleBytes   int // total size of <style> sheets
	MaxCSSRules     int // rules across all style sheets
	MaxCSSWork      int // selector-matching steps while applying style sheets
	// (past MaxStyleBytes, MaxCSSRules or MaxCSSWork the render fails)
	MaxFilterPixels int // pixels of one filter region; a larger one fails the render
	MaxEffectPixels int // pixels processed by filters and pattern tiles in one render
	// MaxPixelOps bounds the total pixel work of one render: pixels covered by
	// fills and strokes, edge crossings the scan converter computes, points
	// flattening computes, composited layer and mask areas, pattern tile
	// pixels, and the pixels of every pass a filter makes over its region (a
	// merge node a pass; a blur's passes count each channel, and a pass that
	// demultiplies and multiplies again counts three), summed over the whole
	// document. It bounds the product of element count and canvas size that
	// MaxRenderNodes and MaxPixels leave open. Set, it is the budget at every
	// scale, raised only to PixelOpsPerPixel operations per output pixel (no
	// floor when that is zero): an override. Zero selects the default,
	// PixelOps(scale, width, height): 128 Mi operations at scale 1, times
	// the scale squared above it, and at least 16 per output pixel.
	MaxPixelOps int
	// PixelOpsPerPixel raises a set MaxPixelOps to this many operations per
	// output pixel, so that a large canvas is not refused for drawing
	// itself a few times over (zero: no floor; with MaxPixelOps zero too,
	// the default's 16).
	PixelOpsPerPixel int
	// MaxCanvasBytes bounds the pixel memory alive at once: the canvas plus
	// every offscreen layer (opacity group, mask, filter) in flight, the
	// clip coverage maps of the elements being drawn, and every result and
	// scratch buffer of a filter chain.
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
	defaultMaxSegs     = 128 << 20 // 2 GiB of 16-byte points
	defaultMaxStyle    = 4 << 20
	defaultMaxRules    = 100_000
	defaultMaxCSSWork  = 100_000_000
	defaultMaxFilterPx = 32 << 20
	defaultMaxEffectPx = math.MaxInt32
	defaultMaxPixelOps = 128 << 20 // at scale 1; see PixelOps
	defaultPixelOpsPer = 16
	defaultMaxCanvasB  = 1 << 30
	// defaultLedgerBytes bounds what a render charges to a ledger when its
	// context carries none (Render called directly): the public API's
	// default.
	defaultLedgerBytes = 2 << 30
)

// WithDefaults is l with every zero field set to its default.
func (l Limits) WithDefaults() Limits { return l.withDefaults() }

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
// Each chunk is charged to the parse's reservation before it is allocated.
type arena struct {
	nodes []node
	attrs []attr
	rs    *reservation
}

const (
	nodeBytes    = int64(unsafe.Sizeof(node{}))
	attrBytes    = int64(unsafe.Sizeof(attr{}))
	rawAttrBytes = int64(unsafe.Sizeof(rawAttr{}))
	cssNodeBytes = int64(unsafe.Sizeof(cssNode{}))
	// idEntryBytes is about what an entry of the document's id map holds,
	// its share of the buckets included.
	idEntryBytes = 64
	nodeChunk    = 512
)

func (a *arena) newNode() (*node, error) {
	if len(a.nodes) == cap(a.nodes) {
		if err := a.rs.take(nodeChunk * nodeBytes); err != nil {
			return nil, err
		}
		a.nodes = make([]node, 0, nodeChunk)
	}
	a.nodes = a.nodes[:len(a.nodes)+1]
	return &a.nodes[len(a.nodes)-1], nil
}

// takeAttrs returns a slice of n attrs with cap==len so appends by the caller
// cannot spill into neighbours.
func (a *arena) takeAttrs(buf []attr) ([]attr, error) {
	n := len(buf)
	if n == 0 {
		return nil, nil
	}
	if cap(a.attrs)-len(a.attrs) < n {
		c := max(4096, n)
		if err := a.rs.take(int64(c) * attrBytes); err != nil {
			return nil, err
		}
		a.attrs = make([]attr, 0, c)
	}
	start := len(a.attrs)
	a.attrs = append(a.attrs, buf...)
	return a.attrs[start : start+n : start+n], nil
}

// addKid appends k to p's children, charging a larger backing array before
// it is made (the old one stays charged: it is garbage only once the
// collector finds it).
func addKid(rs *reservation, p, k *node) error {
	if len(p.kids) == cap(p.kids) {
		c := max(4, 2*cap(p.kids))
		if err := rs.take(int64(c) * 8); err != nil {
			return err
		}
		kids := make([]*node, len(p.kids), c)
		copy(kids, p.kids)
		p.kids = kids
	}
	p.kids = append(p.kids, k)
	k.parent = p
	return nil
}

// grow returns buf with room for one more element, charging a larger
// backing array of elements of size bytes before it is made: the parse's
// scratch buffers grow with the attributes of the largest element.
func grow[T any](rs *reservation, buf []T, size int64) ([]T, error) {
	if len(buf) < cap(buf) {
		return buf, nil
	}
	c := max(16, 2*cap(buf))
	if err := rs.take(int64(c) * size); err != nil {
		return nil, err
	}
	nb := make([]T, len(buf), c)
	copy(nb, buf)
	return nb, nil
}

// parseDocument parses SVG markup into a node tree. It is a small,
// dependency-free XML reader: DTDs are skipped (entities are never expanded
// beyond the predefined and numeric ones), namespaces are reduced to local
// names, and only character data inside text elements is retained.
func parseDocument(src string, lim Limits) (*document, error) {
	return parseDocumentCtx(context.Background(), src, lim)
}

// parseDocumentCtx is parseDocument under ctx: it charges what it builds to
// the ledger ctx carries and polls ctx as it reads (see parseDoc).
func parseDocumentCtx(ctx context.Context, src string, lim Limits) (*document, error) {
	return parseDoc(ctx, budget.LedgerFrom(ctx), src, lim)
}

// parseDoc is parseDocument under ctx (nil: never done), charging l. The
// input is untrusted and the tree is many times its size (a node of about
// a hundred bytes for every few bytes of markup), so everything the parse
// builds is charged to l before it is built: nodes, attributes, the
// attributes kept for selector matching, children lists, the id map,
// decoded entities and style sheet text, then what applying the style
// sheets builds (C8, audit 2026-10-18). Runs of character data count
// against MaxElements as elements do. The input is metered as it is read,
// about a work unit every 16 bytes, which polls ctx about every 16 KiB
// (C22). What the document holds stays charged until release.
func parseDoc(ctx context.Context, l *budget.Ledger, src string, lim Limits) (doc *document, err error) {
	if len(src) > lim.MaxInputBytes {
		return nil, fmt.Errorf("raster: SVG input is %d bytes, limit is %d: %w", len(src), lim.MaxInputBytes, errLimit)
	}
	doc = &document{ids: map[string]*node{}}
	doc.res.l = l
	defer func() {
		if err != nil {
			doc.release()
			doc = nil
		}
	}()
	rs := &doc.res
	m := budget.NewMeter(ctx)
	ar := arena{rs: rs}
	var stack []*node
	var abuf []attr
	var sbuf []attr
	var rbuf []rawAttr
	var skip int // depth inside an unknown element whose subtree is ignored
	wantCSS := strings.Contains(src, "<style")
	var styleBuf strings.Builder // text of the <style> element being read
	styleBytes := 0              // of every sheet, against MaxStyleBytes
	inStyle := false
	i := 0
	n := len(src)
	// Skip UTF-8 BOM.
	if strings.HasPrefix(src, "\xef\xbb\xbf") {
		i = 3
	}
	inText := 0 // depth of <text> ancestors
	read := i   // the input metered so far
	// addStyle appends s to the style sheet being read, its bytes charged
	// (twice: the builder doubles) and bounded as they come.
	addStyle := func(s string) error {
		if styleBytes += len(s); styleBytes > lim.MaxStyleBytes {
			return fmt.Errorf("raster: style sheets of more than %d bytes: %w", lim.MaxStyleBytes, errLimit)
		}
		if err := rs.take(2 * int64(len(s))); err != nil {
			return overLedger("parsing the SVG's style sheets", err)
		}
		styleBuf.WriteString(s)
		return nil
	}
	// addChars makes a run of character data a node of the <text> being
	// read: an element, as far as MaxElements is concerned.
	addChars := func(text string) error {
		top := stack[len(stack)-1]
		if doc.count++; doc.count > lim.MaxElements {
			return fmt.Errorf("raster: SVG has more than %d elements: %w", lim.MaxElements, errLimit)
		}
		c, err := ar.newNode()
		if err != nil {
			return err
		}
		c.tag = tagChars
		c.text = text
		return addKid(rs, top, c)
	}
	// decode is decodeEntities with what it makes charged first: at most
	// the bytes it decodes.
	decode := func(s string) (string, error) {
		if strings.IndexByte(s, '&') < 0 {
			return s, nil
		}
		if err := rs.take(int64(len(s))); err != nil {
			return "", err
		}
		return decodeEntities(s), nil
	}

	for i < n {
		if err := m.Work(1 + int64(i-read)>>4); err != nil {
			return nil, err
		}
		read = i
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
				s, err := decode(chunk)
				if err != nil {
					return nil, overLedger("parsing the SVG", err)
				}
				if err := addStyle(s); err != nil {
					return nil, err
				}
			} else if inText > 0 && skip == 0 && len(stack) > 0 {
				s, err := decode(chunk)
				if err == nil {
					err = addChars(s)
				}
				if err != nil {
					return nil, overLedger("parsing the SVG", err)
				}
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
				if err := addStyle(src[i+9 : i+9+j]); err != nil {
					return nil, err
				}
			} else if inText > 0 && skip == 0 && len(stack) > 0 {
				if err := addChars(src[i+9 : i+9+j]); err != nil {
					return nil, overLedger("parsing the SVG", err)
				}
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
					styleBuf = strings.Builder{}
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
				if err := m.Work(1 + int64(j-read)>>4); err != nil {
					return nil, err
				}
				read = j
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
							v, err := decode(val)
							if err == nil {
								abuf, err = grow(rs, abuf, attrBytes)
							}
							if err != nil {
								return nil, overLedger("parsing the SVG", err)
							}
							abuf = append(abuf, attr{id: aSpace, val: v})
						}
						continue
					default:
						continue
					}
				}
				if an == "xmlns" {
					continue
				}
				val, err := decode(val)
				if err != nil {
					return nil, overLedger("parsing the SVG", err)
				}
				if wantCSS && an != "style" {
					if rbuf, err = grow(rs, rbuf, rawAttrBytes); err != nil {
						return nil, overLedger("parsing the SVG", err)
					}
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
					// At most one declaration a presentation attribute
					// (parseStyleDecls merges them): a bounded scratch
					// buffer, charged when the arena keeps it.
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
				if abuf, err = grow(rs, abuf, attrBytes); err != nil {
					return nil, overLedger("parsing the SVG", err)
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
				return nil, fmt.Errorf("raster: SVG has more than %d elements: %w", lim.MaxElements, errLimit)
			}
			nd, err := ar.newNode()
			if err == nil {
				nd.attrs, err = ar.takeAttrs(abuf)
			}
			if err == nil {
				nd.style, err = ar.takeAttrs(sbuf)
			}
			if err == nil && wantCSS {
				err = rs.take(cssNodeBytes + int64(len(rbuf))*rawAttrBytes)
				nd.css = &cssNode{name: name}
				if len(rbuf) > 0 {
					nd.css.raw = append([]rawAttr(nil), rbuf...)
				}
			}
			if err != nil {
				return nil, overLedger("parsing the SVG", err)
			}
			nd.tag = tag
			if len(stack) == 0 {
				if doc.root != nil {
					return nil, errors.New("raster: multiple root elements")
				}
				if tag != tagSVG {
					return nil, errors.New("raster: root element is not <svg>")
				}
				doc.root = nd
			} else if err := addKid(rs, stack[len(stack)-1], nd); err != nil {
				return nil, overLedger("parsing the SVG", err)
			}
			if id, ok := nd.get(aID); ok && id != "" {
				if _, dup := doc.ids[id]; !dup {
					if err := rs.take(idEntryBytes); err != nil {
						return nil, overLedger("parsing the SVG", err)
					}
					doc.ids[id] = nd
				}
			}
			if !selfClose {
				if len(stack)+1 > lim.MaxDepth {
					return nil, fmt.Errorf("raster: SVG nesting exceeds %d levels: %w", lim.MaxDepth, errLimit)
				}
				stack = append(stack, nd)
				if tag == tagText {
					inText++
				}
				if tag == tagStyle && (styleType == "" || styleType == "text/css") {
					inStyle = true
					styleBuf = strings.Builder{}
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
		if err := doc.applyCSS(ctx, lim); err != nil {
			return nil, err
		}
	}
	rs.trim()
	return doc, nil
}

// release gives back what the document is charged: it is no longer used.
func (doc *document) release() {
	if doc != nil {
		doc.res.release()
	}
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
