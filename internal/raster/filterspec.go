package raster

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Filter resolution follows usvg (crates/usvg/src/parser/filter.rs): the
// filter region, primitive sub-regions, unit handling, input resolution and
// per-primitive attribute defaults all mirror it so that the pixel pipeline in
// filter.go sees exactly what resvg's would.

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
	// bbox-scaled), each rounded to f32 as usvg's are.
	dilate bool
	rx, ry float64
	conv   *convolveSpec

	// feDisplacementMap: the scale (bbox-scaled, f32) and the channel of
	// in2 each axis reads (0-3: R, G, B, A).
	scale    float64
	xch, ych uint8

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
// whose bounding box is bb. nil means the filter is invalid, in which case the
// element is not rendered.
func (r *renderer) resolveFilter(fn *node, st *state, bb rect, hasBB bool) *filterSpec {
	if fn == nil || fn.tag != tagFilter {
		return nil
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
	if !(w > 0 && h > 0) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return nil
	}
	region := rect32(x, y, w, h)
	if unitsOBB {
		if !hasBB {
			return nil // filters on zero-sized shapes are not allowed
		}
		region = bboxTransformRect(region, bb)
	}
	// The first filter in the href chain that has children provides them.
	var src *node
	for f, i := fn, 0; f != nil && i < 8; i++ {
		if f.tag != tagFilter {
			return nil
		}
		if len(f.kids) > 0 {
			src = f
			break
		}
		f = r.hrefTarget(f)
	}
	if src == nil {
		return nil
	}
	if primOBB && !hasBB {
		return nil
	}
	scaleW, scaleH := 1.0, 1.0
	if primOBB {
		scaleW, scaleH = bb.w(), bb.h()
	}
	spec := &filterSpec{node: fn, rect: region}
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
		sub, ok := r.primitiveRegion(fe, primOBB, st, bb, hasBB, region)
		if !ok {
			break
		}
		p := fprim{kind: fe.tag, rect: sub}
		prims := spec.prims
		switch fe.tag {
		case tagFeDropShadow:
			p.stdX, p.stdY = stdDev(fe, scaleW, scaleH, "2 2")
			p.dx = numAttr(fe, aDx, 2) * scaleW
			p.dy = numAttr(fe, aDy, 2) * scaleH
			p.color, p.alpha = floodColor(fe)
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeGaussianBlur:
			p.stdX, p.stdY = stdDev(fe, scaleW, scaleH, "0 0")
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeOffset:
			p.dx = numAttr(fe, aDx, 0) * scaleW
			p.dy = numAttr(fe, aDy, 0) * scaleH
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeBlend:
			p.mode = blendNormal
			if v, ok := fe.get(aMode); ok {
				p.mode = blendNames[strings.TrimSpace(v)]
			}
			p.in1 = resolveInput(fe, aIn, prims)
			p.in2 = resolveInput(fe, aIn2, prims)
		case tagFeFlood:
			p.color, p.alpha = floodColor(fe)
		case tagFeComposite:
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
			case "arithmetic":
				p.op = opArithmetic
				p.k = [4]float64{numAttr(fe, aK1, 0), numAttr(fe, aK2, 0), numAttr(fe, aK3, 0), numAttr(fe, aK4, 0)}
			}
			p.in1 = resolveInput(fe, aIn, prims)
			p.in2 = resolveInput(fe, aIn2, prims)
		case tagFeMerge:
			for _, c := range fe.kids {
				if c.tag == tagFeMergeNode {
					p.merge = append(p.merge, resolveInput(c, aIn, prims))
				}
			}
		case tagFeColorMatrix:
			p.cmKind, p.cmValues = colorMatrixKind(fe)
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeComponentTransfer:
			p.in1 = resolveInput(fe, aIn, prims)
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
				if f, ok := transferFunction(c); ok {
					p.funcs[idx] = f
				}
			}
		case tagFeTile:
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeMorphology:
			p.dilate, p.rx, p.ry = morphology(fe, scaleW, scaleH)
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeConvolveMatrix:
			conv, err := convolveMatrix(fe)
			if err != nil {
				r.fail(err)
				return nil
			}
			if conv == nil {
				p.kind = tagFeUnsupported
				break
			}
			p.conv = conv
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeDisplacementMap:
			// usvg scales by the mean of the bounding box's sides for
			// primitiveUnits="objectBoundingBox" (one scale for both axes).
			mean := float32(float32(scaleW)+float32(scaleH)) / 2
			p.scale = float64(float32(numAttr32(fe, aScale, 0) * mean))
			p.xch, p.ych = colorChannel(fe, aXChannelSelector), colorChannel(fe, aYChannelSelector)
			p.in1 = resolveInput(fe, aIn, prims)
			p.in2 = resolveInput(fe, aIn2, prims)
		case tagFeTurbulence:
			t := turbulenceOf(fe)
			p.turb = &t
		case tagFeDiffuseLighting, tagFeSpecularLighting:
			p.light = r.lightingOf(fe, fe.tag == tagFeSpecularLighting)
			if p.light == nil {
				p.kind = tagFeUnsupported
				break
			}
			p.in1 = resolveInput(fe, aIn, prims)
		case tagFeImage:
			if p.img = r.feImageOf(fe, sub); p.img == nil {
				p.kind = tagFeUnsupported
			}
		default:
			// Every primitive is handled above; anything else would be
			// usvg's dummy, a transparent result keeping the chain intact.
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
		return nil
	}
	return spec
}

func (r *renderer) primitiveRegion(fe *node, obb bool, st *state, bb rect, hasBB bool, region rect) (rect, bool) {
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
	or := func(v float64, has bool, d float64) float64 {
		if has {
			return v
		}
		return d
	}
	base := region
	if fe.tag == tagFeFlood || fe.tag == tagFeImage {
		if obb {
			if !hasBB {
				return rect{}, false
			}
			rc := rect32(or(x, hx, 0), or(y, hy, 0), or(w, hw, 1), or(h, hh, 1))
			if !(rc.w() > 0 && rc.h() > 0) {
				return rect{}, false
			}
			return bboxTransformRect(rc, bb), true
		}
	}
	if obb {
		sub := rect32(or(x, hx, 0), or(y, hy, 0), or(w, hw, 1), or(h, hh, 1))
		if !(sub.w() > 0 && sub.h() > 0) {
			return rect{}, false
		}
		// usvg maps the sub-region onto the filter region (its documented
		// "wrong" behaviour, kept for parity).
		return bboxTransformRect(base, sub), true
	}
	rc := rect32(or(x, hx, base.x0), or(y, hy, base.y0), or(w, hw, base.w()), or(h, hh, base.h()))
	if !(rc.w() > 0 && rc.h() > 0) {
		return rect{}, false
	}
	return rc, true
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

func stdDev(fe *node, sw, sh float64, def string) (float64, float64) {
	text, ok := fe.get(aStdDeviation)
	if !ok {
		text = def
	}
	list, good := parseNumList(text)
	var x, y float64
	switch {
	case good && len(list) == 2:
		x, y = list[0], list[1]
	case good && len(list) == 1:
		x, y = list[0], list[0]
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

func colorMatrixKind(fe *node) (uint8, []float64) {
	vals, hasVals := []float64(nil), false
	if v, ok := fe.get(aValues); ok {
		if l, good := parseNumList(v); good {
			vals, hasVals = l, true
		}
	}
	switch strings.TrimSpace(fe.str(aType)) {
	case "saturate":
		if hasVals {
			if len(vals) > 0 {
				return cmSaturate, []float64{math.Max(0, math.Min(1, vals[0]))}
			}
			return cmSaturate, []float64{1}
		}
	case "hueRotate":
		if hasVals {
			if len(vals) > 0 {
				return cmHueRotate, []float64{vals[0]}
			}
			return cmHueRotate, []float64{0}
		}
	case "luminanceToAlpha":
		return cmLuminance, nil
	default:
		if hasVals && len(vals) == 20 {
			return cmMatrix, vals
		}
	}
	return cmMatrix, []float64{1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0}
}

func transferFunction(c *node) (transferFn, bool) {
	list := func() []float64 {
		if v, ok := c.get(aTableValues); ok {
			if l, good := parseNumList(v); good {
				return l
			}
		}
		return nil
	}
	switch strings.TrimSpace(c.str(aType)) {
	case "identity":
		return transferFn{}, true
	case "table":
		return transferFn{kind: 1, table: list()}, true
	case "discrete":
		return transferFn{kind: 2, table: list()}, true
	case "linear":
		return transferFn{kind: 3, a: numAttr(c, aSlope, 1), b: numAttr(c, aIntercept, 0)}, true
	case "gamma":
		return transferFn{kind: 4, a: numAttr(c, aAmplitude, 1), b: numAttr(c, aExponent, 1), off: numAttr(c, aOffset, 0)}, true
	}
	return transferFn{}, false
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

// morphology resolves feMorphology as usvg's convert_morphology: the
// operator (erode unless dilate) and the radii, scaled by the bounding box
// for primitiveUnits="objectBoundingBox". A radius list that is not one or
// two numbers, or zero, is 1 (Chrome's and Safari's behaviour, which usvg
// follows); a negative one leaves the default, 1.
func morphology(fe *node, sw, sh float64) (dilate bool, rx, ry float64) {
	dilate = fe.str(aOperator) == "dilate"
	sw32, sh32 := float32(sw), float32(sh)
	x, y := sw32, sh32
	if list, ok := numList32(fe, aRadius); ok {
		var a, b float32
		switch len(list) {
		case 2:
			a, b = list[0], list[1]
		case 1:
			a, b = list[0], list[0]
		}
		za, zb := approxZero32(a), approxZero32(b)
		switch {
		case za && zb:
			a, b = 1, 1
		case za:
			a = 1
		case zb:
			b = 1
		}
		if !math.Signbit(float64(a)) && !math.Signbit(float64(b)) {
			x, y = a*sw32, b*sh32
		}
	}
	return dilate, float64(x), float64(y)
}

// maxConvolveTaps bounds an feConvolveMatrix kernel, orderX x orderY: each
// tap is work every pixel of the region does (a 3x3 kernel is 9). resvg has
// no bound, and loops for days on a kernel the input chooses (or crashes,
// when orderX x orderY wraps a u32); a kernel larger than this, which no
// image needs, fails the render with ErrLimit.
const maxConvolveTaps = 1 << 16

// convolveMatrix resolves feConvolveMatrix as usvg's
// convert_convolve_matrix. It is nil, with no error, where usvg makes the
// primitive a dummy (a transparent result): a kernelMatrix whose length is
// not orderX x orderY, a divisor of zero, a target outside the kernel. A
// kernel of more than maxConvolveTaps taps that usvg would draw is an
// ErrLimit error. kernelUnitLength is ignored, as by resvg.
func convolveMatrix(fe *node) (*convolveSpec, error) {
	ox, oy := int64(3), int64(3)
	if v, ok := fe.get(aOrder); ok {
		// svgtypes' number list: the first number (3 when there is none or
		// it does not parse), then the second (the first's when absent);
		// each truncated to an i32.
		sc := numScanner{s: v}
		x := int64(3)
		y, hasY := int64(0), false
		if !sc.atEnd() {
			if f, good := sc.number(); good {
				x = int64(satI32(f))
				sc.skipSep()
				if !sc.atEnd() {
					if f, good := sc.number(); good {
						y, hasY = int64(satI32(f)), true
					}
				}
			}
		}
		if !hasY {
			y = x
		}
		if x > 0 && y > 0 {
			ox, oy = x, y
		}
	}
	// usvg compares the list's length with orderX * orderY as a u32, which
	// wraps.
	taps := ox * oy
	want := uint32(taps)
	v, has := fe.get(aKernelMatrix)
	n, counted := countNums(v)
	if !has || !counted || int64(n) != int64(want) {
		// No kernel: usvg's matrix is empty, which matches a product that
		// wraps to zero (resvg then indexes past it and panics).
		if taps != 0 && want == 0 {
			return nil, fmt.Errorf("raster: an feConvolveMatrix of order %dx%d exceeds %d taps: %w", ox, oy, maxConvolveTaps, errLimit)
		}
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
	// usvg rounds the sum to six decimals "to prevent float precision
	// issues", in f32.
	sum = float32(math.Round(float64(float32(sum*1_000_000)))) / 1_000_000
	if approxZero32(sum) {
		sum = 1
	}
	divisor := numAttr32(fe, aDivisor, sum)
	if approxZero32(divisor) {
		return nil, nil
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
	return &convolveSpec{
		cols: int(ox), rows: int(oy), targetX: tx, targetY: ty, kernel: kernel,
		divisor: divisor, bias: numAttr32(fe, aBias, 0), edge: edge,
		preserveAlpha: fe.str(aPreserveAlpha) == "true",
	}, nil
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
