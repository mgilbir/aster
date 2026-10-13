// Package svgcheck checks the structure of the SVG the engine writes, for
// the tests that feed it hostile specifications, data and scenes. Being well
// formed is not enough: a value written without escaping can close its
// attribute and add elements or attributes that still parse (C1 of the
// 2026-10-11 audit did exactly that). Check asserts the grammar of the
// renderer's output instead: which elements it writes, under which parents,
// with which attributes, and where text may appear. Skeleton reduces a
// document to that structure, so a test can also assert that a string in the
// input changed no part of it.
package svgcheck

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// children lists, for each element the renderer writes, the elements it may
// contain. An element missing from the map is not one the renderer writes.
var children = map[string][]string{
	"svg":            {"rect", "g", "defs"},
	"g":              {"g", "path", "line", "text", "image", "a"},
	"a":              {"g", "path", "line", "text", "image"},
	"path":           nil,
	"line":           nil,
	"rect":           nil,
	"image":          nil,
	"text":           {"tspan"},
	"tspan":          nil,
	"defs":           {"pattern", "linearGradient", "radialGradient", "clipPath"},
	"pattern":        {"rect"},
	"linearGradient": {"stop"},
	"radialGradient": {"stop"},
	"stop":           nil,
	"clipPath":       {"path", "rect"},
}

// hasText lists the elements whose content may be text.
var hasText = map[string]bool{"text": true, "tspan": true}

// Attribute names, grouped as the renderer writes them.
var (
	ariaAttrs  = []string{"aria-hidden", "aria-label", "role", "aria-roledescription"}
	styleAttrs = []string{
		"pointer-events", "display", "fill", "fill-opacity", "stroke", "stroke-opacity", "stroke-width",
		"stroke-linecap", "stroke-linejoin", "stroke-dasharray", "stroke-dashoffset", "stroke-miterlimit",
		"opacity", "style",
	}
	fontAttrs = []string{"text-anchor", "font-family", "font-size", "font-style", "font-variant", "font-weight"}
)

// attrs lists the attributes the renderer writes on each element.
var attrs = map[string]map[string]bool{
	"svg":            set([]string{"xmlns", "xmlns:xlink", "version", "class", "width", "height", "viewBox"}),
	"g":              set(ariaAttrs, []string{"class", "clip-path", "pointer-events", "transform", "fill", "stroke-miterlimit"}, styleAttrs),
	"a":              set([]string{"target", "rel", "xlink:href"}),
	"path":           set(ariaAttrs, []string{"class", "transform", "vector-effect", "d"}, styleAttrs),
	"line":           set(ariaAttrs, []string{"transform", "x2", "y2"}, styleAttrs),
	"rect":           set([]string{"x", "y", "width", "height", "fill"}),
	"image":          set(ariaAttrs, []string{"xlink:href", "transform", "width", "height", "preserveAspectRatio"}, styleAttrs),
	"text":           set(ariaAttrs, []string{"transform"}, fontAttrs, styleAttrs),
	"tspan":          set([]string{"x", "dy"}),
	"defs":           set(),
	"pattern":        set([]string{"id", "viewBox", "width", "height", "preserveAspectRatio"}),
	"linearGradient": set([]string{"id", "x1", "x2", "y1", "y2"}),
	"radialGradient": set([]string{"id", "fx", "fy", "fr", "cx", "cy", "r"}),
	"stop":           set([]string{"offset", "stop-color"}),
	"clipPath":       set([]string{"id"}),
}

func set(groups ...[]string) map[string]bool {
	m := map[string]bool{}
	for _, g := range groups {
		for _, s := range g {
			m[s] = true
		}
	}
	return m
}

// Check reports the first way svg departs from the grammar of the
// renderer's output: XML that is not well formed, an element the renderer
// does not write or writes under another parent, an attribute it does not
// write on that element or writes twice, text outside text elements, or a
// comment, processing instruction or directive.
func Check(svg string) error {
	_, err := walk(svg)
	return err
}

// Skeleton is the structure of svg as a sequence of tokens: "<name" opens an
// element, "@name" is one of its attributes in document order, "#" is a run
// of text and "/" closes the innermost element. Two documents with the same
// skeleton differ only in attribute values and text.
func Skeleton(svg string) ([]string, error) {
	return walk(svg)
}

func walk(svg string) ([]string, error) {
	// Token checks that the document is well formed (matching end tags,
	// legal characters, no duplicate namespace mishaps); RawToken, run
	// alongside, keeps the names as written (xlink:href, not the namespace
	// URL).
	strict := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := strict.Token(); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("not well-formed XML: %w", err)
		}
	}
	d := xml.NewDecoder(strings.NewReader(svg))
	var (
		skel  []string
		stack []string
	)
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not well-formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := qname(t.Name)
			if _, known := children[name]; !known {
				return nil, fmt.Errorf("element <%s> is not one the renderer writes", name)
			}
			if len(stack) == 0 {
				if name != "svg" || len(skel) != 0 {
					return nil, fmt.Errorf("root element <%s>, want one <svg>", name)
				}
			} else if parent := stack[len(stack)-1]; !slices.Contains(children[parent], name) {
				return nil, fmt.Errorf("element <%s> inside <%s>", name, parent)
			}
			skel = append(skel, "<"+name)
			seen := map[string]bool{}
			for _, a := range t.Attr {
				an := qname(a.Name)
				if !attrs[name][an] {
					return nil, fmt.Errorf("attribute %s=%q on <%s> is not one the renderer writes", an, a.Value, name)
				}
				if seen[an] {
					return nil, fmt.Errorf("attribute %s twice on <%s>", an, name)
				}
				seen[an] = true
				skel = append(skel, "@"+an)
			}
			stack = append(stack, name)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			skel = append(skel, "/")
		case xml.CharData:
			if len(t) == 0 {
				continue
			}
			if len(stack) == 0 || !hasText[stack[len(stack)-1]] {
				return nil, fmt.Errorf("text %q outside a text element", string(t))
			}
			if len(skel) == 0 || skel[len(skel)-1] != "#" {
				skel = append(skel, "#")
			}
		default:
			return nil, fmt.Errorf("unexpected %T in the output", tok)
		}
	}
	if len(skel) == 0 {
		return nil, errors.New("empty document")
	}
	return skel, nil
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// Diff describes the first difference between two skeletons, or returns ""
// when they are equal.
func Diff(got, want []string) string {
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			lo := max(0, i-6)
			return fmt.Sprintf("at token %d: got %q, want %q (context %q)", i, g, w, want[lo:min(len(want), i+1)])
		}
	}
	return ""
}
