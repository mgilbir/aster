package aster

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDeepInput feeds every input shape that nests, at depths far past any
// real chart, through the public API: no shape may make the engine recurse
// without bound. In Go a stack overflow is a fatal error that no recover can
// catch: it takes the host process down. So every recursion over input must
// be bounded by a limit, not guarded by a recovery; recursion_audit_test.go
// lists the recursive functions and how each is bounded.
//
// Each case runs in a child process (harness_security_test.go) whose
// goroutine stacks are capped at deepStackMB, so an unbounded recursion fails
// here at a modest depth rather than growing toward Go's 1 GB default. The
// assertion is only that the call came back — an error is a fine answer —
// within deepWall.
func TestDeepInput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs every shape at three depths in child processes")
	}
	for _, sh := range deepShapes {
		for _, depth := range []int{500, 5000, 100000} {
			t.Run(fmt.Sprintf("%s/%d", sh.name, depth), func(t *testing.T) {
				t.Parallel()
				res, killed := runSec(t, secJob{Kind: sh.kind, Input: sh.gen(depth), Timeout: "30s", MaxStackMB: deepStackMB}, deepWall)
				switch {
				case killed:
					t.Errorf("did not come back: %s", firstLine(res.Err))
				case res.Panic != "":
					t.Errorf("panic: %s", firstLine(res.Panic))
				case res.Seconds > deepSlow:
					t.Errorf("took %.1fs (error: %s)", res.Seconds, firstLine(res.Err))
				}
			})
		}
	}
}

const (
	deepStackMB = 64
	deepWall    = 90 * time.Second
	deepSlow    = 20.0 // seconds
)

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

type deepShape struct {
	name string
	kind string // a secJob kind
	gen  func(depth int) string
}

// nest wraps inner in depth copies of open … close.
func nest(depth int, open, inner, close string) string {
	return strings.Repeat(open, depth) + inner + strings.Repeat(close, depth)
}

// chain joins depth copies of item(i) with sep.
func chain(depth int, sep string, item func(i int) string) string {
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = item(i)
	}
	return strings.Join(parts, sep)
}

// vegaSignal is a Vega spec drawing one text mark from a signal's update
// expression.
func vegaSignal(update string) string {
	return fmt.Sprintf(`{"signals":[{"name":"s","update":%q}],"marks":[{"type":"text","encode":{"update":{"text":{"signal":"''+s"}}}}]}`, update)
}

var deepShapes = []deepShape{
	// The specification itself.
	{"spec-json-objects", "vega", func(d int) string {
		return `{"usermeta":` + nest(d, `{"a":`, `1`, `}`) + `}`
	}},
	{"spec-json-arrays", "vega", func(d int) string {
		return `{"usermeta":` + nest(d, `[`, `1`, `]`) + `}`
	}},
	// Data values.
	{"data-nested-arrays", "vega", func(d int) string {
		return `{"data":[{"name":"t","values":[{"v":` + nest(d, `[`, `1`, `]`) + `}]}],"marks":[{"type":"text","from":{"data":"t"},"encode":{"update":{"text":{"field":"v"}}}}]}`
	}},
	{"data-nested-objects", "vega", func(d int) string {
		return `{"data":[{"name":"t","values":[{"v":` + nest(d, `{"a":`, `1`, `}`) + `}]}],"marks":[{"type":"text","from":{"data":"t"},"encode":{"update":{"text":{"signal":"''+datum.v"}}}}]}`
	}},
	// Marks.
	{"nested-groups", "vega", func(d int) string {
		return `{"marks":[` + nest(d, `{"type":"group","marks":[`, `{"type":"rect","encode":{"update":{"width":{"value":5},"height":{"value":5}}}}`, `]}`) + `]}`
	}},
	{"nested-facets", "vega", func(d int) string {
		inner := `{"type":"rect","from":{"data":"f"},"encode":{"update":{"width":{"value":5},"height":{"value":5}}}}`
		return `{"data":[{"name":"t","values":[{"a":1}]}],"marks":[` +
			nest(d, `{"type":"group","from":{"facet":{"name":"f","data":"t","groupby":"a"}},"marks":[`, inner, `]}`) + `]}`
	}},
	// Expressions.
	{"expr-parens", "vega", func(d int) string { return vegaSignal(nest(d, "(", "1", ")")) }},
	{"expr-calls", "vega", func(d int) string { return vegaSignal(nest(d, "abs(", "1", ")")) }},
	{"expr-array-literal", "vega", func(d int) string { return vegaSignal(nest(d, "[", "1", "]")) }},
	{"expr-object-literal", "vega", func(d int) string { return vegaSignal(nest(d, "{a:", "1", "}")) }},
	{"expr-member-chain", "vega", func(d int) string { return vegaSignal("{a:1}" + strings.Repeat(".a", d)) }},
	{"expr-index-chain", "vega", func(d int) string { return vegaSignal("[1]" + strings.Repeat("[0]", d)) }},
	{"expr-unary-minus", "vega", func(d int) string { return vegaSignal(strings.Repeat("-", d) + "1") }},
	{"expr-unary-not", "vega", func(d int) string { return vegaSignal(strings.Repeat("!", d) + "1") }},
	{"expr-ternary-chain", "vega", func(d int) string { return vegaSignal(strings.Repeat("1?1:", d) + "1") }},
	{"expr-binary-chain", "vega", func(d int) string { return vegaSignal(chain(d, "+", func(int) string { return "1" })) }},
	{"expr-right-assoc-chain", "vega", func(d int) string { return vegaSignal(chain(d, "**", func(int) string { return "1" })) }},
	{"expr-logical-chain", "vega", func(d int) string { return vegaSignal(chain(d, "||", func(int) string { return "0" })) }},
	// Event selectors.
	{"event-selector-brackets", "vega", func(d int) string {
		return fmt.Sprintf(`{"signals":[{"name":"s","value":0,"on":[{"events":%q,"update":"1"}]}]}`, nest(d, "[", "mousedown, mouseup] > mousemove", ""))
	}},
	{"event-selector-merge", "vega", func(d int) string {
		return fmt.Sprintf(`{"signals":[{"name":"s","value":0,"on":[{"events":%q,"update":"1"}]}]}`, chain(d, ", ", func(int) string { return "click" }))
	}},
	{"event-selector-filters", "vega", func(d int) string {
		return fmt.Sprintf(`{"signals":[{"name":"s","value":0,"on":[{"events":%q,"update":"1"}]}]}`, "click"+strings.Repeat("[event.x]", d))
	}},
	// Dataflow.
	{"signal-chain", "vega", func(d int) string {
		return `{"signals":[{"name":"s0","value":1},` + chain(d, ",", func(i int) string {
			return fmt.Sprintf(`{"name":"s%d","update":"s%d+1"}`, i+1, i)
		}) + `]}`
	}},
	{"data-source-chain", "vega", func(d int) string {
		return `{"data":[{"name":"d0","values":[{"a":1}]},` + chain(d, ",", func(i int) string {
			return fmt.Sprintf(`{"name":"d%d","source":"d%d"}`, i+1, i)
		}) + `]}`
	}},
	{"transform-run", "vega", func(d int) string {
		return `{"data":[{"name":"t","values":[{"a":1}],"transform":[` + chain(d, ",", func(i int) string {
			return fmt.Sprintf(`{"type":"formula","as":"f%d","expr":"datum.a+%d"}`, i%50, i)
		}) + `]}]}`
	}},
	// A hierarchy as deep as it is long: tree layouts recurse over depth.
	{"hierarchy-chain-tree", "vega", func(d int) string { return deepHierarchy(d, "tree") }},
	{"hierarchy-chain-treemap", "vega", func(d int) string { return deepHierarchy(d, "treemap") }},
	{"hierarchy-chain-partition", "vega", func(d int) string { return deepHierarchy(d, "partition") }},
	{"hierarchy-chain-pack", "vega", func(d int) string { return deepHierarchy(d, "pack") }},
	// Geometry.
	{"geojson-collections", "vega", func(d int) string {
		g := nest(d, `{"type":"GeometryCollection","geometries":[`, `{"type":"Point","coordinates":[0,0]}`, `]}`)
		return `{"data":[{"name":"t","values":[{"type":"Feature","geometry":` + g + `}]}],"projections":[{"name":"p","type":"mercator"}],"marks":[{"type":"shape","from":{"data":"t"},"transform":[{"type":"geoshape","projection":"p"}]}]}`
	}},
	// Vega-Lite.
	{"vl-nested-layers", "vl", func(d int) string {
		return nest(d, `{"layer":[`, `{"mark":"point","data":{"values":[{"a":1}]},"encoding":{"x":{"field":"a","type":"quantitative"}}}`, `]}`)
	}},
	{"vl-nested-hconcat", "vl", func(d int) string {
		return nest(d, `{"hconcat":[`, `{"mark":"point","data":{"values":[{"a":1}]}}`, `]}`)
	}},
	{"vl-nested-facets", "vl", func(d int) string {
		return `{"data":{"values":[{"a":1}]},` + strings.TrimPrefix(nest(d, `{"facet":{"row":{"field":"a"}},"spec":`, `{"mark":"point"}`, `}`), "{")
	}},
	{"vl-filter-and", "vl", func(d int) string {
		return `{"data":{"values":[{"a":1}]},"mark":"point","transform":[{"filter":` + nest(d, `{"and":[`, `{"field":"a","equal":1}`, `]}`) + `}]}`
	}},
	{"vl-filter-not", "vl", func(d int) string {
		return `{"data":{"values":[{"a":1}]},"mark":"point","transform":[{"filter":` + nest(d, `{"not":`, `{"field":"a","equal":1}`, `}`) + `}]}`
	}},
	{"vl-select-on-brackets", "vl", func(d int) string {
		return fmt.Sprintf(`{"data":{"values":[{"a":1}]},"mark":"point","params":[{"name":"p","select":{"type":"point","on":%q}}]}`, nest(d, "[", "mousedown, mouseup] > mousemove", ""))
	}},
	{"vl-transform-run", "vl", func(d int) string {
		return `{"data":{"values":[{"a":1}]},"mark":"point","transform":[` + chain(d, ",", func(i int) string {
			return fmt.Sprintf(`{"calculate":"datum.a+%d","as":"f%d"}`, i, i%50)
		}) + `]}`
	}},
	{"vl-condition-param-chain", "vl", func(d int) string {
		return `{"data":{"values":[{"a":1}]},"mark":"point","encoding":{"color":{"condition":{"test":` + fmt.Sprintf("%q", nest(d, "(", "datum.a", ")")) + `,"value":"red"},"value":"blue"}}}`
	}},
	// SVG to PNG and to PDF.
	{"svg-nested-groups-png", "svgpng", func(d int) string { return deepSVG(nest(d, "<g>", `<rect width="5" height="5"/>`, "</g>")) }},
	{"svg-nested-groups-pdf", "svgpdf", func(d int) string { return deepSVG(nest(d, "<g>", `<rect width="5" height="5"/>`, "</g>")) }},
	{"svg-nested-svg-png", "svgpng", func(d int) string { return deepSVG(nest(d, "<svg>", `<rect width="5" height="5"/>`, "</svg>")) }},
	{"svg-nested-anchors-png", "svgpng", func(d int) string { return deepSVG(nest(d, `<a href="#">`, `<rect width="5" height="5"/>`, "</a>")) }},
	{"svg-nested-tspans-png", "svgpng", func(d int) string { return deepSVG(`<text y="10">` + nest(d, "<tspan>", "x", "</tspan>") + `</text>`) }},
	{"svg-nested-tspans-pdf", "svgpdf", func(d int) string { return deepSVG(`<text y="10">` + nest(d, "<tspan>", "x", "</tspan>") + `</text>`) }},
	{"svg-use-chain-png", "svgpng", func(d int) string {
		return deepSVG(`<defs><rect id="u0" width="5" height="5"/>` + chain(d, "", func(i int) string {
			return fmt.Sprintf(`<use id="u%d" href="#u%d"/>`, i+1, i)
		}) + `</defs>` + fmt.Sprintf(`<use href="#u%d"/>`, d))
	}},
	{"svg-nested-clips-png", "svgpng", func(d int) string {
		return deepSVG(`<defs>` + chain(d, "", func(i int) string {
			ref := ""
			if i > 0 {
				ref = fmt.Sprintf(` clip-path="url(#c%d)"`, i-1)
			}
			return fmt.Sprintf(`<clipPath id="c%d"%s><rect width="5" height="5"/></clipPath>`, i, ref)
		}) + `</defs>` + fmt.Sprintf(`<rect width="5" height="5" clip-path="url(#c%d)"/>`, d-1))
	}},
}

func deepSVG(body string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="20" height="20">` + body + `</svg>`
}

// deepHierarchy is a chain of depth nodes, each the parent of the next,
// laid out by method.
func deepHierarchy(depth int, method string) string {
	rows := chain(depth, ",", func(i int) string {
		if i == 0 {
			return `{"id":0}`
		}
		return fmt.Sprintf(`{"id":%d,"parent":%d}`, i, i-1)
	})
	layout := map[string]string{
		"tree":      `{"type":"tree","size":[200,200]}`,
		"treemap":   `{"type":"treemap","size":[200,200]}`,
		"partition": `{"type":"partition","size":[200,200]}`,
		"pack":      `{"type":"pack","size":[200,200]}`,
	}[method]
	return `{"data":[{"name":"t","values":[` + rows + `],"transform":[{"type":"stratify","key":"id","parentKey":"parent"},` + layout + `]}],"marks":[{"type":"symbol","from":{"data":"t"},"encode":{"update":{"x":{"field":"x"},"y":{"field":"y"}}}}]}`
}
