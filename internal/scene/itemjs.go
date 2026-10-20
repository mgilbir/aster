package scene

import (
	"errors"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// An item whose Raw holds a value (a property given something the typed field
// cannot represent: a string, an array, null for a number) is read the way
// upstream reads its plain object, with JavaScript's operators on the values
// as they are. The functions here are those readings; the typed fields keep
// the fast path for every other item.

// Or0 is `item[prop] || 0` as a value: the property as given when truthy.
func (it *Item) Or0(prop string) jsval.Value {
	v := it.Get(prop)
	if !v.IsTruthy() {
		return jsval.Num(0)
	}
	return v
}

// NumOr0 is `+(item[prop] || 0)` for a numeric property whose field is n: a
// truthy word converts to NaN where a NaN number reads as 0.
func (it *Item) NumOr0(prop string, n Num) float64 {
	if it.Raw != nil {
		if v, ok := it.Raw[prop]; ok {
			if !v.IsTruthy() {
				return 0
			}
			return jsval.ToNumber(v)
		}
	}
	return n.Zero()
}

// Truthy is the truthiness of a numeric property as given (`if
// (item.width)`).
func (it *Item) Truthy(prop string, n Num) bool { return it.truthyProp(prop, n) }

// JSPlus is the + operator on two values: a string, array or object on
// either side makes it a concatenation (of their String, taken with the
// item's Walker), anything else a sum.
func (it *Item) JSPlus(a, b jsval.Value) jsval.Value { return jsPlus(it.Walker(), a, b) }

// or0Value is `v || 0`.
func or0Value(v jsval.Value) jsval.Value {
	if !v.IsTruthy() {
		return jsval.Num(0)
	}
	return v
}

// translateBounds is `bounds.translate(item.x || 0, item.y || 0)`.
func (it *Item) translateBounds(b *Bounds) {
	if it.Raw == nil {
		b.Translate(it.X.Zero(), it.Y.Zero())
		return
	}
	b.TranslateJS(it.Walker(), it.Or0("x"), it.Or0("y"))
}

// rectBoundsJS is the rect mark's bound: bounds.set(x = item.x || 0, y =
// item.y || 0, (x + item.width) || 0, (y + item.height) || 0).
func (it *Item) rectBoundsJS(b *Bounds) {
	w := it.Walker()
	x, y := it.Or0("x"), it.Or0("y")
	b.SetJS(w, x, y, or0Value(jsPlus(w, x, it.Get("width"))), or0Value(jsPlus(w, y, it.Get("height"))))
}

// ruleBoundsJS is the rule mark's bound: the end is x2 as given, else the
// start.
func (it *Item) ruleBoundsJS(b *Bounds) {
	x1, y1 := it.Or0("x"), it.Or0("y")
	x2, y2 := it.Get("x2"), it.Get("y2")
	if x2.IsNullish() {
		x2 = x1
	}
	if y2.IsNullish() {
		y2 = y1
	}
	b.SetJS(it.Walker(), x1, y1, x2, y2)
}

// groupBoxJS is the group's own box: bounds.add(0, 0).add(group.width || 0,
// group.height || 0).
func (it *Item) groupBoxJS(b *Bounds) {
	b.Add(0, 0)
	b.AddJS(it.Walker(), it.Or0("width"), it.Or0("height"))
}

// clipBoxJS is boundClip's box: clipBounds.set(0, 0, group.width,
// group.height), the size as given.
func (g *Item) clipBoxJS(b *Bounds) {
	b.SetJS(g.Walker(), jsval.Num(0), jsval.Num(0), g.Get("width"), g.Get("height"))
}

// ImageSizeJS is imageWidth and imageHeight of the image mark: the size as
// given, else the natural size (scaled to keep the aspect ratio when the
// other side is given).
func ImageSizeJS(it *Item, nw, nh float64) (w, h jsval.Value) {
	nw, nh = it.naturalSizeOr(nw, nh)
	w, h = it.Get("width"), it.Get("height")
	if w.IsNullish() {
		w = jsval.Num(imageWidth(it, nw, nh))
	}
	if h.IsNullish() {
		h = jsval.Num(imageHeight(it, nw, nh))
	}
	return w, h
}

// imageOrigin is the image's drawn corner: (item.x || 0) -
// imageXOffset(item.align, w), and the same for y, both numbers.
func (it *Item) imageOrigin(w, h float64) (x, y float64) {
	x = it.NumOr0("x", it.X) - imageXOffset(it.StrictStr("align", it.Align), w)
	y = it.NumOr0("y", it.Y) - imageYOffset(it.StrictStr("baseline", it.Baseline), h)
	return x, y
}

// AnchorPointJS is the text anchor as upstream computes it: item.x || 0 and
// item.y || 0, displaced by `x += r * Math.cos(t)` when a radius is given, a
// concatenation when the anchor is a string.
func AnchorPointJS(it *Item) (x, y jsval.Value) {
	x, y = it.Or0("x"), it.Or0("y")
	if r := it.Or0("radius"); r.IsTruthy() {
		rf := jsval.ToNumber(r)
		t := it.NumOr0("theta", it.Theta()) - halfPi
		w := it.Walker()
		x = jsPlus(w, x, jsval.Num(float64(rf*jsmath.Cos(t))))
		y = jsPlus(w, y, jsval.Num(float64(rf*jsmath.Sin(t))))
	}
	return x, y
}

// TextOffsetsJS are the text mark's dx and dy: item.dx || 0, and (item.dy ||
// 0) + offset(item), which concatenates when dy is a string.
func TextOffsetsJS(it *Item) (dx, dy jsval.Value) {
	return it.Or0("dx"), jsPlus(it.Walker(), it.Or0("dy"), jsval.Num(BaselineOffset(it)))
}

// textBoundsJS is the text mark's bound for an item with a raw value.
func (bd *Bounder) textBoundsJS(b *Bounds, it *Item, mode int) error {
	wk := it.Walker()
	h := bd.Height(it)
	a := it.StrictStr("align", it.Align)
	x, y := AnchorPointJS(it)
	dx := it.Or0("dx")
	// (item.dy || 0) + offset(item) - Math.round(0.8*h): the subtraction
	// makes it a number, whatever the sum was.
	dy := jsval.ToNumber(jsPlus(wk, it.Or0("dy"), jsval.Num(BaselineOffset(it)))) - jsmath.Round(float64(0.8*h))
	lines, err := TextLinesErr(it)
	if err != nil {
		return err
	}
	var w float64
	if lines.Multi {
		h += float64(LineHeight(it) * float64(lines.N-1))
		for l, ok := lines.Next(); ok; l, ok = lines.Next() {
			if err := bd.poll(); err != nil {
				return err
			}
			w = jsMax(w, bd.Width(it, l))
		}
	} else {
		w = bd.Width(it, lines.Line())
	}
	switch a {
	case "center":
		dx = jsval.Num(jsval.ToNumber(dx) - float64(w/2))
	case "right":
		dx = jsval.Num(jsval.ToNumber(dx) - w)
	}
	x1 := jsPlus(wk, dx, x)
	y1 := jsPlus(wk, jsval.Num(dy), y)
	b.SetJS(wk, x1, y1, jsPlus(wk, x1, jsval.Num(w)), jsPlus(wk, y1, jsval.Num(h)))
	if it.AngleTruthy() && mode == 0 {
		b.Rotate(it.Angle.Val()*degToRad, jsval.ToNumber(x), jsval.ToNumber(y))
	}
	return nil
}

// ErrTextSplit is the TypeError upstream's textLines raises for a text that
// is neither a string nor an array when a line break is set:
// item.text.split is not a function.
var ErrTextSplit = errors.New("TypeError: item.text.split is not a function")

// ErrPathMatch is the TypeError upstream's path parser raises for a path, or
// a symbol shape that is not a built-in name, that is not a string.
var ErrPathMatch = errors.New("TypeError: path.match is not a function")

// ErrGradientStops and ErrGradientStop are the TypeErrors upstream's SVG
// renderer raises writing a gradient whose stops are null or undefined
// (reading their length), or hold such a stop (reading its offset).
var (
	ErrGradientStops = errors.New("TypeError: Cannot read properties of undefined (reading 'length')")
	ErrGradientStop  = errors.New("TypeError: Cannot read properties of undefined (reading 'offset')")
)
