package purego

// Output-injection tests: attacker-controlled spec values must not be able to
// add attributes or elements to the SVG, smuggle CSS into style attributes,
// produce output a conforming XML parser rejects, or reach the network through
// image URLs when the Loader denies everything.

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"
)

// checkSVG parses svg strictly and reports attributes that no legitimate
// Vega output carries (event handlers, data-*, script/foreignObject elements).
func checkSVG(t *testing.T, svg string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("output is not well-formed XML: %v", err)
			return
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "script", "foreignObject", "iframe", "style", "use", "set", "animate":
			t.Errorf("attacker-controlled element <%s> in output", se.Name.Local)
		}
		for _, a := range se.Attr {
			n := strings.ToLower(a.Name.Local)
			if strings.HasPrefix(n, "on") || strings.HasPrefix(n, "data-") {
				t.Errorf("injected attribute %s=%q on <%s>", a.Name.Local, a.Value, se.Name.Local)
			}
		}
	}
}

func renderVegaSVG(t *testing.T, spec string, opts ...Option) string {
	t.Helper()
	c, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	svg, err := c.VegaToSVG([]byte(spec))
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
