package geo

import (
	"maps"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// Ports of d3-geo-projection 4.0.0's airy, hammer, bottomley, bonne, winkel3
// and littrow, registered as a caller registers them. gen_pipeline.mjs
// registers the originals in vega with vega.projection(name, constructor);
// TestVegaPipeline replays its cases with these and compares exactly. Each
// product is rounded (float64(a*b)) as JavaScript rounds it.

// jsSqrt is d3-geo-projection's sqrt: 0 for x <= 0.
func jsSqrt(x float64) float64 {
	if x > 0 {
		return math.Sqrt(x)
	}
	return 0
}

// sinci is d3-geo-projection's sinci: x / sin(x), 1 at 0.
func sinci(x float64) float64 {
	if truthy(x) {
		return x / jsmath.Sin(x)
	}
	return 1
}

func airyRaw(beta float64) RawFuncs {
	tanBeta2 := jsmath.Tan(beta / 2)
	b := float64(2*jsmath.Log(jsmath.Cos(beta/2))) / float64(tanBeta2*tanBeta2)
	fwd := func(x, y float64) (float64, float64) {
		cosx, cosy, siny := jsmath.Cos(x), jsmath.Cos(y), jsmath.Sin(y)
		cosz := float64(cosy * cosx)
		t := -0.5
		if truthy(1 - cosz) {
			t = jsmath.Log((1+cosz)/2) / (1 - cosz)
		}
		k := -(t + b/(1+cosz))
		return float64(float64(k*cosy) * jsmath.Sin(x)), float64(k * siny)
	}
	inv := func(x, y float64) (float64, float64) {
		r := jsSqrt(float64(x*x) + float64(y*y))
		z := -beta / 2
		if !truthy(r) {
			return 0, 0
		}
		for i := 50; ; {
			z2 := z / 2
			cosz2, sinz2 := jsmath.Cos(z2), jsmath.Sin(z2)
			tanz2 := sinz2 / cosz2
			lnsecz2 := -jsmath.Log(abs(cosz2))
			f := 1.0
			if cosz2 < 0 {
				f = 0.7
			}
			num := float64(2/tanz2*lnsecz2) - float64(b*tanz2) - r
			den := -lnsecz2/float64(sinz2*sinz2) + 1 - b/float64(float64(2*cosz2)*cosz2)
			delta := float64(num / den * f)
			z -= delta
			if i--; !(abs(delta) > epsilon && i > 0) {
				break
			}
		}
		sinz := jsmath.Sin(z)
		return jsmath.Atan2(float64(x*sinz), float64(r*jsmath.Cos(z))), asin(float64(y*sinz) / r)
	}
	return RawFuncs{Forward: fwd, Invert: inv}
}

func hammerRaw(b float64) RawFuncs {
	aea := azimuthalEqualAreaRaw
	switch {
	case b == 1:
		return RawFuncs{Forward: aea.fwd, Invert: aea.inv}
	case math.IsInf(b, 1):
		return RawFuncs{Forward: func(lambda, phi float64) (float64, float64) {
			return float64(lambda*jsmath.Cos(phi)) / jsmath.Cos(phi/2), float64(2 * jsmath.Sin(phi/2))
		}}
	}
	a := b
	return RawFuncs{
		Forward: func(lambda, phi float64) (float64, float64) {
			x, y := aea.fwd(lambda/b, phi)
			return float64(x * a), y
		},
		Invert: func(x, y float64) (float64, float64) {
			l, p := aea.inv(x/a, y)
			return float64(l * b), p
		},
	}
}

func bottomleyRaw(sinPsi float64) RawFuncs {
	return RawFuncs{
		Forward: func(lambda, phi float64) (float64, float64) {
			rho := halfPi - phi
			eta := rho
			if truthy(rho) {
				eta = float64(float64(lambda*sinPsi)*jsmath.Sin(rho)) / rho
			}
			return float64(rho*jsmath.Sin(eta)) / sinPsi, halfPi - float64(rho*jsmath.Cos(eta))
		},
		Invert: func(x, y float64) (float64, float64) {
			x1, y1 := float64(x*sinPsi), halfPi-y
			rho := jsSqrt(float64(x1*x1) + float64(y1*y1))
			eta := jsmath.Atan2(x1, y1)
			k := 1.0
			if truthy(rho) {
				k = rho / jsmath.Sin(rho)
			}
			return float64(k*eta) / sinPsi, halfPi - rho
		},
	}
}

func bonneRaw(phi0 float64) RawFuncs {
	if !truthy(phi0) {
		return RawFuncs{
			Forward: func(lambda, phi float64) (float64, float64) { return float64(lambda * jsmath.Cos(phi)), phi },
			Invert:  func(x, y float64) (float64, float64) { return x / jsmath.Cos(y), y },
		}
	}
	cot := 1 / jsmath.Tan(phi0)
	return RawFuncs{
		Forward: func(lambda, phi float64) (float64, float64) {
			rho := cot + phi0 - phi
			e := rho
			if truthy(rho) {
				e = float64(lambda*jsmath.Cos(phi)) / rho
			}
			return float64(rho * jsmath.Sin(e)), cot - float64(rho*jsmath.Cos(e))
		},
		Invert: func(x, y float64) (float64, float64) {
			y = cot - y
			rho := jsSqrt(float64(x*x) + float64(y*y))
			phi := cot + phi0 - rho
			return float64(rho/jsmath.Cos(phi)) * jsmath.Atan2(x, y), phi
		},
	}
}

func aitoffRaw(x, y float64) (float64, float64) {
	cosy := jsmath.Cos(y)
	x /= 2
	sincia := sinci(acos(float64(cosy * jsmath.Cos(x))))
	return float64(float64(float64(2*cosy)*jsmath.Sin(x)) * sincia), float64(jsmath.Sin(y) * sincia)
}

var winkel3Raw = RawFuncs{
	Forward: func(lambda, phi float64) (float64, float64) {
		ax, ay := aitoffRaw(lambda, phi)
		return (ax + lambda/halfPi) / 2, (ay + phi) / 2
	},
	Invert: func(x, y float64) (float64, float64) {
		lambda, phi := x, y
		for i := 25; ; {
			cosphi, sinphi := jsmath.Cos(phi), jsmath.Sin(phi)
			sin2phiAngle := jsmath.Sin(2 * phi)
			sin2phi, cos2phi := float64(sinphi*sinphi), float64(cosphi*cosphi)
			sinlambda := jsmath.Sin(lambda)
			coslambda2, sinlambda2 := jsmath.Cos(lambda/2), jsmath.Sin(lambda/2)
			sin2lambda2 := float64(sinlambda2 * sinlambda2)
			C := 1 - float64(float64(cos2phi*coslambda2)*coslambda2)
			var E, F float64
			if truthy(C) {
				F = 1 / C
				E = float64(acos(float64(cosphi*coslambda2)) * jsSqrt(F))
			}
			fx := float64(0.5*(float64(float64(float64(2*E)*cosphi)*sinlambda2)+lambda/halfPi)) - x
			fy := float64(0.5*(float64(E*sinphi)+phi)) - y
			dxdlambda := float64(float64(0.5*F)*(float64(cos2phi*sin2lambda2)+float64(float64(float64(E*cosphi)*coslambda2)*sin2phi))) + 0.5/halfPi
			dxdphi := float64(F * (float64(sinlambda*sin2phiAngle)/4 - float64(float64(E*sinphi)*sinlambda2)))
			dydlambda := float64(float64(0.125*F) * (float64(sin2phiAngle*sinlambda2) - float64(float64(float64(E*sinphi)*cos2phi)*sinlambda)))
			dydphi := float64(float64(0.5*F)*(float64(sin2phi*coslambda2)+float64(float64(E*sin2lambda2)*cosphi))) + 0.5
			den := float64(dxdphi*dydlambda) - float64(dydphi*dxdlambda)
			dlambda := (float64(fy*dxdphi) - float64(fx*dydphi)) / den
			dphi := (float64(fx*dydlambda) - float64(fy*dxdlambda)) / den
			lambda -= dlambda
			phi -= dphi
			if i--; !((abs(dlambda) > epsilon || abs(dphi) > epsilon) && i > 0) {
				break
			}
		}
		return lambda, phi
	},
}

var littrowRaw = RawFuncs{
	Forward: func(lambda, phi float64) (float64, float64) {
		return jsmath.Sin(lambda) / jsmath.Cos(phi), float64(jsmath.Tan(phi) * jsmath.Cos(lambda))
	},
	Invert: func(x, y float64) (float64, float64) {
		x2, y2 := float64(x*x), float64(y*y)
		y21 := y2 + 1
		x2y21 := x2 + y21
		var cosPhi float64
		if truthy(x) {
			cosPhi = float64(math.Sqrt2 / 2 * jsSqrt((x2y21-jsSqrt(float64(x2y21*x2y21)-float64(4*x2)))/x2))
		} else {
			cosPhi = 1 / jsSqrt(y21)
		}
		return asin(float64(x * cosPhi)), float64(sign(y) * acos(cosPhi))
	},
}

// customPorts are the ports as their d3-geo-projection constructors set them
// up: parameters, scale, clip angle and center.
var customPorts = Registry{
	"airy": {
		Raw:       func(p map[string]float64) RawFuncs { return airyRaw(float64(p["radius"] * radians)) },
		Params:    map[string]float64{"radius": 90},
		Scale:     179.976,
		ClipAngle: 147,
	},
	"hammer": {
		Raw:    func(p map[string]float64) RawFuncs { return hammerRaw(p["coefficient"]) },
		Params: map[string]float64{"coefficient": 2},
		Scale:  169.529,
	},
	"bottomley": {
		Raw:    func(p map[string]float64) RawFuncs { return bottomleyRaw(p["fraction"]) },
		Params: map[string]float64{"fraction": 0.5},
		Scale:  158.837,
	},
	"bonne": {
		Raw:    func(p map[string]float64) RawFuncs { return bonneRaw(float64(p["parallel"] * radians)) },
		Params: map[string]float64{"parallel": 45},
		Scale:  123.082,
		Center: [2]float64{0, 26.1441},
	},
	"winkel3": {
		Raw:   func(map[string]float64) RawFuncs { return winkel3Raw },
		Scale: 158.837,
	},
	"littrow": {
		Raw:       func(map[string]float64) RawFuncs { return littrowRaw },
		Scale:     144.049,
		ClipAngle: 90 - 1e-3,
	},
}

func TestCustomPortsValidate(t *testing.T) {
	for name, c := range customPorts {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestCustomCopy checks that a custom projection's copy draws as the original,
// with the properties and parameters copy() carries over, and keeps its type
// whatever the registry holds where it is copied.
func TestCustomCopy(t *testing.T) {
	o := jsval.ObjectOf("type", jsval.Str("Bonne"), "scale", jsval.Num(90), "rotate", jsval.Arr([]jsval.Value{jsval.Num(20), jsval.Num(-10)}),
		"parallel", jsval.Num(30), "clipAngle", jsval.Num(80), "reflectY", jsval.True)
	p, err := ConfigureProjection(bg, customPorts, nil, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Copy()
	if c.Type() != "bonne" {
		t.Errorf("copy type %q", c.Type())
	}
	for _, pt := range [][2]float64{{0, 0}, {30, 40}, {-120, -60}, {170, 10}} {
		x0, y0, _ := p.Forward(pt[0], pt[1])
		x1, y1, _ := c.Forward(pt[0], pt[1])
		if !same(x0, x1) || !same(y0, y1) {
			t.Errorf("%v: copy projects to (%v, %v), original to (%v, %v)", pt, x1, y1, x0, y0)
		}
	}
	// A parameter of 0 is not copied (copy() tests it for truthiness): the
	// copy has the default, 45.
	o.Set("parallel", jsval.Num(0))
	p, err = ConfigureProjection(bg, customPorts, nil, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Copy().(*standard).params["parallel"]; got != 45 {
		t.Errorf("parallel 0 copied as %v, want the default 45", got)
	}
}

// TestCustomTypeValidate checks the definitions Validate refuses.
func TestCustomTypeValidate(t *testing.T) {
	ok := func(map[string]float64) RawFuncs { return winkel3Raw }
	for _, tc := range []struct {
		c    CustomType
		want string
	}{
		{CustomType{}, "no Raw"},
		{CustomType{Raw: func(map[string]float64) RawFuncs { return RawFuncs{} }}, "no Forward"},
		{CustomType{Raw: func(map[string]float64) RawFuncs { panic("x") }}, "panicked"},
		{CustomType{Raw: ok, Params: map[string]float64{"angle": 1}}, `"angle"`},
		{CustomType{Raw: ok, Params: map[string]float64{"tilt": math.Inf(-1)}}, "not finite"},
		{CustomType{Raw: ok, Scale: math.NaN()}, "scale"},
		{CustomType{Raw: ok, ClipAngle: math.NaN()}, "clip angle"},
		{CustomType{Raw: ok, ClipAngle: -1e300}, "clipAngle"},
		{CustomType{Raw: ok, Center: [2]float64{0, math.Inf(1)}}, "center"},
		{CustomType{Raw: ok, Rotate: [3]float64{math.NaN(), 0, 0}}, "rotation"},
	} {
		err := tc.c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want an error mentioning %q", tc.c, err, tc.want)
		}
	}
	// Raw is handed a copy: changing it changes nothing.
	c := CustomType{Raw: func(p map[string]float64) RawFuncs { p["tilt"] = 99; return winkel3Raw }, Params: map[string]float64{"tilt": 1}}
	before := maps.Clone(c.Params)
	if err := c.Validate(); err != nil || !maps.Equal(c.Params, before) {
		t.Errorf("Validate: %v, params %v", err, c.Params)
	}
}

// TestCustomInvert checks a custom projection's inverse (the raw inverse
// behind the inverse translate, scale, reflection and rotation) by round
// trips; Vega's invert() of one without an inverse gives no position.
func TestCustomInvert(t *testing.T) {
	for _, name := range []string{"airy", "hammer", "bottomley", "bonne", "winkel3", "littrow"} {
		o := jsval.ObjectOf("type", jsval.Str(name), "scale", jsval.Num(130), "translate", jsval.Arr([]jsval.Value{jsval.Num(300), jsval.Num(200)}),
			"rotate", jsval.Arr([]jsval.Value{jsval.Num(15), jsval.Num(-20), jsval.Num(30)}), "reflectX", jsval.True)
		p, err := ConfigureProjection(bg, customPorts, nil, o, nil)
		if err != nil {
			t.Fatal(err)
		}
		for lon := -40.0; lon <= 40; lon += 20 {
			for lat := -40.0; lat <= 40; lat += 20 {
				x, y, _ := p.Forward(lon, lat)
				l, f, ok := p.Invert(x, y)
				if !ok || math.Abs(l-lon) > 1e-6 || math.Abs(f-lat) > 1e-6 {
					t.Errorf("%s: [%v, %v] -> (%v, %v) -> [%v, %v] %v", name, lon, lat, x, y, l, f, ok)
				}
			}
		}
	}
	noInverse := Registry{"fwd": {Raw: func(map[string]float64) RawFuncs { return RawFuncs{Forward: winkel3Raw.Forward} }}}
	p, err := noInverse.NewProjection("fwd")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := p.Invert(10, 10); ok {
		t.Error("a projection with no inverse inverted a point")
	}
}
