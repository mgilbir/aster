package geo

// clipSegment is Liang–Barsky clipping of the segment a-b against the box,
// moving a and b onto the box in place. It reports whether any part of the
// segment is visible.
func clipSegment(a, b *[2]float64, x0, y0, x1, y1 float64) bool {
	ax, ay := a[0], a[1]
	bx, by := b[0], b[1]
	t0, t1 := 0.0, 1.0
	dx, dy := bx-ax, by-ay

	r := x0 - ax
	if dx == 0 && r > 0 {
		return false
	}
	r /= dx
	if dx < 0 {
		if r < t0 {
			return false
		}
		if r < t1 {
			t1 = r
		}
	} else if dx > 0 {
		if r > t1 {
			return false
		}
		if r > t0 {
			t0 = r
		}
	}

	r = x1 - ax
	if dx == 0 && r < 0 {
		return false
	}
	r /= dx
	if dx < 0 {
		if r > t1 {
			return false
		}
		if r > t0 {
			t0 = r
		}
	} else if dx > 0 {
		if r < t0 {
			return false
		}
		if r < t1 {
			t1 = r
		}
	}

	r = y0 - ay
	if dy == 0 && r > 0 {
		return false
	}
	r /= dy
	if dy < 0 {
		if r < t0 {
			return false
		}
		if r < t1 {
			t1 = r
		}
	} else if dy > 0 {
		if r > t1 {
			return false
		}
		if r > t0 {
			t0 = r
		}
	}

	r = y1 - ay
	if dy == 0 && r < 0 {
		return false
	}
	r /= dy
	if dy < 0 {
		if r > t1 {
			return false
		}
		if r > t0 {
			t0 = r
		}
	} else if dy > 0 {
		if r < t0 {
			return false
		}
		if r < t1 {
			t1 = r
		}
	}

	if t0 > 0 {
		a[0], a[1] = ax+float64(t0*dx), ay+float64(t0*dy)
	}
	if t1 < 1 {
		b[0], b[1] = ax+float64(t1*dx), ay+float64(t1*dy)
	}
	return true
}
