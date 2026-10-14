package raster

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Filter resolution follows usvg (crates/usvg/src/parser/filter.rs) where
// usvg follows W3C Filter Effects Module Level 1, and the specification
// where usvg departs from it (ADR 0015): a primitive sub-region in
// primitiveUnits="objectBoundingBox" is fractions of the bounding box, and
// one left out defaults to the union of its inputs' (§9.4); a disabled
// primitive gives a transparent result and the filter goes on; a
// primitive whose parameters the specification makes a pass through filter
// is one (feColorMatrix, feConvolveMatrix, feMorphology, feGaussianBlur);
// lengths along an axis (a displacement, a light's position) are in
// primitiveUnits.

const (
	inSource uint8 = iota
	inAlpha
	inRef
)

type finput struct {
	kind uint8
	name string
}

type transferFn struct {
	kind      uint8 // 0 identity, 1 table, 2 discrete, 3 linear, 4 gamma
	table     []float64
	a, b, off float64 // slope/intercept, or amplitude/exponent/offset
}

const (
	cmMatrix uint8 = iota
	cmSaturate
	cmHueRotate
	cmLuminance
)

const (
	opOver uint8 = iota
	opIn
	opOut
	opAtop
	opXor
	opArithmetic
	// opLighter is Compositing and Blending's plus-lighter, the "lighter"
	// operator Filter Effects 1 §9.8 lists (usvg reads it as over).
	opLighter
)

// fprim is one resolved filter primitive.
type fprim struct {
	kind   tagID
	rect   rect // sub-region, user space
	in1    finput
	in2    finput
	result string
	linear bool

	stdX, stdY float64 // blur / drop shadow, user space (already bbox-scaled)
	dx, dy     float64
	color      rgba // flood / shadow colour (alpha excluded)
	alpha      float64
	mode       blendMode
	op         uint8
	k          [4]float64
	cmKind     uint8
	cmValues   []float64
	merge      []finput
	funcs      [4]transferFn // r, g, b, a

	// feMorphology: dilate (else erode) and its radii, user space (already
	// bbox-scaled), each rounded to f32.
	dilate bool
	rx, ry float64
	conv   *convolveSpec

	// feDisplacementMap: the scale along each axis (scaled for
	// primitiveUnits, f32) and the channel of in2 each axis reads (0-3: R,
	// G, B, A).
	scaleX, scaleY float64
	xch, ych       uint8

	turb  *turbSpec
	light *lightingSpec
	img   *feImageSpec
}

// convolveSpec is a resolved feConvolveMatrix (usvg's ConvolveMatrix).
type convolveSpec struct {
	cols, rows       int
	targetX, targetY int
	kernel           []float32 // rows*cols, row-major as written
	divisor, bias    float32
	edge             uint8 // edgeNone, edgeDuplicate or edgeWrap
	preserveAlpha    bool
	// unitX and unitY are the kernelUnitLength, user space; 0 when it is
	// not specified (the kernel's cells are then the image's pixels).
	unitX, unitY float64
}

const (
	edgeDuplicate uint8 = iota
	edgeNone
	edgeWrap
)

// filterSpec is a resolved filter element for one referencing element.
type filterSpec struct {
	node  *node // the filter element
	rect  rect
	prims []fprim
}

const maxFilterPrims = 128

func parseNum(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func numAttr(n *node, id attrID, def float64) float64 {
	if v, ok := n.get(id); ok {
		if f, ok := parseNum(v); ok {
			return f
		}
	}
	return def
}

// parseNumList parses a comma/space separated list of numbers; ok is false on
// any syntax error.
func parseNumList(s string) ([]float64, bool) {
	sc := numScanner{s: s}
	var out []float64
	for {
		sc.skipWS()
		if sc.atEnd() {
			return out, true
		}
		v, ok := sc.number()
		if !ok {
			return nil, false
		}
		out = append(out, v)
		if len(out) > 1024 {
			return nil, false
		}
		sc.skipSep()
	}
}

// filterChain returns the filter elements reachable through href.
func (r *renderer) filterAttr(fn *node, id attrID) (string, bool) {
	for i := 0; fn != nil && i < 8; i++ {
		if fn.tag != tagFilter {
			return "", false
		}
		if v, ok := fn.get(id); ok {
			return v, true
		}
		fn = r.hrefTarget(fn)
	}
	return "", false
}

// unitValue resolves a length attribute under bbox or user units.
func (st *state) unitValue(s string, obb bool, axis int) (float64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	v, u, ok := parseLength(s)
	if !ok {
		return 0, false
	}
	if obb {
		if u == "%" {
			return v / 100, true
		}
		return st.toPx(v, u, axis), true
	}
	return st.toPx(v, u, axis), true
}

// f32r rounds v to float32 precision (usvg and tiny-skia compute in f32; a few
// of their floor/ceil decisions depend on it).
func f32r(v float64) float64 { return float64(float32(v)) }

// bboxTransformRect maps a rect given in bounding-box fractions onto bb, using
// f32 arithmetic like usvg's NonZeroRect::bbox_transform.
func bboxTransformRect(rc, bb rect) rect {
	bw, bh := float32(bb.w()), float32(bb.h())
	x := float32(bb.x0) + float32(float32(rc.x0)*bw)
	y := float32(bb.y0) + float32(float32(rc.y0)*bh)
	w := float32(float32(rc.w()) * bw)
	h := float32(float32(rc.h()) * bh)
	return rect{float64(x), float64(y), float64(x + w), float64(y + h)}
}

// rect32 builds a rect from origin and size with f32 arithmetic.
func rect32(x, y, w, h float64) rect {
	return rect{f32r(x), f32r(y), float64(float32(x) + float32(w)), float64(float32(y) + float32(h))}
}

// transformRect32 is transformRect with tiny-skia's f32 point mapping.
func transformRect32(rc rect, m matrix) rect {
	a, b, c, d, e, f := float32(m.a), float32(m.b), float32(m.c), float32(m.d), float32(m.e), float32(m.f)
	var bx bbox
	for _, p := range [4]point{{rc.x0, rc.y0}, {rc.x1, rc.y0}, {rc.x1, rc.y1}, {rc.x0, rc.y1}} {
		x, y := float32(p.x), float32(p.y)
		bx.add(point{float64(float32(a*x) + float32(c*y) + e), float64(float32(b*x) + float32(d*y) + f)})
	}
	return bx.r
}

// resolveFilter builds the spec for filter element fn applied to an element
// whose bounding box is bb. A nil spec is a filter that is not applied: the
// element is drawn without it when ignored is set, and not drawn at all
// otherwise. Ignored: objectBoundingBox units for an element whose box has
// no width or height (SVG 2 §8.11: "When the geometry of the applicable
// element has no width or height and objectBoundingBox is specified, then
// the given effect (e.g., a gradient or a filter) will be ignored"; usvg
// does not draw the element). Not drawn: a filter with no primitive (Filter
// Effects 1 §9.3: "If a filter element has no filter primitive tree then
// the element the filter applies to does not get rendered"), and one whose
// region has no width or height (§7, whose sentence is cut short, "...ng
// of the element which referenced the filter": SVG 1.1's "Zero values
// disable rendering of the element which referenced the filter").
func (r *renderer) resolveFilter(fn *node, st *state, bb rect, hasBB bool) (spec *filterSpec, ignored bool) {
	if fn == nil || fn.tag != tagFilter {
		return nil, false
	}
	unitsOBB := true
	if v, ok := r.filterAttr(fn, aFilterUnits); ok && strings.TrimSpace(v) == "userSpaceOnUse" {
		unitsOBB = false
	}
	primOBB := false
	if v, ok := r.filterAttr(fn, aPrimitiveUnits); ok && strings.TrimSpace(v) == "objectBoundingBox" {
		primOBB = true
	}
	get := func(id attrID, def float64, axis int) float64 {
		if v, ok := r.filterAttr(fn, id); ok {
			if f, ok := st.unitValue(v, unitsOBB, axis); ok {
				return f
			}
		}
		return def
	}
	// Defaults are -10%, -10%, 120%, 120%.
	var x, y, w, h float64
	if unitsOBB {
		x, y, w, h = get(aX, -0.1, 0), get(aY, -0.1, 1), get(aWidth, 1.2, 0), get(aHeight, 1.2, 1)
	} else {
		x, y = get(aX, -0.1*st.vw, 0), get(aY, -0.1*st.vh, 1)
		w, h = get(aWidth, 1.2*st.vw, 0), get(aHeight, 1.2*st.vh, 1)
	}
	if (unitsOBB || primOBB) && !hasBB {
		return nil, true
	}
	if !(w > 0 && h > 0) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return nil, false
	}
	region := rect32(x, y, w, h)
	if unitsOBB {
		region = bboxTransformRect(region, bb)
	}
	// The first filter in the href chain that has children provides them.
	var src *node
	for f, i := fn, 0; f != nil && i < 8; i++ {
		if f.tag != tagFilter {
			return nil, false
		}
		if len(f.kids) > 0 {
			src = f
			break
		}
		f = r.hrefTarget(f)
	}
	if src == nil {
		return nil, false
	}
	scaleW, scaleH := 1.0, 1.0
	if primOBB {
		scaleW, scaleH = bb.w(), bb.h()
	}
	units := primUnits{obb: primOBB, bb: bb}
	spec = &filterSpec{node: fn, rect: region}
	names := map[string]bool{}
	idx := 1
	for _, fe := range src.kids {
		if fe.tag < tagFeGaussianBlur || fe.tag >= tagFeUnsupported || fe.tag == tagFeMergeNode ||
			(fe.tag >= tagFeFuncR && fe.tag <= tagFeFuncA) || (fe.tag >= tagFeDistantLight && fe.tag <= tagFeSpotLight) {
			continue
		}
		if len(spec.prims) >= maxFilterPrims {
			break
		}
		prims := spec.prims
		p := fprim{kind: fe.tag}
		// The inputs first: they give the sub-region its defaults.
		switch fe.tag {
		case tagFeFlood, tagFeTurbulence, tagFeImage:
		case tagFeMerge:
			for _, c := range fe.kids {
				if c.tag == tagFeMergeNode {
					p.merge = append(p.merge, resolveInput(c, aIn, prims))
				}
			}
		case tagFeBlend, tagFeComposite, tagFeDisplacementMap:
			p.in1 = resolveInput(fe, aIn, prims)
			p.in2 = resolveInput(fe, aIn2, prims)
		default:
			p.in1 = resolveInput(fe, aIn, prims)
		}
		sub, on := r.primitiveRegion(fe, primOBB, st, bb, defaultSubregion(&p, prims, region))
		p.rect = sub
		switch {
		case !on:
			// Filter Effects 1 §9.2: a width or height that is "negative or
			// zero disables the effect of the given filter primitive (i.e.,
			// the result is a transparent black image)"; the primitives after
			// it still run.
			p.kind = tagFeUnsupported
		case fe.tag == tagFeDropShadow:
			p.stdX, p.stdY = stdDev(fe, scaleW, scaleH, "2 2")
			p.dx = numAttr(fe, aDx, 2) * scaleW
			p.dy = numAttr(fe, aDy, 2) * scaleH
			p.color, p.alpha = floodColor(fe)
		case fe.tag == tagFeGaussianBlur:
			p.stdX, p.stdY = stdDev(fe, scaleW, scaleH, "0 0")
		case fe.tag == tagFeOffset:
			p.dx = numAttr(fe, aDx, 0) * scaleW
			p.dy = numAttr(fe, aDy, 0) * scaleH
		case fe.tag == tagFeBlend:
			p.mode = blendNormal
			if v, ok := fe.get(aMode); ok {
				p.mode = blendNames[strings.TrimSpace(v)]
			}
		case fe.tag == tagFeFlood:
			p.color, p.alpha = floodColor(fe)
		case fe.tag == tagFeComposite:
			p.op = opOver
			switch strings.TrimSpace(fe.str(aOperator)) {
			case "in":
				p.op = opIn
			case "out":
				p.op = opOut
			case "atop":
				p.op = opAtop
			case "xor":
				p.op = opXor
			case "lighter":
				p.op = opLighter
			case "arithmetic":
				p.op = opArithmetic
				p.k = [4]float64{numAttr(fe, aK1, 0), numAttr(fe, aK2, 0), numAttr(fe, aK3, 0), numAttr(fe, aK4, 0)}
			}
		case fe.tag == tagFeColorMatrix:
			var pass bool
			p.cmKind, p.cmValues, pass = colorMatrixKind(fe)
			if pass {
				p.kind = tagFePassThrough
			}
		case fe.tag == tagFeComponentTransfer:
			for _, c := range fe.kids {
				var idx int
				switch c.tag {
				case tagFeFuncR:
					idx = 0
				case tagFeFuncG:
					idx = 1
				case tagFeFuncB:
					idx = 2
				case tagFeFuncA:
					idx = 3
				default:
					continue
				}
				// §9.7: "If more than one transfer function element of the
				// same kind is specified, the last occurrence is to be used",
				// one whose type is not a type being the initial identity.
				p.funcs[idx] = transferFunction(c)
			}
		case fe.tag == tagFeTile, fe.tag == tagFeMerge:
			// No parameters: the inputs are resolved above.
		case fe.tag == tagFeMorphology:
			var pass bool
			p.dilate, p.rx, p.ry, pass = morphology(fe, scaleW, scaleH)
			if pass {
				p.kind = tagFePassThrough
			}
		case fe.tag == tagFeConvolveMatrix:
			conv, err := convolveMatrix(fe, scaleW, scaleH)
			if err != nil {
				r.fail(err)
				return nil, false
			}
			if conv == nil {
				p.kind = tagFePassThrough
				break
			}
			p.conv = conv
		case fe.tag == tagFeDisplacementMap:
			// §9.11: scale is "expressed in the coordinate system established
			// by attribute primitiveUnits": a displacement along x is a
			// length along x, one along y a length along y (usvg scales both
			// by the mean of the bounding box's sides).
			k := numAttr32(fe, aScale, 0)
			p.scaleX = float64(float32(k * float32(scaleW)))
			p.scaleY = float64(float32(k * float32(scaleH)))
			p.xch, p.ych = colorChannel(fe, aXChannelSelector), colorChannel(fe, aYChannelSelector)
		case fe.tag == tagFeTurbulence:
			t := turbulenceOf(fe)
			p.turb = &t
		case fe.tag == tagFeDiffuseLighting, fe.tag == tagFeSpecularLighting:
			if p.light = r.lightingOf(fe, fe.tag == tagFeSpecularLighting, units); p.light == nil {
				p.kind = tagFeUnsupported
			}
		case fe.tag == tagFeImage:
			if p.img = r.feImageOf(fe, sub, units); p.img == nil {
				p.kind = tagFeUnsupported
			}
		default:
			// Every primitive is handled above; anything else would be a
			// transparent result keeping the chain intact.
			p.kind = tagFeUnsupported
		}
		p.linear = r.inheritedState(fe).filterLinear
		// Result name.
		if v, ok := fe.get(aResult); ok && v != "" {
			p.result = v
			names[v] = true
			idx++
		} else {
			for {
				name := "result" + strconv.Itoa(idx)
				idx++
				if !names[name] {
					p.result = name
					break
				}
			}
		}
		spec.prims = append(spec.prims, p)
	}
	if len(spec.prims) == 0 {
		return nil, false
	}
	return spec, false
}

// primUnits is a filter's primitiveUnits: objectBoundingBox (obb), with
// the bounding box of the element it is applied to, or userSpaceOnUse.
type primUnits struct {
	obb bool
	bb  rect
}

// point maps a point given in primitiveUnits to user space: under
// objectBoundingBox, fractions of the bounding box (Filter Effects 1 §7:
// "any length values within the filter definitions represent fractions or
// percentages of the bounding box on the referencing element").
func (u primUnits) point(x, y float64) (float64, float64) {
	if !u.obb {
		return x, y
	}
	return u.bb.x0 + float64(x*u.bb.w()), u.bb.y0 + float64(y*u.bb.h())
}

// length maps a length along no axis (a light's z) to user space: under
// objectBoundingBox, a fraction of the box's normalized diagonal,
// sqrt((w^2 + h^2) / 2), SVG's measure of a length that is neither
// horizontal nor vertical (SVG 2 §8.9), as Chromium resolves a light's z.
func (u primUnits) length(v float64) float64 {
	if !u.obb {
		return v
	}
	w, h := u.bb.w(), u.bb.h()
	return v * math.Sqrt((float64(w*w)+float64(h*h))/2)
}

// defaultSubregion is the rectangle a primitive's x, y, width and height
// default to, Filter Effects 1 §9.4: "x, y, width and height default to the
// union (i.e., tightest fitting bounding box) of the subregions defined for
// all referenced nodes. If there are no referenced nodes (e.g., for feImage
// or feTurbulence), or one or more of the referenced nodes is a standard
// input (...), or for feTile (...), the default subregion is 0%, 0%, 100%,
// 100%, where as a special-case the percentages are relative to the
// dimensions of the filter region". A referenced primitive whose
// sub-region is empty (disabled) adds nothing to the union; when every one
// is, the default is empty too, and the primitive is disabled unless its
// own attributes give it a size.
func defaultSubregion(p *fprim, prims []fprim, region rect) rect {
	ins := p.merge
	if p.kind != tagFeMerge {
		switch p.kind {
		case tagFeFlood, tagFeTurbulence, tagFeImage, tagFeTile:
			return region
		case tagFeBlend, tagFeComposite, tagFeDisplacementMap:
			ins = []finput{p.in1, p.in2}
		default:
			ins = []finput{p.in1}
		}
	}
	if len(ins) == 0 {
		return region
	}
	var u rect
	have := false
	for _, in := range ins {
		if in.kind != inRef {
			return region
		}
		j := lastResult(prims, in.name)
		if j < 0 {
			return region
		}
		rc := prims[j].rect
		if !(rc.w() > 0 && rc.h() > 0) {
			if !have {
				u = rect{rc.x0, rc.y0, rc.x0, rc.y0}
			}
			continue
		}
		if !have || !(u.w() > 0 && u.h() > 0) {
			u, have = rc, true
			continue
		}
		u = rect{math.Min(u.x0, rc.x0), math.Min(u.y0, rc.y0), math.Max(u.x1, rc.x1), math.Max(u.y1, rc.y1)}
	}
	return u
}

// lastResult is the index of the closest preceding primitive whose result
// is name, or -1 (§9.2: "a reference to that result will use the closest
// preceding filter primitive with the given value for attribute result").
func lastResult(prims []fprim, name string) int {
	for j := len(prims) - 1; j >= 0; j-- {
		if prims[j].result == name {
			return j
		}
	}
	return -1
}

// primitiveRegion is fe's filter primitive subregion, user space, Filter
// Effects 1 §9.4: x, y, width and height "represent values in the
// coordinate system established by attribute primitiveUnits", each one
// absent taken from def (defaultSubregion). on is false when the width or
// height is not positive, which disables the primitive. Under
// objectBoundingBox a value is a fraction of the bounding box (usvg maps
// the filter region by the fractions instead, a behaviour it documents as
// wrong), in f32 as the rest of the geometry.
func (r *renderer) primitiveRegion(fe *node, obb bool, st *state, bb rect, def rect) (rect, bool) {
	get := func(id attrID, axis int) (float64, bool) {
		v, ok := fe.get(id)
		if !ok {
			return 0, false
		}
		return st.unitValue(v, obb, axis)
	}
	x, hx := get(aX, 0)
	y, hy := get(aY, 1)
	w, hw := get(aWidth, 0)
	h, hh := get(aHeight, 1)
	if !obb {
		or := func(v float64, has bool, d float64) float64 {
			if has {
				return v
			}
			return d
		}
		rc := rect32(or(x, hx, def.x0), or(y, hy, def.y0), or(w, hw, def.w()), or(h, hh, def.h()))
		return rc, rc.w() > 0 && rc.h() > 0
	}
	bw, bh := float32(bb.w()), float32(bb.h())
	pos := func(v float64, has bool, b0 float64, bs float32, d float64) float32 {
		if has {
			return float32(b0) + float32(float32(v)*bs)
		}
		return float32(d)
	}
	size := func(v float64, has bool, bs float32, d float64) float32 {
		if has {
			return float32(float32(v) * bs)
		}
		return float32(d)
	}
	x0, y0 := pos(x, hx, bb.x0, bw, def.x0), pos(y, hy, bb.y0, bh, def.y0)
	ww, hh2 := size(w, hw, bw, def.w()), size(h, hh, bh, def.h())
	rc := rect{float64(x0), float64(y0), float64(x0 + ww), float64(y0 + hh2)}
	return rc, ww > 0 && hh2 > 0
}

func resolveInput(fe *node, id attrID, prims []fprim) finput {
	v, ok := fe.get(id)
	if ok {
		var in finput
		switch v {
		case "SourceGraphic", "BackgroundImage", "BackgroundAlpha", "FillPaint", "StrokePaint":
			in = finput{kind: inSource}
		case "SourceAlpha":
			in = finput{kind: inAlpha}
		default:
			in = finput{kind: inRef, name: v}
		}
		if in.kind == inRef {
			found := false
			for i := range prims {
				if prims[i].result == in.name {
					found = true
					break
				}
			}
			if !found {
				if len(prims) > 0 {
					return finput{kind: inRef, name: prims[len(prims)-1].result}
				}
				return finput{kind: inSource}
			}
		}
		return in
	}
	if len(prims) > 0 {
		return finput{kind: inRef, name: prims[len(prims)-1].result}
	}
	return finput{kind: inSource}
}

// stdDev is a blur's stdDeviation, scaled for primitiveUnits: def (the
// initial value) when it is absent or not one or two numbers. Filter
// Effects 1 §9.14: "A negative value or a value of zero disables the effect
// of the given filter primitive (i.e., the result is the filter input
// image)", and "If stdDeviation is 0 in only one of X or Y, then the effect
// is that the blur is only applied in the direction that has a non-zero
// value": a negative value in either gives (0, 0), the input unblurred.
func stdDev(fe *node, sw, sh float64, def string) (float64, float64) {
	parse := func(text string) ([]float64, bool) {
		list, good := parseNumList(text)
		return list, good && (len(list) == 1 || len(list) == 2)
	}
	text, ok := fe.get(aStdDeviation)
	list, good := parse(text)
	if !ok || !good {
		list, _ = parse(def)
	}
	x, y := list[0], list[len(list)-1]
	if x < 0 || y < 0 {
		return 0, 0
	}
	x, y = x*sw, y*sh
	if !(x > 0) {
		x = 0
	}
	if !(y > 0) {
		y = 0
	}
	return x, y
}

func floodColor(fe *node) (rgba, float64) {
	c := black
	if v, ok := fe.get(aFloodColor); ok {
		if cc, good := parseColor(strings.TrimSpace(v)); good {
			c = cc
		}
	}
	a := float64(c.a)
	if v, ok := fe.get(aFloodOpacity); ok {
		if o, good := parseOpacity(v); good {
			a *= o
		}
	}
	c.a = 1
	return c, a
}

// colorMatrixKind resolves feColorMatrix, Filter Effects 1 §9.6: the type
// (matrix when absent or not a type) and its values. pass is set where the
// result is the input unchanged: "If the number of entries in the values
// list does not match the required number of entries by the type, the
// filter primitive acts as a pass through filter", and values left out
// default to the identity ("if type="matrix" defaults to the identity
// matrix", "saturate" to 1, "hueRotate" to 0, "which results in the
// identity matrix"), whose result is its input, exactly. A saturation is
// not clamped: "Values outside the 0..1 range under- or oversaturates the
// filter input image respectively."
func colorMatrixKind(fe *node) (kind uint8, vals []float64, pass bool) {
	v, has := fe.get(aValues)
	var list []float64
	if has {
		var good bool
		if list, good = parseNumList(v); !good {
			// An invalid value is no value: the default, the identity.
			has = false
		}
	}
	want := 20
	switch strings.TrimSpace(fe.str(aType)) {
	case "luminanceToAlpha":
		return cmLuminance, nil, false
	case "saturate":
		kind, want = cmSaturate, 1
	case "hueRotate":
		kind, want = cmHueRotate, 1
	default:
		kind = cmMatrix
	}
	if !has || len(list) != want {
		return kind, nil, true
	}
	return kind, list, false
}

// transferFunction is a transfer function element, Filter Effects 1 §9.7:
// a type that is not one of the five is the initial value, identity.
func transferFunction(c *node) transferFn {
	list := func() []float64 {
		if v, ok := c.get(aTableValues); ok {
			if l, good := parseNumList(v); good {
				return l
			}
		}
		return nil
	}
	switch strings.TrimSpace(c.str(aType)) {
	case "table":
		return transferFn{kind: 1, table: list()}
	case "discrete":
		return transferFn{kind: 2, table: list()}
	case "linear":
		return transferFn{kind: 3, a: numAttr(c, aSlope, 1), b: numAttr(c, aIntercept, 0)}
	case "gamma":
		return transferFn{kind: 4, a: numAttr(c, aAmplitude, 1), b: numAttr(c, aExponent, 1), off: numAttr(c, aOffset, 0)}
	}
	return transferFn{}
}

// approxZero32 is usvg's approx_zero_ulps(4) for an f32: v is zero, or
// within four units in the last place of a positive zero (a negative value
// other than -0 is not).
func approxZero32(v float32) bool {
	if math.Signbit(float64(v)) {
		return v == 0
	}
	return math.Float32bits(v) <= 4
}

// satI32 is Rust's `v as i32`: truncation toward zero, saturating at the
// type's bounds, NaN to zero (Go leaves an out-of-range conversion to the
// implementation).
func satI32(v float64) int32 {
	switch {
	case v != v:
		return 0
	case v >= math.MaxInt32:
		return math.MaxInt32
	case v <= math.MinInt32:
		return math.MinInt32
	}
	return int32(v)
}

// satU32 is Rust's `v as u32`.
func satU32(v float64) uint32 {
	switch {
	case !(v > 0):
		return 0
	case v >= math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(v)
}

// numAttr32 is an f32 attribute (usvg parses an f64 and rounds it).
func numAttr32(n *node, id attrID, def float32) float32 {
	if v, ok := n.get(id); ok {
		if f, ok := parseNum(v); ok {
			return float32(f)
		}
	}
	return def
}

// numList32 is a list attribute of f32 numbers; ok is false when it is
// absent or does not parse (usvg's Vec<f32>).
func numList32(n *node, id attrID) ([]float32, bool) {
	v, ok := n.get(id)
	if !ok {
		return nil, false
	}
	l, good := parseNumList(v)
	if !good {
		return nil, false
	}
	out := make([]float32, len(l))
	for i, f := range l {
		out[i] = float32(f)
	}
	return out, true
}

// morphology resolves feMorphology, Filter Effects 1 §9.17: the operator
// (erode unless dilate) and the radii, scaled by the bounding box for
// primitiveUnits="objectBoundingBox", each rounded to f32. The radius is
// one or two numbers, its initial value 0, and "A negative or zero value
// disables the effect of the given filter primitive (i.e., the result is
// the filter input image)": pass is set then, and for a radius that is not
// one or two numbers (the initial value). usvg makes such radii 1, as
// Chrome and Safari do.
func morphology(fe *node, sw, sh float64) (dilate bool, rx, ry float64, pass bool) {
	dilate = fe.str(aOperator) == "dilate"
	v, ok := fe.get(aRadius)
	if !ok {
		return dilate, 0, 0, true
	}
	list, good := parseNumList(v)
	if !good || (len(list) != 1 && len(list) != 2) {
		return dilate, 0, 0, true
	}
	a, b := list[0], list[len(list)-1]
	if !(a > 0 && b > 0) {
		return dilate, 0, 0, true
	}
	return dilate, float64(float32(a) * float32(sw)), float64(float32(b) * float32(sh)), false
}

// maxConvolveTaps bounds an feConvolveMatrix kernel, orderX x orderY: each
// tap is work every pixel of the region does (a 3x3 kernel is 9). resvg has
// no bound, and loops for days on a kernel the input chooses; a kernel
// larger than this, which no image needs, fails the render with ErrLimit.
const maxConvolveTaps = 1 << 16

// convolveMatrix resolves feConvolveMatrix, Filter Effects 1 §9.9. It is
// nil, with no error, where the primitive "acts as a pass through filter":
// "If the result of orderX * orderY is not equal to the the number of
// entries in the value list" (a kernelMatrix absent or not a list of
// numbers has none), and where another of its preconditions fails (the
// specification's changes: "Make feColorMatrix and feConvolveMatrix a pass
// through on unfulfilled pre-conditions"): an order that is not "greater
// than zero", a target outside "0 <= targetX < orderX". usvg makes each of
// these a transparent result instead. "If the specified divisor is 0 then
// the default value will be used instead" (usvg: transparent). A kernel of
// more than maxConvolveTaps taps that would be drawn is an ErrLimit error.
// sw and sh scale a kernelUnitLength for primitiveUnits.
func convolveMatrix(fe *node, sw, sh float64) (*convolveSpec, error) {
	// order: one or two numbers, "Values that are not integers will be
	// truncated"; an invalid value is the initial 3.
	ox, oy := int64(3), int64(3)
	if v, ok := fe.get(aOrder); ok {
		if l, good := parseNumList(v); good && (len(l) == 1 || len(l) == 2) {
			ox, oy = int64(satI32(l[0])), int64(satI32(l[len(l)-1]))
		}
	}
	if ox <= 0 || oy <= 0 {
		return nil, nil
	}
	taps := ox * oy // no overflow: each is at most 2^31-1
	v, has := fe.get(aKernelMatrix)
	n, counted := countNums(v)
	if !has || !counted || int64(n) != taps {
		return nil, nil
	}
	if taps > maxConvolveTaps {
		return nil, fmt.Errorf("raster: an feConvolveMatrix of order %dx%d exceeds %d taps: %w", ox, oy, maxConvolveTaps, errLimit)
	}
	kernel := make([]float32, 0, n)
	sc := numScanner{s: v}
	for !sc.atEnd() {
		f, _ := sc.number()
		kernel = append(kernel, float32(f))
		sc.skipSep()
	}
	var sum float32
	for _, k := range kernel {
		sum += k
	}
	// "The initial value is the sum of all values in kernelMatrix, with the
	// exception that if the sum is zero, then the divisor is set to 1." The
	// sum is rounded to six decimals, as usvg does "to prevent float
	// precision issues": the kernel's decimals summing to zero sum to zero.
	sum = float32(math.Round(float64(float32(sum*1_000_000)))) / 1_000_000
	if approxZero32(sum) {
		sum = 1
	}
	divisor := numAttr32(fe, aDivisor, sum)
	if divisor == 0 {
		divisor = sum
	}
	target := func(id attrID, order int64) (int, bool) {
		def := float32(math.Floor(float64(float32(order) / 2)))
		t := int64(satI32(float64(numAttr32(fe, id, def))))
		if t < 0 || t >= order {
			return 0, false
		}
		return int(t), true
	}
	tx, okx := target(aTargetX, ox)
	ty, oky := target(aTargetY, oy)
	if !okx || !oky {
		return nil, nil
	}
	edge := edgeDuplicate
	switch fe.str(aEdgeMode) {
	case "none":
		edge = edgeNone
	case "wrap":
		edge = edgeWrap
	}
	ux, uy := kernelUnitLength(fe, sw, sh)
	return &convolveSpec{
		cols: int(ox), rows: int(oy), targetX: tx, targetY: ty, kernel: kernel,
		divisor: divisor, bias: numAttr32(fe, aBias, 0), edge: edge,
		preserveAlpha: fe.str(aPreserveAlpha) == "true", unitX: ux, unitY: uy,
	}, nil
}

// kernelUnitLength is fe's kernelUnitLength in user space (sw and sh scale
// it for primitiveUnits), or (0, 0) when it is not specified. Filter
// Effects 1 §9.9 and §9.10: "The first number is the <dx> value. The second
// number is the <dy> value. If the <dy> value is not specified, it
// defaults to the same value as <dx>", and "If a negative or zero value is
// specified the default value will be used instead" (the default: a pixel
// of the offscreen image). An invalid value is not specified.
func kernelUnitLength(fe *node, sw, sh float64) (float64, float64) {
	v, ok := fe.get(aKernelUnitLength)
	if !ok {
		return 0, 0
	}
	l, good := parseNumList(v)
	if !good || (len(l) != 1 && len(l) != 2) {
		return 0, 0
	}
	x, y := l[0]*sw, l[len(l)-1]*sh
	if !(x > 0 && y > 0) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return 0, 0
	}
	return x, y
}

// colorChannel is a channel selector, usvg's: R, G or B, and A for
// anything else.
func colorChannel(fe *node, id attrID) uint8 {
	switch fe.str(id) {
	case "R":
		return 0
	case "G":
		return 1
	case "B":
		return 2
	}
	return 3
}

// countNums counts the numbers of a list without keeping them; ok is false
// when it does not parse.
func countNums(s string) (n int, ok bool) {
	sc := numScanner{s: s}
	for !sc.atEnd() {
		if _, good := sc.number(); !good {
			return 0, false
		}
		n++
		sc.skipSep()
	}
	return n, true
}
