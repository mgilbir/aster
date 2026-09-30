package geo

import (
	"fmt"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// The Set implementations mirror how vega-geo's Projection operator drives a d3
// projection: `proj[key](value)` if the projection has such a method. The value
// is coerced the way each d3 setter's arithmetic coerces it, and where d3 would
// throw a TypeError (indexing null or undefined) an error is returned.

func setErr(typ, prop string, v jsval.Value) error {
	return fmt.Errorf("geo: cannot set projection %s of %q to %s: cannot read properties of %s", prop, typ, v.String(), v.Kind())
}

// elem reads v[i] where upstream would throw if v is null or undefined.
func elem(v jsval.Value, i int) (jsval.Value, bool) {
	if v.IsNullish() {
		return jsval.Undefined, false
	}
	return v.Index(i), true
}

func pairOf(v jsval.Value) (a, b float64, ok bool) {
	x, ok := elem(v, 0)
	if !ok {
		return 0, 0, false
	}
	y, _ := elem(v, 1)
	return jsval.ToNumber(x), jsval.ToNumber(y), true
}

func extentOf(v jsval.Value) (ext [4]float64, ok bool) {
	p0, ok0 := elem(v, 0)
	p1, ok1 := elem(v, 1)
	if !ok0 || !ok1 {
		return ext, false
	}
	a, oka := elem(p0, 0)
	b, okb := elem(p0, 1)
	c, okc := elem(p1, 0)
	d, okd := elem(p1, 1)
	if !oka || !okb || !okc || !okd {
		return ext, false
	}
	return [4]float64{jsval.ToNumber(a), jsval.ToNumber(b), jsval.ToNumber(c), jsval.ToNumber(d)}, true
}

func anglesOf(v jsval.Value) ([]float64, bool) {
	if v.IsNullish() {
		return nil, false
	}
	items := v.Items()
	out := make([]float64, 0, 3)
	for i := 0; i < len(items) && i < 3; i++ {
		out = append(out, jsval.ToNumber(items[i]))
	}
	for len(out) < 2 {
		out = append(out, nan)
	}
	return out, true
}

func (p *standard) Set(prop string, v jsval.Value) error {
	switch prop {
	case "clipAngle":
		p.setClipAngle(jsval.ToNumber(v))
	case "clipExtent":
		if v.IsNullish() {
			p.setClipExtent(nil)
			return nil
		}
		ext, ok := extentOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.setClipExtent(&ext)
	case "scale":
		p.SetScale(jsval.ToNumber(v))
	case "translate":
		x, y, ok := pairOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.SetTranslate(x, y)
	case "center":
		x, y, ok := pairOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.SetCenter(x, y)
	case "rotate":
		a, ok := anglesOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.SetRotate(a)
	case "parallels":
		if !p.conic {
			return nil
		}
		x, y, ok := pairOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.SetParallels(x, y)
	case "precision":
		n := jsval.ToNumber(v)
		p.SetPrecision(n)
	case "reflectX":
		p.SetReflectX(v.IsTruthy())
	case "reflectY":
		p.SetReflectY(v.IsTruthy())
	}
	return nil
}

func (p *identityProj) Set(prop string, v jsval.Value) error {
	switch prop {
	case "clipExtent":
		if v.IsNullish() {
			p.setClipExtentForFit(nil)
			return nil
		}
		ext, ok := extentOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.setClipExtentForFit(&ext)
	case "scale":
		p.SetScale(jsval.ToNumber(v))
	case "translate":
		x, y, ok := pairOf(v)
		if !ok {
			return setErr(p.typ, prop, v)
		}
		p.SetTranslate(x, y)
	case "reflectX":
		p.SetReflectX(v.IsTruthy())
	case "reflectY":
		p.SetReflectY(v.IsTruthy())
	}
	return nil
}

func (a *albersUSA) Set(prop string, v jsval.Value) error {
	switch prop {
	case "scale":
		a.SetScale(jsval.ToNumber(v))
	case "translate":
		x, y, ok := pairOf(v)
		if !ok {
			return setErr(albersUSAName, prop, v)
		}
		a.SetTranslate(x, y)
	case "precision":
		a.SetPrecision(jsval.ToNumber(v))
	}
	return nil
}
