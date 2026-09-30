package raster

import (
	"testing"
)

var fuzzLimits = Limits{MaxPixels: 1 << 18, MaxDimension: 2048, MaxElements: 5000, MaxRenderNodes: 20000, MaxInputBytes: 1 << 20}

func FuzzRender(f *testing.F) {
	for _, s := range synths {
		f.Add(s.svg)
	}
	f.Add(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><path d="M0 0L10 10" stroke="red"/></svg>`)
	f.Fuzz(func(t *testing.T, svg string) {
		img, err := Render([]byte(svg), Options{Limits: fuzzLimits})
		if err == nil && img == nil {
			t.Fatal("nil image without error")
		}
	})
}

func FuzzParseDocument(f *testing.F) {
	for _, s := range synths {
		f.Add(s.svg)
	}
	f.Add(`<!DOCTYPE svg [<!ENTITY a "b">]><svg><text>&a;&#x41;</text></svg>`)
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseDocument(s, fuzzLimits.withDefaults())
	})
}

func FuzzPathData(f *testing.F) {
	f.Add("M10 10 L20 20 C1 2 3 4 5 6 A5 5 0 1 0 10 0 z")
	f.Add("m1e3 -1e-3 .5.5.5 q1 1 2 2t3 3s1 1 2 2a1 1 0 10 5 5")
	f.Fuzz(func(t *testing.T, d string) {
		var p path
		parsePathData(d, &p)
		var fl flat
		fl.flatten(&p, identity, 0.1)
		var st stroker
		var out flat
		st.stroke(&fl, strokeStyle{width: 3, join: joinRound, cap: capRound, miterLimit: 4, dash: []float64{2, 1}}, 1, &out)
	})
}

func FuzzColorAndTransform(f *testing.F) {
	f.Add("rgb(1,2,3)", "translate(1 2) rotate(30)")
	f.Fuzz(func(t *testing.T, c, tf string) {
		parseColor(c)
		parseTransform(tf)
		parsePaint(c)
		parseFontFamilies(c)
	})
}
