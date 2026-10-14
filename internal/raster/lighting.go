package raster

import (
	"math"
	"strings"
)

// feDiffuseLighting and feSpecularLighting are resvg's lighting.rs where it
// follows Filter Effects 1 (§9.10, §9.19, §11): the specification's surface
// normals (Sobel kernels, with their own factors at the edges and corners)
// of the input's alpha, lit by a distant, point or spot light, in f32 as
// resvg computes them. Where resvg departs from the specification this
// follows the specification: the lighting colour is converted to the space
// the primitive works in, the light's position is in primitiveUnits,
// lighting-color="inherit" inherits, a specularExponent is not limited to
// 1-128, kernelUnitLength spaces the normals' samples, and an image two
// pixels wide or high is lit.

const (
	lightDistant uint8 = iota
	lightPoint
	lightSpot
)

// lightSource is a light source, in user space.
type lightSource struct {
	kind               uint8
	azimuth, elevation float32 // distant
	x, y, z            float32 // point and spot
	pointsX, pointsY   float32 // spot
	pointsZ, spotExp   float32
	cone               float32
	hasCone            bool
}

// lightingSpec is a resolved feDiffuseLighting or feSpecularLighting.
type lightingSpec struct {
	specular     bool
	surfaceScale float32
	constant     float32 // diffuseConstant or specularConstant
	exponent     float32 // specularExponent
	color        [3]uint8
	light        lightSource
	// unitX and unitY are the kernelUnitLength, user space; 0 when it is
	// not specified (the normals' samples are then a pixel apart).
	unitX, unitY float64
}

// lightingOf resolves a lighting primitive (usvg's convert_diffuse_lighting
// and convert_specular_lighting): nil, a transparent result, without a
// light source (the content model asks for "exactly one light source
// element", and the specification says no more; Chromium gives a
// transparent result too). Filter Effects 1 gives specularExponent no range
// ("Exponent for specular term, larger is more "shiny". The initial value
// for specularExponent is 1."): usvg, keeping SVG 1.1's 1-128, makes the
// primitive transparent outside it.
func (r *renderer) lightingOf(fe *node, specular bool, u primUnits) *lightingSpec {
	ls, ok := lightSourceOf(fe, u)
	if !ok {
		return nil
	}
	sw, sh := 1.0, 1.0
	if u.obb {
		sw, sh = u.bb.w(), u.bb.h()
	}
	l := &lightingSpec{
		specular:     specular,
		surfaceScale: numAttr32(fe, aSurfaceScale, 1),
		light:        ls,
		color:        r.lightingColor(fe),
	}
	l.unitX, l.unitY = kernelUnitLength(fe, sw, sh)
	if specular {
		l.constant = numAttr32(fe, aSpecularConstant, 1)
		l.exponent = numAttr32(fe, aSpecularExponent, 1)
	} else {
		l.constant = numAttr32(fe, aDiffuseConstant, 1)
	}
	return l
}

// lightSourceOf is the first child of fe that is a light source, its
// position in user space: Filter Effects 1 §11.3 and §11.4, x, y and z are
// each a "location for the light source in the coordinate system
// established by attribute primitiveUnits on the filter element", and so
// are pointsAtX, Y and Z (usvg reads them as user space whatever the
// units). A spot light's specularExponent is any number (§11.4: "The
// initial value for specularExponent is 1"; usvg makes a negative one 1).
func lightSourceOf(fe *node, u primUnits) (lightSource, bool) {
	at := func(c *node, ix, iy, iz attrID) (float32, float32, float32) {
		x, y := u.point(numAttr(c, ix, 0), numAttr(c, iy, 0))
		return float32(x), float32(y), float32(u.length(numAttr(c, iz, 0)))
	}
	for _, c := range fe.kids {
		switch c.tag {
		case tagFeDistantLight:
			return lightSource{kind: lightDistant, azimuth: numAttr32(c, aAzimuth, 0), elevation: numAttr32(c, aElevation, 0)}, true
		case tagFePointLight:
			ls := lightSource{kind: lightPoint}
			ls.x, ls.y, ls.z = at(c, aX, aY, aZ)
			return ls, true
		case tagFeSpotLight:
			ls := lightSource{kind: lightSpot, spotExp: numAttr32(c, aSpecularExponent, 1)}
			ls.x, ls.y, ls.z = at(c, aX, aY, aZ)
			ls.pointsX, ls.pointsY, ls.pointsZ = at(c, aPointsAtX, aPointsAtY, aPointsAtZ)
			if v, ok := c.get(aLimitingConeAngle); ok {
				if f, good := parseNum(v); good {
					ls.cone, ls.hasCone = float32(f), true
				}
			}
			return ls, true
		}
	}
	return lightSource{}, false
}

// lightingColor is fe's lighting-color, Filter Effects 1 §11.5 (initial
// white, not inherited), its alpha dropped: white when absent or not a
// colour, the color property for currentColor, and for inherit the value
// of the parent's (§3: "all properties defined in this specification also
// accept the inherit keyword"), the filter element's, and so up: usvg
// finds nothing to inherit and gives white.
func (r *renderer) lightingColor(fe *node) [3]uint8 {
	for n := fe; n != nil; n = n.parent {
		v, ok := n.get(aLightingColor)
		if !ok {
			break
		}
		v = strings.TrimSpace(v)
		switch v {
		case "inherit":
			continue
		case "currentColor":
			c := r.inheritedState(n).color
			return [3]uint8{c.r, c.g, c.b}
		}
		c, good := parseColor(v)
		if !good {
			break
		}
		return [3]uint8{c.r, c.g, c.b}
	}
	return [3]uint8{255, 255, 255}
}

type vec3 struct{ x, y, z float32 }

// dot is resvg's Vector3::dot: each product rounded before it is added.
func (a vec3) dot(b vec3) float32 {
	return float32(float32(a.x*b.x)+float32(a.y*b.y)) + float32(a.z*b.z)
}

func (a vec3) length() float32 { return sqrt32(a.dot(a)) }

func (a vec3) sub(b vec3) vec3 { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }

// normalized is resvg's Vector3::normalized: a itself when its length is
// (about) zero.
func (a vec3) normalized() vec3 {
	l := a.length()
	if approxZero32(l) {
		return a
	}
	return vec3{a.x / l, a.y / l, a.z / l}
}

func sqrt32(v float32) float32 { return float32(math.Sqrt(float64(v))) }

// The f32 functions of Rust's libm, correctly rounded from f64.
func cos32(v float32) float32    { return float32(math.Cos(float64(v))) }
func sin32(v float32) float32    { return float32(math.Sin(float64(v))) }
func pow32(a, b float32) float32 { return float32(math.Pow(float64(a), float64(b))) }

// radians32 is Rust's f32::to_radians: v times PI/180 rounded to f32.
func radians32(v float32) float32 {
	const k = float32(math.Pi) / 180
	return v * k
}

// approxEq32 is usvg's approx_eq_ulps(4) for f32.
func approxEq32(a, b float32) bool {
	if math.Signbit(float64(a)) != math.Signbit(float64(b)) {
		return a == b
	}
	d := int64(math.Float32bits(a)) - int64(math.Float32bits(b))
	return d >= -4 && d <= 4
}

// lightNormal is resvg's Normal: the Sobel gradient of the alpha, negated,
// and the factors it is scaled by.
type lightNormal struct {
	fx, fy float32
	nx, ny float32
}

func newNormal(fx, fy float32, nx, ny int) lightNormal {
	return lightNormal{fx, fy, float32(-nx), float32(-ny)}
}

// The constants of resvg's normals, f32.
const (
	factor12 = float32(1.0 / 2.0)
	factor13 = float32(1.0 / 3.0)
	factor14 = float32(1.0 / 4.0)
	factor23 = float32(2.0 / 3.0)
)

// normalAt is the specification's normal of pixel (x, y) of an image w x h
// (w, h >= 3), by where the pixel lies: corner, edge or interior.
func normalAt(a func(x, y int) int, x, y, w, h int) lightNormal {
	r, b := w-1, h-1
	switch {
	case y == 0 && x == 0:
		c, rt, bt, br := a(0, 0), a(1, 0), a(0, 1), a(1, 1)
		return newNormal(factor23, factor23, -2*c+2*rt-bt+br, -2*c-rt+2*bt+br)
	case y == 0 && x == r:
		l, c, bl, bt := a(r-1, 0), a(r, 0), a(r-1, 1), a(r, 1)
		return newNormal(factor23, factor23, -2*l+2*c-bl+bt, -l-2*c+bl+2*bt)
	case y == b && x == 0:
		t, tr, c, rt := a(0, b-1), a(1, b-1), a(0, b), a(1, b)
		return newNormal(factor23, factor23, -t+tr-2*c+2*rt, -2*t-tr+2*c+rt)
	case y == b && x == r:
		tl, t, l, c := a(r-1, b-1), a(r, b-1), a(r-1, b), a(r, b)
		return newNormal(factor23, factor23, -tl+t-2*l+2*c, -tl-2*t+l+2*c)
	case y == 0:
		l, c, rt := a(x-1, 0), a(x, 0), a(x+1, 0)
		bl, bt, br := a(x-1, 1), a(x, 1), a(x+1, 1)
		return newNormal(factor13, factor12, -2*l+2*rt-bl+br, -l-2*c-rt+bl+2*bt+br)
	case y == b:
		tl, t, tr := a(x-1, b-1), a(x, b-1), a(x+1, b-1)
		l, c, rt := a(x-1, b), a(x, b), a(x+1, b)
		return newNormal(factor13, factor12, -tl+tr-2*l+2*rt, -tl-2*t-tr+l+2*c+rt)
	case x == 0:
		t, tr := a(0, y-1), a(1, y-1)
		c, rt := a(0, y), a(1, y)
		bt, br := a(0, y+1), a(1, y+1)
		return newNormal(factor12, factor13, -t+tr-2*c+2*rt-bt+br, -2*t-tr+2*bt+br)
	case x == r:
		tl, t := a(r-1, y-1), a(r, y-1)
		l, c := a(r-1, y), a(r, y)
		bl, bt := a(r-1, y+1), a(r, y+1)
		return newNormal(factor12, factor13, -tl+t-2*l+2*c-bl+bt, -tl-2*t+bl+2*bt)
	}
	tl, t, tr := a(x-1, y-1), a(x, y-1), a(x+1, y-1)
	l, rt := a(x-1, y), a(x+1, y)
	bl, bt, br := a(x-1, y+1), a(x, y+1), a(x+1, y+1)
	return newNormal(factor14, factor14, -tl+tr-2*l+2*rt-bl+br, -tl-2*t-tr+bl+2*bt+br)
}

// What a pixel of a lighting pass is charged, by what it computes: its
// normal (eight samples) and light, a point's or a spot's light vector, and
// each power (a spot light's, a specular exponent's other than 1). Each is
// 3-5 ns a charged operation.
const (
	lightCost      = 6
	lightVecCost   = 4
	lightPowerCost = 6
)

// cost is what a pixel of l's pass is charged.
func (l *lightingSpec) cost() int {
	c := lightCost
	if l.light.kind != lightDistant {
		c += lightVecCost
	}
	if l.light.kind == lightSpot {
		c += lightPowerCost
	}
	if l.specular && !approxEq32(l.exponent, 1) {
		c += lightPowerCost
	}
	return c
}

// lighting is feDiffuseLighting / feSpecularLighting (resvg's
// apply_diffuse_lighting / apply_specular_lighting): the light source moved
// into the region's pixels (transform_light_source), then each pixel lit by
// the normal of the input's alpha. The lighting colour is a colour of the
// document, sRGB; the primitive works in its color-interpolation-filters
// space (§9.1: "The color space in which a particular filter primitive
// performs its operations is determined by the value of the property
// color-interpolation-filters"), so in linearRGB the colour is converted to
// it first, as Chromium does (resvg uses it unconverted). An image less
// than two pixels wide or high has no normal (the specification's kernels
// need a neighbour each way) and stays transparent; resvg leaves one less
// than three so too.
func (run *filterRun) lighting(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	l := p.light
	in := run.input(p.in1)
	pix := run.blank()
	res := fimage{pix: pix, region: run.region, linear: p.linear, owned: true}
	if w < 2 || h < 2 {
		return res, true
	}
	color := l.color
	if p.linear {
		for i, c := range color {
			color[i] = srgbToLinearTable[c]
		}
	}
	ls := run.deviceLight(l.light)
	alpha := func(x, y int) int { return int(in.pix[(y*w+x)*4+3]) }
	cost := l.cost()
	// kernelUnitLength in device pixels: the distance of the normal's
	// samples. One pixel (or unspecified) is the plain kernel.
	tx, ty := tsScale(run.ts)
	dx, dy := min(l.unitX*tx, 1e290), min(l.unitY*ty, 1e290)
	spaced := l.unitX != 0 && (dx != 1 || dy != 1)
	if spaced {
		cost += lightSpacedCost
	}
	// A distant light's vector is the same everywhere.
	var lv vec3
	if ls.kind == lightDistant {
		az, el := radians32(ls.azimuth), radians32(ls.elevation)
		lv = vec3{float32(cos32(az) * cos32(el)), float32(sin32(az) * cos32(el)), sin32(el)}
	}
	ok := run.rows(cost, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				var n lightNormal
				if spaced {
					n = normalSpaced(in.pix, x, y, w, h, dx, dy)
				} else {
					n = normalAt(alpha, x, y, w, h)
				}
				if ls.kind != lightDistant {
					// Rounded before the subtraction below, which arm64 would
					// otherwise fuse with it.
					nz := float32(float32(float32(alpha(x, y))/255) * l.surfaceScale)
					v := vec3{ls.x, ls.y, ls.z}.sub(vec3{float32(x), float32(y), nz})
					lv = v.normalized()
				}
				col := lightColour(&ls, color, lv)
				f := l.factor(n, lv)
				o := pix[(y*w+x)*4 : (y*w+x)*4+4]
				for c := 0; c < 3; c++ {
					o[c] = satU8(f32Bound(0, float32(float32(col[c])*f), 255) + 0.5)
				}
				if l.specular {
					o[3] = max(o[0], o[1], o[2])
				} else {
					o[3] = 255
				}
			}
		}
	})
	return res, ok
}

// lightSpacedCost is what a pixel's normal costs more with its samples
// kernelUnitLength apart: eight bilinear samples, four pixels each.
const lightSpacedCost = 8

// normalSpaced is normalAt with the samples dx and dy device pixels from
// the pixel, Filter Effects 1 §9.10: "the dx and dy values (e.g.,
// I(x-dx,y-dy)), represent deltas relative to a given (x,y) position for
// the purpose of estimating the slope of the surface at that point. These
// deltas are determined by the value (explicit or implicit) of attribute
// kernelUnitLength." A sample between pixels is interpolated (bilinear) from
// the four around it. The kernel is the specification's for where the
// sample falls: the interior one where both samples along an axis are in
// the image, an edge's where only the one inward is, and where neither is
// (a distance past half the image) the interior one with the pixel's own
// value for both, a flat surface along that axis. A doubled sample is
// rounded on its own, as the sums are not fused (doubling is exact, so this
// changes nothing but what arm64 is allowed to emit).
func normalSpaced(pix []uint8, x, y, w, h int, dx, dy float64) lightNormal {
	a := func(fx, fy float64) float32 {
		q := bilinear(pix, w, h, fx, fy, edgeDuplicate)
		return q[3]
	}
	fx, fy := float64(x), float64(y)
	// The position of each side's sample, and whether it is in the image.
	l, r := fx-dx, fx+dx
	t, b := fy-dy, fy+dy
	hasL, hasR := l >= 0, r <= float64(w-1)
	hasT, hasB := t >= 0, b <= float64(h-1)
	// An axis with neither sample is flat: both are the pixel's own.
	if !hasL && !hasR {
		l, r, hasL, hasR = fx, fx, true, true
	}
	if !hasT && !hasB {
		t, b, hasT, hasB = fy, fy, true, true
	}
	switch {
	case !hasT && !hasL: // top-left corner
		c, rt, bt, br := a(fx, fy), a(r, fy), a(fx, b), a(r, b)
		return lightNormal{factor23, factor23, -(-float32(2*c) + float32(2*rt) - bt + br), -(-float32(2*c) - rt + float32(2*bt) + br)}
	case !hasT && !hasR: // top-right corner
		lt, c, bl, bt := a(l, fy), a(fx, fy), a(l, b), a(fx, b)
		return lightNormal{factor23, factor23, -(-float32(2*lt) + float32(2*c) - bl + bt), -(-lt - float32(2*c) + bl + float32(2*bt))}
	case !hasB && !hasL: // bottom-left corner
		tp, tr, c, rt := a(fx, t), a(r, t), a(fx, fy), a(r, fy)
		return lightNormal{factor23, factor23, -(-tp + tr - float32(2*c) + float32(2*rt)), -(-float32(2*tp) - tr + float32(2*c) + rt)}
	case !hasB && !hasR: // bottom-right corner
		tl, tp, lt, c := a(l, t), a(fx, t), a(l, fy), a(fx, fy)
		return lightNormal{factor23, factor23, -(-tl + tp - float32(2*lt) + float32(2*c)), -(-tl - float32(2*tp) + lt + float32(2*c))}
	case !hasT: // top row
		lt, c, rt := a(l, fy), a(fx, fy), a(r, fy)
		bl, bt, br := a(l, b), a(fx, b), a(r, b)
		return lightNormal{factor13, factor12, -(-float32(2*lt) + float32(2*rt) - bl + br), -(-lt - float32(2*c) - rt + bl + float32(2*bt) + br)}
	case !hasB: // bottom row
		tl, tp, tr := a(l, t), a(fx, t), a(r, t)
		lt, c, rt := a(l, fy), a(fx, fy), a(r, fy)
		return lightNormal{factor13, factor12, -(-tl + tr - float32(2*lt) + float32(2*rt)), -(-tl - float32(2*tp) - tr + lt + float32(2*c) + rt)}
	case !hasL: // left column
		tp, tr := a(fx, t), a(r, t)
		c, rt := a(fx, fy), a(r, fy)
		bt, br := a(fx, b), a(r, b)
		return lightNormal{factor12, factor13, -(-tp + tr - float32(2*c) + float32(2*rt) - bt + br), -(-float32(2*tp) - tr + float32(2*bt) + br)}
	case !hasR: // right column
		tl, tp := a(l, t), a(fx, t)
		lt, c := a(l, fy), a(fx, fy)
		bl, bt := a(l, b), a(fx, b)
		return lightNormal{factor12, factor13, -(-tl + tp - float32(2*lt) + float32(2*c) - bl + bt), -(-tl - float32(2*tp) + bl + float32(2*bt))}
	}
	tl, tp, tr := a(l, t), a(fx, t), a(r, t)
	lt, rt := a(l, fy), a(r, fy)
	bl, bt, br := a(l, b), a(fx, b), a(r, b)
	return lightNormal{factor14, factor14, -(-tl + tr - float32(2*lt) + float32(2*rt) - bl + br), -(-tl - float32(2*tp) - tr + bl + float32(2*bt) + br)}
}

// deviceLight is resvg's transform_light_source: a point's and a spot's
// positions mapped by the transform into the region's pixels, their z by
// the transform's mean scale (from its a and d entries).
func (run *filterRun) deviceLight(ls lightSource) lightSource {
	if ls.kind == lightDistant {
		return ls
	}
	m := run.ts
	a, b, c, d, e, f := float32(m.a), float32(m.b), float32(m.c), float32(m.d), float32(m.e), float32(m.f)
	mp := func(x, y float32) (float32, float32) {
		return float32(float32(a*x)+float32(c*y)) + e, float32(float32(b*x)+float32(d*y)) + f
	}
	rx, ry := float32(run.region.x0), float32(run.region.y0)
	sz := sqrt32(float32(a*a)+float32(d*d)) / float32(math.Sqrt2)
	x, y := mp(ls.x, ls.y)
	ls.x, ls.y = x-rx, y-ry
	if ls.kind == lightPoint {
		// resvg: light.z * (sx*sx + sy*sy).sqrt() / SQRT_2.
		ls.z = float32(ls.z*sqrt32(float32(a*a)+float32(d*d))) / float32(math.Sqrt2)
		return ls
	}
	ls.z *= sz
	x, y = mp(ls.pointsX, ls.pointsY)
	ls.pointsX, ls.pointsY = x-rx, y-ry
	ls.pointsZ *= sz
	return ls
}

// lightColour is resvg's light_color: the lighting colour, for a spot light
// attenuated by the angle to its axis (black outside its cone).
func lightColour(ls *lightSource, col [3]uint8, lv vec3) [3]uint8 {
	if ls.kind != lightSpot {
		return col
	}
	origin := vec3{ls.x, ls.y, ls.z}
	dir := vec3{ls.pointsX, ls.pointsY, ls.pointsZ}.sub(origin).normalized()
	s := -lv.dot(dir)
	if s <= 0 {
		return [3]uint8{}
	}
	if ls.hasCone && s < cos32(radians32(ls.cone)) {
		return [3]uint8{}
	}
	f := pow32(s, ls.spotExp)
	var out [3]uint8
	for i, v := range col {
		out[i] = satU8(f32Bound(0, float32(float32(v)*f), 255) + 0.5)
	}
	return out
}

// factor is resvg's light_factor of a diffuse or a specular lighting.
func (l *lightingSpec) factor(n lightNormal, lv vec3) float32 {
	flat := approxZero32(n.nx) && approxZero32(n.ny)
	surface := func() vec3 {
		k := l.surfaceScale / 255
		return vec3{float32(float32(n.nx*k) * n.fx), float32(float32(n.ny*k) * n.fy), 1}
	}
	if !l.specular {
		var k float32
		if flat {
			k = lv.z
		} else {
			nv := surface()
			k = nv.dot(lv) / nv.length()
		}
		return float32(l.constant * k)
	}
	hv := vec3{lv.x + 0, lv.y + 0, lv.z + 1}
	hl := hv.length()
	if approxZero32(hl) {
		return 0
	}
	var ndh float32
	if flat {
		ndh = hv.z / hl
	} else {
		nv := surface()
		ndh = nv.dot(hv) / nv.length() / hl
	}
	if !approxEq32(l.exponent, 1) {
		ndh = pow32(ndh, l.exponent)
	}
	return float32(l.constant * ndh)
}
