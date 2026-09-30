package raster

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/text"
)

// Untrusted-input tests for the CSS, mask, filter, pattern and marker paths:
// every case must finish quickly, without panicking, either rendering or
// returning an error.

const secHead = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="200" viewBox="0 0 200 200">`

func mustFinish(t *testing.T, name, svg string, lim Limits, max time.Duration) error {
	t.Helper()
	start := time.Now()
	_, err := Render([]byte(svg), Options{Limits: lim})
	if d := time.Since(start); d > max {
		t.Errorf("%s: took %v (limit %v)", name, d, max)
	}
	return err
}

func TestCSSSelectorBacktracking(t *testing.T) {
	// A descendant selector that almost matches on a deep chain would take
	// exponential time without the step budget.
	var b strings.Builder
	b.WriteString(secHead + `<style>` + strings.Repeat("g ", 30) + `rect.never { fill: red } ` + strings.Repeat("g ", 30) + `rect { fill: blue }</style>`)
	for i := 0; i < 200; i++ {
		b.WriteString("<g>")
	}
	b.WriteString(`<rect width="5" height="5"/>`)
	for i := 0; i < 200; i++ {
		b.WriteString("</g>")
	}
	b.WriteString("</svg>")
	if err := mustFinish(t, "backtracking", b.String(), Limits{MaxCSSWork: 2_000_000}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCSSManyRulesManyElements(t *testing.T) {
	var b strings.Builder
	b.WriteString(secHead + `<style>`)
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "g > rect.c%d, [data-a%d] { fill: red }\n", i, i)
	}
	b.WriteString(`* { stroke: black }</style>`)
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, `<g><rect class="c%d" width="1" height="1"/></g>`, i)
	}
	b.WriteString("</svg>")
	if err := mustFinish(t, "rules x elements", b.String(), Limits{MaxCSSRules: 30000, MaxCSSWork: 5_000_000}, 8*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestSelfReferencingEffects(t *testing.T) {
	cases := map[string]string{
		"mask-self": `<defs><mask id="m"><rect width="200" height="200" fill="white" mask="url(#m)"/></mask></defs><rect width="100" height="100" mask="url(#m)"/>`,
		"mask-pair": `<defs><mask id="a" mask="url(#b)"><rect width="100" height="100" fill="white"/></mask><mask id="b" mask="url(#a)"><rect width="100" height="100" fill="white"/></mask></defs><rect width="100" height="100" mask="url(#a)"/>`,
		"mask-chain-deep": func() string {
			var b strings.Builder
			b.WriteString(`<defs>`)
			for i := 0; i < 300; i++ {
				fmt.Fprintf(&b, `<mask id="m%d" mask="url(#m%d)"><rect width="10" height="10" fill="white"/></mask>`, i, i+1)
			}
			b.WriteString(`</defs><rect width="100" height="100" mask="url(#m0)"/>`)
			return b.String()
		}(),
		"pattern-self":     `<defs><pattern id="p" width="10" height="10" patternUnits="userSpaceOnUse"><rect width="10" height="10" fill="url(#p)"/></pattern></defs><rect width="100" height="100" fill="url(#p)"/>`,
		"pattern-pair":     `<defs><pattern id="a" width="10" height="10" patternUnits="userSpaceOnUse"><rect width="10" height="10" fill="url(#b)"/></pattern><pattern id="b" width="10" height="10" patternUnits="userSpaceOnUse"><circle r="5" fill="url(#a)" stroke="url(#a)"/></pattern></defs><rect width="100" height="100" fill="url(#a)"/>`,
		"marker-self":      `<defs><marker id="m" markerWidth="5" markerHeight="5"><path d="M0 0L5 5" stroke="black" marker-start="url(#m)" marker-mid="url(#m)" marker-end="url(#m)"/></marker></defs><path d="M10 10L50 50L90 10" stroke="black" marker-start="url(#m)" marker-mid="url(#m)" marker-end="url(#m)"/>`,
		"marker-pair":      `<defs><marker id="a" markerWidth="5" markerHeight="5"><path d="M0 0L5 5L0 5" marker-mid="url(#b)"/></marker><marker id="b" markerWidth="5" markerHeight="5"><path d="M0 0L5 5L0 5" marker-mid="url(#a)"/></marker></defs><path d="M10 10L50 50L90 10L99 99" marker-mid="url(#a)"/>`,
		"clip-self":        `<defs><clipPath id="c" clip-path="url(#c)"><rect width="50" height="50"/></clipPath></defs><rect width="100" height="100" clip-path="url(#c)"/>`,
		"filter-href-loop": `<defs><filter id="f" href="#g"/><filter id="g" href="#f"/></defs><rect width="100" height="100" filter="url(#f)"/>`,
		"use-mask":         `<defs><mask id="m"><use href="#r"/></mask><rect id="r" width="20" height="20" mask="url(#m)" fill="white"/></defs><use href="#r" mask="url(#m)"/>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_ = mustFinish(t, name, secHead+body+"</svg>", Limits{}, 5*time.Second)
		})
	}
}

func TestFilterLimits(t *testing.T) {
	cases := map[string]string{
		"huge-region":  `<filter id="f" x="-1e9" y="-1e9" width="2e9" height="2e9"><feGaussianBlur stdDeviation="3"/></filter>`,
		"huge-values":  `<filter id="f" x="-1e300" width="1e300" height="1e300"><feOffset dx="1e300" dy="-1e300"/></filter>`,
		"huge-sigma":   `<filter id="f"><feGaussianBlur stdDeviation="1e30 1e30"/></filter>`,
		"nan-sigma":    `<filter id="f"><feGaussianBlur stdDeviation="nan"/><feOffset dx="inf"/></filter>`,
		"negative":     `<filter id="f" width="-5"><feGaussianBlur stdDeviation="-3"/></filter>`,
		"empty":        `<filter id="f"/>`,
		"bad-matrix":   `<filter id="f"><feColorMatrix type="matrix" values="1 2 3"/><feColorMatrix type="saturate" values="x"/></filter>`,
		"bad-transfer": `<filter id="f"><feComponentTransfer><feFuncR type="table" tableValues=""/><feFuncG type="discrete"/><feFuncB type="gamma" exponent="1e300"/></feComponentTransfer></filter>`,
		"bad-refs":     `<filter id="f"><feMerge><feMergeNode in="nope"/><feMergeNode/></feMerge><feComposite in2="result9" operator="arithmetic" k1="1e30"/></filter>`,
		"many-prims": func() string {
			return `<filter id="f">` + strings.Repeat(`<feGaussianBlur stdDeviation="2"/>`, 500) + `</filter>`
		}(),
	}
	for name, def := range cases {
		t.Run(name, func(t *testing.T) {
			svg := secHead + `<defs>` + def + `</defs><rect x="20" y="20" width="60" height="60" fill="red" filter="url(#f)"/></svg>`
			_ = mustFinish(t, name, svg, Limits{}, 10*time.Second)
		})
	}
	// The pixel budget rejects filter storms.
	var b strings.Builder
	b.WriteString(secHead + `<defs><filter id="f" x="0" y="0" width="1" height="1"><feGaussianBlur stdDeviation="2"/></filter></defs>`)
	for i := 0; i < 2000; i++ {
		b.WriteString(`<rect width="150" height="150" filter="url(#f)"/>`)
	}
	b.WriteString("</svg>")
	err := mustFinish(t, "filter storm", b.String(), Limits{MaxEffectPixels: 3_000_000}, 20*time.Second)
	if !errors.Is(err, errLimit) {
		t.Errorf("filter storm: want resource limit error, got %v", err)
	}
	// A filter region over MaxFilterPixels is skipped rather than allocated.
	if err := mustFinish(t, "region cap", secHead+`<defs><filter id="f" filterUnits="userSpaceOnUse" x="-1000" y="-1000" width="3000" height="3000"><feGaussianBlur stdDeviation="3"/></filter></defs><rect width="10" height="10" filter="url(#f)"/></svg>`, Limits{MaxFilterPixels: 1000}, time.Second); err != nil {
		t.Errorf("region cap: %v", err)
	}
}

func TestPatternLimits(t *testing.T) {
	huge := secHead + `<defs><pattern id="p" width="1e9" height="1e9" patternUnits="userSpaceOnUse"><rect width="10" height="10"/></pattern></defs><rect width="100" height="100" fill="url(#p)"/></svg>`
	if err := mustFinish(t, "huge tile", huge, Limits{}, 2*time.Second); err != nil {
		t.Errorf("huge tile: %v", err)
	}
	tiny := secHead + `<defs><pattern id="p" width="1e-9" height="1e-9" patternUnits="userSpaceOnUse"><rect width="10" height="10"/></pattern></defs><rect width="100" height="100" fill="url(#p)"/></svg>`
	if err := mustFinish(t, "tiny tile", tiny, Limits{}, 2*time.Second); err != nil {
		t.Errorf("tiny tile: %v", err)
	}
	// A pattern per element (objectBoundingBox) is charged to the pixel budget.
	var b strings.Builder
	b.WriteString(secHead + `<defs><pattern id="p" width="1" height="1"><rect width="1" height="1"/></pattern></defs>`)
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, `<rect x="%d" width="%d" height="100" fill="url(#p)"/>`, i%50, 100+i)
	}
	b.WriteString("</svg>")
	err := mustFinish(t, "pattern storm", b.String(), Limits{MaxEffectPixels: 1_000_000}, 10*time.Second)
	if !errors.Is(err, errLimit) {
		t.Errorf("pattern storm: want resource limit error, got %v", err)
	}
}

func TestMarkerBomb(t *testing.T) {
	var d strings.Builder
	d.WriteString("M0 0")
	for i := 1; i < 20000; i++ {
		fmt.Fprintf(&d, " L%d %d", i%190, (i*7)%190)
	}
	svg := secHead + `<defs><marker id="m" markerWidth="4" markerHeight="4">` + strings.Repeat(`<circle r="1"/>`, 50) + `</marker></defs><path d="` + d.String() + `" stroke="black" marker-mid="url(#m)"/></svg>`
	err := mustFinish(t, "marker bomb", svg, Limits{MaxRenderNodes: 100_000}, 10*time.Second)
	if !errors.Is(err, errLimit) {
		t.Errorf("marker bomb: want resource limit error, got %v", err)
	}
}

func TestTextEdgeCases(t *testing.T) {
	cases := map[string]string{
		"textLength-huge":  `<text x="10" y="50" textLength="1e300" lengthAdjust="spacing">abc</text><text x="10" y="90" textLength="1e300" lengthAdjust="spacingAndGlyphs">abc</text>`,
		"textLength-zero":  `<text x="10" y="50" textLength="0" lengthAdjust="spacingAndGlyphs">abc</text>`,
		"textLength-neg":   `<text x="10" y="50" textLength="-4">abc</text>`,
		"rotate-huge":      `<text x="10" y="50" rotate="1e300 -1e300 nan inf">abcdef</text>`,
		"rotate-empty":     `<text x="10" y="50" rotate="">abc</text>`,
		"rotate-many":      `<text x="10" y="50" rotate="` + strings.Repeat("10 ", 5000) + `">abc</text>`,
		"decoration-empty": `<text x="10" y="50" text-decoration="underline overline line-through"></text><text text-decoration="underline"> </text>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_ = mustFinish(t, name, secHead+body+"</svg>", Limits{}, 5*time.Second)
		})
	}
}

func TestShaperWithOptions(t *testing.T) {
	// A custom default family flows through to the shaper.
	sh, err := NewShaperWithOptions(text.WithDefaultFontFamily("Liberation Mono"))
	if err != nil {
		t.Fatal(err)
	}
	glyphs := sh.Shape("iiii", FontRequest{Families: []string{"no-such-family"}, Weight: 400}, 10)
	if len(glyphs) != 4 {
		t.Fatalf("got %d glyphs", len(glyphs))
	}
	if adv := glyphs[0].Advance; adv < 5.9 || adv > 6.1 {
		t.Errorf("unknown family should fall back to the configured monospace default, advance %v", adv)
	}
	def, err := NewShaperWithOptions()
	if err != nil {
		t.Fatal(err)
	}
	if adv := def.Shape("i", FontRequest{Families: []string{"sans-serif"}, Weight: 400}, 10)[0].Advance; adv > 4 {
		t.Errorf("sans-serif 'i' advance %v looks monospaced", adv)
	}
	m, err := text.New(text.WithExactAdvances())
	if err != nil {
		t.Fatal(err)
	}
	if got := NewShaperFromMeasurer(m).Shape("ab", FontRequest{Families: []string{"Liberation Sans"}, Weight: 400}, 12); len(got) != 2 {
		t.Errorf("FromMeasurer: %d glyphs", len(got))
	}
	// And it renders.
	img, err := Render([]byte(svgHead+`<text x="10" y="50" font-family="nope" font-size="20">Hi</text></svg>`), Options{Shaper: sh})
	if err != nil || img == nil {
		t.Fatalf("render with shaper: %v", err)
	}
}

func TestFontMetrics(t *testing.T) {
	sh, err := NewShaper()
	if err != nil {
		t.Fatal(err)
	}
	g := sh.Shape("x", FontRequest{Families: []string{"Liberation Sans"}, Weight: 400}, 100)
	mf, ok := g[0].Face.(MetricsFace)
	if !ok {
		t.Fatal("face does not report metrics")
	}
	m, ok := mf.Metrics()
	if !ok || m.UnitsPerEm != 2048 {
		t.Fatalf("metrics: %+v ok=%v", m, ok)
	}
	if m.Ascender <= 0 || m.Descender >= 0 || m.UnderlineThickness <= 0 || m.UnderlinePosition >= 0 || m.StrikePosition <= 0 || m.XHeight <= 0 {
		t.Errorf("implausible metrics %+v", m)
	}
	if _, ok := parseFaceMetrics([]byte("not a font"), 1000); ok {
		t.Error("garbage parsed as a font")
	}
}
