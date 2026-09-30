package raster

import "testing"

// featureSynths cover the general-SVG features beyond what Vega emits; each is
// compared with resvg at scale 1 and 2.5 with its own thresholds.
var featureSynths = []synth{
	// ---- CSS ----
	{"css-basic", svgHead + `<style type="text/css"><![CDATA[
 rect { fill: red } .a { fill: blue } #x { fill: green !important; stroke: black; stroke-width: 3 }
 g > .b, circle.c:first-child { fill: orange }
 g rect.b { fill: purple }
 @media print { rect { fill: pink } }
]]></style>
<rect id="x" class="a" width="30" height="30" style="fill:yellow"/>
<g><rect class="b" x="40" width="30" height="30"/></g>
<rect class="a" x="80" width="30" height="30"/>
<rect x="0" y="50" width="30" height="30" fill="cyan"/></svg>`, 0.1, 0.25},
	{"css-selectors", svgHead + `<style>
 [data-k] { fill: red } [data-k="v"] { fill: green } [data-k~="w"] { stroke: blue; stroke-width: 4 }
 [lang|="en"] { fill: orange } * { stroke-linejoin: round }
 rect:first-child { opacity: 0.5 } rect + rect { fill: navy } svg > g > rect { fill-opacity: 0.6 }
 rect, circle { stroke: black }
</style>
<g><rect data-k="v" x="5" y="5" width="30" height="30"/><rect data-k="q w" x="45" y="5" width="30" height="30"/><rect lang="en-US" x="85" y="5" width="30" height="30"/></g>
<circle cx="30" cy="70" r="15"/></svg>`, 0.15, 0.25},
	{"css-specificity", svgHead + `<style>
 rect { fill: red } .c { fill: green } #i { fill: blue } rect.c { fill: orange } .c { fill: purple }
 .imp { fill: teal !important } rect#j { fill: gold !important; fill: pink }
</style>
<rect x="0" width="25" height="40" class="c"/><rect x="30" width="25" height="40" class="c" id="i"/>
<rect x="60" width="25" height="40" class="imp" id="i" style="fill:black"/>
<rect x="90" width="25" height="40" id="j" style="fill:black !important"/></svg>`, 0.1, 0.25},
	{"css-inherit", svgHead + `<style>
 g.outer { fill: crimson; stroke: navy; stroke-width: 3 } .inner { stroke: none } text { font-family: sans-serif; font-size: 14px }
 .hidden { display: none }
</style>
<g class="outer"><rect x="5" y="5" width="40" height="40"/><g class="inner"><rect x="55" y="5" width="40" height="40"/></g>
<rect class="hidden" x="5" y="55" width="40" height="40"/></g><text x="10" y="80">CSS</text></svg>`, 0.1, 0.25},
	{"css-marker-shorthand-url", svgHead + `<style>.p { fill: url(#g) } rect { stroke: #000; stroke-dasharray: 4 2; }</style>
<defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs>
<rect class="p" x="10" y="10" width="100" height="60"/></svg>`, 0.1, 0.25},
	// ---- shape-rendering ----
	{"crisp-edges", svgHead + `<g shape-rendering="crispEdges"><circle cx="30" cy="30" r="20.3" fill="red"/><path d="M60 10 L110 40 L70 80z" fill="blue" stroke="black" stroke-width="3"/></g>
<g shape-rendering="optimizeSpeed"><rect x="10.4" y="60.6" width="30.5" height="20.5" fill="green"/></g></svg>`, 0.3, 0.29},
	{"crisp-css", svgHead + `<style>ellipse { shape-rendering: crispEdges }</style><ellipse cx="60" cy="50" rx="45" ry="30" fill="purple"/></svg>`, 0.62, 0.49},
	// ---- mask ----
	{"mask-luminance", svgHead + `<defs><linearGradient id="g"><stop offset="0" stop-color="white"/><stop offset="1" stop-color="black"/></linearGradient>
<mask id="m"><rect x="0" y="0" width="120" height="100" fill="url(#g)"/></mask></defs>
<rect width="120" height="100" fill="#3366cc" mask="url(#m)"/></svg>`, 0.1, 0.25},
	{"mask-alpha-type", svgHead + `<defs><mask id="m" mask-type="alpha"><circle cx="60" cy="50" r="30" fill="black" fill-opacity="0.7"/></mask></defs>
<rect width="120" height="100" fill="crimson" mask="url(#m)"/></svg>`, 0.1, 0.25},
	{"mask-units", svgHead + `<defs><mask id="m" maskUnits="userSpaceOnUse" x="20" y="20" width="60" height="50" maskContentUnits="userSpaceOnUse"><rect x="0" y="0" width="120" height="100" fill="white"/><rect x="30" y="30" width="20" height="20" fill="black"/></mask>
<mask id="n" x="0.1" y="0.1" width="0.5" height="0.5" maskContentUnits="objectBoundingBox"><rect x="0" y="0" width="1" height="1" fill="#888"/><circle cx="0.5" cy="0.5" r="0.3" fill="white"/></mask></defs>
<rect width="120" height="100" fill="green" mask="url(#m)"/>
<rect x="60" y="10" width="50" height="60" fill="navy" mask="url(#n)"/></svg>`, 0.1, 0.25},
	{"mask-nested-group", svgHead + `<defs><mask id="a"><rect width="120" height="50" fill="white"/></mask><mask id="b" mask="url(#a)"><circle cx="60" cy="50" r="40" fill="white"/></mask></defs>
<g mask="url(#b)" opacity="0.8" transform="translate(5 5)"><rect width="100" height="90" fill="orange"/><rect x="30" width="30" height="90" fill="purple"/></g></svg>`, 0.1, 0.25},
	{"mask-text", svgHead + `<defs><mask id="m"><rect width="120" height="100" fill="black"/><text x="5" y="60" font-family="sans-serif" font-size="50" font-weight="bold" fill="white">Hi</text></mask></defs>
<rect width="120" height="100" fill="teal" mask="url(#m)"/></svg>`, 0.1, 0.25},
	// ---- clipPath extras ----
	{"clip-obb", svgHead + `<defs><clipPath id="c" clipPathUnits="objectBoundingBox"><circle cx="0.5" cy="0.5" r="0.45"/></clipPath></defs>
<rect x="10" y="10" width="100" height="60" fill="orange" clip-path="url(#c)"/><rect x="20" y="75" width="80" height="20" fill="blue" clip-path="url(#c)"/></svg>`, 0.18, 0.25},
	{"clip-text", svgHead + `<defs><clipPath id="c"><text x="5" y="60" font-family="sans-serif" font-size="50" font-weight="bold">Clip</text></clipPath></defs>
<rect width="120" height="100" fill="url(#g)" clip-path="url(#c)"/><defs><linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs></svg>`, 0.17, 0.46},
	// ---- filters ----
	{"filter-blur-box", svgHead + `<defs><filter id="f"><feGaussianBlur stdDeviation="4"/></filter></defs>
<rect x="30" y="25" width="60" height="50" fill="crimson" filter="url(#f)"/></svg>`, 0.1, 0.25},
	{"filter-blur-iir", svgHead + `<defs><filter id="f"><feGaussianBlur stdDeviation="1.2 0.6"/></filter></defs>
<circle cx="60" cy="50" r="25" fill="navy" filter="url(#f)"/></svg>`, 0.1, 0.25},
	{"filter-offset-flood-merge", svgHead + `<defs><filter id="f" x="-20%" y="-20%" width="150%" height="150%">
<feOffset in="SourceAlpha" dx="6" dy="5" result="o"/><feGaussianBlur in="o" stdDeviation="3" result="b"/><feFlood flood-color="#333" flood-opacity="0.6" result="fl"/><feComposite in="fl" in2="b" operator="in" result="sh"/>
<feMerge><feMergeNode in="sh"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>
<g filter="url(#f)"><rect x="20" y="20" width="50" height="40" fill="gold"/><circle cx="80" cy="60" r="18" fill="tomato"/></g></svg>`, 0.52, 0.25},
	{"filter-colormatrix", svgHead + `<defs><filter id="a"><feColorMatrix type="saturate" values="0.2"/></filter><filter id="b"><feColorMatrix type="hueRotate" values="90"/></filter>
<filter id="c"><feColorMatrix type="luminanceToAlpha"/></filter><filter id="d" color-interpolation-filters="sRGB"><feColorMatrix type="matrix" values="0 1 0 0 0  1 0 0 0 0  0 0 1 0 0  0 0 0 1 0"/></filter></defs>
<rect x="5" y="5" width="25" height="40" fill="#e03030" filter="url(#a)"/><rect x="35" y="5" width="25" height="40" fill="#e03030" filter="url(#b)"/>
<rect x="65" y="5" width="25" height="40" fill="#e03030" filter="url(#c)"/><rect x="95" y="5" width="20" height="40" fill="#e03030" filter="url(#d)"/></svg>`, 0.17, 0.25},
	{"filter-composite-over", svgHead + `<defs><filter id="o" x="0" y="0" width="1" height="1"><feFlood flood-color="blue" flood-opacity="0.7" result="f"/><feComposite in="SourceGraphic" in2="f" operator="over"/></filter></defs>
<g filter="url(#o)"><circle cx="40" cy="50" r="30" fill="red"/></g></svg>`, 0.12, 0.27},
	{"filter-composite-in", svgHead + `<defs><filter id="o" x="0" y="0" width="1" height="1"><feFlood flood-color="blue" flood-opacity="0.7" result="f"/><feComposite in="SourceGraphic" in2="f" operator="in"/></filter></defs>
<g filter="url(#o)"><circle cx="40" cy="50" r="30" fill="red"/></g></svg>`, 0.1, 0.25},
	{"filter-composite-out", svgHead + `<defs><filter id="o" x="0" y="0" width="1" height="1"><feFlood flood-color="blue" flood-opacity="0.7" result="f"/><feComposite in="SourceGraphic" in2="f" operator="out"/></filter></defs>
<g filter="url(#o)"><circle cx="40" cy="50" r="30" fill="red"/></g></svg>`, 0.1, 0.25},
	{"filter-composite-atop", svgHead + `<defs><filter id="o" x="0" y="0" width="1" height="1"><feFlood flood-color="blue" flood-opacity="0.7" result="f"/><feComposite in="SourceGraphic" in2="f" operator="atop"/></filter></defs>
<g filter="url(#o)"><circle cx="40" cy="50" r="30" fill="red"/></g></svg>`, 0.1, 0.27},
	{"filter-composite-xor", svgHead + `<defs><filter id="o" x="0" y="0" width="1" height="1"><feFlood flood-color="blue" flood-opacity="0.7" result="f"/><feComposite in="SourceGraphic" in2="f" operator="xor"/></filter></defs>
<g filter="url(#o)"><circle cx="40" cy="50" r="30" fill="red"/></g></svg>`, 0.1, 0.25},
	{"filter-arithmetic-blend", svgHead + `<defs><filter id="f" x="0" y="0" width="1" height="1"><feFlood flood-color="#2080ff" result="f"/><feBlend in="SourceGraphic" in2="f" mode="multiply" result="m"/>
<feComposite in="m" in2="SourceGraphic" operator="arithmetic" k1="0" k2="0.7" k3="0.4" k4="0"/></filter></defs>
<rect x="10" y="10" width="100" height="80" fill="url(#g)" filter="url(#f)"/><defs><linearGradient id="g"><stop offset="0" stop-color="#ffcc00"/><stop offset="1" stop-color="#cc0044"/></linearGradient></defs></svg>`, 0.1, 0.25},
	{"filter-dropshadow", svgHead + `<defs><filter id="f"><feDropShadow dx="4" dy="4" stdDeviation="3" flood-color="black" flood-opacity="0.6"/></filter></defs>
<rect x="20" y="20" width="60" height="50" rx="8" fill="#9c6" filter="url(#f)"/></svg>`, 0.1, 0.25},
	{"filter-userspace-units", svgHead + `<defs><filter id="f" filterUnits="userSpaceOnUse" x="10" y="10" width="80" height="60" primitiveUnits="userSpaceOnUse"><feGaussianBlur stdDeviation="3"/><feOffset dx="5" dy="5"/></filter></defs>
<rect x="15" y="15" width="70" height="70" fill="purple" filter="url(#f)"/></svg>`, 0.1, 0.25},
	{"filter-primitive-subregion", svgHead + `<defs><filter id="f" primitiveUnits="objectBoundingBox"><feFlood x="0.2" y="0.2" width="0.5" height="0.5" flood-color="lime"/><feGaussianBlur stdDeviation="0.05"/></filter></defs>
<rect x="10" y="10" width="100" height="80" fill="gray" filter="url(#f)"/></svg>`, 0.1, 0.25},
	{"filter-transformed", svgHead + `<defs><filter id="f"><feGaussianBlur stdDeviation="3"/></filter></defs>
<g transform="translate(60 50) rotate(25) scale(1.3 0.8)"><rect x="-30" y="-20" width="60" height="40" fill="orange" filter="url(#f)"/></g></svg>`, 0.1, 0.25},
	{"filter-opacity-mask-clip", svgHead + `<defs><filter id="f"><feGaussianBlur stdDeviation="2.5"/></filter><clipPath id="c"><rect x="0" y="0" width="70" height="100"/></clipPath>
<mask id="m"><rect width="120" height="100" fill="#ccc"/></mask></defs>
<rect x="30" y="20" width="60" height="60" fill="darkred" filter="url(#f)" clip-path="url(#c)" mask="url(#m)" opacity="0.7"/></svg>`, 0.1, 0.25},
	{"filter-invalid-link", svgHead + `<rect width="50" height="50" fill="red" filter="url(#nope)"/><rect x="60" width="50" height="50" fill="blue"/></svg>`, 0.1, 0.25},
	// ---- pattern ----
	{"pattern-userspace", svgHead + `<defs><pattern id="p" patternUnits="userSpaceOnUse" x="3" y="2" width="20" height="16"><circle cx="8" cy="8" r="6" fill="crimson"/><rect width="20" height="16" fill="none" stroke="navy" stroke-width="1"/></pattern></defs>
<rect x="5" y="5" width="110" height="90" fill="url(#p)"/></svg>`, 2.65, 7.5},
	{"pattern-obb", svgHead + `<defs><pattern id="p" width="0.25" height="0.5" patternContentUnits="objectBoundingBox"><circle cx="0.1" cy="0.2" r="0.08" fill="teal"/><rect x="0.1" y="0.1" width="0.1" height="0.2" fill="orange"/></pattern></defs>
<rect x="10" y="10" width="100" height="80" fill="url(#p)" stroke="black"/></svg>`, 0.11, 0.25},
	{"pattern-viewbox-transform", svgHead + `<defs><pattern id="p" patternUnits="userSpaceOnUse" width="24" height="24" viewBox="0 0 10 10" patternTransform="rotate(30) scale(1.2)"><path d="M0 0 L10 0 L0 10z" fill="purple"/><circle cx="7" cy="7" r="2" fill="gold"/></pattern></defs>
<circle cx="60" cy="50" r="42" fill="url(#p)"/></svg>`, 1.62, 2.15},
	{"pattern-href-stroke", svgHead + `<defs><pattern id="base" patternUnits="userSpaceOnUse" width="10" height="10"><rect width="5" height="5" fill="black"/><rect x="5" y="5" width="5" height="5" fill="gray"/></pattern>
<pattern id="p" href="#base" x="1" y="1"/></defs>
<rect x="10" y="10" width="100" height="80" fill="none" stroke="url(#p)" stroke-width="14"/></svg>`, 0.1, 0.25},
	{"pattern-opacity-fallback", svgHead + `<defs><pattern id="p" patternUnits="userSpaceOnUse" width="8" height="8"><circle cx="4" cy="4" r="3" fill="blue"/></pattern></defs>
<rect x="5" y="5" width="50" height="90" fill="url(#p)" fill-opacity="0.5"/><rect x="65" y="5" width="50" height="90" fill="url(#missing) green"/></svg>`, 0.33, 0.25},
	// ---- marker ----
	{"marker-basic", svgHead + `<defs><marker id="m" markerWidth="6" markerHeight="6" refX="3" refY="3" orient="auto"><path d="M0 0 L6 3 L0 6z" fill="red"/></marker></defs>
<path d="M10 80 L40 20 L70 70 L110 30" fill="none" stroke="black" stroke-width="2" marker-start="url(#m)" marker-mid="url(#m)" marker-end="url(#m)"/></svg>`, 0.1, 0.25},
	{"marker-units-viewbox", svgHead + `<defs><marker id="m" markerUnits="userSpaceOnUse" markerWidth="16" markerHeight="16" viewBox="0 0 10 10" refX="5" refY="5" orient="45"><circle cx="5" cy="5" r="5" fill="orange" stroke="black"/></marker></defs>
<polyline points="10,20 50,60 90,20" fill="none" stroke="gray" stroke-width="4" marker-start="url(#m)" marker-end="url(#m)" marker-mid="url(#m)"/></svg>`, 0.23, 0.54},
	{"marker-start-reverse-overflow", svgHead + `<defs><marker id="m" markerWidth="8" markerHeight="8" refX="0" refY="4" orient="auto-start-reverse" overflow="visible"><path d="M0 0 L8 4 L0 8z" fill="blue"/></marker></defs>
<line x1="20" y1="30" x2="100" y2="30" stroke="black" stroke-width="3" marker-start="url(#m)" marker-end="url(#m)"/>
<path d="M20 70 C 40 40, 70 100, 100 70" fill="none" stroke="green" stroke-width="2" marker-start="url(#m)" marker-mid="url(#m)" marker-end="url(#m)"/></svg>`, 0.22, 1.1},
	{"marker-css-shorthand-group", svgHead + `<style>.mk { marker: url(#m) }</style><defs><marker id="m" markerWidth="4" markerHeight="4" refX="2" refY="2"><rect width="4" height="4" fill="crimson"/></marker></defs>
<g opacity="0.6"><polygon class="mk" points="20,20 100,30 60,80" fill="#ccf" stroke="black" stroke-width="3"/></g></svg>`, 0.1, 0.25},
	{"marker-on-rect", svgHead + `<defs><marker id="m" markerWidth="4" markerHeight="4" refX="2" refY="2"><rect width="4" height="4" fill="crimson"/></marker></defs>
<rect x="20" y="20" width="60" height="40" fill="none" stroke="black" marker-start="url(#m)" marker-mid="url(#m)" marker-end="url(#m)"/></svg>`, 0.1, 0.25},
	{"paint-order", svgHead + `<text x="10" y="45" font-family="sans-serif" font-size="40" font-weight="bold" fill="white" stroke="black" stroke-width="6" paint-order="stroke">Halo</text>
<rect x="10" y="60" width="40" height="30" fill="gold" stroke="navy" stroke-width="10" style="paint-order: stroke fill"/><path d="M70 60 L110 90" stroke="red" stroke-width="8" fill="none" style="paint-order: stroke markers" marker-start="url(#m)" marker-end="url(#m)"/>
<defs><marker id="m" markerWidth="4" markerHeight="4" refX="2" refY="2"><circle cx="2" cy="2" r="2" fill="green"/></marker></defs></svg>`, 0.25, 0.5},
	// ---- text ----
	{"text-rotate", svgHead + `<text x="10" y="50" font-family="sans-serif" font-size="18" rotate="0 15 30 -20 45">Rotate me</text></svg>`, 0.1, 0.25},
	{"text-textlength", svgHead + `<text x="10" y="30" font-family="sans-serif" font-size="14" textLength="100" lengthAdjust="spacing">Spacing</text>
<text x="10" y="60" font-family="sans-serif" font-size="14" textLength="100" lengthAdjust="spacingAndGlyphs">Glyphs</text>
<text x="10" y="90" font-family="sans-serif" font-size="14" textLength="60">Short</text></svg>`, 0.1, 0.4},
	{"text-decoration", svgHead + `<text x="10" y="25" font-family="sans-serif" font-size="18" text-decoration="underline">Under</text><text x="10" y="55" font-family="serif" font-size="18" text-decoration="line-through">Through</text>
<text x="10" y="85" font-family="monospace" font-size="18" text-decoration="overline underline">Over</text></svg>`, 0.1, 0.29},
	{"image-optimize-speed", svgHead + `<image x="10" y="10" width="100" height="80" href="data:image/png;base64,` + tinyPNG + `" image-rendering="optimizeSpeed"/></svg>`, 0.1, 0.25},
	{"image-rendering-css", svgHead + `<style>image { image-rendering: pixelated }</style><image x="10" y="10" width="100" height="80" href="data:image/png;base64,` + tinyPNG + `"/></svg>`, 0.1, 0.25},
}

func TestFeatures(t *testing.T) { runSynths(t, "feat-", featureSynths) }
