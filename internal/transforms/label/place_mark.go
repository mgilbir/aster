package label

import "math"

var (
	aligns    = [3]string{"right", "center", "left"}
	baselines = [3]string{"bottom", "middle", "top"}
)

// markPlacer returns the placement function for labels around a mark or
// point: it tries each anchor/offset pair in order and takes the first
// position whose bitmap footprint is free.
func markPlacer(sc *scaler, bms [2]*bitmap, anchors []int8, offsets []float64, textWidthOf func(*Label) float64) func(*item) (bool, error) {
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
		b := d.boundary
		textHeight := d.label.FontSize

		// A mark outside the layout cannot be labelled.
		if b[2] < 0 || b[5] < 0 || b[0] > width || b[3] > height {
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

			x1 := b[1+int(dx)] + float64(offsets[i]*dx*sizeFactor)
			yc := b[4+int(dy)] + float64(insideFactor*textHeight*dy)/2 + float64(offsets[i]*dy*sizeFactor)
			y1 := yc - textHeight/2
			y2 := yc + textHeight/2

			ix1, iy1, iy2 := sc.scale(x1), sc.scale(y1), sc.scale(y2)

			if textWidth == 0 || math.IsNaN(textWidth) {
				// Probe with a one-pixel-wide label first so the (costly) text
				// measurement only happens for positions that could work.
				if !test(ix1, ix1, iy1, iy2, isInside) {
					continue
				}
				textWidth = 0
				if textWidthOf != nil {
					textWidth = textWidthOf(d.label)
				}
			}

			xc := x1 + float64(insideFactor*textWidth*dx)/2
			x1 = xc - textWidth/2
			x2 := xc + textWidth/2
			ix1, ix2 := sc.scale(x1), sc.scale(x2)

			if test(ix1, ix2, iy1, iy2, isInside) {
				switch {
				case dx == 0:
					d.x = xc
				case dx*insideFactor < 0:
					d.x = x2
				default:
					d.x = x1
				}
				switch {
				case dy == 0:
					d.y = yc
				case dy*insideFactor < 0:
					d.y = y2
				default:
					d.y = y1
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
