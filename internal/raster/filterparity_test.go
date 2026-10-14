package raster

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// Parity with resvg of the filter primitives ported after the first nine
// (feTile, feMorphology, feConvolveMatrix, ...), through runSynths: each
// case at scale 1 and 2.5 against resvg's render.
//
// Most cases filter alignedShapes: rectangles on even coordinates, so the
// source graphic is the same pixels in both renderers at both scales (no
// anti-aliasing) and what is compared is the primitive's own arithmetic.
// Those are held to (nearly) exact. The aaShapes cases are the typical
// drawing, a circle's anti-aliased edge included: there the source graphic
// itself differs from resvg's by a little (an unfiltered render of it is at
// RMSE 0.3, max 12), and a primitive that amplifies edges (an edge-detecting
// kernel, a lighting) amplifies that, so their bounds are looser.
//
// Where Filter Effects 1 defines a primitive differently from resvg, the
// engine follows the specification (ADR 0015): those cases are not here
// but in filterDivergences, which asserts that they differ from resvg, and
// TestSpec* asserts what the specification says they draw.

// alignedShapes are translucent and opaque rectangles on even coordinates,
// one at the region's edge.
const alignedShapes = `<rect x="40" y="30" width="60" height="50" fill="#c33"/><rect x="110" y="60" width="50" height="60" fill="#36c" fill-opacity="0.7"/>` +
	`<rect x="70" y="70" width="60" height="30" fill="#2a8" fill-opacity="0.45"/><rect x="20" y="94" width="44" height="40" fill="#fc0" fill-opacity="0.9"/>` +
	`<rect x="0" y="0" width="12" height="150" fill="#f0f"/>`

// aaShapes is a rectangle and a translucent circle (anti-aliased).
const aaShapes = `<rect x="40" y="30" width="60" height="50" fill="#c33"/><circle cx="130" cy="90" r="30" fill="#36c" fill-opacity="0.7"/>`

// filterSVG is a 200x150 document drawing shapes, over a dark background,
// through a filter of the whole canvas (user space) made of prims; attrs are
// more attributes of the filter element.
func filterSVG(prims, attrs, shapes string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150">` +
		`<defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse" ` + attrs + `>` + prims + `</filter></defs>` +
		`<rect width="200" height="150" fill="#345"/><g filter="url(#f)">` + shapes + `</g></svg>`
}

// exact is the bound of an aligned case: equal but for a rare off-by-one.
func exact(name, prims string) synth {
	return synth{name, filterSVG(prims, "", alignedShapes), 0.01, 0}
}

// feImagePNG is a 16x16 PNG, as a data: URI.
const feImagePNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAALUlEQVR4nGNgaGD4j4z/o2GC8sPCgP//kfF/NExQflgYMPCxMPAGDHwsDLABAMHmfh/dG/h0AAAAAElFTkSuQmCC"

const kernel9x7 = `-0.82 -1.484 -0.02 -1.225 -1.667 0.008 2.59 2.002 1.826 -0.89 0.683 -0.617 -1.137 -1.469 -0.928 2.637 2.145 2.033 2.002 -1.033 -0.451 ` +
	`1.135 1.659 2.273 2.4 -1.566 1.029 1.359 0.53 -1.111 0.368 -1.553 2.673 2.327 0.738 -0.499 2.544 0.862 2.412 2.24 0.542 0.07 0.995 ` +
	`0.155 -1.193 -0.474 2.063 -1.784 -1.768 1.132 -0.598 0.673 0.356 -0.286 2.986 -1.022 0.064 -0.987 1.163 -0.618 -0.221 1.735 -0.397`

var filterParity = []synth{
	// ---- feTile ----
	{"tile-flood", filterSVG(`<feFlood x="40" y="30" width="30" height="25" flood-color="green" result="a"/><feOffset in="SourceGraphic" result="b"/><feTile in="a"/>`, "", aaShapes), 0.01, 0},
	{"tile-offset-subregion", filterSVG(`<feOffset in="SourceGraphic" x="40" y="30" width="40" height="40" result="b"/><feTile in="b"/>`, "", aaShapes), 0.1, 0.25},
	exact("tile-moved", `<feOffset dx="20" dy="10" x="30" y="20" width="30" height="25" result="o"/><feTile in="o"/>`),
	// Wholly outside the image: a transparent result here (Filter Effects
	// 1), the filter failing in resvg; the same pixels, the tile being the
	// filter's last primitive (TestSpecTile tells them apart).
	exact("tile-outside", `<feFlood x="300" y="300" width="40" height="30" flood-color="lime" result="a"/><feTile in="a"/>`),
	exact("tile-source", `<feTile/>`),
	exact("tile-own-subregion", `<feTile x="20" y="20" width="100" height="100"/>`),

	// ---- feMorphology ----
	{"morph-dilate", filterSVG(`<feMorphology operator="dilate" radius="2"/>`, "", aaShapes), 0.1, 0.25},
	{"morph-erode", filterSVG(`<feMorphology operator="erode" radius="2"/>`, "", aaShapes), 0.1, 0.25},
	{"morph-xy", filterSVG(`<feMorphology operator="dilate" radius="1 4"/>`, "", aaShapes), 0.1, 0.25},
	exact("morph-erode-frac", `<feMorphology operator="erode" radius="3.5 1"/>`),
	exact("morph-dilate-6", `<feMorphology operator="dilate" radius="6"/>`),
	exact("morph-dilate-srgb", `<feMorphology operator="dilate" radius="3" color-interpolation-filters="sRGB"/>`),
	// A radius past the image: the window is the whole line.
	exact("morph-radius-huge", `<feMorphology operator="dilate" radius="1e6"/>`),
	exact("morph-radius-huge-x", `<feMorphology radius="1e7 2"/>`),
	exact("morph-radius-frac", `<feMorphology operator="dilate" radius="0.2"/>`),
	exact("morph-radius-denormal", `<feMorphology operator="dilate" radius="1e-40"/>`),
	exact("morph-operator-case", `<feMorphology operator="Dilate" radius="3"/>`),
	{"morph-obb", filterSVG(`<feMorphology operator="dilate" radius="0.05"/>`, `primitiveUnits="objectBoundingBox"`, alignedShapes), 0.01, 0},

	// ---- feConvolveMatrix ----
	{"conv-blur", filterSVG(`<feConvolveMatrix order="3" kernelMatrix="1 1 1 1 1 1 1 1 1" divisor="9"/>`, "", aaShapes), 0.1, 0.25},
	// An edge kernel amplifies the circle's anti-aliasing differences (x8).
	{"conv-edge", filterSVG(`<feConvolveMatrix order="3" kernelMatrix="-1 -1 -1 -1 8 -1 -1 -1 -1" preserveAlpha="true"/>`, "", aaShapes), 0.6, 0.7},
	exact("conv-edge-aligned", `<feConvolveMatrix order="3" kernelMatrix="-1 -1 -1 -1 8 -1 -1 -1 -1" preserveAlpha="true"/>`),
	exact("conv-edge-premultiplied", `<feConvolveMatrix order="3" kernelMatrix="-1 -1 -1 -1 8 -1 -1 -1 -1"/>`),
	exact("conv-emboss-wrap-bias", `<feConvolveMatrix order="3 3" kernelMatrix="-2 -1 0 -1 1 1 0 1 2" bias="0.3" edgeMode="wrap"/>`),
	exact("conv-5x3-target-none", `<feConvolveMatrix order="5 3" kernelMatrix="1 2 3 2 1 0 0 0 0 0 -1 -2 -3 -2 -1" divisor="4" targetX="1" targetY="2" edgeMode="none"/>`),
	exact("conv-9x7-wrap", `<feConvolveMatrix order="9 7" kernelMatrix="`+kernel9x7+`" edgeMode="wrap"/>`),
	exact("conv-wrap-far", `<feConvolveMatrix order="7" targetX="6" targetY="0" kernelMatrix="`+strings.Repeat("0 ", 48)+`1" edgeMode="wrap"/>`),
	exact("conv-preserve-alpha-bias", `<feConvolveMatrix kernelMatrix="1 0 0 0 1 0 0 0 -1" preserveAlpha="true" bias="0.2"/>`),
	exact("conv-divisor-negative", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" divisor="-9" bias="1"/>`),
	exact("conv-kernel-sums-to-zero", `<feConvolveMatrix kernelMatrix="0 -1 0 -1 4 -1 0 -1 0"/>`),
	exact("conv-order-1", `<feConvolveMatrix order="1" kernelMatrix="2"/>`),
	exact("conv-order-frac", `<feConvolveMatrix order="2.7" kernelMatrix="1 2 3 4"/>`),
	exact("conv-target-frac", `<feConvolveMatrix kernelMatrix="1 2 1 0 0 0 -1 -2 -1" targetY="2.9"/>`),
	exact("conv-edge-mode-unknown", `<feConvolveMatrix kernelMatrix="1 0 0 0 0 0 0 0 1" edgeMode="Wrap"/>`),

	// ---- feDisplacementMap ----
	// Every pixel displaced past the image, whichever way the scale is
	// applied (the rest diverge: filterDivergences).
	exact("disp-scale-huge", `<feDisplacementMap in2="SourceGraphic" scale="1e30" xChannelSelector="B" yChannelSelector="R"/>`),

	// ---- feTurbulence ----
	// The noise is the same bits as resvg's (the reference code's): these are
	// exact whatever the source (the source is not read). Stitched noise, a
	// seed past 32 bits and a numOctaves that is not whole diverge
	// (filterDivergences).
	exact("turb-turbulence", `<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2"/>`),
	exact("turb-fractal", `<feTurbulence type="fractalNoise" baseFrequency="0.05" numOctaves="2" seed="1"/>`),
	exact("turb-srgb", `<feTurbulence baseFrequency="0.05" numOctaves="2" color-interpolation-filters="sRGB"/>`),
	exact("turb-seed-fraction", `<feTurbulence baseFrequency="0.02 0.08" numOctaves="4" seed="-7.9"/>`),
	exact("turb-seed-max", `<feTurbulence baseFrequency="0.03" numOctaves="2" seed="1e10"/>`),
	exact("turb-octaves-40", `<feTurbulence baseFrequency="0.05" numOctaves="40"/>`),
	exact("turb-frequency-high", `<feTurbulence baseFrequency="3.7" numOctaves="3"/>`),
	exact("turb-type-case", `<feTurbulence baseFrequency="0.05" numOctaves="2" type="FractalNoise"/>`),
	{"light-turbulence-bump", filterSVG(`<feTurbulence type="fractalNoise" baseFrequency="0.04" numOctaves="2" result="n"/><feDiffuseLighting in="n" surfaceScale="2" lighting-color="white"><feDistantLight azimuth="225" elevation="45"/></feDiffuseLighting>`, "", aaShapes), 0.01, 0},
	exact("turb-octaves-zero", `<feTurbulence baseFrequency="0.03" numOctaves="0" type="fractalNoise"/>`),
	exact("turb-octaves-negative", `<feTurbulence baseFrequency="0.03" numOctaves="-3"/>`),
	exact("turb-frequency-zero", `<feTurbulence baseFrequency="0" numOctaves="3" type="fractalNoise"/>`),
	exact("turb-frequency-negative", `<feTurbulence baseFrequency="0.05 -0.1" numOctaves="3"/>`),
	exact("turb-frequency-three", `<feTurbulence baseFrequency="0.05 0.1 0.2" numOctaves="3"/>`),
	// Past 1,024 octaves 2^k is infinite: the noise, where it is not finite,
	// makes the sum NaN (transparent), elsewhere adds nothing.
	{"turb-octaves-3000", `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="18"><defs><filter id="f" x="-5" y="-3" width="30" height="25" filterUnits="userSpaceOnUse">` +
		`<feTurbulence baseFrequency="1e-40" numOctaves="3000"/></filter></defs><rect width="24" height="18" fill="#345" filter="url(#f)"/></svg>`, 0.01, 0},
	{"turb-octaves-1100-tiny-scale", `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="18" viewBox="0 0 2.4e-30 1.8e-30"><defs><filter id="f" x="0" y="0" width="2.4e-30" height="1.8e-30" filterUnits="userSpaceOnUse">` +
		`<feTurbulence baseFrequency="3e-45 1e-20" numOctaves="1100" stitchTiles="stitch" type="fractalNoise"/></filter></defs><rect width="2.4e-30" height="1.8e-30" fill="#345" filter="url(#f)"/></svg>`, 0.01, 0},

	// ---- feDiffuseLighting, feSpecularLighting ----
	exact("light-diffuse-spot-no-cone", `<feDiffuseLighting surfaceScale="2" lighting-color="yellow"><feSpotLight x="10" y="140" z="80" pointsAtX="150" pointsAtY="30" specularExponent="0"/></feDiffuseLighting>`),
	exact("light-specular-spot-cone", `<feSpecularLighting surfaceScale="4" specularConstant="1.5" specularExponent="12" lighting-color="#fff"><feSpotLight x="60" y="20" z="60" pointsAtX="100" pointsAtY="90" pointsAtZ="0" specularExponent="2" limitingConeAngle="40"/></feSpecularLighting>`),
	exact("light-specular-point-exponent-128", `<feSpecularLighting surfaceScale="3" specularExponent="128" specularConstant="2"><fePointLight x="120" y="90" z="15"/></feSpecularLighting>`),
	exact("light-specular-negative-scale-cone", `<feSpecularLighting surfaceScale="-8" specularExponent="5"><feSpotLight x="100" y="75" z="40" pointsAtX="100" pointsAtY="75" pointsAtZ="-1" limitingConeAngle="-20"/></feSpecularLighting>`),
	// No light source: a transparent result in both.
	exact("light-no-source", `<feDiffuseLighting surfaceScale="3"/>`),
	// The first light source counts.
	exact("light-two-sources", `<feDiffuseLighting surfaceScale="3"><fePointLight x="10" y="10" z="20"/><feDistantLight azimuth="90" elevation="10"/></feDiffuseLighting>`),
	// lighting-color in sRGB (nothing to convert), a bad colour (white),
	// currentColor with no color (black).
	exact("light-srgb", `<feDiffuseLighting surfaceScale="3" lighting-color="#f80" color-interpolation-filters="sRGB"><feDistantLight azimuth="135" elevation="30"/></feDiffuseLighting>`),
	exact("light-bad-color", `<feDiffuseLighting surfaceScale="3" lighting-color="nope"><feDistantLight azimuth="135" elevation="30"/></feDiffuseLighting>`),
	exact("light-current-color-none", `<feDiffuseLighting surfaceScale="3" lighting-color="currentColor"><feDistantLight azimuth="135" elevation="30"/></feDiffuseLighting>`),
	// On anti-aliased shapes the normals amplify the edges' small alpha
	// differences (the gradient of the alpha times surfaceScale).
	{"light-bevel-chain-aa", filterSVG(`<feGaussianBlur stdDeviation="3" result="b"/><feSpecularLighting in="b" surfaceScale="5" specularExponent="20" lighting-color="#fff" result="s"><fePointLight x="60" y="40" z="80"/></feSpecularLighting>`+
		`<feComposite in="s" in2="SourceGraphic" operator="in" result="c"/><feComposite in="SourceGraphic" in2="c" operator="arithmetic" k1="0" k2="1" k3="1" k4="0"/>`, "", aaShapes), 0.15, 0.6},

	// ---- feImage ----
	// An image, as an <image> draws it (its smoothing differs from resvg's
	// as a plain <image>'s does: the same numbers).
	{"image-data", filterSVG(`<feImage xlink:href="`+feImagePNG+`" x="20" y="20" width="80" height="80"/>`, "", alignedShapes), 0.6, 4},
	{"image-data-slice", filterSVG(`<feImage xlink:href="`+feImagePNG+`" x="20" y="20" width="120" height="60" preserveAspectRatio="xMidYMid slice"/>`, "", alignedShapes), 0.6, 8},
	{"image-data-xmax-over", filterSVG(`<feImage xlink:href="`+feImagePNG+`" x="20" y="10" width="140" height="60" preserveAspectRatio="xMaxYMax meet"/><feComposite in2="SourceGraphic" operator="over"/>`, "", alignedShapes), 0.3, 4},
	{"image-data-obb", filterSVG(`<feImage xlink:href="`+feImagePNG+`" x="0.1" y="0.2" width="0.5" height="0.5"/>`, `primitiveUnits="objectBoundingBox"`, alignedShapes), 0.6, 8},
	// Exact at scale 1; at 2.5 a nearest sample's edge falls a pixel apart
	// from resvg's, as a plain pixelated <image>'s does (the same numbers).
	{"image-data-pixelated", filterSVG(`<feImage xlink:href="`+feImagePNG+`" x="20" y="10" width="140" height="60" preserveAspectRatio="none" image-rendering="optimizeSpeed"/>`, "", alignedShapes), 0.25, 0.4},
	// The dummy: no href, an image that does not decode, a fragment of no
	// element.
	exact("image-none", `<feImage/>`),
	exact("image-data-bad", `<feImage xlink:href="data:image/png;base64,AAAA"/>`),
	exact("image-fragment-missing", `<feImage xlink:href="#nope"/>`),
	// A chain: each feImage draws an element filtered by the next filter
	// (the filter region at the user space's origin, untransformed: where
	// both place an element alike).
	{"image-chain", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs>` +
		`<filter id="f1" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e1"/><feOffset dx="10" dy="5"/></filter>` +
		`<filter id="f2" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e2"/><feOffset dx="10" dy="5"/></filter>` +
		`<filter id="f3" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e3"/><feOffset dx="10" dy="5"/></filter>` +
		`<rect id="e1" x="20" y="20" width="30" height="30" fill="red" filter="url(#f2)"/><rect id="e2" x="30" y="30" width="30" height="30" fill="green" filter="url(#f3)"/>` +
		`<rect id="e3" x="40" y="40" width="30" height="30" fill="blue"/></defs><rect width="200" height="150" fill="#345"/>` +
		`<rect x="100" y="80" width="40" height="40" fill="#c33" filter="url(#f1)"/></svg>`, 0.01, 0},

	// ---- feColorMatrix hueRotate ----
	// Angles where f64 and resvg's f32 agree (at 90 and 270 degrees they do
	// not: filterDivergences).
	exact("hue-30", `<feColorMatrix type="hueRotate" values="30"/>`),
	exact("hue-negative", `<feColorMatrix type="hueRotate" values="-45"/>`),
	exact("hue-large", `<feColorMatrix type="hueRotate" values="1e6"/>`),
	exact("hue-fraction", `<feColorMatrix type="hueRotate" values="123.456"/>`),

	// ---- the filter region ----
	// Larger than its layer (4x the canvas): cut to the layer, its
	// primitives' sub-regions measured from the cut corner.
	{"region-past-layer", `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="150"><defs><filter id="f" x="-1000" y="-1000" width="5000" height="5000" filterUnits="userSpaceOnUse">` +
		`<feFlood x="10" y="10" width="20" height="15" flood-color="lime" result="a"/><feTile in="a"/></filter></defs><rect width="200" height="150" fill="#345"/>` +
		`<rect x="40" y="30" width="60" height="50" fill="#c33" filter="url(#f)"/></svg>`, 0.01, 0},
	{"region-past-layer-subregion", `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="150"><defs><filter id="f" x="-1000" y="-1000" width="5000" height="5000" filterUnits="userSpaceOnUse">` +
		`<feFlood x="10" y="10" width="50" height="50" flood-color="lime"/></filter></defs><rect width="200" height="150" fill="#345"/>` +
		`<rect x="40" y="30" width="60" height="50" fill="#c33" filter="url(#f)"/></svg>`, 0.01, 0},
}

func TestFilterPrimitiveParity(t *testing.T) { runSynths(t, "fp-", filterParity) }

// A convolution kernel the input makes larger than maxConvolveTaps, which
// resvg would run (for days), fails with ErrLimit; an order whose product
// is not the kernel's length is a pass through, with nothing to run or
// bound (Filter Effects 1 §9.9), even where the product wraps a u32 to the
// length (usvg accepts that kernel, and resvg indexes past it or crashes).
func TestConvolveKernelBound(t *testing.T) {
	big := func(order string, n int) string {
		return filterSVG(`<feConvolveMatrix order="`+order+`" kernelMatrix="`+strings.TrimSpace(strings.Repeat("1 ", n))+`"/>`, "", alignedShapes)
	}
	for _, tc := range []struct {
		name, svg string
		limit     bool
	}{
		{"at the bound", big("256", 1<<16), false},
		{"past the bound", big("257 256", 257*256), true},
		// orderX * orderY wraps a u32 to the kernel's length.
		{"wraps to an empty kernel", filterSVG(`<feConvolveMatrix order="65536"/>`, "", alignedShapes), false},
		{"wraps to the kernel's length", big("65536 65537", 65536), false},
		{"wrong length", big("300", 100), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A small canvas (8x6), so that a kernel under the bound is drawn
			// within MaxPixelOps.
			_, err := Render([]byte(tc.svg), Options{Scale: 0.04})
			if got := errors.Is(err, budget.ErrLimit); got != tc.limit {
				t.Errorf("err = %v; want ErrLimit: %v", err, tc.limit)
			}
		})
	}
}
