package raster

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"strings"
	"testing"
)

// What the PNG's filters draw where the engine follows W3C Filter Effects
// Module Level 1 and resvg does not (ADR 0015; filterDivergences lists the
// cases, each asserted to differ from resvg). The expected values here come
// from the specification: a table worked by hand from its formula, or a
// reference transcribed from its text, written apart from the code under
// test. Where the specification leaves a choice open (how a value in [0, 1]
// becomes a byte), the engine's documented choice is used and named.

// render draws svg at scale, failing the test on an error.
func render(t *testing.T, svg string, scale float64) *image.NRGBA {
	t.Helper()
	img, err := Render([]byte(svg), Options{Scale: scale})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// samePixels reports whether a and b are the same image, byte for byte.
func samePixels(a, b *image.NRGBA) bool { return a.Rect == b.Rect && bytes.Equal(a.Pix, b.Pix) }

// identityFilter is a filter whose result is its input: an offset by
// (0, 0) (§9.18). An image drawn through it is the input a pass through
// filter must give, its layer composited as the filter's is (drawing the
// shapes straight onto the background rounds differently).
const identityFilter = `<feOffset dx="0" dy="0"/>`

// passThrough asserts that prims, a pass through filter, draws filterSVG's
// shapes as they are, at scale 1 and 2.5.
func passThrough(t *testing.T, name, prims string) {
	t.Helper()
	sameAs(t, name+" (a pass through filter)", prims, identityFilter)
}

// sameAs asserts that two filters draw the same pixels at scale 1 and 2.5.
func sameAs(t *testing.T, name, prims, want string) {
	t.Helper()
	for _, scale := range []float64{1, 2.5} {
		got := render(t, filterSVG(prims, "", alignedShapes), scale)
		if !samePixels(got, render(t, filterSVG(want, "", alignedShapes), scale)) {
			t.Errorf("%s at scale %g: differs from %s", name, scale, want)
		}
	}
}

var (
	bg   = [4]uint8{0x33, 0x44, 0x55, 255} // filterSVG's background
	lime = [4]uint8{0, 255, 0, 255}
)

// wantPixels checks the pixels of img against a table.
func wantPixels(t *testing.T, name string, img *image.NRGBA, table []pixelWant) {
	t.Helper()
	for _, p := range table {
		if got := pixelAt(img, p.x, p.y); got != p.want {
			t.Errorf("%s: (%d, %d) = %v, want %v", name, p.x, p.y, got, p.want)
		}
	}
}

type pixelWant struct {
	x, y int
	want [4]uint8
}

// ---- feDisplacementMap (§9.11) ------------------------------------------------

// P'(x,y) <- P(x + scale*(XC - .5), y + scale*(YC - .5)), XC and YC read
// from the map's non-premultiplied values, the scale applied once. Six
// pixels in a row, scale 4, x from R and y from G; every map pixel's G is
// half its alpha (YC = .5, or a byte's rounding of it: no y offset).
func TestSpecDisplacementMap(t *testing.T) {
	src := []uint8{10, 0, 0, 255, 20, 0, 0, 255, 30, 0, 0, 255, 40, 0, 0, 255, 50, 0, 0, 255, 60, 0, 0, 255}
	mp := []uint8{
		255, 128, 0, 255, // XC = 1: +2, pixel 2
		64, 64, 0, 128, // XC = 64/128 = .5: 0 (premultiplied, 64/255: -1)
		0, 128, 0, 255, // XC = 0: -2, pixel 0
		192, 128, 0, 255, // XC = 192/255: +1.01, pixel 4 (the scale twice: +4.05, outside)
		32, 32, 0, 64, // XC = .5: 0, pixel 4 (premultiplied: -1.5 rounds to -2, pixel 2)
		255, 128, 0, 255, // +2: past the image, transparent
	}
	want := [][4]uint8{{30, 0, 0, 255}, {20, 0, 0, 255}, {10, 0, 0, 255}, {50, 0, 0, 255}, {50, 0, 0, 255}, [4]uint8{}}
	run := &filterRun{w: 6, h: 1, region: irect{0, 0, 6, 1}, src: src, ts: identity,
		results: []fresult{{name: "m", img: fimage{pix: mp, region: irect{0, 0, 6, 1}}}}}
	res, ok := run.displace(&fprim{kind: tagFeDisplacementMap, in2: finput{kind: inRef, name: "m"}, scaleX: 4, scaleY: 4, xch: 0, ych: 1})
	if !ok {
		t.Fatal("not done")
	}
	for i, w := range want {
		if got := [4]uint8(res.pix[i*4 : i*4+4]); got != w {
			t.Errorf("pixel %d = %v, want %v", i, got, w)
		}
	}

	// "The in source image must remain in its current color space": with
	// no displacement (a scale of 0) the result is the input as it was,
	// byte for byte, though the primitive works in linearRGB.
	sameAs(t, "a displacement of 0 (its input converted to the primitive's space?)", `<feFlood flood-color="#808080" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="0"/>`, identityFilter)

	// primitiveUnits="objectBoundingBox": a displacement along x is a
	// length along x (the box's width, 100), along y one along y (its
	// height, 50). A map of R = G = 1 moves the image by -0.5 x scale:
	// 0.08 x 100 x 0.5 = 4 to the left, 0.08 x 50 x 0.5 = 2 up.
	obb := `<svg xmlns="http://www.w3.org/2000/svg" width="140" height="90"><defs><filter id="f" primitiveUnits="objectBoundingBox">` +
		`<feFlood flood-color="#ffff00" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="0.08" xChannelSelector="R" yChannelSelector="G"/></filter></defs>` +
		`<rect width="140" height="90" fill="#345"/><g filter="url(#f)"><rect x="10" y="10" width="100" height="50" fill="lime"/><rect x="50" y="30" width="10" height="10" fill="red"/></g></svg>`
	red := [4]uint8{255, 0, 0, 255}
	wantPixels(t, "obb", render(t, obb, 1), []pixelWant{
		{46, 28, red}, {55, 37, red}, {45, 28, lime}, {56, 28, lime}, {46, 27, lime}, {46, 38, lime},
	})
}

// ---- feTile (§9.20) --------------------------------------------------------

func TestSpecTile(t *testing.T) {
	// A tile partly outside the filter region: its period is its
	// sub-region's, 40 x 30 from (-10, -5); the part outside the region is
	// transparent black. Lime where ((x + 10) mod 40) >= 10 and ((y + 5) mod
	// 30) >= 5.
	img := render(t, filterSVG(`<feFlood x="-10" y="-5" width="40" height="30" flood-color="lime" result="a"/><feTile in="a"/>`, "", alignedShapes), 1)
	for y := 0; y < 150; y += 3 {
		for x := 0; x < 200; x++ {
			want := bg
			if (x+10)%40 >= 10 && (y+5)%30 >= 5 {
				want = lime
			}
			if got := pixelAt(img, x, y); got != want {
				t.Fatalf("partly outside: (%d, %d) = %v, want %v", x, y, got, want)
			}
		}
	}

	// A linearRGB input is tiled in its own space: the tiles are the
	// input's pixels as the untiled input draws them.
	tr := `<feComponentTransfer x="40" y="30" width="26" height="20" result="c"><feFuncR type="linear" slope="0.5"/><feFuncG type="linear" slope="0.3" intercept="0.2"/></feComponentTransfer>`
	alone := render(t, filterSVG(tr, "", alignedShapes), 1)
	tiled := render(t, filterSVG(tr+`<feTile in="c"/>`, "", alignedShapes), 1)
	for y := 30; y < 50; y++ {
		for x := 40; x < 66; x++ {
			want := pixelAt(alone, x, y)
			for _, d := range [][2]int{{0, 0}, {26, 0}, {0, 20}, {-26, -20}, {52, 40}} {
				if got := pixelAt(tiled, x+d[0], y+d[1]); got != want {
					t.Fatalf("linear input: (%d, %d) = %v, the input's (%d, %d) %v", x+d[0], y+d[1], got, x, y, want)
				}
			}
		}
	}

	// A tile wholly outside the image: a transparent result, and the
	// filter goes on (here to merge the source over it).
	sameAs(t, "a tile outside the image (the filter not going on?)", `<feFlood x="300" y="300" width="40" height="30" flood-color="lime" result="a"/><feTile in="a" result="t"/><feMerge color-interpolation-filters="sRGB"><feMergeNode in="t"/><feMergeNode in="SourceGraphic"/></feMerge>`, identityFilter)
}

// ---- feTurbulence (§9.21) ------------------------------------------------------

// refTurbulence is Filter Effects 1's reference code, transcribed: the
// random number generator, init, noise2 and turbulence. Its init draws the
// gradients as SVG 1.1's does, every one normalized.
type refTurbulence struct {
	lattice  [0x100 + 0x100 + 2]int
	gradient [4][0x100 + 0x100 + 2][2]float64
}

func refSetupSeed(seed int64) int64 {
	const m = 2147483647
	if seed <= 0 {
		seed = -(seed % (m - 1)) + 1
	}
	if seed > m-1 {
		seed = m - 1
	}
	return seed
}

func refRandom(seed int64) int64 {
	const a, m, q, r = 16807, 2147483647, 127773, 2836
	result := a*(seed%q) - r*(seed/q)
	if result <= 0 {
		result += m
	}
	return result
}

func newRefTurbulence(seed int64) *refTurbulence {
	const bSize = 0x100
	t := &refTurbulence{}
	seed = refSetupSeed(seed)
	var i int
	for k := 0; k < 4; k++ {
		for i = 0; i < bSize; i++ {
			t.lattice[i] = i
			for j := 0; j < 2; j++ {
				seed = refRandom(seed)
				t.gradient[k][i][j] = float64((seed%(bSize+bSize))-bSize) / bSize
			}
			s := math.Sqrt(float64(t.gradient[k][i][0]*t.gradient[k][i][0]) + float64(t.gradient[k][i][1]*t.gradient[k][i][1]))
			t.gradient[k][i][0] /= s
			t.gradient[k][i][1] /= s
		}
	}
	for i--; i > 0; i-- { // while(--i)
		k := t.lattice[i]
		seed = refRandom(seed)
		j := int(seed % bSize)
		t.lattice[i] = t.lattice[j]
		t.lattice[j] = k
	}
	for i := 0; i < bSize+2; i++ {
		t.lattice[bSize+i] = t.lattice[i]
		for k := 0; k < 4; k++ {
			for j := 0; j < 2; j++ {
				t.gradient[k][bSize+i][j] = t.gradient[k][i][j]
			}
		}
	}
	return t
}

type refStitch struct{ width, height, wrapX, wrapY int }

func (t *refTurbulence) noise2(ch int, vx, vy float64, st *refStitch) float64 {
	const perlinN, bm = 0x1000, 0xff
	// Each product rounded before it is added, as C computes it (arm64
	// would fuse them).
	sCurve := func(t float64) float64 { return float64(t*t) * (3 - float64(2*t)) }
	lerp := func(t, a, b float64) float64 { return a + float64(t*(b-a)) }
	tt := vx + perlinN
	bx0 := int(tt)
	bx1 := bx0 + 1
	rx0 := tt - float64(int(tt))
	rx1 := rx0 - 1
	tt = vy + perlinN
	by0 := int(tt)
	by1 := by0 + 1
	ry0 := tt - float64(int(tt))
	ry1 := ry0 - 1
	if st != nil {
		if bx0 >= st.wrapX {
			bx0 -= st.width
		}
		if bx1 >= st.wrapX {
			bx1 -= st.width
		}
		if by0 >= st.wrapY {
			by0 -= st.height
		}
		if by1 >= st.wrapY {
			by1 -= st.height
		}
	}
	bx0 &= bm
	bx1 &= bm
	by0 &= bm
	by1 &= bm
	i, j := t.lattice[bx0], t.lattice[bx1]
	b00, b10 := t.lattice[i+by0], t.lattice[j+by0]
	b01, b11 := t.lattice[i+by1], t.lattice[j+by1]
	sx, sy := sCurve(rx0), sCurve(ry0)
	g := &t.gradient[ch]
	u := float64(rx0*g[b00][0]) + float64(ry0*g[b00][1])
	v := float64(rx1*g[b10][0]) + float64(ry0*g[b10][1])
	a := lerp(sx, u, v)
	u = float64(rx0*g[b01][0]) + float64(ry1*g[b01][1])
	v = float64(rx1*g[b11][0]) + float64(ry1*g[b11][1])
	b := lerp(sx, u, v)
	return lerp(sy, a, b)
}

func (t *refTurbulence) turbulence(ch int, px, py, fx, fy float64, octaves int, fractal, stitch bool, tileX, tileY, tileW, tileH float64) float64 {
	const perlinN = 0x1000
	var st *refStitch
	if stitch {
		adjust := func(f, n float64) float64 {
			if f == 0 {
				return f
			}
			lo, hi := math.Floor(n*f)/n, math.Ceil(n*f)/n
			if f/lo < hi/f {
				return lo
			}
			return hi
		}
		fx, fy = adjust(fx, tileW), adjust(fy, tileH)
		st = &refStitch{}
		st.width = int(float64(tileW*fx) + 0.5)
		st.wrapX = int(float64(tileX*fx) + perlinN + float64(st.width))
		st.height = int(float64(tileH*fy) + 0.5)
		st.wrapY = int(float64(tileY*fy) + perlinN + float64(st.height))
	}
	sum := 0.0
	vx, vy := px*fx, py*fy
	ratio := 1.0
	for o := 0; o < octaves; o++ {
		if fractal {
			sum += t.noise2(ch, vx, vy, st) / ratio
		} else {
			sum += math.Abs(t.noise2(ch, vx, vy, st)) / ratio
		}
		vx *= 2
		vy *= 2
		ratio *= 2
		if st != nil {
			st.width *= 2
			st.wrapX = 2*st.wrapX - perlinN
			st.height *= 2
			st.wrapY = 2*st.wrapY - perlinN
		}
	}
	return sum
}

func TestSpecTurbulence(t *testing.T) {
	// The reference code's own check: "the algorithm should produce the
	// result 1043618065 as the 10,000th generated number if the original
	// seed is 1".
	s := int32(1)
	for range 10000 {
		s = turbRandom(s)
	}
	if s != 1043618065 {
		t.Errorf("the 10,000th random number from seed 1 is %d, want 1043618065", s)
	}

	// The lattice and gradients, for seeds in and past 32 bits (the seed
	// "truncated, i.e. rounded to the closest integer value towards zero").
	for _, seed := range []int64{0, 1, 5, -7, 2147483646, 2147483647, 2147483648, 1e10, -1e10, -2147483648, -2147483649} {
		got, want := newTurbTables(seed), newRefTurbulence(seed)
		if got.lattice != want.lattice || got.gradient != want.gradient {
			t.Errorf("seed %d: the tables differ from the reference code's", seed)
		}
	}

	// The noise of each pixel, stitched over the primitive's sub-region
	// (fTileX, fTileY, fTileWidth, fTileHeight), its point the pixel's
	// corner in user space. The bytes: colorValue clamped to [0, 1],
	// times 255 rounded, premultiplied rounded (the engine's choice).
	for _, tc := range []struct {
		name    string
		tile    rect
		fractal bool
		freq    [2]float64
		seed    int64
		octaves uint32
	}{
		{"fractal, stitched", rect{3, 2, 23, 19}, true, [2]float64{0.13, 0.21}, 3, 3},
		{"turbulence, stitched", rect{-4, 6, 20, 30}, false, [2]float64{0.09, 0.05}, 11, 2},
	} {
		const w, h = 24, 20
		run := &filterRun{w: w, h: h, region: irect{0, 0, w, h}, ts: matrix{1, 0, 0, 1, -5, -3}}
		p := &fprim{kind: tagFeTurbulence, rect: tc.tile, turb: &turbSpec{freqX: tc.freq[0], freqY: tc.freq[1], octaves: tc.octaves, seed: tc.seed, stitch: true, fractal: tc.fractal}}
		res, ok := run.turbulence(p)
		if !ok {
			t.Fatal("not done")
		}
		ref := newRefTurbulence(tc.seed)
		bad := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var px [4]uint8
				for ch := 0; ch < 4; ch++ {
					// The pixel's point: the canvas is user space moved by (-5, -3).
					v := ref.turbulence(ch, float64(x)+5, float64(y)+3, tc.freq[0], tc.freq[1], int(tc.octaves), tc.fractal, true, tc.tile.x0, tc.tile.y0, tc.tile.w(), tc.tile.h())
					// colorValue = (v + 1) / 2 or v, clamped to [0, 1], in
					// bytes: x 255 in f64, then f32 + 0.5 truncated.
					if tc.fractal {
						v = (float64(v*255) + 255) / 2
					} else {
						v *= 255
					}
					f := float32(v)
					if !(f > 0) {
						f = 0
					}
					px[ch] = uint8(min(f, 255) + 0.5)
				}
				a := float32(px[3]) / 255
				for c := 0; c < 3; c++ {
					px[c] = uint8(float32(float32(px[c])*a) + 0.5)
				}
				if got := [4]uint8(res.pix[(y*w+x)*4:]); got != px && bad < 5 {
					bad++
					t.Errorf("%s: pixel (%d, %d) = %v, the reference's %v", tc.name, x, y, got, px)
				}
			}
		}
	}

	// numOctaves = "<integer>": 2.5 is no integer, and the initial value,
	// 1, holds.
	doc := func(oct string) string {
		return filterSVG(`<feTurbulence baseFrequency="0.05" numOctaves="`+oct+`"/>`, "", alignedShapes)
	}
	if one := render(t, doc("1"), 1); !samePixels(render(t, doc("2.5"), 1), one) || samePixels(render(t, doc("3"), 1), one) {
		t.Error(`numOctaves="2.5" is not the initial 1`)
	}
}

// ---- lighting (§9.10, §9.19, §11) --------------------------------------------------

// flatLight is a lighting primitive over an opaque square filling its
// filter region, a flat surface (N = (0, 0, 1)), under a light straight
// above (elevation 90: L = (0, 0, 1), N.L = 1): the diffuse result is the
// light's colour.
func flatLight(prim, filterAttrs, elemAttrs string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><defs><filter id="f" x="0" y="0" width="40" height="40" filterUnits="userSpaceOnUse" ` + filterAttrs + `>` + prim + `</filter></defs>` +
		`<rect width="40" height="40" ` + elemAttrs + ` filter="url(#f)"/></svg>`
}

func TestSpecLightingColor(t *testing.T) {
	grey := [4]uint8{128, 128, 128, 255}
	white := [4]uint8{255, 255, 255, 255}
	for _, tc := range []struct {
		name                         string
		prim, filterAttrs, elemAttrs string
		want                         [4]uint8
	}{
		// In linearRGB #808080 is 0.2158, lit and converted back 0.502:
		// 128 (resvg lights with 0.502 as a linear value: 188).
		{"linearRGB", `<feDiffuseLighting lighting-color="#808080"><feDistantLight elevation="90"/></feDiffuseLighting>`, "", "", grey},
		{"sRGB", `<feDiffuseLighting lighting-color="#808080" color-interpolation-filters="sRGB"><feDistantLight elevation="90"/></feDiffuseLighting>`, "", "", grey},
		{"currentColor", `<feDiffuseLighting lighting-color="currentColor" color="#808080"><feDistantLight elevation="90"/></feDiffuseLighting>`, "", "", grey},
		// inherit: the parent's, the filter element's (resvg: white).
		{"inherit", `<feDiffuseLighting lighting-color="inherit"><feDistantLight elevation="90"/></feDiffuseLighting>`, `lighting-color="#808080"`, "", grey},
		{"inherit twice", `<feDiffuseLighting lighting-color="inherit"><feDistantLight elevation="90"/></feDiffuseLighting>`, `lighting-color="inherit"`, "", white},
		// Not inherited: the filtered element's colour does not reach it.
		{"not inherited", `<feDiffuseLighting><feDistantLight elevation="90"/></feDiffuseLighting>`, "", `lighting-color="#808080"`, white},
	} {
		img := render(t, flatLight(tc.prim, tc.filterAttrs, tc.elemAttrs), 1)
		wantPixels(t, tc.name, img, []pixelWant{{10, 10, tc.want}, {20, 30, tc.want}})
	}
	// "inherit twice": the filter's own inherit reaches defs, which has no
	// lighting-color: the initial white. With the colour there it is the
	// colour.
	svg := strings.Replace(flatLight(`<feDiffuseLighting lighting-color="inherit"><feDistantLight elevation="90"/></feDiffuseLighting>`, `lighting-color="inherit"`, ""), "<defs>", `<defs lighting-color="#808080">`, 1)
	wantPixels(t, "inherit through the filter", render(t, svg, 1), []pixelWant{{10, 10, grey}})
}

func TestSpecLightUnits(t *testing.T) {
	// primitiveUnits="objectBoundingBox" over a box (20, 20) 80 x 60: a
	// point light at (0.5, 0.5, 0.1) is at (60, 50) in user space, its z
	// 0.1 of the box's normalized diagonal, sqrt((80^2 + 60^2) / 2) =
	// 70.71: z = 7.071, 6.071 above the surface (Z = surfaceScale x A = 1).
	// At (60, 50) the light is straight above: 255. At (70, 50) N.L =
	// 6.071 / sqrt(10^2 + 6.071^2) = 0.519, 132.3: 132.
	doc := func(light string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="100"><defs><filter id="f" x="0" y="0" width="120" height="100" filterUnits="userSpaceOnUse" primitiveUnits="objectBoundingBox" color-interpolation-filters="sRGB">` +
			`<feDiffuseLighting>` + light + `</feDiffuseLighting></filter></defs><rect x="20" y="20" width="80" height="60" filter="url(#f)"/></svg>`
	}
	grey := func(v uint8) [4]uint8 { return [4]uint8{v, v, v, 255} }
	img := render(t, doc(`<fePointLight x="0.5" y="0.5" z="0.1"/>`), 1)
	wantPixels(t, "point light", img, []pixelWant{{60, 50, grey(255)}, {70, 50, grey(132)}, {50, 50, grey(132)}, {60, 60, grey(132)}, {60, 40, grey(132)}})
	// A spot light there, pointing straight down with a cone of 10
	// degrees: (60, 50) is lit, (70, 50) is 58.8 degrees off its axis.
	img = render(t, doc(`<feSpotLight x="0.5" y="0.5" z="0.1" pointsAtX="0.5" pointsAtY="0.5" pointsAtZ="0" limitingConeAngle="10"/>`), 1)
	wantPixels(t, "spot light", img, []pixelWant{{60, 50, grey(255)}, {70, 50, grey(0)}})
}

func TestSpecLightingExponent(t *testing.T) {
	// A flat surface under a light straight above: H = (0, 0, 1), N.H = 1,
	// pow(1, specularExponent) = 1 whatever the exponent: the specular
	// result is the light's colour, its alpha their maximum. resvg makes a
	// primitive whose exponent is outside 1-128 transparent.
	for _, e := range []string{"200", "0.5", "-3"} {
		img := render(t, flatLight(`<feSpecularLighting specularExponent="`+e+`" lighting-color="#ff8000" color-interpolation-filters="sRGB"><feDistantLight elevation="90"/></feSpecularLighting>`, "", ""), 1)
		wantPixels(t, "specularExponent "+e, img, []pixelWant{{10, 10, [4]uint8{255, 128, 0, 255}}})
	}
	// A spot light's specularExponent of -2: the light at an angle whose
	// cosine is s is its colour times s^-2, clamped: white. At (70, 50) of
	// TestSpecLightUnits' surface (user space now), s = N.L = 0.519: the
	// diffuse result 0.519 x 255 = 132 (an exponent of 1, usvg's for a
	// negative one: 0.519^2 x 255 = 69).
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="100"><defs><filter id="f" x="0" y="0" width="120" height="100" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">` +
		`<feDiffuseLighting><feSpotLight x="60" y="50" z="7.071" pointsAtX="60" pointsAtY="50" pointsAtZ="0" specularExponent="-2"/></feDiffuseLighting></filter></defs><rect x="20" y="20" width="80" height="60" filter="url(#f)"/></svg>`
	wantPixels(t, "spot specularExponent -2", render(t, svg, 1), []pixelWant{{70, 50, [4]uint8{132, 132, 132, 255}}})
}

// An image two pixels wide has the specification's corner kernels: lit,
// where resvg leaves one under three pixels transparent. One pixel wide
// has no normal (no neighbour): transparent.
func TestSpecLightingSmall(t *testing.T) {
	doc := func(w string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><defs><filter id="f" x="10" y="10" width="` + w + `" height="20" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">` +
			`<feDiffuseLighting><feDistantLight elevation="90"/></feDiffuseLighting></filter></defs><rect width="40" height="40" filter="url(#f)"/></svg>`
	}
	white := [4]uint8{255, 255, 255, 255}
	wantPixels(t, "two pixels", render(t, doc("2"), 1), []pixelWant{{10, 10, white}, {11, 29, white}, {12, 10, [4]uint8{}}})
	wantPixels(t, "one pixel", render(t, doc("1"), 1), []pixelWant{{10, 10, [4]uint8{}}})
}

func TestSpecLightingKernelUnit(t *testing.T) {
	// The normal's samples kernelUnitLength apart. An opaque rectangle
	// from x = 50: at x = 48 the samples a pixel away (47, 49) are both
	// clear, a flat surface, and a light at 45 degrees gives N.L = sin 45:
	// 180. Three pixels away (45, 51) the right one is the rectangle: the
	// interior kernel's Nx = -1/4 x 4 = -1 (surfaceScale 1, alpha 1), N =
	// (-1, 0, 1)/sqrt 2, L = (cos 45, 0, sin 45): N.L = 0.
	doc := func(klu string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><defs><filter id="f" x="0" y="0" width="100" height="40" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">` +
			`<feDiffuseLighting ` + klu + `><feDistantLight azimuth="0" elevation="45"/></feDiffuseLighting></filter></defs>` +
			`<g filter="url(#f)"><rect x="0" y="0" width="1" height="1" fill-opacity="0"/><rect x="50" y="0" width="50" height="40"/></g></svg>`
	}
	grey := func(v uint8) [4]uint8 { return [4]uint8{v, v, v, 255} }
	wantPixels(t, "no kernelUnitLength", render(t, doc(""), 1), []pixelWant{{48, 20, grey(180)}})
	wantPixels(t, "kernelUnitLength 3", render(t, doc(`kernelUnitLength="3"`), 1), []pixelWant{{48, 20, grey(0)}, {44, 20, grey(180)}})
	// At scale 2 a kernelUnitLength of 0.5 is a device pixel: the plain
	// kernel; 1 at scale 1 too.
	if !samePixels(render(t, doc(`kernelUnitLength="0.5"`), 2), render(t, doc(""), 2)) || !samePixels(render(t, doc(`kernelUnitLength="1"`), 1), render(t, doc(""), 1)) {
		t.Error("a kernelUnitLength of one device pixel is not the plain kernel")
	}

	// The spaced kernels against the specification's, the samples taken
	// apart at distance d (interior: both sides; edge: the pixel and the
	// inward one), on a random alpha.
	const w, h = 13, 11
	rng := rand.New(rand.NewSource(5))
	pix := make([]uint8, w*h*4)
	for i := 3; i < len(pix); i += 4 {
		pix[i] = uint8(rng.Intn(256))
	}
	I := func(x, y int) float32 { return float32(pix[(y*w+x)*4+3]) }
	for _, d := range []int{2, 3} {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				n := normalSpaced(pix, x, y, w, h, float64(d), float64(d))
				// Along x: which samples exist, and Filter Effects 1's
				// Sobel sums and factors for that case (interior 1/4 over
				// three rows, edge 1/2 and corner 2/3).
				l, r := x-d, x+d
				if l < 0 && r > w-1 {
					l, r = x, x
				}
				tp, b := y-d, y+d
				if tp < 0 && b > h-1 {
					tp, b = y, y
				}
				hasL, hasR, hasT, hasB := l >= 0, r <= w-1, tp >= 0, b <= h-1
				if !hasL {
					l = x
				}
				if !hasR {
					r = x
				}
				if !hasT {
					tp = y
				}
				if !hasB {
					b = y
				}
				// The rows (columns) the x (y) difference spans, weighted
				// 1-2-1, or 2-1 at an edge.
				rowW := func(has1, has2 bool) (float32, float32, float32) {
					switch {
					case has1 && has2:
						return 1, 2, 1
					case has2:
						return 0, 2, 1
					default:
						return 1, 2, 0
					}
				}
				wt, wm, wb := rowW(hasT, hasB)
				wl, wc, wr := rowW(hasL, hasR)
				sx := wt*(I(r, tp)-I(l, tp)) + wm*(I(r, y)-I(l, y)) + wb*(I(r, b)-I(l, b))
				sy := wl*(I(l, b)-I(l, tp)) + wc*(I(x, b)-I(x, tp)) + wr*(I(r, b)-I(r, tp))
				fac := func(both bool, other bool) float32 {
					switch {
					case both && other:
						return 1.0 / 4
					case both:
						return 1.0 / 3
					case other:
						return 1.0 / 2
					}
					return 2.0 / 3
				}
				fx, fy := fac(hasL && hasR, hasT && hasB), fac(hasT && hasB, hasL && hasR)
				if n.nx != -sx || n.ny != -sy || n.fx != fx || n.fy != fy {
					t.Fatalf("d %d, (%d, %d): normal (%g x %g, %g x %g), the specification's (%g x %g, %g x %g)", d, x, y, n.nx, n.fx, n.ny, n.fy, -sx, fx, -sy, fy)
				}
			}
		}
	}
}

// ---- feImage (§9.15) ------------------------------------------------------------

// solidPNG is a data: URI of a w x h PNG of one colour.
func solidPNG(t *testing.T, w, h int, c color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestSpecFeImagePlacement(t *testing.T) {
	const head = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150">`
	// An element is drawn as a <use> draws it: at its own coordinates in
	// the user space of the filtered element, here moved by (30, 20); the
	// sub-region (from (5, 5)) clips it. The lime square is at (10, 10)
	// 20 x 20 of that space: (40, 30) to (60, 50) on the canvas (resvg: from
	// the sub-region's corner, (45, 35) to (65, 55)).
	svg := head + `<defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e" x="5" y="5"/></filter>` +
		`<rect id="e" x="10" y="10" width="20" height="20" fill="lime"/></defs><g transform="translate(30 20)" filter="url(#f)"><rect width="10" height="10" fill="#c33"/></g></svg>`
	wantPixels(t, "element", render(t, svg, 1), []pixelWant{{40, 30, lime}, {59, 49, lime}, {39, 30, [4]uint8{}}, {60, 49, [4]uint8{}}, {59, 50, [4]uint8{}}})
	// At scale 2, the same at twice the size.
	wantPixels(t, "element, scale 2", render(t, svg, 2), []pixelWant{{80, 60, lime}, {119, 99, lime}, {79, 60, [4]uint8{}}, {120, 99, [4]uint8{}}})

	// primitiveUnits="objectBoundingBox": the element in the bounding
	// box's space. The filtered rectangle is (40, 30) 80 x 40; the element
	// (0, 0) 0.5 x 0.25 of it covers (40, 30) to (80, 40).
	svg = head + `<defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse" primitiveUnits="objectBoundingBox"><feImage xlink:href="#q"/></filter>` +
		`<rect id="q" width="0.5" height="0.25" fill="lime"/></defs><rect x="40" y="30" width="80" height="40" fill="#c33" filter="url(#f)"/></svg>`
	wantPixels(t, "objectBoundingBox", render(t, svg, 1), []pixelWant{{40, 30, lime}, {79, 39, lime}, {80, 39, [4]uint8{}}, {79, 40, [4]uint8{}}, {39, 30, [4]uint8{}}})

	// An image is an <image> with the sub-region for its viewport, drawn
	// with the filtered element's whole transform: rotated by 90 degrees
	// about (100, 75), the image at (20, 20) 60 x 40 lands on (115, -5) to
	// (155, 55); (50, 40) maps to (135, 25). resvg draws it unrotated.
	gold := [4]uint8{250, 200, 20, 255}
	svg = head + `<defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="` + solidPNG(t, 6, 4, color.NRGBA{250, 200, 20, 255}) + `" x="20" y="20" width="60" height="40" preserveAspectRatio="none"/></filter></defs>` +
		`<g transform="rotate(90 100 75)" filter="url(#f)"><rect x="40" y="30" width="60" height="50" fill="#c33"/></g></svg>`
	wantPixels(t, "image, rotated", render(t, svg, 1), []pixelWant{{135, 25, gold}, {120, 2, gold}, {150, 50, gold}, {50, 40, [4]uint8{}}, {160, 25, [4]uint8{}}})

	// A filtered element in an feImage draws as it does in the document:
	// its blur reaches in from past the feImage's canvas (resvg bounds the
	// element's filter layer by the feImage's region).
	blur := `<filter id="b"><feGaussianBlur stdDeviation="4"/></filter>`
	viaFeImage := render(t, head+`<defs>`+blur+`<filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e"/></filter>`+
		`<rect id="e" x="150" y="100" width="80" height="80" fill="gold" filter="url(#b)"/></defs><rect x="10" y="10" width="40" height="40" fill="#c33" filter="url(#f)"/></svg>`, 1)
	direct := render(t, head+`<defs>`+blur+`</defs><rect x="150" y="100" width="80" height="80" fill="gold" filter="url(#b)"/></svg>`, 1)
	if !samePixels(viaFeImage, direct) {
		t.Error("a blurred element drawn by an feImage differs from the element drawn in the document")
	}
}

func TestSpecFeImageCycle(t *testing.T) {
	const head = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150">`
	// An element whose filter's feImage draws it: a circular reference,
	// the feImage's result transparent, and so the filter's (an offset of
	// it). The rectangle draws nothing, where it is and where its copy
	// would be, and neither does another element with that filter.
	svg := head + `<defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#r"/><feOffset dx="40" dy="20"/></filter></defs>` +
		`<rect id="r" x="10" y="10" width="50" height="40" fill="lime" filter="url(#f)"/><rect x="120" y="70" width="60" height="60" fill="#36c" filter="url(#f)"/></svg>`
	img := render(t, svg, 1)
	for i, v := range img.Pix {
		if v != 0 {
			t.Fatalf("a circular feImage drew byte %d = %d", i, v)
		}
	}

	// The circle closes at the inner filter: an feImage outside it still
	// draws, and so does one drawn after it. f1's feImage draws a group of
	// a lime square, an element whose filter, f2, draws that element (f2's
	// feImage is on a circle and transparent), and then an element whose
	// filter, f3, draws a blue square, as deep as f2's: f1's and f3's
	// feImages are on no circle, and draw.
	blue := [4]uint8{0, 0, 255, 255}
	svg = head + `<defs><filter id="f1" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#a"/></filter>` +
		`<filter id="f2" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#b"/></filter>` +
		`<filter id="f3" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#d"/></filter>` +
		`<rect id="d" x="10" y="60" width="30" height="30" fill="blue"/>` +
		`<g id="a"><rect x="10" y="10" width="30" height="30" fill="lime"/><rect id="b" x="60" y="10" width="30" height="30" fill="red" filter="url(#f2)"/>` +
		`<rect x="0" y="0" width="1" height="1" filter="url(#f3)"/></g></defs>` +
		`<rect x="100" y="80" width="20" height="20" fill="#c33" filter="url(#f1)"/></svg>`
	wantPixels(t, "circle inside", render(t, svg, 1), []pixelWant{{20, 20, lime}, {70, 20, [4]uint8{}}, {20, 70, blue}, {110, 90, [4]uint8{}}})
}

// ---- feColorMatrix (§9.6) -----------------------------------------------------------

// hueRotate at 90 and 270 degrees, exactly: cos and sin are 0 and +-1, the
// matrix's entries sums of the specification's three-decimal constants,
// times each channel, clamped to [0, 1], to a byte by truncation (the
// engine's). At 90 degrees, [0 0 1; .356 .855 -.211; -.574 1.43 .144]; at
// 270, [.426 1.43 -.856; .07 .575 .355; 1 0 0].
func TestSpecHueRotate(t *testing.T) {
	for _, tc := range []struct {
		deg     float64
		in, out [3]uint8
	}{
		{90, [3]uint8{255, 0, 0}, [3]uint8{0, 90, 0}},         // .356 x 255 = 90.78
		{90, [3]uint8{0, 255, 0}, [3]uint8{0, 218, 255}},      // .855 x 255 = 218.03; 1.43
		{90, [3]uint8{0, 0, 255}, [3]uint8{255, 0, 36}},       // 1; -.211; .144 x 255 = 36.72
		{90, [3]uint8{255, 0, 255}, [3]uint8{255, 36, 0}},     // 0 + 1; .356 - .211 = .145, 36.98; -.43
		{90, [3]uint8{0, 255, 255}, [3]uint8{255, 164, 255}},  // 1; .644 x 255 = 164.22; 1.574
		{270, [3]uint8{255, 0, 0}, [3]uint8{108, 17, 255}},    // .426: 108.63; .07: 17.85; 1
		{270, [3]uint8{0, 0, 255}, [3]uint8{0, 90, 0}},        // -.856; .355: 90.53; 0
		{270, [3]uint8{255, 255, 0}, [3]uint8{255, 164, 255}}, // 1.856; .645: 164.48; 1
	} {
		pix := []uint8{tc.in[0], tc.in[1], tc.in[2], 255}
		hueRotate(tc.deg, pix)
		if got := [3]uint8(pix[:3]); got != tc.out {
			t.Errorf("hueRotate %g of %v = %v, want %v", tc.deg, tc.in, got, tc.out)
		}
	}
}

func TestSpecColorMatrix(t *testing.T) {
	// saturate 2, not clamped to 1: (200, 100, 50) is (.784, .392, .196);
	// R' = 1.787r - .715g - .072b = 1.107 (1: 255), G' = -.213r + 1.285g
	// - .072b = .3228 (82.3: 82), B' = -.213r - .715g + 1.928b = -.069 (0).
	pix := []uint8{200, 100, 50, 255}
	colorMatrix(&fprim{cmKind: cmSaturate, cmValues: []float64{2}}, pix)
	if got := [3]uint8(pix[:3]); got != [3]uint8{255, 82, 0} {
		t.Errorf("saturate 2 of (200, 100, 50) = %v, want (255, 82, 0)", got)
	}
	// Through the document: the attribute is not clamped either.
	sat := func(v string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><filter id="f" color-interpolation-filters="sRGB"><feColorMatrix type="saturate" values="` + v + `"/></filter><rect width="20" height="20" fill="rgb(200,100,50)" filter="url(#f)"/></svg>`
	}
	wantPixels(t, "saturate 2", render(t, sat("2"), 1), []pixelWant{{10, 10, [4]uint8{255, 82, 0, 255}}})

	// luminanceToAlpha: .2126R + .7152G + .0722B. (0, 26, 255): .7152 x
	// 26/255 + .0722 = .14512, 37.006: 37 (SVG 1.1's .2125, .7154, .0721,
	// resvg's: .14504, 36.99: 36).
	pix = []uint8{0, 26, 255, 255}
	colorMatrix(&fprim{cmKind: cmLuminance}, pix)
	if pix[3] != 37 || pix[0] != 0 || pix[1] != 0 || pix[2] != 0 {
		t.Errorf("luminanceToAlpha of (0, 26, 255) = %v, want (0, 0, 0, 37)", pix)
	}

	// Values that are not the type's count, and values left out: a pass
	// through (the identity, exactly; its arithmetic through the bytes is
	// not).
	for _, prims := range []string{
		`<feColorMatrix values="1 0 0 0 0"/>`,
		`<feColorMatrix type="saturate" values="0.5 0.5"/>`,
		`<feColorMatrix type="hueRotate" values=""/>`,
		`<feColorMatrix type="matrix"/>`,
		`<feColorMatrix type="saturate"/>`,
		`<feColorMatrix type="hueRotate" values="x"/>`,
	} {
		passThrough(t, prims, prims)
	}
}

// ---- feConvolveMatrix (§9.9) -----------------------------------------------------------

func TestSpecConvolvePassThrough(t *testing.T) {
	for _, prims := range []string{
		`<feConvolveMatrix order="0" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`,
		`<feConvolveMatrix order="-3" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`,
		`<feConvolveMatrix order="3 -3" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`,
		// Not a <number-optional-number>: the initial 3, 9 entries wanted.
		`<feConvolveMatrix order="2 abc" kernelMatrix="1 2 3 4"/>`,
		`<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" targetX="-1"/>`,
		`<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" targetY="3"/>`,
		`<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1"/>`,
		`<feConvolveMatrix/>`,
		`<feConvolveMatrix kernelMatrix="1 1 1 1 x 1 1 1 1"/>`,
	} {
		passThrough(t, prims, prims)
	}
	// A divisor of 0 is the default: the kernel's sum.
	sameAs(t, "divisor 0", `<feConvolveMatrix kernelMatrix="1 2 1 0 3 0 -1 1 1" divisor="0"/>`, `<feConvolveMatrix kernelMatrix="1 2 1 0 3 0 -1 1 1"/>`)
	sameAs(t, "divisor 0, sum 0", `<feConvolveMatrix kernelMatrix="0 -1 0 -1 4 -1 0 -1 0" divisor="0"/>`, `<feConvolveMatrix kernelMatrix="0 -1 0 -1 4 -1 0 -1 0" divisor="1"/>`)
}

func TestSpecConvolveKernelUnit(t *testing.T) {
	// Cells two pixels apart are the kernel with a cell of zeros between
	// each two: a 3x3 kernel at kernelUnitLength 2 is the 5x5 kernel of its
	// cells spread out, its target twice as far, for every edge mode.
	const w, h = 11, 9
	rng := rand.New(rand.NewSource(9))
	src := make([]uint8, w*h*4)
	for i := 0; i < len(src); i += 4 {
		a := uint8(rng.Intn(256))
		src[i+3] = a
		for c := 0; c < 3; c++ {
			src[i+c] = uint8(rng.Intn(int(a) + 1))
		}
	}
	k3 := []float32{0.5, -1, 2, 0.25, 1, -0.75, 1.5, 0, -0.5}
	for _, edge := range []uint8{edgeDuplicate, edgeWrap, edgeNone} {
		for _, pa := range []bool{false, true} {
			spaced := &convolveSpec{cols: 3, rows: 3, targetX: 1, targetY: 2, kernel: k3, divisor: 2, bias: 0.1, edge: edge, preserveAlpha: pa, unitX: 2, unitY: 2}
			k5 := make([]float32, 25)
			for r := 0; r < 3; r++ {
				for c := 0; c < 3; c++ {
					k5[(2*r)*5+2*c] = k3[r*3+c]
				}
			}
			wide := &convolveSpec{cols: 5, rows: 5, targetX: 2, targetY: 4, kernel: k5, divisor: 2, bias: 0.1, edge: edge, preserveAlpha: pa}
			conv := func(cm *convolveSpec) []uint8 {
				run := &filterRun{w: w, h: h, region: irect{0, 0, w, h}, src: src, ts: identity}
				res, ok := run.convolve(&fprim{kind: tagFeConvolveMatrix, conv: cm})
				if !ok {
					t.Fatal("not done")
				}
				return res.pix
			}
			if got, want := conv(spaced), conv(wide); !bytes.Equal(got, want) {
				t.Errorf("edge %d, preserveAlpha %v: kernelUnitLength 2 is not the spread-out kernel", edge, pa)
			}
		}
	}
	// Between pixels, a cell is interpolated: one cell at +1.5 (a kernel
	// [0 0 0 0 1] targetX 0, cells 1.5 apart... here a 2-cell kernel [0
	// 1], target 0, unit 1.5) reads the mean of the pixels 1 and 2 to the
	// right; past the image (edgeMode none), zero.
	row := []uint8{0, 0, 0, 40, 0, 0, 0, 80, 0, 0, 0, 120, 0, 0, 0, 160}
	run := &filterRun{w: 4, h: 1, region: irect{0, 0, 4, 1}, src: row, ts: identity}
	res, _ := run.convolve(&fprim{kind: tagFeConvolveMatrix, conv: &convolveSpec{cols: 2, rows: 1, kernel: []float32{1, 0}, divisor: 1, edge: edgeNone, unitX: 1.5, unitY: 1}})
	// The kernel is turned half round: the cell after the target is
	// kernel[0]. Alphas: (80+120)/2, (120+160)/2, (160+0)/2, 0.
	for i, want := range []uint8{100, 140, 80, 0} {
		if got := res.pix[i*4+3]; got != want {
			t.Errorf("a cell 1.5 pixels away: pixel %d alpha %d, want %d", i, got, want)
		}
	}
	// A kernelUnitLength of one device pixel is the plain kernel.
	k := `<feConvolveMatrix kernelMatrix="1 0 2 0 1 0 -1 0 1"`
	for _, scale := range []float64{1, 2} {
		unit := "1"
		if scale == 2 {
			unit = "0.5"
		}
		if !samePixels(render(t, filterSVG(k+` kernelUnitLength="`+unit+`"/>`, "", alignedShapes), scale), render(t, filterSVG(k+`/>`, "", alignedShapes), scale)) {
			t.Errorf("scale %g: kernelUnitLength %s is not the plain kernel", scale, unit)
		}
	}
}

// ---- the rest ------------------------------------------------------------

func TestSpecMorphology(t *testing.T) {
	for _, prims := range []string{
		`<feMorphology operator="dilate"/>`,
		`<feMorphology operator="dilate" radius="0"/>`,
		`<feMorphology operator="dilate" radius="-1"/>`,
		`<feMorphology operator="dilate" radius="0 3"/>`,
		`<feMorphology operator="erode" radius="3 -2"/>`,
		`<feMorphology operator="dilate" radius="1 2 3"/>`,
		`<feMorphology operator="dilate" radius="abc"/>`,
	} {
		passThrough(t, prims, prims)
	}
	// A radius whose 2 x ceil(r) is past 32 bits is a kernel of the whole
	// line, as is one of 10^6 (resvg's u32 wraps it to zero).
	sameAs(t, "erode 2^31", `<feMorphology operator="erode" radius="2147483648"/>`, `<feMorphology operator="erode" radius="1e6"/>`)
	sameAs(t, "dilate 2^31", `<feMorphology operator="dilate" radius="2147483648 1"/>`, `<feMorphology operator="dilate" radius="1e6 1"/>`)
}

func TestSpecBlurNegative(t *testing.T) {
	// A negative stdDeviation disables the blur (a pass through), even
	// beside a positive one.
	passThrough(t, "blur -1 3", `<feGaussianBlur stdDeviation="-1 3"/>`)
	passThrough(t, "blur 3 -1", `<feGaussianBlur stdDeviation="3 -1"/>`)
	// feDropShadow's stdDeviation that is not a value is the initial 2.
	sameAs(t, "drop shadow", `<feDropShadow stdDeviation="abc" dx="4" dy="3"/>`, `<feDropShadow stdDeviation="2" dx="4" dy="3"/>`)
}

func TestSpecComponentTransferLast(t *testing.T) {
	// The last feFuncR counts, its type not a type: identity.
	sameAs(t, "last feFuncR", `<feComponentTransfer><feFuncR type="linear" slope="0"/><feFuncR type="bogus"/></feComponentTransfer>`, `<feComponentTransfer/>`)
	sameAs(t, "last feFuncG", `<feComponentTransfer><feFuncG type="bogus"/><feFuncG type="linear" slope="0"/></feComponentTransfer>`, `<feComponentTransfer><feFuncG type="linear" slope="0"/></feComponentTransfer>`)
}

func TestSpecCompositeLighter(t *testing.T) {
	// plus-lighter, premultiplied: rgb(100,50,0) and rgb(0,60,120) at
	// opacity .5 (alpha 128) are (50, 25, 0, 128) and (0, 30, 60, 128);
	// their sum (50, 55, 60, 256), its alpha clamped: opaque (50, 55, 60).
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><filter id="f" color-interpolation-filters="sRGB" x="0" y="0" width="1" height="1">` +
		`<feFlood flood-color="rgb(100,50,0)" flood-opacity="0.5" result="a"/><feFlood flood-color="rgb(0,60,120)" flood-opacity="0.5" result="b"/><feComposite in="a" in2="b" operator="lighter"/></filter>` +
		`<rect width="20" height="20" filter="url(#f)"/></svg>`
	wantPixels(t, "lighter", render(t, svg, 1), []pixelWant{{10, 10, [4]uint8{50, 55, 60, 255}}})
}

func TestSpecSubregion(t *testing.T) {
	const head = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="150"><rect width="200" height="150" fill="#345"/>`
	// primitiveUnits="objectBoundingBox": the sub-region (.25, .25) .5 x
	// .5 of the box (40, 20) 80 x 60 is (60, 35) to (100, 65); the offset
	// .02 x 80 = 1.6, a pixel (truncated, the engine's).
	svg := head + `<defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse" primitiveUnits="objectBoundingBox">` +
		`<feOffset x="0.25" y="0.25" width="0.5" height="0.5" dx="0.02"/></filter></defs><rect x="40" y="20" width="80" height="60" fill="lime" filter="url(#f)"/></svg>`
	wantPixels(t, "objectBoundingBox", render(t, svg, 1), []pixelWant{{59, 40, bg}, {60, 35, lime}, {99, 64, lime}, {100, 40, bg}, {80, 65, bg}, {80, 34, bg}, {50, 50, bg}})

	// The default: the union of the referenced results' sub-regions. The
	// offset of a flood (40, 30) 30 x 25 is clipped to the flood's
	// sub-region: moved by (20, 10), lime from (60, 40) to (70, 55) only.
	svg = filterSVG(`<feFlood x="40" y="30" width="30" height="25" flood-color="lime" result="a"/><feOffset in="a" dx="20" dy="10"/>`, "", alignedShapes)
	wantPixels(t, "union", render(t, svg, 1), []pixelWant{{60, 40, lime}, {69, 54, lime}, {70, 45, bg}, {65, 55, bg}, {45, 35, bg}})
	// A blur of a flood under objectBoundingBox: clipped to the flood's
	// sub-region, (30, 26) to (80, 66) of the box (10, 10) 100 x 80.
	svg = head + `<defs><filter id="f" primitiveUnits="objectBoundingBox"><feFlood x="0.2" y="0.2" width="0.5" height="0.5" flood-color="lime"/><feGaussianBlur stdDeviation="0.05"/></filter></defs>` +
		`<rect x="10" y="10" width="100" height="80" fill="gray" filter="url(#f)"/></svg>`
	img := render(t, svg, 1)
	wantPixels(t, "blur of a flood", img, []pixelWant{{29, 40, bg}, {50, 25, bg}, {81, 40, bg}, {50, 66, bg}, {55, 46, lime}})
	if got := pixelAt(img, 30, 40); got == bg {
		t.Error("blur of a flood: (30, 40), inside the sub-region, is the background")
	}

	// A primitive whose width is 0 is a transparent result, and the
	// filter goes on.
	sameAs(t, "a disabled primitive (ending the filter, or drawing?)", `<feFlood width="0" flood-color="lime" result="z"/><feMerge color-interpolation-filters="sRGB"><feMergeNode in="z"/><feMergeNode in="SourceGraphic"/></feMerge>`, identityFilter)
}

// ---- the filter property (§5), and filters not applied ------------------------

func TestSpecFilterReference(t *testing.T) {
	const defs = `<defs><filter id="f"><feOffset dx="9" dy="6"/></filter><filter id="e"/><filter id="u" filterUnits="userSpaceOnUse" x="0" y="0" width="120" height="100"><feOffset dx="9" dy="6"/></filter></defs>`
	doc := func(elem string) string { return svgHead + defs + elem + `</svg>` }
	red := `<rect x="10" y="10" width="50" height="50" fill="red"`
	plain := render(t, doc(red+`/>`), 1)
	// A reference to no filter, alone or in a chain: no filter at all.
	for _, f := range []string{`url(#nope)`, `url(#f) url(#nope)`, `url(#nope) url(#f)`} {
		if !samePixels(render(t, doc(red+` filter="`+f+`"/>`), 1), plain) {
			t.Errorf("filter=%q: not the element unfiltered", f)
		}
	}
	// A filter with no primitive in the chain: the element is not drawn.
	for i, v := range render(t, doc(red+` filter="url(#f) url(#e)"/>`), 1).Pix {
		if v != 0 {
			t.Fatalf("a chain with an empty filter drew byte %d = %d", i, v)
		}
	}
	// objectBoundingBox units on an element whose box has no height (a
	// horizontal line): the filter is ignored, the line drawn unfiltered;
	// one in user space is applied.
	line := `<path d="M10 50 H100" stroke="red" stroke-width="6"`
	if !samePixels(render(t, doc(line+` filter="url(#f)"/>`), 1), render(t, doc(line+`/>`), 1)) {
		t.Error("objectBoundingBox on a box of no height: not the line unfiltered")
	}
	wantPixels(t, "user space on a box of no height", render(t, doc(line+` filter="url(#u)"/>`), 1), []pixelWant{{50, 50, [4]uint8{}}, {50, 56, [4]uint8{255, 0, 0, 255}}})
}
