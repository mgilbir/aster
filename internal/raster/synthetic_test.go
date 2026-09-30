package raster

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="120" height="100" viewBox="0 0 120 100">`

// tinyPNG is a 4x3 RGBA PNG with distinct colours and some transparency.
var tinyPNG = func() string {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(60 * x), uint8(90 * y), uint8(255 - 60*x), uint8(255 - 40*y)})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}()

type synth struct {
	name   string
	svg    string
	maxMAE float64 // per-channel mean absolute error (0..255)
	maxPct float64 // % of pixels differing by > 24
}

var synths = []synth{
	{"fill-rect", svgHead + `<rect x="10.3" y="10.7" width="50.5" height="30.2" fill="#3366cc"/></svg>`, 0.3, 0.3},
	{"rounded-rect", svgHead + `<rect x="10" y="10" width="80" height="50" rx="12" ry="8" fill="orange" stroke="black" stroke-width="3"/></svg>`, 0.5, 1},
	{"circle-ellipse", svgHead + `<circle cx="40" cy="40" r="25" fill="green"/><ellipse cx="80" cy="60" rx="30" ry="15" fill="none" stroke="purple" stroke-width="4"/></svg>`, 0.5, 1},
	{"paths-arcs-rel", svgHead + `<path d="M10 80 a20 15 30 1 0 40 0 a20 15 -30 0 1 -40 0z m 60 0 q 10 -30 30 0 t 15 0 s 10 10 15 -10 c1 2 3 4 5 5 h5 v-5 l-3 -3" fill="red" stroke="blue" stroke-width="2"/></svg>`, 0.6, 1.5},
	{"path-implicit-repeat", svgHead + `<path d="M10,10 20,10 20,20 10,20z M40 40 l10 0 0 10 -10 0z" fill="teal"/></svg>`, 0.3, 0.3},
	{"evenodd", svgHead + `<path fill-rule="evenodd" d="M10 10h80v80h-80z M30 30h40v40h-40z" fill="crimson"/><path fill-rule="nonzero" d="M95 10h20v20h-20z M100 15h10v10h-10z" fill="navy"/></svg>`, 0.3, 0.5},
	{"stroke-joins", svgHead + `<g fill="none" stroke="black" stroke-width="8"><path d="M10 40 L30 10 L50 40" stroke-linejoin="miter"/><path d="M60 40 L80 10 L100 40" stroke-linejoin="round"/><path d="M10 90 L30 60 L50 90" stroke-linejoin="bevel"/><path d="M60 90 L80 60 L100 90" stroke-linejoin="miter" stroke-miterlimit="1.2"/></g></svg>`, 1.0, 1.5},
	{"stroke-caps", svgHead + `<g stroke="black" stroke-width="10" fill="none"><path d="M15 15h60" stroke-linecap="butt"/><path d="M15 40h60" stroke-linecap="round"/><path d="M15 65h60" stroke-linecap="square"/><path d="M95 20v0" stroke-linecap="round"/><path d="M95 50v0" stroke-linecap="square"/></g></svg>`, 0.8, 1},
	{"dashes", svgHead + `<g fill="none" stroke="black" stroke-width="3"><path d="M5 10H115" stroke-dasharray="10 5"/><path d="M5 25H115" stroke-dasharray="10 5 2" stroke-dashoffset="4"/><circle cx="30" cy="60" r="20" stroke-dasharray="6 4"/><rect x="65" y="45" width="40" height="30" stroke-dasharray="8" stroke-linecap="round"/></g></svg>`, 1.0, 1.5},
	{"transforms", svgHead + `<g transform="translate(60 50) rotate(30) scale(1.2 0.8) skewX(10)"><rect x="-20" y="-15" width="40" height="30" fill="steelblue" stroke="black" stroke-width="2"/></g><g transform="matrix(1 0 0 1 5 5)"><circle r="4" fill="red"/></g></svg>`, 0.6, 1},
	{"opacity-group", svgHead + `<g opacity="0.5"><rect x="10" y="10" width="60" height="60" fill="red"/><rect x="40" y="30" width="60" height="60" fill="blue"/></g><rect x="80" y="5" width="30" height="30" fill="green" stroke="black" stroke-width="6" opacity="0.4"/></svg>`, 0.4, 0.5},
	{"fill-stroke-opacity", svgHead + `<rect x="10" y="10" width="60" height="60" fill="red" stroke="blue" stroke-width="10" fill-opacity="0.5" stroke-opacity="0.5"/></svg>`, 0.4, 0.5},
	{"linear-gradient", svgHead + `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="red"/><stop offset="0.5" stop-color="yellow" stop-opacity="0.5"/><stop offset="1" stop-color="blue"/></linearGradient></defs><rect x="5" y="5" width="110" height="40" fill="url(#g)"/></svg>`, 0.6, 1},
	{"gradient-userspace-transform", svgHead + `<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="10" y1="0" x2="60" y2="0" gradientTransform="rotate(20)" spreadMethod="reflect"><stop offset="0" stop-color="#f00"/><stop offset="1" stop-color="#00f"/></linearGradient></defs><rect x="5" y="5" width="110" height="90" fill="url(#g)"/></svg>`, 1.0, 2},
	{"radial-gradient", svgHead + `<defs><radialGradient id="r" cx="0.5" cy="0.5" r="0.5" fx="0.3" fy="0.3"><stop offset="0" stop-color="white"/><stop offset="1" stop-color="black"/></radialGradient></defs><circle cx="60" cy="50" r="40" fill="url(#r)"/></svg>`, 1.0, 2},
	{"clip-path", svgHead + `<defs><clipPath id="c"><circle cx="50" cy="50" r="30"/></clipPath><clipPath id="d"><rect x="60" y="0" width="60" height="50"/></clipPath></defs><rect width="100" height="100" fill="orange" clip-path="url(#c)"/><g clip-path="url(#d)"><rect width="120" height="100" fill="purple"/></g></svg>`, 0.6, 1},
	{"use-symbol", svgHead + `<defs><g id="s"><rect width="10" height="10" fill="red"/><circle cx="10" cy="10" r="5" fill="blue"/></g><symbol id="y" viewBox="0 0 10 10" width="30" height="30"><rect width="10" height="10" fill="green"/><circle cx="5" cy="5" r="3" fill="white"/></symbol></defs><use href="#s" x="10" y="10"/><use xlink:href="#s" transform="translate(50 50) scale(2)"/><use href="#y" x="80" y="5"/></svg>`, 0.5, 1},
	{"nested-svg", svgHead + `<svg x="10" y="10" width="50" height="50" viewBox="0 0 100 100"><rect width="100" height="100" fill="skyblue"/><circle cx="100" cy="100" r="60" fill="tomato"/></svg></svg>`, 0.5, 1},
	{"preserve-aspect", `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="60" viewBox="0 0 30 30"><rect width="30" height="30" fill="none" stroke="red" stroke-width="2"/><circle cx="15" cy="15" r="10" fill="blue"/></svg>`, 0.5, 1},
	{"polygons-lines", svgHead + `<polygon points="10,10 50,10 30,40" fill="gold" stroke="black"/><polyline points="60,10 80,40 100,10" fill="none" stroke="red" stroke-width="3"/><line x1="10" y1="60" x2="110" y2="90" stroke="green" stroke-width="2.5"/></svg>`, 0.5, 1},
	{"colors", svgHead + `<rect width="20" height="20" fill="rgb(255,0,128)"/><rect x="20" width="20" height="20" fill="rgba(0,0,255,0.5)"/><rect x="40" width="20" height="20" fill="hsl(120,100%,25%)"/><rect x="60" width="20" height="20" fill="#0f08"/><rect x="80" width="20" height="20" style="fill:currentColor;color:teal"/><rect x="100" width="20" height="20" fill="rgb(10%,50%,90%)"/></svg>`, 0.3, 0.3},
	{"style-precedence", svgHead + `<rect width="50" height="50" fill="red" style="fill:blue;stroke:black;stroke-width:4"/><g fill="green" stroke="none"><rect x="60" width="50" height="50"/></g></svg>`, 0.3, 0.5},
	{"display-visibility", svgHead + `<rect width="40" height="40" fill="red" display="none"/><g visibility="hidden"><rect x="50" width="40" height="40" fill="blue"/><rect x="0" y="50" width="40" height="40" fill="green" visibility="visible"/></g></svg>`, 0.3, 0.5},
	{"text-basic", svgHead + `<text x="10" y="30" font-family="sans-serif" font-size="16">Hello, World 123</text><text x="10" y="60" font-family="serif" font-size="14" font-style="italic" font-weight="bold" fill="maroon">Serif Bold Italic</text><text x="10" y="85" font-family="monospace" font-size="12">mono 0123</text></svg>`, 0.8, 2},
	{"text-anchor", svgHead + `<line x1="60" y1="0" x2="60" y2="100" stroke="#ccc"/><text x="60" y="20" text-anchor="start" font-family="sans-serif" font-size="12">Start</text><text x="60" y="45" text-anchor="middle" font-family="sans-serif" font-size="12">Middle text</text><text x="60" y="70" text-anchor="end" font-family="sans-serif" font-size="12">End anchor</text></svg>`, 0.8, 2},
	{"text-tspan", svgHead + `<text x="10" y="20" font-family="sans-serif" font-size="12">Line <tspan font-weight="bold" fill="red">bold red</tspan> tail<tspan x="10" dy="16">Second line</tspan><tspan dx="5" dy="-4">shifted</tspan></text></svg>`, 0.8, 2},
	{"text-transform-stroke", svgHead + `<text transform="translate(20,80) rotate(-30)" font-family="sans-serif" font-size="20" fill="none" stroke="black" stroke-width="0.8">Rotated</text><text x="10" y="20" font-size="14" font-family="sans-serif" fill="blue" stroke="red" stroke-width="0.5">A&amp;B &lt;x&gt; &#169;</text></svg>`, 1.0, 2},
	{"text-whitespace", svgHead + `<text x="10" y="20" font-family="sans-serif" font-size="12">  many   spaces
	and newline  </text><text x="10" y="50" font-family="sans-serif" font-size="12" xml:space="preserve">a   b</text></svg>`, 0.8, 2},
	{"image-data", svgHead + `<image x="10" y="10" width="40" height="40" href="data:image/png;base64,` + tinyPNG + `" image-rendering="optimizeSpeed"/><image x="60" y="10" width="50" height="30" preserveAspectRatio="xMidYMid meet" xlink:href="data:image/png;base64,` + tinyPNG + `"/></svg>`, 2.5, 6},
	{"mix-blend", svgHead + `<rect width="120" height="100" fill="#ddd"/><circle cx="45" cy="50" r="30" fill="red"/><circle cx="75" cy="50" r="30" fill="blue" style="mix-blend-mode:multiply"/></svg>`, 1.0, 2},
	{"scale-large-coords", svgHead + `<path d="M-1e5 50 L1e5 50" stroke="red" stroke-width="5"/><rect x="-1e6" y="-1e6" width="2e6" height="2e6" fill="none" stroke="blue" stroke-width="3"/></svg>`, 0.5, 1},
}

func TestSynthetic(t *testing.T) { runSynths(t, "syn-", synths) }

// runSynths renders each case at scale 1 and 2.5 and compares with resvg.
func runSynths(t *testing.T, prefix string, list []synth) {
	dump := os.Getenv("RASTER_DUMP")
	for _, s := range list {
		t.Run(s.name, func(t *testing.T) {
			for _, scale := range []float64{1, 2.5} {
				ours, err := Render([]byte(s.svg), Options{Scale: scale})
				if err != nil {
					t.Fatal(err)
				}
				ref := refPNG(t, []byte(s.svg), scale)
				if ref == nil {
					t.Fatal("resvg failed")
				}
				st := compareImages(ours, ref, 24)
				t.Logf("scale %g: MAE %.3f  >24: %.3f%%  >64: %.3f%%  PSNR %.1f max %d", scale, st.MAE, st.PctOver, st.PctBig, st.PSNR, st.Max)
				if st.SizeDiff {
					t.Fatalf("size mismatch %v vs %v", ours.Rect, ref.Rect)
				}
				if st.MAE > s.maxMAE || st.PctOver > s.maxPct {
					t.Errorf("scale %g: MAE %.3f (max %.3f), >24 %.3f%% (max %.3f%%)", scale, st.MAE, s.maxMAE, st.PctOver, s.maxPct)
					if dump != "" {
						_ = os.MkdirAll(dump, 0o755)
						writeDiff(filepath.Join(dump, prefix+s.name+".png"), ours, ref)
					}
				}
			}
		})
	}
}
