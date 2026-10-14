package raster

import (
	"testing"
)

// The engine's PNG filters follow W3C Filter Effects Module Level 1 where
// resvg, the reference the PNG is otherwise tested against, departs from it
// (ADR 0015). filterDivergences lists each such departure as a case that
// must differ from resvg's render beyond the parity bound it would have
// (an exact one for aligned shapes): a divergence asserted, not a parity
// case skipped. What the specification says the case draws is asserted by
// the TestSpec* test each names (filterspec_test.go), against values
// derived from the specification, not from resvg.
type divergence struct {
	s    synth
	cite string // the specification's text the engine follows
	spec string // the test asserting the specification's result
}

// The specification's text for each divergence (Filter Effects 1 unless
// another is named).
const (
	citeTile = `§9.20: "The top/left corner of each given tile is at location (x + i*width, y + j*height), where (x,y) represents the top/left of the input image's filter primitive subregion, width and height represent the width and height of the input image's filter primitive subregion"; ` +
		`§10: color-interpolation-filters "has no effect on filter primitives like feOffset, feImage, feTile or feFlood"`
	citeMorphKernel       = `§9.17: "The dilation (or erosion) kernel is a rectangle with a width of 2*x-radius and a height of 2*y-radius"`
	citeMorphRadius       = `§9.17: "A negative or zero value disables the effect of the given filter primitive (i.e., the result is the filter input image)"; "The initial value for radius is 0"`
	citeConvPass          = `§9.9: "If the result of orderX * orderY is not equal to the the number of entries in the value list, the filter primitive acts as a pass through filter"; changes: "Make feColorMatrix and feConvolveMatrix a pass through on unfulfilled pre-conditions"`
	citeConvDivisor       = `§9.9: "If the specified divisor is 0 then the default value will be used instead"`
	citeKernelUnit        = `§9.9, §9.10: "kernelUnitLength, in combination with the other attributes, defines an implicit pixel grid in the filter effects coordinate system"; "the dx and dy values (e.g., I(x-dx,y-dy)), represent deltas ... determined by the value (explicit or implicit) of attribute kernelUnitLength"`
	citeDisplacement      = `§9.11: "P'(x,y) ← P( x + scale * (XC(x,y) - .5), y + scale * (YC(x,y) - .5))"; "The calculations using the pixel values from in2 are performed using non-premultiplied color values"; "The in source image must remain in its current color space"`
	citeTurbulence        = `§9.21: the reference code draws a gradient again "while(fGradient[k][i][0] == 0 && fGradient[k][i][1] == 0)" and "if (s > 1) { i--; // discard the current random vector; try it again"; "The filter primitive subregion is to be passed as the arguments fTileX, fTileY, fTileWidth and fTileHeight"; numOctaves = "<integer>"`
	citeLightingColor     = `§9.1: "The color space in which a particular filter primitive performs its operations is determined by the value of the property color-interpolation-filters on the given filter primitive"`
	citeSpecularExp       = `§9.19: specularExponent, "Exponent for specular term, larger is more "shiny". The initial value for specularExponent is 1." (no range; SVG 1.1's 1-128 is gone); §11.4: a spot light's "The initial value for specularExponent is 1."`
	citeLightingInherit   = `§3: "all properties defined in this specification also accept the inherit keyword as their property value"; §11.5: lighting-color "Inherited: no"`
	citeLightUnits        = `§11.3: "X location for the light source in the coordinate system established by attribute primitiveUnits on the filter element"`
	citeLightingSmall     = `§9.10: the surface normal's kernels for the corners and edges of the image need one neighbour each way`
	citeFeImagePlace      = `§9.15: "the referenced resource is rendered according to the behavior of the use element. In either case, the current user coordinate system depends on the value of attribute primitiveUnits on the filter element"`
	citeFeImageCycle      = `SVG 2 §16.1.2: "URL references that directly or indirectly reference themselves are treated as invalid circular references"; §9.15: a reference that "is non-existent, or that cannot be displayed ... fills the filter primitive subregion with transparent black"`
	citeFeImageLayer      = `§9.15: the element "is rendered according to the behavior of the use element", its own filter with the filter region it has`
	citeHueRotate         = `§9.6: the hueRotate matrix, at no precision the specification sets: f64 agrees with exact arithmetic where resvg's f32 does not`
	citeSaturate          = `§9.6: "Values outside the 0..1 range under- or oversaturates the filter input image respectively"`
	citeColorMatrixPass   = `§9.6: "If the number of entries in the values list does not match the required number of entries by the type, the filter primitive acts as a pass through filter"`
	citeLuminance         = `§9.6: the luminanceToAlpha row 0.2126 0.7152 0.0722 ("The precision of the luminance coefficients increased in comparison to previous specification texts")`
	citeLighter           = `§9.8: operator = "over | in | out | atop | xor | lighter | arithmetic", each "as described in [COMPOSITING-1]"`
	citeTransferLast      = `§9.7: "If more than one transfer function element of the same kind is specified, the last occurrence is to be used"; type "The initial value for type is identity"`
	citeBlurNegative      = `§9.14: "A negative value or a value of zero disables the effect of the given filter primitive (i.e., the result is the filter input image)"`
	citeDropShadowStd     = `§9.12: stdDeviation "The initial value for stdDeviation is 2"`
	citeSubregionUnits    = `§9.4: x, y, width and height "represent values in the coordinate system established by attribute primitiveUnits on the filter element"`
	citeSubregionDefault  = `§9.4: "x, y, width and height default to the union (i.e., tightest fitting bounding box) of the subregions defined for all referenced nodes"; "The filter primitive subregion acts as a hard clip clipping rectangle on the filter primitive result"`
	citeFilterRef         = `§5: "If the filter references a non-existent object or the referenced object is not a filter element, then the whole filter chain is ignored. No filter is applied to the object."`
	citeEmptyFilter       = `§9.3: "If a filter element has no filter primitive tree then the element the filter applies to does not get rendered."`
	citeZeroBBox          = `SVG 2 §8.11: "When the geometry of the applicable element has no width or height and objectBoundingBox is specified, then the given effect (e.g., a gradient or a filter) will be ignored."`
	citeSubregionDisabled = `§9.2: width "A negative or zero value disables the effect of the given filter primitive (i.e., the result is a transparent black image)"`
)

var filterDivergences = []divergence{
	{exact("tile-partly-outside", `<feFlood x="-10" y="-5" width="40" height="30" flood-color="lime" result="a"/><feTile in="a"/>`), citeTile, "TestSpecTile"},
	{exact("tile-linear-input", `<feComponentTransfer x="40" y="30" width="26" height="20" result="c"><feFuncR type="linear" slope="0.5"/></feComponentTransfer><feTile in="c"/>`), citeTile, "TestSpecTile"},
	{exact("morph-radius-wraps", `<feMorphology operator="erode" radius="2147483648"/>`), citeMorphKernel, "TestSpecMorphology"},
	{exact("morph-radius-wraps-dilate", `<feMorphology operator="dilate" radius="2147483648 1"/>`), citeMorphKernel, "TestSpecMorphology"},
	{exact("morph-radius-negative", `<feMorphology operator="dilate" radius="-1"/>`), citeMorphRadius, "TestSpecMorphology"},
	{exact("morph-radius-zero", `<feMorphology operator="dilate" radius="0"/>`), citeMorphRadius, "TestSpecMorphology"},
	{exact("morph-radius-one-zero", `<feMorphology operator="dilate" radius="0 3"/>`), citeMorphRadius, "TestSpecMorphology"},
	{exact("morph-radius-three", `<feMorphology operator="dilate" radius="1 2 3"/>`), citeMorphRadius, "TestSpecMorphology"},
	{exact("morph-radius-bad", `<feMorphology operator="dilate" radius="abc"/>`), citeMorphRadius, "TestSpecMorphology"},
	{exact("conv-order-bad-second", `<feConvolveMatrix order="2 abc" kernelMatrix="1 2 3 4"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-order-zero", `<feConvolveMatrix order="0" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-order-negative", `<feConvolveMatrix order="-3" kernelMatrix="1 1 1 1 1 1 1 1 1"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-target-negative", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" targetX="-1"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-target-outside", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" targetY="3"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-kernel-short", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-kernel-missing", `<feConvolveMatrix/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-kernel-bad", `<feConvolveMatrix kernelMatrix="1 1 1 1 x 1 1 1 1"/>`), citeConvPass, "TestSpecConvolvePassThrough"},
	{exact("conv-divisor-zero", `<feConvolveMatrix kernelMatrix="1 1 1 1 1 1 1 1 1" divisor="0"/>`), citeConvDivisor, "TestSpecConvolvePassThrough"},
	{exact("disp-flood", `<feFlood flood-color="#80c0ff" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="2" xChannelSelector="R" yChannelSelector="B"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-flood-translucent", `<feFlood flood-color="#c040ff" flood-opacity="0.6" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="10" xChannelSelector="R" yChannelSelector="G"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-self", `<feDisplacementMap in="SourceGraphic" in2="SourceGraphic" scale="6" xChannelSelector="G" yChannelSelector="A"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-alpha-default-channels", `<feDisplacementMap in2="SourceAlpha" scale="3"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-scale-default", `<feDisplacementMap in2="SourceGraphic"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-scale-negative", `<feDisplacementMap in2="SourceGraphic" scale="-7" xChannelSelector="B" yChannelSelector="R"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-channel-unknown", `<feDisplacementMap in2="SourceGraphic" scale="5" xChannelSelector="r" yChannelSelector="X"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-srgb", `<feDisplacementMap in2="SourceGraphic" scale="4" xChannelSelector="R" yChannelSelector="G" color-interpolation-filters="sRGB"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{exact("disp-transfer-map", `<feOffset dx="3" result="o"/><feComponentTransfer in="SourceGraphic" result="m"><feFuncR type="linear" slope="2" intercept="-0.3"/><feFuncG type="discrete" tableValues="0 0.3 0.8 1"/></feComponentTransfer><feDisplacementMap in="o" in2="m" scale="3.5" xChannelSelector="R" yChannelSelector="G"/>`), citeDisplacement, "TestSpecDisplacementMap"},
	{synth{"disp-obb", filterSVG(`<feDisplacementMap in2="SourceGraphic" scale="0.05" xChannelSelector="R" yChannelSelector="G"/>`, `primitiveUnits="objectBoundingBox"`, alignedShapes), 0.01, 0}, citeDisplacement, "TestSpecDisplacementMap"},
	{synth{"disp-aa", filterSVG(`<feFlood flood-color="#80c0ff" result="m"/><feDisplacementMap in="SourceGraphic" in2="m" scale="2" xChannelSelector="R" yChannelSelector="B"/>`, "", aaShapes), 0.1, 0.25}, citeDisplacement, "TestSpecDisplacementMap"},
	{exact("turb-turbulence", `<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-fractal", `<feTurbulence type="fractalNoise" baseFrequency="0.05" numOctaves="2" seed="1"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-stitch-fractal", `<feTurbulence type="fractalNoise" baseFrequency="0.03 0.07" numOctaves="3" seed="5" stitchTiles="stitch"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-stitch-odd", `<feTurbulence baseFrequency="0.013 0.0217" numOctaves="5" stitchTiles="stitch" type="fractalNoise" seed="3"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-stitch-turbulence", `<feTurbulence baseFrequency="0.04" numOctaves="3" stitchTiles="stitch" seed="11"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-subregion", `<feTurbulence x="30" y="20" width="90" height="70" baseFrequency="0.05" numOctaves="2" stitchTiles="stitch"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-srgb", `<feTurbulence baseFrequency="0.05" numOctaves="2" color-interpolation-filters="sRGB"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-seed-fraction", `<feTurbulence baseFrequency="0.02 0.08" numOctaves="4" seed="-7.9"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-seed-min", `<feTurbulence baseFrequency="0.03" numOctaves="2" seed="-1e10" type="fractalNoise"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-seed-max", `<feTurbulence baseFrequency="0.03" numOctaves="2" seed="1e10"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-octaves-fraction", `<feTurbulence baseFrequency="0.03" numOctaves="2.5"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-octaves-40", `<feTurbulence baseFrequency="0.05" numOctaves="40"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-frequency-high", `<feTurbulence baseFrequency="3.7" numOctaves="3"/>`), citeTurbulence, "TestSpecTurbulence"},
	{exact("turb-type-case", `<feTurbulence baseFrequency="0.05" numOctaves="2" type="FractalNoise"/>`), citeTurbulence, "TestSpecTurbulence"},
	{synth{"turb-displacement", filterSVG(`<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2" result="n"/><feDisplacementMap in="SourceGraphic" in2="n" scale="5" xChannelSelector="R" yChannelSelector="G"/>`, "", alignedShapes), 0.01, 0}, citeTurbulence, "TestSpecTurbulence"},
	{synth{"turb-displacement-aa", filterSVG(`<feTurbulence type="turbulence" baseFrequency="0.05" numOctaves="2" result="n"/><feDisplacementMap in="SourceGraphic" in2="n" scale="2" xChannelSelector="R" yChannelSelector="G"/>`, "", aaShapes), 0.1, 0.25}, citeTurbulence, "TestSpecTurbulence"},
	{synth{"light-turbulence-bump", filterSVG(`<feTurbulence type="fractalNoise" baseFrequency="0.04" numOctaves="2" result="n"/><feDiffuseLighting in="n" surfaceScale="2" lighting-color="white"><feDistantLight azimuth="225" elevation="45"/></feDiffuseLighting>`, "", aaShapes), 0.01, 0}, citeTurbulence, "TestSpecTurbulence"},
	{exact("light-diffuse-distant", `<feDiffuseLighting surfaceScale="3" diffuseConstant="1.2" lighting-color="#f80"><feDistantLight azimuth="45" elevation="50"/></feDiffuseLighting>`), citeLightingColor, "TestSpecLightingColor"},
	{exact("light-diffuse-point", `<feDiffuseLighting surfaceScale="5" lighting-color="#8cf"><fePointLight x="90" y="60" z="30"/></feDiffuseLighting>`), citeLightingColor, "TestSpecLightingColor"},
	{exact("light-specular-distant-exponent-1", `<feSpecularLighting surfaceScale="6" specularExponent="1" lighting-color="#fd0"><feDistantLight azimuth="-30" elevation="20"/></feSpecularLighting>`), citeLightingColor, "TestSpecLightingColor"},
	{exact("light-current-color", `<feDiffuseLighting surfaceScale="3" lighting-color="currentColor" color="#0c6"><feDistantLight azimuth="135" elevation="30"/></feDiffuseLighting>`), citeLightingColor, "TestSpecLightingColor"},
	{exact("light-style-color", `<feDiffuseLighting surfaceScale="3" style="lighting-color: rgba(0,100,255,0.3)"><feDistantLight azimuth="200" elevation="60"/></feDiffuseLighting>`), citeLightingColor, "TestSpecLightingColor"},
	{synth{"light-diffuse-distant-aa", filterSVG(`<feDiffuseLighting surfaceScale="3" diffuseConstant="1" lighting-color="#f80"><feDistantLight azimuth="45" elevation="50"/></feDiffuseLighting>`, "", aaShapes), 0.15, 0.6}, citeLightingColor, "TestSpecLightingColor"},
	{synth{"light-specular-point-aa", filterSVG(`<feSpecularLighting surfaceScale="3" specularConstant="1" specularExponent="20" lighting-color="#f80"><fePointLight x="100" y="40" z="40"/></feSpecularLighting>`, "", aaShapes), 0.15, 0.6}, citeLightingColor, "TestSpecLightingColor"},
	{synth{"light-specular-spot-aa", filterSVG(`<feSpecularLighting surfaceScale="3" specularConstant="1" specularExponent="20" lighting-color="#f80"><feSpotLight x="60" y="20" z="60" pointsAtX="100" pointsAtY="90" pointsAtZ="0" specularExponent="2" limitingConeAngle="40"/></feSpecularLighting>`, "", aaShapes), 0.15, 0.6}, citeLightingColor, "TestSpecLightingColor"},
	{exact("light-specular-exponent-over-128", `<feSpecularLighting specularExponent="129"><feDistantLight/></feSpecularLighting>`), citeSpecularExp, "TestSpecLightingExponent"},
	{exact("light-specular-exponent-under-1", `<feSpecularLighting specularExponent="0.5"><feDistantLight/></feSpecularLighting>`), citeSpecularExp, "TestSpecLightingExponent"},
	{synth{"light-inherit-color", filterSVG(`<feDiffuseLighting surfaceScale="3" lighting-color="inherit"><feDistantLight azimuth="135" elevation="30"/></feDiffuseLighting>`, `lighting-color="#0af"`, alignedShapes), 0.01, 0}, citeLightingInherit, "TestSpecLightingColor"},
	{synth{"light-obb-units", filterSVG(`<feDiffuseLighting surfaceScale="3"><fePointLight x="0.5" y="0.5" z="0.2"/></feDiffuseLighting>`, `primitiveUnits="objectBoundingBox"`, alignedShapes), 0.01, 0}, citeLightUnits, "TestSpecLightUnits"},
	{synth{"image-fragment", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse">` +
		`<feImage xlink:href="#shp" x="10" y="10" width="100" height="100"/></filter><circle id="shp" cx="40" cy="40" r="25" fill="orange"/></defs>` +
		`<rect width="200" height="150" fill="#345"/><g filter="url(#f)">` + aaShapes + `</g></svg>`, 0.1, 0.3}, citeFeImagePlace, "TestSpecFeImagePlacement"},
	{synth{"image-fragment-group", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse">` +
		`<feImage xlink:href="#gg" x="30" y="20"/><feOffset dx="5" dy="3"/></filter><g id="gg" transform="rotate(10)" fill="#e70"><rect x="10" y="10" width="40" height="30"/>` +
		`<circle cx="70" cy="40" r="18" fill-opacity="0.6"/><text x="10" y="80" font-family="sans-serif" font-size="20">Ab</text></g></defs>` +
		`<rect width="200" height="150" fill="#345"/><g filter="url(#f)"><rect x="40" y="30" width="60" height="50" fill="#c33"/></g></svg>`, 0.1, 0.3}, citeFeImagePlace, "TestSpecFeImagePlacement"},
	{synth{"image-fragment-merge", filterSVG(`<feImage xlink:href="#ar" x="30" y="20"/><feMerge><feMergeNode/><feMergeNode in="SourceGraphic"/></feMerge>`, "", alignedShapes+`<rect id="ar" x="2" y="4" width="20" height="16" fill="#0cf"/>`), 0.01, 0}, citeFeImagePlace, "TestSpecFeImagePlacement"},
	{synth{"image-fragment-scaled", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150" viewBox="0 0 100 75"><defs><filter id="f" x="0" y="0" width="100" height="75" filterUnits="userSpaceOnUse">` +
		`<feImage xlink:href="#cc" x="10" y="5"/></filter><rect id="cc" x="8" y="8" width="24" height="20" fill="orange"/></defs>` +
		`<rect width="100" height="75" fill="#345"/><g filter="url(#f)" transform="translate(7 4)"><rect x="20" y="15" width="30" height="25" fill="#c33"/></g></svg>`, 0.01, 0}, citeFeImagePlace, "TestSpecFeImagePlacement"},
	{synth{"image-fragment-self", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse">` +
		`<feImage xlink:href="#r"/><feOffset dx="40" dy="20"/></filter></defs><rect width="200" height="150" fill="#345"/>` +
		`<rect id="r" x="10" y="10" width="50" height="40" fill="lime" filter="url(#f)"/><rect x="120" y="70" width="60" height="60" fill="#36c" filter="url(#f)"/></svg>`, 0.01, 0}, citeFeImageCycle, "TestSpecFeImageCycle"},
	{synth{"image-fragment-filtered", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs>` +
		`<filter id="b"><feGaussianBlur stdDeviation="4"/></filter><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse"><feImage xlink:href="#e"/></filter>` +
		`<rect id="e" x="150" y="100" width="80" height="80" fill="gold" filter="url(#b)"/></defs><rect width="200" height="150" fill="#345"/>` +
		`<rect x="10" y="10" width="40" height="40" fill="#c33" filter="url(#f)"/></svg>`, 0.01, 0}, citeFeImageLayer, "TestSpecFeImagePlacement"},
	{synth{"hue-90-aa", filterSVG(`<feColorMatrix type="hueRotate" values="90"/>`, "", aaShapes), 0.1, 0.25}, citeHueRotate, "TestSpecHueRotate"},
	{exact("hue-90", `<feColorMatrix type="hueRotate" values="90"/>`), citeHueRotate, "TestSpecHueRotate"},
	{exact("hue-90-srgb", `<feColorMatrix type="hueRotate" values="90" color-interpolation-filters="sRGB"/>`), citeHueRotate, "TestSpecHueRotate"},
	{exact("hue-270", `<feColorMatrix type="hueRotate" values="270"/>`), citeHueRotate, "TestSpecHueRotate"},
	{exact("saturate-over-1", `<feColorMatrix type="saturate" values="2"/>`), citeSaturate, "TestSpecColorMatrix"},
	// Found auditing the rest of the filter code against the specification.
	{exact("tile-outside-chain", `<feFlood x="300" y="300" width="40" height="30" flood-color="lime" result="a"/><feTile in="a" result="t"/><feMerge><feMergeNode in="t"/><feMergeNode in="SourceGraphic"/></feMerge>`), citeTile, "TestSpecTile"},
	{exact("morph-radius-absent", `<feMorphology operator="dilate"/>`), citeMorphRadius, "TestSpecMorphology"},
	{exact("conv-kernel-unit-length", `<feConvolveMatrix kernelMatrix="1 0 0 0 0 0 0 0 1" kernelUnitLength="3"/>`), citeKernelUnit, "TestSpecConvolveKernelUnit"},
	{exact("light-kernel-unit-length", `<feDiffuseLighting surfaceScale="3" kernelUnitLength="4"><feDistantLight azimuth="45" elevation="40"/></feDiffuseLighting>`), citeKernelUnit, "TestSpecLightingKernelUnit"},
	{synth{"light-two-pixels", `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><defs><filter id="f" x="10" y="10" width="2" height="20" filterUnits="userSpaceOnUse">` +
		`<feDiffuseLighting><feDistantLight elevation="90"/></feDiffuseLighting></filter></defs><rect width="40" height="40" fill="#345"/><rect width="40" height="40" filter="url(#f)"/></svg>`, 0.01, 0}, citeLightingSmall, "TestSpecLightingSmall"},
	{exact("light-spot-exponent-negative", `<feDiffuseLighting surfaceScale="2" lighting-color="white"><feSpotLight x="10" y="140" z="80" pointsAtX="150" pointsAtY="30" specularExponent="-2"/></feDiffuseLighting>`), citeSpecularExp, "TestSpecLightingExponent"},
	{exact("cmat-values-wrong-count", `<feColorMatrix values="1 0 0 0 0"/>`), citeColorMatrixPass, "TestSpecColorMatrix"},
	// No background: the alpha (37, or resvg's 36) is the output's.
	{synth{"cmat-luminance", `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30"><filter id="f" color-interpolation-filters="sRGB"><feColorMatrix type="luminanceToAlpha"/></filter>` +
		`<rect x="10" y="5" width="20" height="20" fill="rgb(0,26,255)" filter="url(#f)"/></svg>`, 0.01, 0}, citeLuminance, "TestSpecColorMatrix"},
	{exact("composite-lighter", `<feFlood flood-color="#3060c0" flood-opacity="0.5" result="f"/><feComposite in="SourceGraphic" in2="f" operator="lighter"/>`), citeLighter, "TestSpecCompositeLighter"},
	{exact("transfer-last-occurrence", `<feComponentTransfer><feFuncR type="linear" slope="0"/><feFuncR type="bogus"/></feComponentTransfer>`), citeTransferLast, "TestSpecComponentTransferLast"},
	{exact("blur-negative", `<feGaussianBlur stdDeviation="-1 3"/>`), citeBlurNegative, "TestSpecBlurNegative"},
	{exact("drop-shadow-std-invalid", `<feDropShadow stdDeviation="abc" dx="4" dy="3"/>`), citeDropShadowStd, "TestSpecBlurNegative"},
	{synth{"subregion-obb", filterSVG(`<feOffset x="0.25" y="0.25" width="0.5" height="0.5" dx="0.02"/>`, `primitiveUnits="objectBoundingBox"`, alignedShapes), 0.01, 0}, citeSubregionUnits, "TestSpecSubregion"},
	{synth{"subregion-obb-blur", svgHead + `<defs><filter id="f" primitiveUnits="objectBoundingBox"><feFlood x="0.2" y="0.2" width="0.5" height="0.5" flood-color="lime"/><feGaussianBlur stdDeviation="0.05"/></filter></defs>` +
		`<rect x="10" y="10" width="100" height="80" fill="gray" filter="url(#f)"/></svg>`, 0.1, 0.25}, citeSubregionDefault, "TestSpecSubregion"},
	{exact("subregion-union-offset", `<feFlood x="40" y="30" width="30" height="25" flood-color="lime" result="a"/><feOffset in="a" dx="20" dy="10"/>`), citeSubregionDefault, "TestSpecSubregion"},
	{exact("subregion-disabled", `<feFlood width="0" flood-color="lime"/><feMerge><feMergeNode in="SourceGraphic"/></feMerge>`), citeSubregionDisabled, "TestSpecSubregion"},
	{synth{"filter-invalid-link", svgHead + `<rect width="50" height="50" fill="red" filter="url(#nope)"/><rect x="60" width="50" height="50" fill="blue"/></svg>`, 0.1, 0.25}, citeFilterRef, "TestSpecFilterReference"},
	{synth{"filter-chain-invalid-link", svgHead + `<defs><filter id="f"><feOffset dx="9" dy="6"/></filter></defs><rect x="10" y="10" width="50" height="50" fill="red" filter="url(#f) url(#nope)"/></svg>`, 0.1, 0.25}, citeFilterRef, "TestSpecFilterReference"},
	{synth{"filter-chain-empty", svgHead + `<defs><filter id="f"><feOffset dx="9" dy="6"/></filter><filter id="e"/></defs><rect x="10" y="10" width="50" height="50" fill="red" filter="url(#f) url(#e)"/></svg>`, 0.1, 0.25}, citeEmptyFilter, "TestSpecFilterReference"},
	{synth{"filter-zero-bbox", svgHead + `<defs><filter id="f"><feOffset dx="9" dy="6"/></filter></defs><path d="M10 50 H100" stroke="red" stroke-width="6" filter="url(#f)"/></svg>`, 0.1, 0.25}, citeZeroBBox, "TestSpecFilterReference"},
	{synth{"image-element-obb", filterSVG(`<feImage xlink:href="#q"/>`, `primitiveUnits="objectBoundingBox"`, alignedShapes+`<rect id="q" width="0.5" height="0.25" fill="#0cf"/>`), 0.01, 0}, citeFeImagePlace, "TestSpecFeImagePlacement"},
	{synth{"image-data-rotated", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="150"><defs><filter id="f" x="0" y="0" width="200" height="150" filterUnits="userSpaceOnUse">` +
		`<feImage xlink:href="` + feImagePNG + `" x="20" y="20" width="60" height="40" preserveAspectRatio="none" image-rendering="optimizeSpeed"/></filter></defs>` +
		`<rect width="200" height="150" fill="#345"/><g transform="rotate(30 100 75)" filter="url(#f)"><rect x="40" y="30" width="60" height="50" fill="#c33"/></g></svg>`, 0.25, 0.4}, citeFeImagePlace, "TestSpecFeImagePlacement"},
}

// A filter primitive whose specification the engine follows where resvg
// does not draws differently from resvg: beyond the parity bound of an
// aligned case at at least one scale.
func TestFilterDivergesFromResvg(t *testing.T) {
	for _, d := range filterDivergences {
		t.Run(d.s.name, func(t *testing.T) {
			if d.cite == "" || d.spec == "" {
				t.Fatal("a divergence names the specification's text and the test asserting it")
			}
			diverges := false
			for _, scale := range []float64{1, 2.5} {
				ours, err := Render([]byte(d.s.svg), Options{Scale: scale})
				if err != nil {
					t.Fatal(err)
				}
				ref := refPNG(t, []byte(d.s.svg), scale)
				if ref == nil {
					// resvg fails the render: it differs.
					diverges = true
					continue
				}
				st := compareImages(ours, ref, 24)
				t.Logf("scale %g: MAE %.3f  >24: %.3f%%  max %d", scale, st.MAE, st.PctOver, st.Max)
				if st.SizeDiff || st.MAE > d.s.maxMAE || st.PctOver > d.s.maxPct {
					diverges = true
				}
			}
			if !diverges {
				t.Errorf("agrees with resvg within MAE %.3f, %.3f%% over 24: the divergence (%s) is gone", d.s.maxMAE, d.s.maxPct, d.cite)
			}
		})
	}
}
