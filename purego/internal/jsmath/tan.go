package jsmath

import "math"

var tanT = [13]float64{
	3.33333333333334091986e-01,
	1.33333333333201242699e-01,
	5.39682539762260521377e-02,
	2.18694882948595424599e-02,
	8.86323982359930005737e-03,
	3.59207910759131235356e-03,
	1.45620945432529025516e-03,
	5.88041240820264096874e-04,
	2.46463134818469906812e-04,
	7.81794442939557092300e-05,
	7.14072491382608190305e-05,
	-1.85586374855275456654e-05,
	2.59073051863633712884e-05,
}

const (
	tanPio4   = 7.85398163397448278999e-01
	tanPio4lo = 3.06161699786838301793e-17
)

// kernelTan is fdlibm's __kernel_tan on [-pi/4, pi/4]: tan(x+y) for iy = 1 and
// -1/tan(x+y) for iy = -1.
func kernelTan(x, y float64, iy int) float64 {
	T := &tanT
	hx := hi(x)
	ix := hx & 0x7fffffff
	if ix < 0x3E300000 { // |x| < 2**-28
		if int(x) == 0 {
			if uint32(ix)|lo(x)|uint32(iy+1) == 0 {
				return 1 / math.Abs(x)
			}
			if iy == 1 {
				return x
			}
			// compute -1/(x+y) carefully
			w := x + y
			z := withLow(w, 0)
			v := y - (z - x)
			a := -1 / w
			t := withLow(a, 0)
			s := fma(t, z, 1)
			return fma(a, fma(t, v, s), t)
		}
	}
	if ix >= 0x3FE59428 { // |x| >= 0.6744
		if hx < 0 {
			x = -x
			y = -y
		}
		z := tanPio4 - x
		w := tanPio4lo - y
		x = z + w
		y = 0
	}
	z := mul(x, x)
	w := mul(z, z)
	// x^5(T[1]+x^4*T[3]+...+x^20*T[11]) + x^5(x^2*(T[2]+x^4*T[4]+...+x^22*T[12]))
	r := fma(w, fma(w, fma(w, fma(w, fma(w, T[11], T[9]), T[7]), T[5]), T[3]), T[1])
	v := mul(z, fma(w, fma(w, fma(w, fma(w, fma(w, T[12], T[10]), T[8]), T[6]), T[4]), T[2]))
	s := mul(z, x)
	r = fma(z, fma(s, r+v, y), y)
	r = fma(T[0], s, r)
	w = x + r
	if ix >= 0x3FE59428 {
		v = float64(iy)
		return float64(1-((hx>>30)&2)) * fma(-2, x-(mul(w, w)/(w+v)-r), v)
	}
	if iy == 1 {
		return w
	}
	// compute -1.0/(x+r) accurately
	z = withLow(w, 0)
	v = r - (z - x) // z+v = r+x
	a := -1 / w
	t := withLow(a, 0)
	s = fma(t, z, 1)
	return fma(a, fma(t, v, s), t)
}

// Tan is Math.tan.
func Tan(x float64) float64 {
	ix := hi(x) & 0x7fffffff
	if ix <= 0x3FE921FB {
		return kernelTan(x, 0, 1)
	}
	if ix >= 0x7ff00000 {
		return x - x
	}
	n, y0, y1 := remPio2(x)
	return kernelTan(y0, y1, 1-((n&1)<<1))
}
