package label

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// jsv is a coordinate as upstream holds it: a number, or a string (a bounds
// coordinate of a mark at a position given as text). The layout adds to
// coordinates, and `+` concatenates when either side is a string, so a label
// beside such a mark is placed at a string; every other operator, and the
// bitmap scaler's comparisons, read the number the string converts to (f).
type jsv struct {
	f   float64 // the number: the value itself, or what the string converts to
	s   string  // the text, when str
	str bool
}

func num(f float64) jsv { return jsv{f: f} }

// textOrNum is the coordinate whose number is f and whose text, when it has
// one, is *s.
func textOrNum(f float64, s *string) jsv {
	if s == nil {
		return jsv{f: f}
	}
	return jsv{f: f, s: *s, str: true}
}

// text is the coordinate as `+` concatenates it.
func (a jsv) text() string {
	if a.str {
		return a.s
	}
	return jsval.JSNumberString(a.f)
}

// plus is a + b.
func (a jsv) plus(b jsv) jsv {
	if a.str || b.str {
		s := a.text() + b.text()
		return jsv{f: jsval.StringToNumber(s), s: s, str: true}
	}
	return jsv{f: a.f + b.f}
}

// textPtr is the coordinate's text when it is a string, else nil.
func (a jsv) textPtr() *string {
	if !a.str {
		return nil
	}
	s := a.s
	return &s
}

// textPath is the state of a layout whose input holds a coordinate as text:
// each label's boundary as upstream holds it, and the text a label was placed
// at. A layout of numbers makes none of it.
type textPath struct {
	bnd map[*item][6]jsv
	out map[*item][2]*string
}

// boundaryJS is boundaryOf with the coordinates that are text kept as text.
func boundaryJS(l *Label, marktype, grouptype, lineAnchor string, markIndex int) [6]jsv {
	f := boundaryOf(l, marktype, grouptype, lineAnchor, markIndex)
	xy := func(x, y jsv) [6]jsv { return [6]jsv{x, x, x, y, y, y} }
	var lt Texts
	if l.Strs != nil {
		lt = *l.Strs
	}
	var bt BaseTexts
	if l.Base != nil && l.Base.Strs != nil {
		bt = *l.Base.Strs
	}
	switch {
	case marktype == "" || l.Base == nil:
		return xy(textOrNum(f[0], lt.X), textOrNum(f[3], lt.Y))
	case marktype == "line" || marktype == "area":
		return xy(textOrNum(f[0], bt.X), textOrNum(f[3], bt.Y))
	case grouptype == "line":
		return xy(num(f[0]), num(f[3]))
	}
	b := l.Base.Bounds
	x1, y1 := textOrNum(b.X1, bt.Bounds[0]), textOrNum(b.Y1, bt.Bounds[1])
	x2, y2 := textOrNum(b.X2, bt.Bounds[2]), textOrNum(b.Y2, bt.Bounds[3])
	return [6]jsv{x1, num(x1.plus(x2).f / 2), x2, y1, num(y1.plus(y2).f / 2), y2}
}

// markPlacerJS is markPlacer for a layout with text coordinates: the offsets
// are added to a boundary coordinate with +, which concatenates when it is
// text, and everything else reads the number the text converts to.
func markPlacerJS(sc *scaler, bms [2]*bitmap, anchors []int8, offsets []float64, textWidthOf func(*Label) float64, tp *textPath) func(*item) (bool, error) {
	width, height := sc.width, sc.height
	bm0, bm1 := bms[0], bms[1]
	n := len(offsets)

	test := func(x1, x2, y1, y2 int, isInside bool) bool {
		if bm0.outOfBounds(x1, y1, x2, y2) {
			return false
		}
		bm := bm0
		if isInside && bm1 != nil {
			bm = bm1
		}
		return !bm.getRange(x1, y1, x2, y2)
	}

	return func(d *item) (bool, error) {
		b := tp.bnd[d]
		textHeight := d.label.FontSize

		if b[2].f < 0 || b[5].f < 0 || b[0].f > width || b[3].f > height {
			return false, nil
		}
		textWidth := d.textWidth
		for i := 0; i < n; i++ {
			dx := float64(anchors[i]&0x3) - 1
			dy := float64((anchors[i]>>2)&0x3) - 1

			isInside := (dx == 0 && dy == 0) || offsets[i] < 0
			sizeFactor := 1.0
			if dx != 0 && dy != 0 {
				sizeFactor = math.Sqrt2 / 2
			}
			insideFactor := 1.0
			if offsets[i] < 0 {
				insideFactor = -1
			}

			x1 := b[1+int(dx)].plus(num(float64(offsets[i] * dx * sizeFactor)))
			yc := b[4+int(dy)].plus(num(float64(insideFactor*textHeight*dy) / 2)).plus(num(float64(offsets[i] * dy * sizeFactor)))
			y1 := num(yc.f - float64(textHeight/2))
			y2 := yc.plus(num(textHeight / 2))

			ix1, iy1, iy2 := sc.scaleJS(x1), sc.scaleJS(y1), sc.scaleJS(y2)

			if textWidth == 0 || math.IsNaN(textWidth) {
				if !test(ix1, ix1, iy1, iy2, isInside) {
					continue
				}
				textWidth = 0
				if textWidthOf != nil {
					textWidth = textWidthOf(d.label)
				}
			}

			xc := x1.plus(num(float64(insideFactor*textWidth*dx) / 2))
			x1 = num(xc.f - float64(textWidth/2))
			x2 := xc.plus(num(textWidth / 2))
			ix1, ix2 := sc.scaleJS(x1), sc.scaleJS(x2)

			if test(ix1, ix2, iy1, iy2, isInside) {
				pick := func(lo, mid, hi jsv, delta float64) jsv {
					switch {
					case delta == 0:
						return mid
					case delta*insideFactor < 0:
						return hi
					}
					return lo
				}
				px, py := pick(x1, xc, x2, dx), pick(y1, yc, y2, dy)
				d.x, d.y = px.f, py.f
				if px.str || py.str {
					tp.out[d] = [2]*string{px.textPtr(), py.textPtr()}
				}
				d.hasPos = true
				d.align = aligns[int(dx*insideFactor)+1]
				d.baseline = baselines[int(dy*insideFactor)+1]
				bm0.setRange(ix1, iy1, ix2, iy2)
				return true, nil
			}
		}
		return false, nil
	}
}
