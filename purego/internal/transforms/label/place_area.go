package label

import (
	"context"
	"math"
)

// areaPlacer holds the shared state of the three group-area placement
// methods.
type areaPlacer struct {
	ctx           context.Context
	sc            *scaler
	bm0, bm1, bm2 *bitmap // placed labels, area outlines, flood-fill visits
	avoidBaseMark bool
	markIndex     int
	textWidth     func(*Label) float64
}

func (p *areaPlacer) points(d *item) []Point {
	b := d.label.Base
	if b == nil || p.markIndex < 0 || p.markIndex >= len(b.Marks) {
		return nil
	}
	return b.Marks[p.markIndex].Points
}

func (p *areaPlacer) measure(l *Label) float64 {
	if p.textWidth == nil {
		return 0
	}
	return p.textWidth(l)
}

// span returns the (x1, y1, x2, y2) of an area sample; a missing second
// baseline coincides with the first.
func span(pt Point) (x1, y1, x2, y2 float64) {
	x1, y1, x2, y2 = pt.X, pt.Y, pt.X, pt.Y
	if pt.HasX2 {
		x2 = pt.X2
	}
	if pt.HasY2 {
		y2 = pt.Y2
	}
	return
}

func outOfBounds(x, y, textWidth, textHeight, width, height float64) bool {
	r := textWidth / 2
	if x-r < 0 || x+r > width {
		return true
	}
	r = textHeight / 2
	return y-r < 0 || y+r > height
}

// collision reports whether a label of aspect textWidth:textHeight scaled to
// height h centred at (x, y) hits the layout edge or a set bitmap cell.
func (p *areaPlacer) collision(x, y, textHeight, textWidth, h float64, bm0, bm1 *bitmap) bool {
	w := (textWidth * h) / (textHeight * 2)
	h /= 2
	x1, x2 := p.sc.scale(x-w), p.sc.scale(x+w)
	y1, y2 := p.sc.scale(y-h), p.sc.scale(y+h)
	return bm0.outOfBounds(x1, y1, x2, y2) ||
		bm0.getRange(x1, y1, x2, y2) ||
		(bm1 != nil && bm1.getRange(x1, y1, x2, y2))
}

// finish records a successful placement in the label bitmap.
func (p *areaPlacer) finish(d *item, textWidth, textHeight float64) {
	x, y := textWidth/2, textHeight/2
	p.bm0.setRange(p.sc.scale(d.x-x), p.sc.scale(d.y-y), p.sc.scale(d.x+x), p.sc.scale(d.y+y))
	d.align, d.baseline = "center", "middle"
}

// naive centres the label on the widest slice of the area and only shifts
// its alignment/baseline to keep it inside the layout.
func (p *areaPlacer) naive(d *item) (bool, error) {
	pts := p.points(d)
	textHeight := d.label.FontSize
	textWidth := p.measure(d.label)
	maxAreaWidth := 0.0
	for _, pt := range pts {
		x1, y1, x2, y2 := span(pt)
		areaWidth := math.Abs(x2 - x1 + y2 - y1)
		if areaWidth >= maxAreaWidth {
			maxAreaWidth = areaWidth
			d.setPos((x1+x2)/2, (y1+y2)/2)
		}
	}
	x, y := textWidth/2, textHeight/2
	x1, x2 := d.x-x, d.x+x
	y1, y2 := d.y-y, d.y+y
	width, height := p.sc.width, p.sc.height

	d.align = "center"
	if x1 < 0 && x2 <= width {
		d.align = "left"
	} else if 0 <= x1 && width < x2 {
		d.align = "right"
	}
	d.baseline = "middle"
	if y1 < 0 && y2 <= height {
		d.baseline = "top"
	} else if 0 <= y1 && height < y2 {
		d.baseline = "bottom"
	}
	return true, nil
}

// tryLabel searches for the largest scale at which the label fits at the
// bitmap cell (cx, cy); ok is false unless it beats maxSize.
func (p *areaPlacer) tryLabel(cx, cy int, maxSize, textWidth, textHeight float64) (x, y, size float64, ok bool) {
	x, y = p.sc.invert(cx), p.sc.invert(cy)
	lo, hi := maxSize, p.sc.height
	if !outOfBounds(x, y, textWidth, textHeight, p.sc.width, p.sc.height) &&
		!p.collision(x, y, textHeight, textWidth, lo, p.bm0, p.bm1) &&
		!p.collision(x, y, textHeight, textWidth, textHeight, p.bm0, nil) {
		for hi-lo >= 1 {
			mid := (lo + hi) / 2
			if p.collision(x, y, textHeight, textWidth, mid, p.bm0, p.bm1) {
				hi = mid
			} else {
				lo = mid
			}
		}
		if lo > maxSize {
			return x, y, lo, true
		}
	}
	return 0, 0, 0, false
}

// sliceFallback is the shared "place at slice centre" step used when no
// interior position was found and overlap with other areas is allowed.
func (p *areaPlacer) sliceFallback(d *item, x1, y1, x2, y2, textWidth, textHeight float64, maxAreaWidth *float64) bool {
	areaWidth := math.Abs(x2 - x1 + y2 - y1)
	x, y := (x1+x2)/2, (y1+y2)/2
	if areaWidth >= *maxAreaWidth &&
		!outOfBounds(x, y, textWidth, textHeight, p.sc.width, p.sc.height) &&
		!p.collision(x, y, textHeight, textWidth, textHeight, p.bm0, nil) {
		*maxAreaWidth = areaWidth
		d.setPos(x, y)
		return true
	}
	return false
}

// reducedSearch scans the bitmap cells between each area slice's midpoint and
// its borders for the position admitting the largest label.
func (p *areaPlacer) reducedSearch(d *item) (bool, error) {
	pts := p.points(d)
	textHeight := d.label.FontSize
	textWidth := p.measure(d.label)
	maxSize := 0.0
	if p.avoidBaseMark {
		maxSize = textHeight
	}
	placed, placed2 := false, false
	maxAreaWidth := 0.0

	for _, pt := range pts {
		if err := p.ctx.Err(); err != nil {
			return false, err
		}
		x1, y1, x2, y2 := span(pt)
		if x1 > x2 {
			x1, x2 = x2, x1
		}
		if y1 > y2 {
			y1, y2 = y2, y1
		}
		ix1, ix2 := p.sc.scale(x1), p.sc.scale(x2)
		xMid := toInt32(float64(ix1+ix2) / 2)
		iy1, iy2 := p.sc.scale(y1), p.sc.scale(y2)
		yMid := toInt32(float64(iy1+iy2) / 2)

		try := func(cx, cy int) {
			if x, y, size, ok := p.tryLabel(cx, cy, maxSize, textWidth, textHeight); ok {
				d.setPos(x, y)
				maxSize, placed = size, true
			}
		}
		// Bounded by the bitmap so absurd coordinates cannot spin.
		for cx := xMid; cx >= ix1 && cx >= -1<<20; cx-- {
			if err := p.ctx.Err(); err != nil {
				return false, err
			}
			for cy := yMid; cy >= iy1 && cy >= -1<<20; cy-- {
				try(cx, cy)
			}
		}
		for cx := xMid; cx <= ix2 && cx <= p.sc.w+1<<20; cx++ {
			if err := p.ctx.Err(); err != nil {
				return false, err
			}
			for cy := yMid; cy <= iy2 && cy <= p.sc.h+1<<20; cy++ {
				try(cx, cy)
			}
		}
		if !placed && !p.avoidBaseMark {
			if p.sliceFallback(d, x1, y1, x2, y2, textWidth, textHeight, &maxAreaWidth) {
				placed2 = true
			}
		}
	}
	if placed || placed2 {
		p.finish(d, textWidth, textHeight)
		return true, nil
	}
	return false, nil
}

var (
	floodDX = [4]int{-1, -1, 1, 1}
	floodDY = [4]int{-1, 1, -1, 1}
)

// floodFill grows outwards from each area slice's centre through free bitmap
// cells, trying a label at every cell it reaches.
func (p *areaPlacer) floodFill(d *item) (bool, error) {
	pts := p.points(d)
	textHeight := d.label.FontSize
	textWidth := p.measure(d.label)
	maxSize := 0.0
	if p.avoidBaseMark {
		maxSize = textHeight
	}
	placed, placed2 := false, false
	maxAreaWidth := 0.0
	var stack [][2]int
	steps := 0

	for _, pt := range pts {
		x1, y1, x2, y2 := span(pt)
		stack = append(stack, [2]int{p.sc.scale((x1 + x2) / 2), p.sc.scale((y1 + y2) / 2)})
		for len(stack) > 0 {
			if steps++; steps&1023 == 0 {
				if err := p.ctx.Err(); err != nil {
					return false, err
				}
			}
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			cx, cy := c[0], c[1]
			if p.bm0.get(cx, cy) || p.bm1.get(cx, cy) || p.bm2.get(cx, cy) {
				continue
			}
			p.bm2.set(cx, cy)
			for j := 0; j < 4; j++ {
				nx, ny := cx+floodDX[j], cy+floodDY[j]
				if !p.bm2.outOfBounds(nx, ny, nx, ny) {
					stack = append(stack, [2]int{nx, ny})
				}
			}
			x, y := p.sc.invert(cx), p.sc.invert(cy)
			lo, hi := maxSize, p.sc.height
			if !outOfBounds(x, y, textWidth, textHeight, p.sc.width, p.sc.height) &&
				!p.collision(x, y, textHeight, textWidth, lo, p.bm0, p.bm1) &&
				!p.collision(x, y, textHeight, textWidth, textHeight, p.bm0, nil) {
				for hi-lo >= 1 {
					mid := (lo + hi) / 2
					if p.collision(x, y, textHeight, textWidth, mid, p.bm0, p.bm1) {
						hi = mid
					} else {
						lo = mid
					}
				}
				if lo > maxSize {
					d.setPos(x, y)
					maxSize = lo
					placed = true
				}
			}
		}
		if !placed && !p.avoidBaseMark {
			if p.sliceFallback(d, x1, y1, x2, y2, textWidth, textHeight, &maxAreaWidth) {
				placed2 = true
			}
		}
	}
	if placed || placed2 {
		p.finish(d, textWidth, textHeight)
		return true, nil
	}
	return false, nil
}
