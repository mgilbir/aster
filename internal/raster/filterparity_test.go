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

const kernel9x7 = `-0.82 -1.484 -0.02 -1.225 -1.667 0.008 2.59 2.002 1.826 -0.89 0.683 -0.617 -1.137 -1.469 -0.928 2.637 2.145 2.033 2.002 -1.033 -0.451 ` +
	`1.135 1.659 2.273 2.4 -1.566 1.029 1.359 0.53 -1.111 0.368 -1.553 2.673 2.327 0.738 -0.499 2.544 0.862 2.412 2.24 0.542 0.07 0.995 ` +
	`0.155 -1.193 -0.474 2.063 -1.784 -1.768 1.132 -0.598 0.673 0.356 -0.286 2.986 -1.022 0.064 -0.987 1.163 -0.618 -0.221 1.735 -0.397`

var filterParity = []synth{
	// ---- feTile ----
	{"tile-flood", filterSVG(`<feFlood x="40" y="30" width="30" height="25" flood-color="green" result="a"/><feOffset in="SourceGraphic" result="b"/><feTile in="a"/>`, "", aaShapes), 0.01, 0},
	{"tile-offset-subregion", filterSVG(`<feOffset in="SourceGraphic" x="40" y="30" width="40" height="40" result="b"/><feTile in="b"/>`, "", aaShapes), 0.1, 0.25},
	exact("tile-moved", `<feOffset dx="20" dy="10" x="30" y="20" width="30" height="25" result="o"/><feTile in="o"/>`),
	exact("tile-partly-outside", `<feFlood x="-10" y="-5" width="40" height="30" flood-color="lime" result="a"/><feTile in="a"/>`),
	// Wholly outside the image: resvg's copy fails, and with it the filter.
	exact("tile-outside", `<feFlood x="300" y="300" width="40" height="30" flood-color="lime" result="a"/><feTile in="a"/>`),
	exact("tile-source", `<feTile/>`),
	exact("tile-own-subregion", `<feTile x="20" y="20" width="100" height="100"/>`),
	// A linearRGB input is tiled as it is, and tagged sRGB, as by resvg.
	exact("tile-linear-input", `<feComponentTransfer x="40" y="30" width="26" height="20" result="c"><feFuncR type="linear" slope="0.5"/></feComponentTransfer><feTile in="c"/>`),

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
	// 2*ceil(r) wraps a u32 to zero: resvg visits no pixel.
	exact("morph-radius-wraps", `<feMorphology operator="erode" radius="2147483648"/>`),
	exact("morph-radius-wraps-dilate", `<feMorphology operator="dilate" radius="2147483648 1"/>`),
	exact("morph-radius-negative", `<feMorphology operator="dilate" radius="-1"/>`),
	exact("morph-radius-zero", `<feMorphology operator="dilate" radius="0"/>`),
	exact("morph-radius-one-zero", `<feMorphology operator="dilate" radius="0 3"/>`),
	exact("morph-radius-three", `<feMorphology operator="dilate" radius="1 2 3"/>`),
	exact("morph-radius-bad", `<feMorphology operator="dilate" radius="abc"/>`),
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
	exact("conv-order-bad-second", `<feConvolveMatrix order="2 abc" kernelMatrix="1 2 3 4"/>`),
	exact("conv-target-frac", `<feConvolveMatrix kernelMatrix="1 2 1 0 0 0 -1 -2 -1" targetY="2.9"/>`),
	exact("conv-edge-mode-unknown", `<feConvolveMatrix kernelMatrix="1 0 0 0 0 0 0 0 1" edgeMode="Wrap"/>`),
	// Invalid: usvg's dummy, a transparent result.
	exact("conv-order-zero", `<feConvolveMatrix order="0" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`),
	exact("conv-order-negative", `<feConvolveMatrix order="-3" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`),
	exact("conv-target-negative", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" targetX="-1"/>`),
	exact("conv-target-outside", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" targetY="3"/>`),
	exact("conv-divisor-zero", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" divisor="0"/>`),
	exact("conv-kernel-short", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1"/>`),
	exact("conv-kernel-missing", `<feConvolveMatrix/>`),
	exact("conv-kernel-bad", `<feConvolveMatrix kernelMatrix="1 1 1 1 x 1 1 1 1"/>`),

	// ---- feDisplacementMap ----
	// resvg multiplies the offset by the scale twice: a scale of 10 moves a
	// pixel by up to 50 (0.5 x 10 x 10).
	exact("disp-flood", `<feFlood flood-color="#80c0ff" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="2" xChannelSelector="R" yChannelSelector="B"/>`),
	exact("disp-flood-translucent", `<feFlood flood-color="#c040ff" flood-opacity="0.6" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="10" xChannelSelector="R" yChannelSelector="G"/>`),
	exact("disp-self", `<feDisplacementMap in="SourceGraphic" in2="SourceGraphic" scale="6" xChannelSelector="G" yChannelSelector="A"/>`),
	exact("disp-alpha-default-channels", `<feDisplacementMap in2="SourceAlpha" scale="3"/>`),
	exact("disp-scale-default", `<feDisplacementMap in2="SourceGraphic"/>`),
	exact("disp-scale-negative", `<feDisplacementMap in2="SourceGraphic" scale="-7" xChannelSelector="B" yChannelSelector="R"/>`),
	exact("disp-scale-huge", `<feDisplacementMap in2="SourceGraphic" scale="1e30" xChannelSelector="B" yChannelSelector="R"/>`),
	exact("disp-channel-unknown", `<feDisplacementMap in2="SourceGraphic" scale="5" xChannelSelector="r" yChannelSelector="X"/>`),
	exact("disp-srgb", `<feDisplacementMap in2="SourceGraphic" scale="4" xChannelSelector="R" yChannelSelector="G" color-interpolation-filters="sRGB"/>`),
	exact("disp-transfer-map", `<feOffset dx="3" result="o"/><feComponentTransfer in="SourceGraphic" result="m"><feFuncR type="linear" slope="2" intercept="-0.3"/><feFuncG type="discrete" tableValues="0 0.3 0.8 1"/></feComponentTransfer><feDisplacementMap in="o" in2="m" scale="3.5" xChannelSelector="R" yChannelSelector="G"/>`),
	{"disp-obb", filterSVG(`<feDisplacementMap in2="SourceGraphic" scale="0.05" xChannelSelector="R" yChannelSelector="G"/>`, `primitiveUnits="objectBoundingBox"`, alignedShapes), 0.01, 0},
	{"disp-aa", filterSVG(`<feFlood flood-color="#80c0ff" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="2" xChannelSelector="R" yChannelSelector="B"/>`, "", aaShapes), 0.1, 0.25},

	// ---- feTurbulence ----
	// The noise is the same bits as resvg's: these are exact whatever the
	// source (the source is not read).
	exact("turb-turbulence", `<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2"/>`),
	exact("turb-fractal", `<feTurbulence type="fractalNoise" baseFrequency="0.05" numOctaves="2" seed="1"/>`),
	exact("turb-stitch-fractal", `<feTurbulence type="fractalNoise" baseFrequency="0.03 0.07" numOctaves="3" seed="5" stitchTiles="stitch"/>`),
	exact("turb-stitch-odd", `<feTurbulence baseFrequency="0.013 0.0217" numOctaves="5" stitchTiles="stitch" type="fractalNoise" seed="3"/>`),
	exact("turb-stitch-turbulence", `<feTurbulence baseFrequency="0.04" numOctaves="3" stitchTiles="stitch" seed="11"/>`),
	exact("turb-subregion", `<feTurbulence x="30" y="20" width="90" height="70" baseFrequency="0.05" numOctaves="2" stitchTiles="stitch"/>`),
	exact("turb-srgb", `<feTurbulence baseFrequency="0.05" numOctaves="2" color-interpolation-filters="sRGB"/>`),
	exact("turb-seed-fraction", `<feTurbulence baseFrequency="0.02 0.08" numOctaves="4" seed="-7.9"/>`),
	exact("turb-seed-min", `<feTurbulence baseFrequency="0.03" numOctaves="2" seed="-1e10" type="fractalNoise"/>`),
	exact("turb-seed-max", `<feTurbulence baseFrequency="0.03" numOctaves="2" seed="1e10"/>`),
	exact("turb-octaves-zero", `<feTurbulence baseFrequency="0.03" numOctaves="0" type="fractalNoise"/>`),
	exact("turb-octaves-negative", `<feTurbulence baseFrequency="0.03" numOctaves="-3"/>`),
	exact("turb-octaves-fraction", `<feTurbulence baseFrequency="0.03" numOctaves="2.5"/>`),
	exact("turb-octaves-40", `<feTurbulence baseFrequency="0.05" numOctaves="40"/>`),
	exact("turb-frequency-zero", `<feTurbulence baseFrequency="0" numOctaves="3" type="fractalNoise"/>`),
	exact("turb-frequency-negative", `<feTurbulence baseFrequency="0.05 -0.1" numOctaves="3"/>`),
	exact("turb-frequency-three", `<feTurbulence baseFrequency="0.05 0.1 0.2" numOctaves="3"/>`),
	exact("turb-frequency-high", `<feTurbulence baseFrequency="3.7" numOctaves="3"/>`),
	exact("turb-type-case", `<feTurbulence baseFrequency="0.05" numOctaves="2" type="FractalNoise"/>`),
	// Past 1,024 octaves 2^k is infinite: the noise, where it is not finite,
	// makes the sum NaN (transparent), elsewhere adds nothing.
	{"turb-octaves-3000", `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="18"><defs><filter id="f" x="-5" y="-3" width="30" height="25" filterUnits="userSpaceOnUse">` +
		`<feTurbulence baseFrequency="1e-40" numOctaves="3000"/></filter></defs><rect width="24" height="18" fill="#345" filter="url(#f)"/></svg>`, 0.01, 0},
	{"turb-octaves-1100-tiny-scale", `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="18" viewBox="0 0 2.4e-30 1.8e-30"><defs><filter id="f" x="0" y="0" width="2.4e-30" height="1.8e-30" filterUnits="userSpaceOnUse">` +
		`<feTurbulence baseFrequency="3e-45 1e-20" numOctaves="1100" stitchTiles="stitch" type="fractalNoise"/></filter></defs><rect width="2.4e-30" height="1.8e-30" fill="#345" filter="url(#f)"/></svg>`, 0.01, 0},
	{"turb-displacement", filterSVG(`<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2" result="n"/><feDisplacementMap in="SourceGraphic" in2="n" scale="5" xChannelSelector="R" yChannelSelector="G"/>`, "", alignedShapes), 0.01, 0},
	{"turb-displacement-aa", filterSVG(`<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2" result="n"/><feDisplacementMap in="SourceGraphic" in2="n" scale="2" xChannelSelector="R" yChannelSelector="G"/>`, "", aaShapes), 0.1, 0.25},

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
// resvg would run (for days) or crash on, fails with ErrLimit; one usvg
// makes a dummy of (a kernel of the wrong length) does not.
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
		// orderX * orderY wraps a u32 to the kernel's length (zero): usvg
		// accepts it, and resvg indexes past the kernel.
		{"wraps to an empty kernel", filterSVG(`<feConvolveMatrix order="65536"/>`, "", alignedShapes), true},
		{"wraps to the kernel's length", big("65536 65537", 65536), true},
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
