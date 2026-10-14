package raster

import (
	"fmt"
	"math/rand"
	"strings"
)

// genDocs returns n random SVG documents, the same for the same seed, made to
// reach every way pixels get onto a canvas: shapes filled and stroked with
// solid colours, gradients and patterns at opacities down to those that round
// to nothing, groups and elements drawn through offscreen layers (opacity on
// a fill and a stroke both, blend modes, nested groups), clip paths, masks,
// filters (one turning colour into alpha), markers, images, text and, when
// colour is set, colour glyphs, under transforms, partly or wholly off the
// canvas, with crisp edges and hairlines.
func genDocs(seed int64, n int, colour bool) []string {
	rng := rand.New(rand.NewSource(seed))
	docs := make([]string, n)
	for i := range docs {
		g := &docGen{rng: rng, colour: colour}
		docs[i] = g.doc()
	}
	return docs
}

type docGen struct {
	rng    *rand.Rand
	colour bool
	w, h   int
	b      strings.Builder
}

func (g *docGen) pick(s ...string) string { return s[g.rng.Intn(len(s))] }

func (g *docGen) num(lo, hi float64) string {
	return fmt.Sprintf("%.3g", lo+g.rng.Float64()*(hi-lo))
}

// coord is a coordinate along a side of length n, mostly on the canvas, now
// and then off it or far off it.
func (g *docGen) coord(n int) string {
	switch g.rng.Intn(20) {
	case 0:
		return g.num(-float64(n), 0)
	case 1:
		return g.num(float64(n), 2*float64(n))
	case 2:
		return g.pick("-1e7", "1e7", "123456.5")
	}
	return g.num(-5, float64(n)+5)
}

func (g *docGen) colourValue() string {
	switch g.rng.Intn(6) {
	case 0:
		return "currentColor"
	case 1:
		return fmt.Sprintf("rgba(%d,%d,%d,%s)", g.rng.Intn(256), g.rng.Intn(256), g.rng.Intn(256), g.pick("0", "0.001", "0.003", "0.2", "0.5"))
	}
	return fmt.Sprintf("#%02x%02x%02x", g.rng.Intn(256), g.rng.Intn(256), g.rng.Intn(256))
}

func (g *docGen) paint() string {
	switch g.rng.Intn(10) {
	case 0:
		return "none"
	case 1:
		return g.pick("url(#lg)", "url(#rg)")
	case 2:
		return "url(#pt)"
	case 3:
		return "url(#missing) " + g.colourValue()
	}
	return g.colourValue()
}

func (g *docGen) opacity() string {
	return g.pick("1", "1", "1", "0.5", "0.8", "0.1", "0.003", "0.001", "0")
}

// presentation is a random set of painting attributes.
func (g *docGen) presentation() string {
	var a []string
	add := func(k, v string) { a = append(a, fmt.Sprintf(`%s="%s"`, k, v)) }
	if g.rng.Intn(4) != 0 {
		add("fill", g.paint())
	}
	if g.rng.Intn(2) == 0 {
		add("stroke", g.paint())
		add("stroke-width", g.pick("0.05", "0.3", "1", "2.5", "8"))
		if g.rng.Intn(4) == 0 {
			add("stroke-dasharray", g.pick("3 2", "0.5 4", "10"))
		}
		if g.rng.Intn(3) == 0 {
			add("stroke-linejoin", g.pick("miter", "round", "bevel"))
			add("stroke-linecap", g.pick("butt", "round", "square"))
			add("stroke-miterlimit", g.pick("1", "4", "20"))
		}
		if g.rng.Intn(10) == 0 {
			add("vector-effect", "non-scaling-stroke")
		}
	}
	if g.rng.Intn(3) == 0 {
		add("opacity", g.opacity())
	}
	if g.rng.Intn(4) == 0 {
		add("fill-opacity", g.opacity())
	}
	if g.rng.Intn(4) == 0 {
		add("stroke-opacity", g.opacity())
	}
	if g.rng.Intn(8) == 0 {
		add("mix-blend-mode", g.pick("multiply", "screen", "overlay", "darken", "lighten", "color-dodge", "color-burn", "hard-light", "soft-light", "difference", "exclusion"))
	}
	if g.rng.Intn(8) == 0 {
		add("shape-rendering", "crispEdges")
	}
	if g.rng.Intn(6) == 0 {
		add("transform", g.transform())
	}
	switch g.rng.Intn(14) {
	case 0:
		add("clip-path", g.pick("url(#cp)", "url(#cpb)"))
	case 1:
		add("mask", g.pick("url(#ml)", "url(#ma)"))
	case 2:
		add("filter", g.pick("url(#fb)", "url(#fo)", "url(#fa)", "url(#ff)"))
	}
	if g.rng.Intn(10) == 0 {
		add("fill-rule", "evenodd")
	}
	if g.rng.Intn(5) == 0 {
		add("color", g.colourValue())
	}
	return strings.Join(a, " ")
}

func (g *docGen) transform() string {
	switch g.rng.Intn(4) {
	case 0:
		return fmt.Sprintf("translate(%s,%s)", g.num(-20, 20), g.num(-20, 20))
	case 1:
		return fmt.Sprintf("rotate(%s %s %s)", g.num(-180, 180), g.num(0, float64(g.w)), g.num(0, float64(g.h)))
	case 2:
		return fmt.Sprintf("scale(%s,%s)", g.num(0.2, 3), g.num(0.2, 3))
	}
	return fmt.Sprintf("skewX(%s)", g.num(-40, 40))
}

func (g *docGen) shape() string {
	p := g.presentation()
	switch g.rng.Intn(9) {
	case 0:
		return fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="%s" rx="%s" %s/>`, g.coord(g.w), g.coord(g.h), g.num(0, float64(g.w)/2), g.num(0, float64(g.h)/2), g.pick("0", "0", "3"), p)
	case 1:
		return fmt.Sprintf(`<circle cx="%s" cy="%s" r="%s" %s/>`, g.coord(g.w), g.coord(g.h), g.pick(g.num(0.05, 2), g.num(2, 40)), p)
	case 2:
		return fmt.Sprintf(`<ellipse cx="%s" cy="%s" rx="%s" ry="%s" %s/>`, g.coord(g.w), g.coord(g.h), g.num(0.5, 40), g.num(0.5, 40), p)
	case 3:
		return fmt.Sprintf(`<line x1="%s" y1="%s" x2="%s" y2="%s" %s marker-end="%s"/>`, g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), p, g.pick("", "url(#mk)"))
	case 4:
		return fmt.Sprintf(`<polyline points="%s,%s %s,%s %s,%s %s,%s" %s/>`, g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), p)
	case 5:
		return fmt.Sprintf(`<path d="M%s,%sC%s,%s %s,%s %s,%sA%s,%s 0 1,1 %s,%sZ" %s/>`, g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), g.coord(g.w), g.coord(g.h), g.num(1, 30), g.num(1, 30), g.coord(g.w), g.coord(g.h), p)
	case 6:
		// A sliver: a shape that touches pixels with very little coverage.
		x, y := g.rng.Float64()*float64(g.w), g.rng.Float64()*float64(g.h)
		return fmt.Sprintf(`<path d="M%.4f,%.4fl%.4f,0l0,%.4fZ" %s/>`, x, y, 0.01+g.rng.Float64()*0.2, 0.01+g.rng.Float64()*0.2, p)
	case 7:
		fam := g.pick("sans-serif", "monospace")
		txt := g.pick("Label", "Wg 12", "ÅÉ", "x")
		if g.colour && g.rng.Intn(2) == 0 {
			fam = g.pick("ColourTest", "SbixTest")
			txt = g.pick("\U0001F534\U0001F7E2", "\U0001F308A", "\U0001F600", "\U0001F3A8\U0001F4A0")
		}
		return fmt.Sprintf(`<text x="%s" y="%s" font-family="%s" font-size="%s" %s>%s</text>`, g.coord(g.w), g.coord(g.h), fam, g.pick("6", "11", "24"), p, txt)
	}
	return fmt.Sprintf(`<image x="%s" y="%s" width="%s" height="%s" preserveAspectRatio="%s" %s %s href="data:image/png;base64,%s"/>`, g.coord(g.w), g.coord(g.h), g.num(1, 50), g.num(1, 50),
		g.pick("none", "xMidYMid", "xMinYMax slice"), g.pick("", `image-rendering="pixelated"`), p, tinyPNG)
}

func (g *docGen) element(depth int) string {
	if depth < 3 && g.rng.Intn(4) == 0 {
		var kids strings.Builder
		for range 1 + g.rng.Intn(4) {
			kids.WriteString(g.element(depth + 1))
		}
		if g.rng.Intn(6) == 0 {
			return fmt.Sprintf(`<use href="#sym" x="%s" y="%s" %s/>`, g.coord(g.w), g.coord(g.h), g.presentation())
		}
		return fmt.Sprintf(`<g %s>%s</g>`, g.presentation(), kids.String())
	}
	return g.shape()
}

func (g *docGen) doc() string {
	g.w, g.h = 20+g.rng.Intn(140), 15+g.rng.Intn(110)
	b := &g.b
	b.Reset()
	fmt.Fprintf(b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%d" height="%d" viewBox="0 0 %d %d">`, g.w, g.h, g.w, g.h)
	b.WriteString(`<defs>` +
		`<linearGradient id="lg"><stop offset="0" stop-color="#f00" stop-opacity="0"/><stop offset="1" stop-color="#00f"/></linearGradient>` +
		`<radialGradient id="rg" cx="0.3" cy="0.3" r="0.6"><stop offset="0" stop-color="#0f0"/><stop offset="0.6" stop-color="#000" stop-opacity="0.002"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient>` +
		`<pattern id="pt" width="6" height="6" patternUnits="userSpaceOnUse"><rect width="3" height="3" fill="#a0a"/><circle cx="4.5" cy="4.5" r="1" fill="rgba(0,0,0,0.01)"/></pattern>` +
		`<clipPath id="cp"><circle cx="40" cy="30" r="25"/><rect x="60" y="5" width="20" height="60"/></clipPath>` +
		`<clipPath id="cpb" clipPathUnits="objectBoundingBox"><rect x="0.25" y="0" width="0.5" height="1"/></clipPath>` +
		`<mask id="ml"><rect width="100%" height="100%" fill="url(#lg)"/><circle cx="30" cy="30" r="20" fill="#fff"/></mask>` +
		`<mask id="ma" mask-type="alpha"><rect x="10" y="10" width="60" height="40" fill="#000" fill-opacity="0.3"/></mask>` +
		`<filter id="fb"><feGaussianBlur stdDeviation="2"/></filter>` +
		`<filter id="fo"><feOffset dx="7" dy="-4"/></filter>` +
		`<filter id="fa"><feColorMatrix type="matrix" values="0 0 0 0 0  0 0 0 0 0  0 0 0 0 0  1 0 0 0 0"/></filter>` +
		`<filter id="ff" x="-0.5" y="-0.5" width="2" height="2"><feFlood flood-color="#0a0" flood-opacity="0.4"/><feComposite in2="SourceAlpha" operator="out"/></filter>` +
		`<marker id="mk" markerWidth="6" markerHeight="6" refX="3" refY="3" orient="auto"><path d="M0,0L6,3L0,6Z" fill="#333"/></marker>` +
		`<symbol id="sym" viewBox="0 0 10 10" width="20" height="20"><circle cx="5" cy="5" r="4" fill="#c60" stroke="#000" opacity="0.6"/></symbol>` +
		`</defs>`)
	closing := `</svg>`
	if g.rng.Intn(5) == 0 {
		// A clip of the whole drawing, as a Vega group with clip=true makes.
		fmt.Fprintf(b, `<clipPath id="root"><rect x="%d" y="%d" width="%d" height="%d"/></clipPath><g clip-path="url(#root)">`, g.rng.Intn(10), g.rng.Intn(10), g.w/2+g.rng.Intn(g.w), g.h/2+g.rng.Intn(g.h))
		closing = `</g></svg>`
	}
	for range 1 + g.rng.Intn(10) {
		b.WriteString(g.element(0))
	}
	b.WriteString(closing)
	return b.String()
}
