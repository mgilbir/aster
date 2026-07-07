package svgpdf

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// element is a parsed SVG element. Only the Vega SVG-renderer output subset
// is accepted; anything else fails parsing with a descriptive error so the
// caller can fall back to raster output.
type element struct {
	name     string
	attrs    map[string]string
	children []*element
	text     string // character data, only meaningful for <text>
}

// attr returns the attribute value and whether it was present.
func (e *element) attr(name string) (string, bool) {
	v, ok := e.attrs[name]
	return v, ok
}

// supportedElements is the element vocabulary of Vega's SVG renderer that the
// translator understands. Encountering anything else is an error.
var supportedElements = map[string]bool{
	"svg":      true,
	"g":        true,
	"rect":     true,
	"path":     true,
	"line":     true,
	"text":     true,
	"defs":     true,
	"clipPath": true,
}

// parseSVG parses an SVG document into an element tree, rejecting elements
// outside the supported subset.
func parseSVG(svg string) (*element, error) {
	dec := xml.NewDecoder(strings.NewReader(svg))
	var root *element
	var stack []*element

	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("svgpdf: parsing SVG: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if !supportedElements[name] {
				return nil, fmt.Errorf("svgpdf: unsupported SVG element <%s>", name)
			}
			el := &element{name: name, attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				// Namespace declarations arrive with Space "xmlns"; keep
				// plain local names for everything else.
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				el.attrs[a.Name.Local] = a.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("svgpdf: multiple root elements")
				}
				if name != "svg" {
					return nil, fmt.Errorf("svgpdf: root element is <%s>, expected <svg>", name)
				}
				root = el
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, el)
			}
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("svgpdf: unbalanced SVG markup")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 && stack[len(stack)-1].name == "text" {
				stack[len(stack)-1].text += string(t)
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		}
	}
	if root == nil {
		return nil, fmt.Errorf("svgpdf: no <svg> root element found")
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("svgpdf: unbalanced SVG markup")
	}
	return root, nil
}

// collectClipPaths walks the tree and indexes <clipPath> definitions by id.
func collectClipPaths(root *element) (map[string]*element, error) {
	clips := make(map[string]*element)
	var walk func(e *element) error
	walk = func(e *element) error {
		if e.name == "clipPath" {
			id, ok := e.attr("id")
			if !ok {
				return fmt.Errorf("svgpdf: <clipPath> without id")
			}
			clips[id] = e
			return nil
		}
		for _, c := range e.children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return clips, nil
}
