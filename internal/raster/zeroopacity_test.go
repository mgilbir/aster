package raster

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestZeroOpacityLeavesAreNotDrawn checks that shapes and text at opacity 0
// (as Vega emits the labels it could not place) cost no pixel work and leave
// the image as the document without them, over a backdrop and in any blend
// mode; at opacity 0.5 the same elements are drawn.
func TestZeroOpacityLeavesAreNotDrawn(t *testing.T) {
	leaves := []string{
		`<text x="5" y="40" font-size="30" fill="#c33" stroke="#13c" stroke-width="2" OP>Hgy Ölç</text>`,
		`<rect x="10" y="10" width="50" height="40" fill="#0a0" stroke="black" stroke-width="3" OP/>`,
		`<circle cx="60" cy="50" r="30" fill="url(#g)" OP/>`,
		`<path d="M0,0L90,80Q20,90 10,40Z" fill="#888" stroke="#f0f" stroke-dasharray="3 2" OP/>`,
		`<polyline points="0,0 30,80 60,10" fill="none" stroke="blue" stroke-width="5" OP/>`,
		`<line x1="0" y1="90" x2="100" y2="0" stroke="green" stroke-width="7" OP/>`,
		`<ellipse cx="50" cy="50" rx="40" ry="10" fill="yellow" clip-path="url(#c)" OP/>`,
		`<polygon points="5,5 95,5 50,95" fill="#123" OP/>`,
	}
	doc := func(body string) []byte {
		return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><defs>` +
			`<linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>` +
			`<clipPath id="c"><rect width="70" height="70"/></clipPath></defs>` +
			`<rect width="100" height="100" fill="#4af" fill-opacity="0.6"/>` + body + `<circle cx="80" cy="80" r="10"/></svg>`)
	}
	render := func(svg []byte) ([]byte, int) {
		ops := -1
		ctx := WithWorkReport(context.Background(), func(w Work) { ops = w.Ops })
		img, err := Render(svg, Options{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		return img.Pix, ops
	}
	base, baseOps := render(doc(""))
	if baseOps <= 0 {
		t.Fatalf("no work reported: %d", baseOps)
	}
	for _, l := range leaves {
		for _, op := range []string{`opacity="0"`, `opacity="0%"`, `style="opacity:0"`, `opacity="0" style="mix-blend-mode:difference"`, `opacity="0" style="mix-blend-mode:multiply"`} {
			svg := doc(strings.Replace(l, "OP", op, 1))
			pix, ops := render(svg)
			if !bytes.Equal(pix, base) {
				t.Errorf("%s: drawn", svg)
			}
			if ops != baseOps {
				t.Errorf("%s: %d pixel operations, %d without it", svg, ops, baseOps)
			}
		}
		svg := doc(strings.Replace(l, "OP", `opacity="0.5"`, 1))
		if pix, ops := render(svg); bytes.Equal(pix, base) || ops <= baseOps {
			t.Errorf("%s: not drawn at opacity 0.5 (%d pixel operations, %d without it)", svg, ops, baseOps)
		}
	}
}
