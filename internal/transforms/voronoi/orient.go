package voronoi

import "math"

// This file is the adaptive-precision orient2d predicate of Shewchuk's robust
// predicates. Every product that feeds a sum is wrapped in an explicit
// float64 conversion: Go may otherwise fuse x*y+z into an FMA, which is exact
// where the algorithm relies on the rounded product and would change results.

const (
	epsilon        = 1.1102230246251565e-16
	splitter       = 134217729
	resultErrBound = (3 + 8*epsilon) * epsilon
	ccwErrBoundA   = (3 + 16*epsilon) * epsilon
	ccwErrBoundB   = (2 + 12*epsilon) * epsilon
	ccwErrBoundC   = (9 + 64*epsilon) * epsilon * epsilon
)

// orient2d is positive when a, b, c turn clockwise in a y-up frame (that is,
// counter-clockwise on screen with y down) — the opposite sign of the textbook
// determinant, as in robust-predicates — negative for the reverse, and zero
// only when the points are exactly collinear.
func orient2d(ax, ay, bx, by, cx, cy float64) float64 {
	detLeft := float64((ay - cy) * (bx - cx))
	detRight := float64((ax - cx) * (by - cy))
	det := detLeft - detRight
	detSum := math.Abs(detLeft + detRight)
	if math.Abs(det) >= ccwErrBoundA*detSum {
		return det
	}
	return -orient2dAdapt(ax, ay, bx, by, cx, cy, detSum)
}

func split(a float64) (hi, lo float64) {
	c := float64(splitter * a)
	hi = c - (c - a)
	return hi, a - hi
}

// twoProduct returns x = fl(a*b) and the exact rounding error y.
func twoProduct(a, b float64) (x, y float64) {
	x = float64(a * b)
	ahi, alo := split(a)
	bhi, blo := split(b)
	y = float64(alo*blo) - (x - float64(ahi*bhi) - float64(alo*bhi) - float64(ahi*blo))
	return x, y
}

// twoTwoDiff computes (s1,s0) - (t1,t0) as a four-component expansion.
func twoTwoDiff(s1, s0, t1, t0 float64, out *[4]float64) {
	i := s0 - t0
	bv := s0 - i
	out[0] = s0 - (i + bv) + (bv - t0)
	j := s1 + i
	bv = j - s1
	z := s1 - (j - bv) + (i - bv)
	i = z - t1
	bv = z - i
	out[1] = z - (i + bv) + (bv - t1)
	u3 := j + i
	bv = u3 - j
	out[2] = j - (u3 - bv) + (i - bv)
	out[3] = u3
}

func crossExpansion(a, b, c, d float64, out *[4]float64) {
	// a*b - c*d as an expansion
	s1, s0 := twoProduct(a, b)
	t1, t0 := twoProduct(c, d)
	twoTwoDiff(s1, s0, t1, t0, out)
}

func orient2dAdapt(ax, ay, bx, by, cx, cy, detSum float64) float64 {
	acx := ax - cx
	bcx := bx - cx
	acy := ay - cy
	bcy := by - cy

	var B, u [4]float64
	crossExpansion(acx, bcy, acy, bcx, &B)
	det := estimate(B[:])
	errBound := ccwErrBoundB * detSum
	if det >= errBound || -det >= errBound {
		return det
	}

	bv := ax - acx
	acxTail := ax - (acx + bv) + (bv - cx)
	bv = bx - bcx
	bcxTail := bx - (bcx + bv) + (bv - cx)
	bv = ay - acy
	acyTail := ay - (acy + bv) + (bv - cy)
	bv = by - bcy
	bcyTail := by - (bcy + bv) + (bv - cy)

	if acxTail == 0 && acyTail == 0 && bcxTail == 0 && bcyTail == 0 {
		return det
	}

	errBound = float64(ccwErrBoundC*detSum) + float64(resultErrBound*math.Abs(det))
	det += (float64(acx*bcyTail) + float64(bcy*acxTail)) - (float64(acy*bcxTail) + float64(bcx*acyTail))
	if det >= errBound || -det >= errBound {
		return det
	}

	var c1 [8]float64
	var c2 [12]float64
	var d [16]float64
	crossExpansion(acxTail, bcy, acyTail, bcx, &u)
	c1len := expSum(B[:], u[:], c1[:])
	crossExpansion(acx, bcyTail, acy, bcxTail, &u)
	c2len := expSum(c1[:c1len], u[:], c2[:])
	crossExpansion(acxTail, bcyTail, acyTail, bcxTail, &u)
	dlen := expSum(c2[:c2len], u[:], d[:])
	return d[dlen-1]
}

func estimate(e []float64) float64 {
	q := e[0]
	for _, v := range e[1:] {
		q += v
	}
	return q
}

// expSum is fast_expansion_sum_zeroelim: h = e + f with zero components
// eliminated; it returns the length of h.
func expSum(e, f, h []float64) int {
	elen, flen := len(e), len(f)
	var q, qnew, hh, bvirt float64
	enow, fnow := e[0], f[0]
	eindex, findex := 0, 0
	if (fnow > enow) == (fnow > -enow) {
		q = enow
		eindex++
		if eindex < elen {
			enow = e[eindex]
		}
	} else {
		q = fnow
		findex++
		if findex < flen {
			fnow = f[findex]
		}
	}
	hindex := 0
	if eindex < elen && findex < flen {
		if (fnow > enow) == (fnow > -enow) {
			qnew = enow + q
			hh = q - (qnew - enow)
			eindex++
			if eindex < elen {
				enow = e[eindex]
			}
		} else {
			qnew = fnow + q
			hh = q - (qnew - fnow)
			findex++
			if findex < flen {
				fnow = f[findex]
			}
		}
		q = qnew
		if hh != 0 {
			h[hindex] = hh
			hindex++
		}
		for eindex < elen && findex < flen {
			if (fnow > enow) == (fnow > -enow) {
				qnew = q + enow
				bvirt = qnew - q
				hh = q - (qnew - bvirt) + (enow - bvirt)
				eindex++
				if eindex < elen {
					enow = e[eindex]
				}
			} else {
				qnew = q + fnow
				bvirt = qnew - q
				hh = q - (qnew - bvirt) + (fnow - bvirt)
				findex++
				if findex < flen {
					fnow = f[findex]
				}
			}
			q = qnew
			if hh != 0 {
				h[hindex] = hh
				hindex++
			}
		}
	}
	for eindex < elen {
		qnew = q + enow
		bvirt = qnew - q
		hh = q - (qnew - bvirt) + (enow - bvirt)
		eindex++
		if eindex < elen {
			enow = e[eindex]
		}
		q = qnew
		if hh != 0 {
			h[hindex] = hh
			hindex++
		}
	}
	for findex < flen {
		qnew = q + fnow
		bvirt = qnew - q
		hh = q - (qnew - bvirt) + (fnow - bvirt)
		findex++
		if findex < flen {
			fnow = f[findex]
		}
		q = qnew
		if hh != 0 {
			h[hindex] = hh
			hindex++
		}
	}
	if q != 0 || hindex == 0 {
		h[hindex] = q
		hindex++
	}
	return hindex
}
