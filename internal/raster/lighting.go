package raster

import (
	"math"
	"strings"
)

// feDiffuseLighting and feSpecularLighting are resvg's lighting.rs: the
// specification's surface normals (Sobel kernels, with their own factors at
// the edges and corners) of the input's alpha, lit by a distant, point or
// spot light, in f32 as resvg computes them. resvg uses the lighting colour
// as it is, in whatever space the primitive works in (the result is tagged
// with it, unconverted), and ignores primitiveUnits for the light's
// position: both kept, being its output that is matched.

const (
	lightDistant uint8 = iota
	lightPoint
	lightSpot
)

// lightSource is usvg's LightSource, in user space.
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
	exponent     float32 // specularExponent, in [1, 128]
	color        [3]uint8
	light        lightSource
}

// lightingOf resolves a lighting primitive as usvg's
// convert_diffuse_lighting and convert_specular_lighting: nil (usvg's dummy,
// a transparent result) without a light source, or with a
// specularExponent outside [1, 128].
func (r *renderer) lightingOf(fe *node, specular bool) *lightingSpec {
	ls, ok := lightSourceOf(fe)
	if !ok {
		return nil
	}
	l := &lightingSpec{
		specular:     specular,
		surfaceScale: numAttr32(fe, aSurfaceScale, 1),
		light:        ls,
		color:        r.lightingColor(fe),
	}
	if specular {
		l.constant = numAttr32(fe, aSpecularConstant, 1)
		l.exponent = numAttr32(fe, aSpecularExponent, 1)
		if !(l.exponent >= 1 && l.exponent <= 128) {
			return nil
		}
	} else {
		l.constant = numAttr32(fe, aDiffuseConstant, 1)
	}
	return l
}

// lightSourceOf is usvg's convert_light_source: the first child that is a
// light source.
func lightSourceOf(fe *node) (lightSource, bool) {
	for _, c := range fe.kids {
		switch c.tag {
		case tagFeDistantLight:
			return lightSource{kind: lightDistant, azimuth: numAttr32(c, aAzimuth, 0), elevation: numAttr32(c, aElevation, 0)}, true
		case tagFePointLight:
			return lightSource{kind: lightPoint, x: numAttr32(c, aX, 0), y: numAttr32(c, aY, 0), z: numAttr32(c, aZ, 0)}, true
		case tagFeSpotLight:
			ls := lightSource{
				kind: lightSpot, x: numAttr32(c, aX, 0), y: numAttr32(c, aY, 0), z: numAttr32(c, aZ, 0),
				pointsX: numAttr32(c, aPointsAtX, 0), pointsY: numAttr32(c, aPointsAtY, 0), pointsZ: numAttr32(c, aPointsAtZ, 0),
				spotExp: numAttr32(c, aSpecularExponent, 1),
			}
			// usvg's PositiveF32: finite and not negative, else 1.
			if !(ls.spotExp >= 0) || math.IsInf(float64(ls.spotExp), 0) {
				ls.spotExp = 1
			}
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

// lightingColor is usvg's convert_lighting_color: white when absent, not a
// colour or inherit (resvg finds no value to inherit, the filter's
// included), the color property for currentColor (black when it has none),
// its alpha dropped.
func (r *renderer) lightingColor(fe *node) [3]uint8 {
	v, ok := fe.get(aLightingColor)
	if !ok {
		return [3]uint8{255, 255, 255}
	}
	v = strings.TrimSpace(v)
	if v == "currentColor" {
		c := r.inheritedState(fe).color
		return [3]uint8{c.r, c.g, c.b}
	}
	c, good := parseColor(v)
	if !good {
		return [3]uint8{255, 255, 255}
	}
	return [3]uint8{c.r, c.g, c.b}
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

// lighting is resvg's apply_diffuse_lighting / apply_specular_lighting:
// the light source moved into the region's pixels (transform_light_source),
// then each pixel lit by the normal of the input's alpha. An image smaller
// than 3x3 stays transparent, as resvg leaves it.
func (run *filterRun) lighting(p *fprim) (fimage, bool) {
	w, h := run.w, run.h
	l := p.light
	in := run.input(p.in1)
	pix := run.blank()
	res := fimage{pix: pix, region: run.region, linear: p.linear, owned: true}
	if w < 3 || h < 3 {
		return res, true
	}
	ls := run.deviceLight(l.light)
	alpha := func(x, y int) int { return int(in.pix[(y*w+x)*4+3]) }
	// A distant light's vector is the same everywhere.
	var lv vec3
	if ls.kind == lightDistant {
		az, el := radians32(ls.azimuth), radians32(ls.elevation)
		lv = vec3{float32(cos32(az) * cos32(el)), float32(sin32(az) * cos32(el)), sin32(el)}
	}
	ok := run.rows(l.cost(), func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				n := normalAt(alpha, x, y, w, h)
				if ls.kind != lightDistant {
					// Rounded before the subtraction below, which arm64 would
					// otherwise fuse with it.
					nz := float32(float32(float32(alpha(x, y))/255) * l.surfaceScale)
					v := vec3{ls.x, ls.y, ls.z}.sub(vec3{float32(x), float32(y), nz})
					lv = v.normalized()
				}
				col := lightColour(&ls, l.color, lv)
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
