package jsmath

import "math"

// Payne-Hanek style reduction for arguments beyond 2^19*pi/2, ported from V8's
// __kernel_rem_pio2 (fdlibm). Every product in it is between values of at most
// 24 significant bits, hence exact, so fused multiply-add cannot change it.

// twoOverPi is 2/pi in 24-bit chunks (396 hex digits).
var twoOverPi = [...]int32{
	0xA2F983, 0x6E4E44, 0x1529FC, 0x2757D1, 0xF534DD, 0xC0DB62, 0x95993C,
	0x439041, 0xFE5163, 0xABDEBB, 0xC561B7, 0x246E3A, 0x424DD2, 0xE00649,
	0x2EEA09, 0xD1921C, 0xFE1DEB, 0x1CB129, 0xA73EE8, 0x8235F5, 0x2EBB44,
	0x84E99C, 0x7026B4, 0x5F7E41, 0x3991D6, 0x398353, 0x39F49C, 0x845F8B,
	0xBDF928, 0x3B1FF8, 0x97FFDE, 0x05980F, 0xEF2F11, 0x8B5A0A, 0x6D1F6D,
	0x367ECF, 0x27CB09, 0xB74F46, 0x3F669E, 0x5FEA2D, 0x7527BA, 0xC7EBE5,
	0xF17B3D, 0x0739F7, 0x8A5292, 0xEA6BFB, 0x5FB11F, 0x8D5D08, 0x560330,
	0x46FC7B, 0x6BABF0, 0xCFBC20, 0x9AF436, 0x1DA9E3, 0x91615E, 0xE61B08,
	0x659985, 0x5F14A0, 0x68408D, 0xFFD880, 0x4D7327, 0x310606, 0x1556CA,
	0x73A8C9, 0x60E27B, 0xC08C6B,
}

var piO2 = [8]float64{
	1.57079625129699707031e+00,
	7.54978941586159635335e-08,
	5.39030252995776476554e-15,
	3.28200341580791294123e-22,
	1.27065575308067607349e-29,
	1.22933308981111328932e-36,
	2.73370053816464559624e-44,
	2.16741683877804819444e-51,
}

const (
	two24  = 1.67772160000000000000e+07
	twoN24 = 5.96046447753906250000e-08
)

// kernelRemPio2 computes x*2/pi mod 8 for the scaled 24-bit chunks x (nx of
// them, value scaled by 2^e0), returning n mod 8 and the remainder y0+y1 in
// units of pi/2. It is fdlibm's __kernel_rem_pio2 with prec = 2.
func kernelRemPio2(x []float64, e0, nx int) (n int32, y0, y1 float64) {
	const jk = 4 // init_jk[2]
	const jp = jk
	var (
		iq    [20]int32
		f, fq [20]float64
		q     [20]float64
	)
	jx := nx - 1
	jv := (e0 - 3) / 24
	if jv < 0 {
		jv = 0
	}
	q0 := e0 - 24*(jv+1)

	j := jv - jx
	m := jx + jk
	for i := 0; i <= m; i, j = i+1, j+1 {
		if j < 0 {
			f[i] = 0
		} else {
			f[i] = float64(twoOverPi[j])
		}
	}
	for i := 0; i <= jk; i++ {
		fw := 0.0
		for j := 0; j <= jx; j++ {
			fw += x[j] * f[jx+i-j]
		}
		q[i] = fw
	}

	jz := jk
	var z, fw float64
	var ih int32
	for {
		// distill q[] into iq[] reversingly
		z = q[jz]
		for i, j := 0, jz; j > 0; i, j = i+1, j-1 {
			fw = float64(int32(twoN24 * z))
			iq[i] = int32(z - two24*fw)
			z = q[j-1] + fw
		}

		z = math.Ldexp(z, q0)        // actual value of z
		z -= 8 * math.Floor(z*0.125) // trim off integer >= 8
		n = int32(z)
		z -= float64(n)
		ih = 0
		if q0 > 0 { // need iq[jz-1] to determine n
			i := iq[jz-1] >> uint(24-q0)
			n += i
			iq[jz-1] -= i << uint(24-q0)
			ih = iq[jz-1] >> uint(23-q0)
		} else if q0 == 0 {
			ih = iq[jz-1] >> 23
		} else if z >= 0.5 {
			ih = 2
		}

		if ih > 0 { // q > 0.5
			n++
			carry := int32(0)
			for i := 0; i < jz; i++ { // compute 1-q
				j := iq[i]
				if carry == 0 {
					if j != 0 {
						carry = 1
						iq[i] = 0x1000000 - j
					}
				} else {
					iq[i] = 0xffffff - j
				}
			}
			if q0 > 0 { // rare case: chance is 1 in 12
				switch q0 {
				case 1:
					iq[jz-1] &= 0x7fffff
				case 2:
					iq[jz-1] &= 0x3fffff
				}
			}
			if ih == 2 {
				z = 1 - z
				if carry != 0 {
					z -= math.Ldexp(1, q0)
				}
			}
		}

		// check if recomputation is needed
		if z == 0 {
			j := int32(0)
			for i := jz - 1; i >= jk; i-- {
				j |= iq[i]
			}
			if j == 0 { // need recomputation
				k := 1
				for jk >= k && iq[jk-k] == 0 {
					k++ // k = no. of terms needed
				}
				for i := jz + 1; i <= jz+k; i++ { // add q[jz+1] to q[jz+k]
					f[jx+i] = float64(twoOverPi[jv+i])
					fw := 0.0
					for j := 0; j <= jx; j++ {
						fw += x[j] * f[jx+i-j]
					}
					q[i] = fw
				}
				jz += k
				continue
			}
		}
		break
	}

	// chop off zero terms
	if z == 0 {
		jz--
		q0 -= 24
		for iq[jz] == 0 {
			jz--
			q0 -= 24
		}
	} else { // break z into 24-bit if necessary
		z = math.Ldexp(z, -q0)
		if z >= two24 {
			fw = float64(int32(twoN24 * z))
			iq[jz] = int32(z - two24*fw)
			jz++
			q0 += 24
			iq[jz] = int32(fw)
		} else {
			iq[jz] = int32(z)
		}
	}

	// convert integer "bit" chunk to floating-point value
	fw = math.Ldexp(1, q0)
	for i := jz; i >= 0; i-- {
		q[i] = fw * float64(iq[i])
		fw *= twoN24
	}

	// compute PIo2[0,...,jp]*q[jz,...,0]
	for i := jz; i >= 0; i-- {
		fw = 0
		for k := 0; k <= jp && k <= jz-i; k++ {
			fw += piO2[k] * q[i+k]
		}
		fq[jz-i] = fw
	}

	// compress fq[] into y[]
	fw = 0
	for i := jz; i >= 0; i-- {
		fw += fq[i]
	}
	y0 = fw
	if ih != 0 {
		y0 = -fw
	}
	fw = fq[0] - fw
	for i := 1; i <= jz; i++ {
		fw += fq[i]
	}
	y1 = fw
	if ih != 0 {
		y1 = -fw
	}
	return n & 7, y0, y1
}

// remPio2Large reduces |x| > 2^19*pi/2 (finite) modulo pi/2.
func remPio2Large(x float64) (n int, y0, y1 float64) {
	hx := hi(x)
	ix := hx & 0x7fffffff
	// z = scalbn(|x|, ilogb(x)-23)
	e0 := int(ix>>20) - 1046 // ilogb(z)-23
	z := withHigh(x, ix-int32(uint32(e0)<<20))
	var tx [3]float64
	for i := 0; i < 2; i++ {
		tx[i] = float64(int32(z))
		z = (z - tx[i]) * two24
	}
	tx[2] = z
	nx := 3
	for tx[nx-1] == 0 { // skip zero term
		nx--
	}
	n32, y0, y1 := kernelRemPio2(tx[:], e0, nx)
	if hx < 0 {
		return -int(n32), -y0, -y1
	}
	return int(n32), y0, y1
}
