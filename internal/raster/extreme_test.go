package raster

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
)

// extremeNumbers are the values that turn float arithmetic into NaN, an
// infinity or a subnormal: a number derived from them and converted to an
// int, to index a table or to size a loop, is implementation-defined in Go
// (and differs between amd64 and arm64), and as an index it panics. Every
// numeric attribute of the features below takes each of them; a render must
// end with an image or an error, never an internal error (C61, X1).
var extremeNumbers = []string{"NaN", "inf", "-inf", "0", "-0", "1e-160", "-1e-160", "1e-320", "1e-45", "1e38", "-1e38", "1e300", "-1e300", "3e9", "-3e9"}

func extremeTemplates(t *testing.T) []string {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	grad := `<stop offset="0" stop-color="red"/><stop offset="{v}" stop-color="lime" stop-opacity="{v}"/><stop offset="1" stop-color="blue"/>`
	return []string{
		`<defs><linearGradient id="g" x1="{v}" x2="{v}" y2="{v}" spreadMethod="repeat">` + grad + `</linearGradient></defs><rect width="20" height="20" fill="url(#g)"/>`,
		`<defs><linearGradient id="g" x2="{v}" spreadMethod="reflect">` + grad + `</linearGradient></defs><rect width="20" height="20" fill="url(#g)"/>`,
		`<defs><linearGradient id="g" x2="{v}">` + grad + `</linearGradient></defs><rect width="20" height="20" fill="url(#g)"/>`,
		`<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x2="{v}" gradientTransform="scale({v})" spreadMethod="repeat">` + grad + `</linearGradient></defs><rect width="20" height="20" fill="url(#g)"/>`,
		`<defs><radialGradient id="g" r="{v}" fx="{v}" spreadMethod="repeat">` + grad + `</radialGradient></defs><rect width="20" height="20" fill="url(#g)"/>`,
		`<defs><radialGradient id="g" cx="{v}" cy="{v}" spreadMethod="reflect">` + grad + `</radialGradient></defs><rect width="20" height="20" fill="url(#g)" stroke="url(#g)" stroke-width="{v}"/>`,
		`<image width="{v}" height="{v}" href="` + img + `"/>`,
		`<image width="{v}" height="{v}" transform="rotate(30)" href="` + img + `"/>`,
		`<image x="{v}" y="{v}" width="20" height="{v}" preserveAspectRatio="xMidYMid slice" href="` + img + `"/>`,
		`<g transform="matrix({v} 0 0 {v} 1 1)"><image width="20" height="20" href="` + img + `"/></g>`,
		`<path d="M0 0C{v} 0 0 {v} 10 10Q{v} 5 20 0A{v} {v} {v} 1 1 5 5z" fill="red" stroke="blue" stroke-width="{v}" stroke-linejoin="round" stroke-dasharray="{v} 1"/>`,
		`<path d="M1 1L19 19L1 19" fill="none" stroke="red" stroke-width="{v}" stroke-miterlimit="{v}" stroke-linecap="square"/>`,
		`<circle cx="10" cy="10" r="{v}" stroke="red" stroke-width="2"/><ellipse rx="{v}" ry="5"/><rect width="5" height="5" rx="{v}"/>`,
		`<defs><filter id="f" x="{v}" width="{v}"><feGaussianBlur stdDeviation="{v}"/><feOffset dx="{v}" dy="{v}"/><feDropShadow stdDeviation="{v}" dx="{v}"/></filter></defs><rect width="20" height="20" filter="url(#f)"/>`,
		`<defs><filter id="f"><feComponentTransfer><feFuncR type="table" tableValues="{v} 1"/><feFuncG type="discrete" tableValues="0 {v}"/><feFuncB type="gamma" exponent="{v}" amplitude="{v}"/></feComponentTransfer><feColorMatrix type="hueRotate" values="{v}"/><feComposite operator="arithmetic" k1="{v}" k4="{v}"/></filter></defs><rect width="20" height="20" fill="red" filter="url(#f)"/>`,
		`<defs><pattern id="p" width="{v}" height="{v}" patternUnits="userSpaceOnUse" patternTransform="scale({v})"><rect width="3" height="3"/></pattern></defs><rect width="20" height="20" fill="url(#p)"/>`,
		`<defs><marker id="m" markerWidth="{v}" markerHeight="{v}" refX="{v}" orient="{v}"><rect width="2" height="2"/></marker></defs><path d="M1 1L10 10L19 1" stroke="red" marker-start="url(#m)" marker-mid="url(#m)"/>`,
		`<defs><mask id="m" x="{v}" width="{v}"><rect width="20" height="20" fill="white"/></mask></defs><rect width="20" height="20" mask="url(#m)"/>`,
		`<defs><clipPath id="c" clipPathUnits="objectBoundingBox"><rect width="{v}" height="0.5"/></clipPath></defs><rect width="20" height="20" clip-path="url(#c)"/>`,
		`<text x="{v}" y="10" font-size="{v}" letter-spacing="{v}" textLength="{v}" rotate="{v}">Ab</text>`,
		`<text y="10" font-size="12" stroke="red" stroke-width="{v}" text-decoration="underline">Ab<tspan dy="{v}" baseline-shift="{v}">c</tspan></text>`,
		`<g opacity="{v}"><rect width="20" height="20" fill="red" fill-opacity="{v}"/></g>`,
		`<svg x="{v}" width="{v}" height="10" viewBox="0 0 {v} {v}"><rect width="20" height="20"/></svg>`,
		`<g transform="matrix({v} 0 0 1 0 0)"><path d="M0 0L1e9 10L-1e9 20L1e9 1e9z" stroke="red"/></g>`,
		`<g transform="matrix(1 0 {v} 1e300 0 0)"><path d="M0 0L1e9 10L-1e9 20L1e9 -1e9z"/><circle r="1e9" fill="url(#none) red"/></g>`,
		`<g transform="matrix({v} {v} {v} {v} 1 1)"><path d="M0 0L1e9 10L-1e9 20L1e9 1e9z" stroke="red" stroke-linejoin="round"/><text>x</text></g>`,
	}
}

func TestExtremeNumbersNeverPanic(t *testing.T) {
	for i, tmpl := range extremeTemplates(t) {
		for _, v := range extremeNumbers {
			svg := `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20">` + strings.ReplaceAll(tmpl, "{v}", v) + `</svg>`
			_, err := Render([]byte(svg), Options{Limits: Limits{MaxPixelOps: 1 << 24}})
			if err != nil && strings.Contains(err.Error(), "internal error") {
				t.Errorf("template %d with %s: %v", i, v, err)
			}
		}
	}
}

// Path data is drawn up to its first error (SVG 2, "Error handling in path
// data"), and a number that is not one, as an upstream SVG writes "NaN"
// where a value is null, is an error like any other (X12).
func TestPathDataUpToFirstError(t *testing.T) {
	for _, d := range []string{"M0 0L10 0L10 10L0 NaN Z", "M0 0L10 0L10 10L0 inf Z", "M0 0L10 0L10 10L0 1e999 Z", "M0 0L10 0L10 10L NaN 0Z"} {
		var p path
		parsePathData(d, &p)
		if len(p.pts) != 3 || p.verbs[len(p.verbs)-1] == vClose {
			t.Errorf("%q: %d points, verbs %v; want the three before the error", d, len(p.pts), p.verbs)
		}
	}
}
