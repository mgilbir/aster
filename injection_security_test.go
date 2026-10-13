package aster

// Output-injection tests: attacker-controlled spec values must not be able to
// add attributes or elements to the SVG, smuggle CSS into style attributes,
// produce output a conforming XML parser rejects, or reach the network through
// image URLs when the Loader denies everything.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/svgcheck"
)

// checkSVG checks svg against the grammar of the renderer's output
// (svgcheck.Check): only the elements it writes, under their parents, with
// the attributes it writes on them, and text only inside text elements. A
// spec value that made markup (a <script>, an event handler, an attribute the
// element does not carry) fails it.
func checkSVG(t *testing.T, svg string) {
	t.Helper()
	if err := svgcheck.Check(svg); err != nil {
		t.Errorf("%v\n%s", err, svg)
	}
}

func renderVegaSVG(t *testing.T, spec string, opts ...Option) string {
	t.Helper()
	c, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// What the Loader refuses is reported, and these tests look at what the
	// render then writes, so they let it go on.
	svg, err := c.VegaToSVG([]byte(spec), WithErrorHandler(func(error) error { return nil }))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return svg
}

// The item "angle" property is written into transform="... rotate(<angle>)"
// with the raw JavaScript string when the spec gives a string instead of a
// number (internal/svg/style.go appendAngle), without escaping.
func TestInjectionRotateAngleString(t *testing.T) {
	for _, mark := range []string{"text", "symbol"} {
		t.Run(mark, func(t *testing.T) {
			spec := fmt.Sprintf(`{"width":50,"height":50,"marks":[{"type":%q,"encode":{"update":{
				"x":{"value":10},"y":{"value":10},"text":{"value":"hi"},
				"angle":{"value":"1)\" onmouseover=\"alert(1)\" data-x=\"("}}}}]}`, mark)
			checkSVG(t, renderVegaSVG(t, spec))
		})
	}
}

// A text position given a word is joined into transform="translate(x,y)" as a
// string (internal/svg/textimg.go appendWordTransform), with or without an
// angle; the attribute must be escaped as a whole.
func TestInjectionTextPositionString(t *testing.T) {
	for _, angle := range []string{"0", "30"} {
		t.Run("angle="+angle, func(t *testing.T) {
			spec := fmt.Sprintf(`{"width":50,"height":50,"marks":[{"type":"text","encode":{"update":{
				"text":{"value":"hi"},"angle":{"value":%s},
				"x":{"value":"1\" onmouseover=\"alert(1)"},"y":{"value":"2\" data-x=\"y"}}}}]}`, angle)
			svg := renderVegaSVG(t, spec)
			checkSVG(t, svg)
			if !strings.Contains(svg, `translate(1&quot; onmouseover=&quot;alert(1)`) {
				t.Errorf("the position is not in the transform: %s", svg)
			}
		})
	}
}

// A gradient object's id is written unescaped into fill="url(#id)" and
// stroke="url(#id)" (internal/svg/style.go paintAttr uses attrRaw).
func TestInjectionGradientID(t *testing.T) {
	for _, prop := range []string{"fill", "stroke"} {
		t.Run(prop, func(t *testing.T) {
			spec := fmt.Sprintf(`{"width":50,"height":50,"marks":[{"type":"rect","encode":{"update":{
				"x":{"value":1},"y":{"value":1},"width":{"value":9},"height":{"value":9},
				%q:{"signal":"{gradient:'linear',id:'q\" onload=\"alert(5)',stops:[{offset:0,color:'red'},{offset:1,color:'blue'}]}"}}}}]}`, prop)
			checkSVG(t, renderVegaSVG(t, spec))
		})
	}
}

// blend is interpolated into style="mix-blend-mode: <blend>;" so a spec can
// add arbitrary CSS declarations to the element (e.g. background:url(...),
// which makes an embedding page fetch an attacker URL). It must be restricted
// to the CSS blend keywords.
func TestInjectionBlendCSS(t *testing.T) {
	spec := `{"width":50,"height":50,"marks":[{"type":"rect","encode":{"update":{
		"x":{"value":1},"y":{"value":1},"width":{"value":9},"height":{"value":9},
		"blend":{"value":"normal;background:url(http://attacker.example/x);position:fixed"}}}}]}`
	svg := renderVegaSVG(t, spec)
	if strings.Contains(svg, "attacker.example") || strings.Contains(svg, "position:fixed") {
		t.Errorf("CSS injected through blend: %s", svg[strings.Index(svg, "style="):])
	}
}

// Image URLs are not passed through Loader.Sanitize (svg.imageItem calls the
// static SanitizeURL), so with the default DenyLoader an image mark still puts
// an attacker-chosen http(s)/file/data URL into the output, which any consumer
// of the SVG will fetch. Hrefs on marks do go through the Loader.
func TestImageURLGoesThroughLoader(t *testing.T) {
	spec := `{"width":50,"height":50,"marks":[{"type":"image","encode":{"update":{
		"url":{"value":"http://169.254.169.254/latest/meta-data/"},
		"x":{"value":1},"y":{"value":1},"width":{"value":9},"height":{"value":9}}}}]}`
	svg := renderVegaSVG(t, spec) // DenyLoader
	if strings.Contains(svg, "169.254.169.254") {
		t.Errorf("image URL emitted although the Loader denies everything: %s", svg[strings.Index(svg, "<image"):])
	}
	// Without an error handler the refusal fails the render.
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.VegaToSVG([]byte(spec))
	var le *LoadError
	if !errors.As(err, &le) || le.Resource != "image" || le.URL != "http://169.254.169.254/latest/meta-data/" {
		t.Errorf("err = %v, want a *LoadError for the image", err)
	}
}

type sanitizeRecorder struct {
	StaticLoader
	seen []string
}

func (r *sanitizeRecorder) Sanitize(ctx context.Context, uri string) (string, error) {
	r.seen = append(r.seen, uri)
	return r.StaticLoader.Sanitize(ctx, uri)
}

func TestImageURLIsOfferedToLoader(t *testing.T) {
	rec := &sanitizeRecorder{}
	spec := `{"width":50,"height":50,"marks":[{"type":"image","encode":{"update":{
		"url":{"value":"http://cdn.example/a.png"},
		"x":{"value":1},"y":{"value":1},"width":{"value":9},"height":{"value":9}}}}]}`
	renderVegaSVG(t, spec, WithLoader(rec))
	for _, u := range rec.seen {
		if strings.Contains(u, "cdn.example") {
			return
		}
	}
	t.Errorf("Loader.Sanitize never saw the image URL; saw %v", rec.seen)
}

// StaticLoader.Sanitize accepts every URI and svg.Href takes the Loader's word,
// so a mark href of javascript: reaches <a xlink:href>. Vega-loader's scheme
// allow-list (which SanitizeURL implements) is applied only when the Loader is
// not used. A permissive Loader must not be able to emit script URLs.
func TestHrefSchemeAllowListWithPermissiveLoader(t *testing.T) {
	spec := `{"width":50,"height":50,"marks":[{"type":"rect","encode":{"update":{
		"x":{"value":1},"y":{"value":1},"width":{"value":9},"height":{"value":9},
		"href":{"value":"javascript:alert(document.cookie)"}}}}]}`
	svg := renderVegaSVG(t, spec, WithLoader(&StaticLoader{Value: []any{}}))
	if strings.Contains(svg, "javascript:") {
		t.Errorf("javascript: URL emitted as a link")
	}
}

// Hrefs must be sanitized by the configured Loader (this is the intended,
// working behaviour; the test guards it).
func TestHrefRejectedByLoaderIsDropped(t *testing.T) {
	spec := `{"width":50,"height":50,"marks":[{"type":"rect","encode":{"update":{
		"x":{"value":1},"y":{"value":1},"width":{"value":9},"height":{"value":9},
		"href":{"value":"https://evil.example/x"}}}}]}`
	svg := renderVegaSVG(t, spec) // DenyLoader
	if strings.Contains(svg, "evil.example") {
		t.Errorf("href emitted although the Loader denies it")
	}
}

// XML 1.0 has no representation for most C0 control characters or U+FFFE.
// Text with such characters (from data as well as from the spec) makes the
// SVG unparseable for browsers and for this package's own SVGToPDF, so one
// bad character in a data field breaks VegaToPDF for the whole chart.
func TestControlCharactersKeepOutputWellFormed(t *testing.T) {
	spec := `{"width":50,"height":50,"title":"t\u0001x","marks":[{"type":"text","encode":{"update":{
		"text":{"value":"a\u0001b\u0008c￾d"},"x":{"value":1},"y":{"value":10}}}}]}`
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	svg, err := c.VegaToSVG([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, svg)
	if _, err := c.VegaToPDF([]byte(spec)); err != nil {
		t.Errorf("VegaToPDF: %v", err)
	}
}

// hostileText would make markup if it reached the SVG unescaped: it closes
// an attribute (and the transform's parenthesis), closes the element, opens a
// <script>, and opens an element with an event handler.
const hostileText = "1)\"/><script>alert(1)</script><g onload=\"x' &amp;"

// benignText is hostileText with every character that is special in markup
// replaced: a string of the same kind and length that can make no markup.
var benignText = strings.Map(func(r rune) rune {
	if strings.ContainsRune("<>&\"'", r) {
		return '_'
	}
	return r
}, hostileText)

// structureOf renders a spec through the public API with a permissive
// Loader, past every load or evaluation error, and returns its skeleton
// (svgcheck.Skeleton) after checking it against the renderer's grammar.
func structureOf(t *testing.T, c *Converter, lite bool, spec string) ([]string, error) {
	t.Helper()
	render := c.VegaToSVG
	if lite {
		render = c.VegaLiteToSVG
	}
	svg, err := render([]byte(spec), WithErrorHandler(func(error) error { return nil }))
	if err != nil {
		return nil, err
	}
	skel, err := svgcheck.Skeleton(svg)
	if err != nil {
		t.Errorf("%v\nspec: %s\n%s", err, spec, svg)
	}
	return skel, nil
}

// assertSameStructure renders the spec made with the hostile string and with
// the benign one. The two must have the same elements and attributes: what a
// string holds may change attribute values and text, never the structure.
func assertSameStructure(t *testing.T, c *Converter, lite bool, label string, spec func(s string) string) {
	t.Helper()
	got, gerr := structureOf(t, c, lite, spec(hostileText))
	want, werr := structureOf(t, c, lite, spec(benignText))
	if (gerr == nil) != (werr == nil) {
		t.Errorf("%s: hostile error %v, benign error %v", label, gerr, werr)
		return
	}
	if gerr != nil || got == nil || want == nil {
		return
	}
	if d := svgcheck.Diff(got, want); d != "" {
		t.Errorf("%s: a string in the input changed the structure of the SVG: %s", label, d)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Every encoding property of every Vega mark type, given a string as a value
// and from a data field, and every Vega-Lite channel with a raw field
// (scale: null) or a value: no string from the spec or the data can make
// markup. Upstream escapes each through its markup writer; so must the
// engine (C1 of the 2026-10-11 audit, where a string x reached transform
// unescaped).
func TestSpecAndDataStringsMakeNoMarkup(t *testing.T) {
	c, err := New(WithLoader(&StaticLoader{Value: []any{}}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	vgMarks := []string{"arc", "area", "group", "image", "line", "path", "rect", "rule", "shape", "symbol", "text", "trail"}
	vgProps := []string{
		"x", "y", "x2", "y2", "xc", "yc", "width", "height", "angle", "size", "fontSize", "path", "url", "href", "fill", "stroke",
		"strokeWidth", "strokeDash", "strokeDashOffset", "strokeOpacity", "fillOpacity", "opacity", "strokeMiterLimit",
		"strokeCap", "strokeJoin", "cornerRadius", "text", "font", "fontWeight", "fontStyle", "fontVariant", "align",
		"baseline", "dx", "dy", "radius", "theta", "limit", "lineHeight", "ellipsis", "dir", "shape", "interpolate",
		"orient", "description", "ariaRole", "ariaRoleDescription", "aria", "cursor", "tooltip", "blend",
		"innerRadius", "outerRadius", "startAngle", "endAngle", "padAngle", "scaleX", "scaleY", "zindex", "clip",
	}
	for _, mt := range vgMarks {
		for _, prop := range vgProps {
			for _, fromData := range []bool{false, true} {
				label := "vega " + mt + "." + prop
				if fromData {
					label += " (data)"
				}
				spec := func(s string) string {
					ref := `{"value":` + jsonString(s) + `}`
					if fromData {
						ref = `{"field":"a"}`
					}
					return `{"width":60,"height":60,"data":[{"name":"t","values":[{"a":` + jsonString(s) + `,"i":0},{"a":` + jsonString(s) + `,"i":1}]}],` +
						`"marks":[{"type":"` + mt + `","from":{"data":"t"},"encode":{"update":{` +
						`"x":{"field":"i"},"y":{"field":"i"},"width":{"value":10},"height":{"value":10},"size":{"value":40},` +
						`"angle":{"value":30},"text":{"value":"t"},"path":{"value":"M0,0L5,5"},"url":{"value":"a.png"},` +
						`"href":{"value":"http://a.example/"},"fill":{"value":"red"},"stroke":{"value":"blue"},` +
						`"outerRadius":{"value":5},"endAngle":{"value":1},"description":{"value":"d"},` +
						jsonString(prop) + `:` + ref + `}}}]}`
				}
				assertSameStructure(t, c, false, label, spec)
			}
		}
	}

	vlMarks := []string{"point", "circle", "square", "text", "image", "rule", "bar", "line", "area", "tick", "rect", "trail", "arc"}
	vlChannels := []string{
		"x", "y", "x2", "y2", "xOffset", "yOffset", "theta", "radius", "angle", "size", "color", "fill", "stroke",
		"opacity", "fillOpacity", "strokeOpacity", "strokeWidth", "strokeDash", "shape", "href", "url", "text",
		"description", "tooltip",
	}
	for _, mt := range vlMarks {
		for _, ch := range vlChannels {
			for _, mode := range []string{"field", "value"} {
				label := "vega-lite " + mt + "." + ch + " (" + mode + ")"
				spec := func(s string) string {
					def := `{"field":"a","type":"nominal","scale":null}`
					switch {
					case mode == "value":
						def = `{"value":` + jsonString(s) + `}`
					case ch == "href" || ch == "url" || ch == "text" || ch == "description" || ch == "tooltip":
						def = `{"field":"a","type":"nominal"}`
					}
					enc := map[string]string{
						"x": `{"field":"i","type":"quantitative"}`, "y": `{"field":"i","type":"quantitative"}`,
					}
					if mt == "text" {
						enc["text"] = `{"value":"t"}`
					}
					if mt == "image" {
						enc["url"] = `{"value":"a.png"}`
					}
					if mt == "arc" {
						enc["theta"] = `{"field":"i","type":"quantitative"}`
					}
					enc[ch] = def
					var b strings.Builder
					for _, k := range []string{"x", "y", "theta", "text", "url", ch} {
						if v, ok := enc[k]; ok {
							if b.Len() > 0 {
								b.WriteByte(',')
							}
							b.WriteString(jsonString(k) + ":" + v)
							delete(enc, k)
						}
					}
					return `{"$schema":"https://vega.github.io/schema/vega-lite/v6.json",` +
						`"data":{"values":[{"a":` + jsonString(s) + `,"i":0},{"a":` + jsonString(s) + `,"i":1}]},` +
						`"mark":{"type":"` + mt + `","width":10,"height":10},"encoding":{` + b.String() + `}}`
				}
				assertSameStructure(t, c, true, label, spec)
			}
		}
	}
}
