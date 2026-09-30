package scene

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
)

// Bounds is an axis-aligned bounding box. The empty box has X1 > X2 (both
// sentinels at ±MaxFloat64), so Union/Add on it behave as on a fresh JavaScript
// Bounds. The zero value is NOT empty: use NewBounds or Clear.
type Bounds struct{ X1, Y1, X2, Y2 float64 }

// NewBounds returns an empty box.
func NewBounds() Bounds {
	var b Bounds
	b.Clear()
	return b
}

// Clear resets b to the empty box.
func (b *Bounds) Clear() *Bounds {
	b.X1, b.Y1 = math.MaxFloat64, math.MaxFloat64
	b.X2, b.Y2 = -math.MaxFloat64, -math.MaxFloat64
	return b
}

// Empty reports whether b is the cleared box.
func (b *Bounds) Empty() bool {
	return b.X1 == math.MaxFloat64 && b.Y1 == math.MaxFloat64 &&
		b.X2 == -math.MaxFloat64 && b.Y2 == -math.MaxFloat64
}

// Set assigns the box, ordering the corners.
func (b *Bounds) Set(x1, y1, x2, y2 float64) *Bounds {
	if x2 < x1 {
		b.X2, b.X1 = x1, x2
	} else {
		b.X1, b.X2 = x1, x2
	}
	if y2 < y1 {
		b.Y2, b.Y1 = y1, y2
	} else {
		b.Y1, b.Y2 = y1, y2
	}
	return b
}

// Add grows b to include the point. NaN coordinates are ignored, as the
// comparisons in upstream are all false for NaN.
func (b *Bounds) Add(x, y float64) *Bounds {
	if x < b.X1 {
		b.X1 = x
	}
	if y < b.Y1 {
		b.Y1 = y
	}
	if x > b.X2 {
		b.X2 = x
	}
	if y > b.Y2 {
		b.Y2 = y
	}
	return b
}

// Expand grows every side by d.
func (b *Bounds) Expand(d float64) *Bounds {
	b.X1 -= d
	b.Y1 -= d
	b.X2 += d
	b.Y2 += d
	return b
}

// Round snaps outward to integers.
func (b *Bounds) Round() *Bounds {
	b.X1, b.Y1 = math.Floor(b.X1), math.Floor(b.Y1)
	b.X2, b.Y2 = math.Ceil(b.X2), math.Ceil(b.Y2)
	return b
}

// Scale multiplies all coordinates.
func (b *Bounds) Scale(s float64) *Bounds {
	b.X1 *= s
	b.Y1 *= s
	b.X2 *= s
	b.Y2 *= s
	return b
}

// Translate shifts the box.
func (b *Bounds) Translate(dx, dy float64) *Bounds {
	b.X1 += dx
	b.X2 += dx
	b.Y1 += dy
	b.Y2 += dy
	return b
}

// RotatedPoints returns the four corners (x1,y1 / x1,y2 / x2,y1 / x2,y2) after
// rotating by angle radians about (x, y), in upstream's order.
func (b *Bounds) RotatedPoints(angle, x, y float64) [8]float64 {
	cos, sin := jsmath.Cos(angle), jsmath.Sin(angle)
	// float64(a*b) keeps the products rounded so arm64 does not fuse them into
	// FMA instructions, which would change the last bit against V8.
	cx := x - float64(x*cos) + float64(y*sin)
	cy := y - float64(x*sin) - float64(y*cos)
	return [8]float64{
		float64(cos*b.X1) - float64(sin*b.Y1) + cx, float64(sin*b.X1) + float64(cos*b.Y1) + cy,
		float64(cos*b.X1) - float64(sin*b.Y2) + cx, float64(sin*b.X1) + float64(cos*b.Y2) + cy,
		float64(cos*b.X2) - float64(sin*b.Y1) + cx, float64(sin*b.X2) + float64(cos*b.Y1) + cy,
		float64(cos*b.X2) - float64(sin*b.Y2) + cx, float64(sin*b.X2) + float64(cos*b.Y2) + cy,
	}
}

// Rotate replaces b with the bounds of itself rotated about (x, y).
func (b *Bounds) Rotate(angle, x, y float64) *Bounds {
	p := b.RotatedPoints(angle, x, y)
	b.Clear()
	return b.Add(p[0], p[1]).Add(p[2], p[3]).Add(p[4], p[5]).Add(p[6], p[7])
}

// Union grows b to enclose o.
func (b *Bounds) Union(o *Bounds) *Bounds {
	if o.X1 < b.X1 {
		b.X1 = o.X1
	}
	if o.Y1 < b.Y1 {
		b.Y1 = o.Y1
	}
	if o.X2 > b.X2 {
		b.X2 = o.X2
	}
	if o.Y2 > b.Y2 {
		b.Y2 = o.Y2
	}
	return b
}

// Intersect shrinks b to its overlap with o.
func (b *Bounds) Intersect(o *Bounds) *Bounds {
	if o.X1 > b.X1 {
		b.X1 = o.X1
	}
	if o.Y1 > b.Y1 {
		b.Y1 = o.Y1
	}
	if o.X2 < b.X2 {
		b.X2 = o.X2
	}
	if o.Y2 < b.Y2 {
		b.Y2 = o.Y2
	}
	return b
}

// Encloses reports whether b contains o.
func (b *Bounds) Encloses(o *Bounds) bool {
	return b.X1 <= o.X1 && b.X2 >= o.X2 && b.Y1 <= o.Y1 && b.Y2 >= o.Y2
}

// AlignsWith reports whether any side of b coincides with the same side of o.
func (b *Bounds) AlignsWith(o *Bounds) bool {
	return b.X1 == o.X1 || b.X2 == o.X2 || b.Y1 == o.Y1 || b.Y2 == o.Y2
}

// Intersects reports whether the boxes overlap (touching counts).
func (b *Bounds) Intersects(o *Bounds) bool {
	return !(b.X2 < o.X1 || b.X1 > o.X2 || b.Y2 < o.Y1 || b.Y1 > o.Y2)
}

// Contains reports whether the point lies inside (edges inclusive).
func (b *Bounds) Contains(x, y float64) bool {
	return !(x < b.X1 || x > b.X2 || y < b.Y1 || y > b.Y2)
}

// Width is X2-X1.
func (b *Bounds) Width() float64 { return b.X2 - b.X1 }

// Height is Y2-Y1.
func (b *Bounds) Height() float64 { return b.Y2 - b.Y1 }
