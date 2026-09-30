package vega

import (
	"slices"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale/color"
	"github.com/mgilbir/aster/internal/transforms"
	"github.com/mgilbir/aster/internal/transforms/contour"
)

func init() {
	// heatmap paints each raster grid into a canvas and stores it in the `as`
	// field (default "image") of the tuple or mark item, where an image mark
	// picks it up. The canvas is a jsval object (width, height) carrying the
	// painted bitmap as its host payload.
	transformFactories["heatmap"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		hp := contour.HeatmapParams{Shared: p.str("resolve") == "shared"}
		if f := p.field("field"); !f.IsNil() {
			hp.Field = contour.Accessor(f.Get)
		}
		px := &pixelDatum{}
		switch c := p.Get("color").(type) {
		case transforms.Field:
			px.colorDep = pixelDependent(c)
			hp.ColorFn = func(datum jsval.Value, pix contour.Pixel) (contour.RGB, error) {
				if px.colorDep {
					return parseColor(c.Apply(px.at(datum, pix))), nil
				}
				if !px.colorOK || px.colorFor != datum.ObjValue() {
					px.colorFor, px.colorOK = datum.ObjValue(), true
					px.color = parseColor(c.Apply(px.fresh(datum, pix)))
				}
				return px.color, nil
			}
		default:
			// `rgb(color || '#888')`: a falsy constant selects the default.
			if v := p.Value("color"); v.IsTruthy() {
				hp.Color, hp.ColorSet = parseColor(v), true
			}
		}
		switch o := p.Get("opacity").(type) {
		case transforms.Field:
			px.opacityDep = pixelDependent(o)
			hp.OpacityFn = func(datum jsval.Value, pix contour.Pixel) (float64, error) {
				if px.opacityDep {
					return jsval.ToNumber(o.Apply(px.at(datum, pix))), nil
				}
				if !px.opacityOK || px.opacityFor != datum.ObjValue() {
					px.opacityFor, px.opacityOK = datum.ObjValue(), true
					px.opacity = jsval.ToNumber(o.Apply(px.fresh(datum, pix)))
				}
				return px.opacity, nil
			}
		default:
			if v := p.Value("opacity"); v.IsTruthy() {
				hp.Opacity = jsval.ToNumber(v)
			}
		}
		as := p.str("as")
		if as == "" {
			as = "image"
		}
		imgs, err := contour.Heatmap(n.g.ctx, in, hp)
		if err != nil {
			return nil, err
		}
		for i, t := range in {
			if o := t.ObjValue(); o != nil {
				o.Set(as, canvasValue(imgs[i]))
			}
		}
		return in, nil
	})
}

// canvasValue presents a painted image as a canvas object.
func canvasValue(img *contour.Image) jsval.Value {
	o := jsval.ObjectOf("width", jsval.Int(img.Width), "height", jsval.Int(img.Height))
	o.SetHost(img)
	return jsval.Obj(o)
}

// parseColor is `rgb(v)`: d3-color reads the string form of its argument, and
// a color it cannot parse has NaN channels (which paint as 0).
func parseColor(v jsval.Value) contour.RGB {
	c := color.ParseRGB(v.AsString())
	return contour.RGB{R: c.R, G: c.G, B: c.B}
}

// pixelDependent reports whether the expression reads a per-pixel field; only
// then is it evaluated for every pixel, otherwise once per grid.
func pixelDependent(b transforms.Field) bool {
	return slices.ContainsFunc(b.Fields, func(f string) bool {
		return f == "$x" || f == "$y" || f == "$value" || f == "$max"
	})
}

// pixelDatum builds the object the color and opacity expressions see as
// `datum`: the source tuple extended with $x, $y, $value and $max, which is
// rewritten for every pixel.
type pixelDatum struct {
	colorDep, opacityDep bool

	obj *jsval.Object
	src *jsval.Object

	color      contour.RGB
	colorFor   *jsval.Object
	colorOK    bool
	opacity    float64
	opacityFor *jsval.Object
	opacityOK  bool
}

// fresh is the datum of a grid's first evaluation: pixel fields at zero, and
// the grid's maximum.
func (d *pixelDatum) fresh(datum jsval.Value, pix contour.Pixel) jsval.Value {
	return d.at(datum, contour.Pixel{Max: pix.Max})
}

func (d *pixelDatum) at(datum jsval.Value, pix contour.Pixel) jsval.Value {
	if src := datum.ObjValue(); d.obj == nil || src != d.src {
		d.src = src
		if src != nil {
			d.obj = src.Clone()
		} else {
			d.obj = jsval.NewObject(4)
		}
	}
	d.obj.Set("$x", jsval.Num(pix.X))
	d.obj.Set("$y", jsval.Num(pix.Y))
	d.obj.Set("$value", jsval.Num(pix.Value))
	d.obj.Set("$max", jsval.Num(pix.Max))
	return jsval.Obj(d.obj)
}
